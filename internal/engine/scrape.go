// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/dht/v2/krpc"
	"github.com/anacrolix/torrent/tracker"
	trHttp "github.com/anacrolix/torrent/tracker/http"
	"github.com/anacrolix/torrent/types/infohash"
	"golang.org/x/net/proxy"

	"github.com/rivulet-kodi/rivulet-engine/internal/logging"
	"github.com/rivulet-kodi/rivulet-engine/internal/types"
)

const (
	// scrapeTimeout bounds one tracker's scrape round trip. Scrapes run in
	// parallel, so this is roughly the worst-case latency of a whole call.
	scrapeTimeout = 4 * time.Second
	// scrapeCacheTTL is how long a torrent's result is reused. Swarm sizes
	// move slowly; this keeps a stream list re-render from re-asking trackers.
	scrapeCacheTTL = 5 * time.Minute
	// scrapeBatch is the most info hashes sent in one tracker request. UDP
	// scrape fits ~74 per packet (BEP15); HTTP trackers commonly cap lower.
	scrapeBatch = 50
	// scrapeConcurrency caps simultaneous tracker requests per call.
	scrapeConcurrency = 16
	// scrapeMaxTrackersPerTorrent caps the caller-supplied trackers used per
	// torrent (the engine's ranked list is always added on top).
	scrapeMaxTrackersPerTorrent = 20
	// dhtScrapeTimeout bounds one torrent's DHT scrape traversal; most
	// answers arrive in the first few seconds.
	dhtScrapeTimeout = 10 * time.Second
	// dhtScrapeIdle ends a DHT scrape early once its estimate has not grown for
	// this long.
	dhtScrapeIdle = 3 * time.Second
	// dhtScrapeConcurrency caps simultaneous DHT scrape traversals per call.
	dhtScrapeConcurrency = 32
)

// scrapeCache holds recent scrape results keyed by lower-case info hash.
type scrapeCache struct {
	mu      sync.Mutex
	entries map[string]scrapeCacheEntry
}

type scrapeCacheEntry struct {
	result types.ScrapeResult
	at     time.Time
}

func (c *scrapeCache) get(ih string, now time.Time) (types.ScrapeResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[ih]
	if !ok || now.Sub(e.at) > scrapeCacheTTL {
		return types.ScrapeResult{}, false
	}
	return e.result, true
}

// put stores results and drops expired entries, so the cache never outgrows
// the set of torrents scraped within the last TTL.
func (c *scrapeCache) put(results []types.ScrapeResult, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]scrapeCacheEntry)
	}
	for ih, e := range c.entries {
		if now.Sub(e.at) > scrapeCacheTTL {
			delete(c.entries, ih)
		}
	}
	for _, r := range results {
		c.entries[r.InfoHash] = scrapeCacheEntry{result: r, at: now}
	}
}

// scrapeFunc scrapes one tracker for a batch of info hashes. Results are in
// the order of ihs. Swappable in tests.
type scrapeFunc func(ctx context.Context, trackerURL string, ihs []infohash.T) ([]scrapeCounts, error)

type scrapeCounts struct {
	seeders, leechers, completed int
}

// Scrape implements types.EngineManager.
func (m *manager) Scrape(ctx context.Context, reqs []types.ScrapeRequest, onUpdate func(types.ScrapeUpdate)) []types.ScrapeResult {
	src := scrapeSources{
		extraTrackers: m.scrapeTrackers(),
		tracker:       m.trackerScrapeFunc(),
		dht:           m.dhtScrapeFunc(),
		live:          m.liveSwarm,
		hints:         m.addPeerHints,
	}
	return scrapeAll(ctx, reqs, src, &m.scrapes, time.Now(), onUpdate)
}

// scrapeTrackers returns the engine's own trackers added to every scrape, or
// nil when tracker announces are disabled (then only caller trackers are used
// if any — and with trackers disabled, none are).
func (m *manager) scrapeTrackers() []string {
	if m.cfg.DisableTrackers {
		return nil
	}
	return getTrackers()
}

// trackerScrapeFunc builds the real tracker scraper. HTTP(S) trackers honour
// RIVULET_BT_PROXY the same way announces do; UDP trackers go direct, as UDP
// announces do.
func (m *manager) trackerScrapeFunc() scrapeFunc {
	disabled := m.cfg.DisableTrackers
	httpOpts := scrapeHTTPOpts(m.cfg.BTProxy)
	return func(ctx context.Context, trackerURL string, ihs []infohash.T) ([]scrapeCounts, error) {
		if disabled {
			return nil, fmt.Errorf("trackers disabled")
		}
		cl, err := tracker.NewClient(trackerURL, tracker.NewClientOpts{
			Http:    httpOpts,
			Slogger: logging.For("scrape").With("tracker", trackerURL),
		})
		if err != nil {
			return nil, fmt.Errorf("tracker client: %w", err)
		}
		defer func() { _ = cl.Close() }()
		resp, err := cl.Scrape(ctx, ihs)
		if err != nil {
			return nil, fmt.Errorf("scrape: %w", err)
		}
		if len(resp) != len(ihs) {
			return nil, fmt.Errorf("scrape: %d results for %d hashes", len(resp), len(ihs))
		}
		out := make([]scrapeCounts, len(resp))
		for i, r := range resp {
			out[i] = scrapeCounts{seeders: int(r.Seeders), leechers: int(r.Leechers), completed: int(r.Completed)}
		}
		return out, nil
	}
}

// scrapeHTTPOpts mirrors applyBTProxy for the standalone tracker client.
func scrapeHTTPOpts(proxyURL string) trHttp.NewClientOpts {
	var opts trHttp.NewClientOpts
	if proxyURL == "" {
		return opts
	}
	u, err := url.Parse(proxyURL)
	if err != nil {
		return opts // applyBTProxy already rejected this at startup
	}
	opts.Proxy = http.ProxyURL(u)
	scheme := strings.ToLower(u.Scheme)
	if scheme == "socks5" || scheme == "socks5h" {
		if d, err := proxy.FromURL(u, proxy.Direct); err == nil {
			if cd, ok := d.(proxy.ContextDialer); ok {
				opts.DialContext = cd.DialContext
			}
		}
	}
	return opts
}

// scrapeSources are the ways a scrape learns swarm sizes, plus where peer
// addresses it comes across go. dht, live and hints may be nil.
type scrapeSources struct {
	extraTrackers []string
	tracker       scrapeFunc
	dht           dhtScrapeFunc
	live          liveSwarmFunc
	hints         func(ih string, addrs []krpc.NodeAddr)
}

// scrapeAgg folds every source's answers into one result per torrent and
// reports each improvement. All access goes through mu, which also
// serializes onUpdate.
type scrapeAgg struct {
	mu       sync.Mutex
	pending  map[string]*types.ScrapeResult
	onUpdate func(types.ScrapeUpdate)
}

// fold applies f to ih's result and reports it when f says it improved.
func (a *scrapeAgg) fold(ih, source string, f func(r *types.ScrapeResult) bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	r, ok := a.pending[ih]
	if !ok || !f(r) || a.onUpdate == nil {
		return
	}
	a.onUpdate(types.ScrapeUpdate{ScrapeResult: *r, Source: source})
}

// raise sets *dst to v when v is larger, reporting whether it changed.
func raise(dst *int, v int) bool {
	if v > *dst {
		*dst = v
		return true
	}
	return false
}

// scrapeAll answers reqs from the cache where fresh and asks every source for
// the rest concurrently: open engines at once, trackers once each (batching
// their info hashes), the DHT per torrent in request order. Improvements are
// reported through onUpdate as they arrive. It returns one result per request
// in request order; requests with an invalid info hash get a zero result.
func scrapeAll(ctx context.Context, reqs []types.ScrapeRequest, src scrapeSources, cache *scrapeCache, now time.Time, onUpdate func(types.ScrapeUpdate)) []types.ScrapeResult {
	agg := &scrapeAgg{pending: make(map[string]*types.ScrapeResult), onUpdate: onUpdate}
	byTracker := make(map[string][]string) // tracker URL → ihs
	var trackerOrder, order, cached, toScrape []string

	for _, req := range reqs {
		ih := strings.ToLower(strings.TrimSpace(req.InfoHash))
		if _, err := hex.DecodeString(ih); err != nil || len(ih) != 40 {
			continue
		}
		if _, ok := agg.pending[ih]; ok {
			continue // duplicate request; filled from the same result below
		}
		order = append(order, ih)
		if r, ok := cache.get(ih, now); ok {
			agg.pending[ih] = &r
			cached = append(cached, ih)
			continue
		}
		agg.pending[ih] = &types.ScrapeResult{InfoHash: ih}
		toScrape = append(toScrape, ih)
		for _, tr := range scrapeTrackerList(req.Trackers, src.extraTrackers) {
			if _, seen := byTracker[tr]; !seen {
				trackerOrder = append(trackerOrder, tr)
			}
			byTracker[tr] = appendUnique(byTracker[tr], ih)
		}
	}

	for _, ih := range cached {
		agg.fold(ih, "cache", func(*types.ScrapeResult) bool { return true })
	}
	if src.live != nil {
		for _, ih := range order {
			if seeders, leechers, ok := src.live(ih); ok {
				agg.fold(ih, "live", func(r *types.ScrapeResult) bool {
					first := !r.Live
					r.Live = true
					s, l := raise(&r.Seeders, seeders), raise(&r.Leechers, leechers)
					return first || s || l
				})
			}
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		runScrapes(ctx, trackerOrder, byTracker, agg, src.tracker)
	}()
	go func() {
		defer wg.Done()
		runDHTScrapes(ctx, toScrape, agg, src.dht, src.hints)
	}()
	wg.Wait()

	agg.mu.Lock()
	defer agg.mu.Unlock()
	fresh := make([]types.ScrapeResult, 0, len(toScrape))
	for _, ih := range toScrape {
		// Only cache answers: a torrent no source answered for may just have
		// hit a slow moment, so it is retried on the next call.
		if r := agg.pending[ih]; r.Known() {
			fresh = append(fresh, *r)
		}
	}
	cache.put(fresh, now)

	results := make([]types.ScrapeResult, len(reqs))
	for i, req := range reqs {
		ih := strings.ToLower(strings.TrimSpace(req.InfoHash))
		results[i].InfoHash = ih
		if r, ok := agg.pending[ih]; ok {
			results[i] = *r
		}
	}
	return results
}

// runScrapes issues every (tracker, batch) request concurrently and folds each
// answer in as it arrives, keeping the largest figures per torrent. A
// torrent's first tracker answer is always reported, even when it is zero.
func runScrapes(ctx context.Context, trackerOrder []string, byTracker map[string][]string, agg *scrapeAgg, scrape scrapeFunc) {
	var (
		wg  sync.WaitGroup
		sem = make(chan struct{}, scrapeConcurrency)
	)
	for _, tr := range trackerOrder {
		ihs := byTracker[tr]
		for start := 0; start < len(ihs); start += scrapeBatch {
			batch := ihs[start:min(start+scrapeBatch, len(ihs))]
			wg.Add(1)
			go func(tr string, batch []string) {
				defer wg.Done()
				select {
				case sem <- struct{}{}:
				case <-ctx.Done():
					return
				}
				defer func() { <-sem }()

				hashes := make([]infohash.T, len(batch))
				for i, ih := range batch {
					_ = hashes[i].FromHexString(ih) // validated by scrapeAll
				}
				tctx, cancel := context.WithTimeout(ctx, scrapeTimeout)
				counts, err := scrape(tctx, tr, hashes)
				cancel()
				if err != nil {
					logging.For("scrape").Debug("tracker scrape failed", "tracker", tr, "err", err)
					return
				}
				for i, ih := range batch {
					c := counts[i]
					agg.fold(ih, "tracker", func(r *types.ScrapeResult) bool {
						r.Trackers++
						s, l := raise(&r.Seeders, c.seeders), raise(&r.Leechers, c.leechers)
						done := raise(&r.Completed, c.completed)
						return r.Trackers == 1 || s || l || done
					})
				}
			}(tr, batch)
		}
	}
	wg.Wait()
}

// runDHTScrapes runs a DHT scrape per torrent, at most dhtScrapeConcurrency at
// a time and in the given order, so the caller's first torrents settle first.
func runDHTScrapes(ctx context.Context, ihs []string, agg *scrapeAgg, scrape dhtScrapeFunc, hints func(ih string, addrs []krpc.NodeAddr)) {
	if scrape == nil {
		return
	}
	var (
		wg  sync.WaitGroup
		sem = make(chan struct{}, dhtScrapeConcurrency)
	)
	defer wg.Wait()
	for _, ih := range ihs {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		wg.Add(1)
		go func(ih string) {
			defer wg.Done()
			defer func() { <-sem }()
			var hash infohash.T
			_ = hash.FromHexString(ih) // validated by scrapeAll
			tctx, cancel := context.WithTimeout(ctx, dhtScrapeTimeout)
			defer cancel()
			scrape(tctx, hash, func(seeds, peers int) {
				agg.fold(ih, "dht", func(r *types.ScrapeResult) bool {
					first := !r.DHT
					r.DHT = true
					s, l := raise(&r.Seeders, seeds), raise(&r.Leechers, peers)
					return first || s || l
				})
			}, func(addrs []krpc.NodeAddr) {
				if hints != nil {
					hints(ih, addrs)
				}
			})
		}(ih)
	}
}

// scrapeTrackerList merges a request's trackers (capped) with the engine's
// list, keeping only schemes the tracker client can scrape, deduplicated.
func scrapeTrackerList(reqTrackers, extra []string) []string {
	if len(reqTrackers) > scrapeMaxTrackersPerTorrent {
		reqTrackers = reqTrackers[:scrapeMaxTrackersPerTorrent]
	}
	var out []string
	for _, tr := range announceableTrackers(append(append([]string{}, reqTrackers...), extra...), false) {
		out = appendUnique(out, strings.TrimSpace(tr))
	}
	return out
}

func appendUnique(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

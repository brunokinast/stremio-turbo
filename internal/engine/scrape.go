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
func (m *manager) Scrape(ctx context.Context, reqs []types.ScrapeRequest) []types.ScrapeResult {
	return scrapeAll(ctx, reqs, m.scrapeTrackers(), m.trackerScrapeFunc(), &m.scrapes, time.Now())
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

// scrapeAll answers reqs from the cache where fresh, scrapes the rest (each
// tracker once, batching its info hashes), and returns one result per request
// in request order. Requests with an invalid info hash get a zero result.
func scrapeAll(ctx context.Context, reqs []types.ScrapeRequest, extraTrackers []string, scrape scrapeFunc, cache *scrapeCache, now time.Time) []types.ScrapeResult {
	results := make([]types.ScrapeResult, len(reqs))
	pending := make(map[string]*types.ScrapeResult) // ih → aggregated result
	byTracker := make(map[string][]string)          // tracker URL → ihs
	var trackerOrder []string

	for i, req := range reqs {
		ih := strings.ToLower(strings.TrimSpace(req.InfoHash))
		results[i].InfoHash = ih
		if _, err := hex.DecodeString(ih); err != nil || len(ih) != 40 {
			continue
		}
		if r, ok := cache.get(ih, now); ok {
			results[i] = r
			continue
		}
		if _, ok := pending[ih]; ok {
			continue // duplicate request; filled from pending below
		}
		pending[ih] = &types.ScrapeResult{InfoHash: ih}
		for _, tr := range scrapeTrackerList(req.Trackers, extraTrackers) {
			if _, seen := byTracker[tr]; !seen {
				trackerOrder = append(trackerOrder, tr)
			}
			byTracker[tr] = appendUnique(byTracker[tr], ih)
		}
	}

	if len(pending) > 0 {
		runScrapes(ctx, trackerOrder, byTracker, pending, scrape)
		fresh := make([]types.ScrapeResult, 0, len(pending))
		for _, r := range pending {
			// Only cache answers: a torrent no tracker answered for may just
			// have hit a slow moment, so it is retried on the next call.
			if r.Trackers > 0 {
				fresh = append(fresh, *r)
			}
		}
		cache.put(fresh, now)
	}

	for i := range results {
		if r, ok := pending[results[i].InfoHash]; ok {
			results[i] = *r
		}
	}
	return results
}

// runScrapes issues every (tracker, batch) request concurrently and folds the
// answers into pending, keeping the largest figures per torrent.
func runScrapes(ctx context.Context, trackerOrder []string, byTracker map[string][]string, pending map[string]*types.ScrapeResult, scrape scrapeFunc) {
	var (
		mu  sync.Mutex
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
				mu.Lock()
				defer mu.Unlock()
				for i, ih := range batch {
					r := pending[ih]
					r.Trackers++
					r.Seeders = max(r.Seeders, counts[i].seeders)
					r.Leechers = max(r.Leechers, counts[i].leechers)
					r.Completed = max(r.Completed, counts[i].completed)
				}
			}(tr, batch)
		}
	}
	wg.Wait()
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

// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"io"
	"math"
	"sync"
	"time"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/dht/v2/krpc"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/types/infohash"
)

// dhtScrapeFunc estimates one torrent's swarm from the DHT, calling report
// with the (seeds, peers) estimate each time it grows and found with the peer
// addresses storing nodes return, until ctx ends or the traversal is
// exhausted. Swappable in tests.
type dhtScrapeFunc func(ctx context.Context, ih infohash.T, report func(seeds, peers int), found func([]krpc.NodeAddr))

// liveSwarmFunc reports an open engine's connected seeders and other peers;
// ok is false when the torrent is not open.
type liveSwarmFunc func(ih string) (seeders, leechers int, ok bool)

// dhtScrapeFunc returns a BEP 33 scraper over the client's DHT servers (IPv4
// and IPv6), or nil when the DHT is off.
func (m *manager) dhtScrapeFunc() dhtScrapeFunc {
	servers := m.dhtServers()
	if len(servers) == 0 {
		return nil
	}
	return func(ctx context.Context, ih infohash.T, report func(seeds, peers int), found func([]krpc.NodeAddr)) {
		dhtScrape(ctx, servers, ih, report, found)
	}
}

// dhtScrape walks the DHT toward ih on every server, without announcing, and
// unions the seed and peer bloom filters (BEP 33) that storing nodes return,
// passing on the peer addresses they list too.
// Each node only knows the peers that announced to it, so the union of all
// filters estimates the whole DHT-visible swarm far better than any single
// node's filter does.
func dhtScrape(ctx context.Context, servers []*dht.Server, ih infohash.T, report func(seeds, peers int), found func([]krpc.NodeAddr)) {
	var (
		mu                   sync.Mutex
		seeds, peers         krpc.ScrapeBloomFilter
		lastSeeds, lastPeers int
		wg                   sync.WaitGroup
	)
	for _, s := range servers {
		a, err := s.AnnounceTraversal(ih, dht.Scrape())
		if err != nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer a.Close()
			// The walk converges on the storing nodes within a few seconds
			// and then only wanders (nodes keep replying, with nothing new);
			// once filters have arrived, stop when the estimate stops growing.
			idle := time.NewTimer(dhtScrapeTimeout)
			defer idle.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-idle.C:
					return
				case pv, ok := <-a.Peers:
					if !ok {
						return
					}
					if len(pv.Peers) > 0 {
						found(pv.Peers)
					}
					if pv.BFsd == nil && pv.BFpe == nil {
						continue
					}
					mu.Lock()
					bloomUnion(&seeds, pv.BFsd)
					bloomUnion(&peers, pv.BFpe)
					s, p := bloomCount(&seeds), bloomCount(&peers)
					grew := s > lastSeeds || p > lastPeers
					if grew {
						lastSeeds, lastPeers = max(s, lastSeeds), max(p, lastPeers)
						report(lastSeeds, lastPeers)
					}
					mu.Unlock()
					if grew {
						idle.Reset(dhtScrapeIdle)
					}
				}
			}
		}()
	}
	wg.Wait()
}

// bloomUnion ORs src into dst. Bloom filters with the same hashing union
// exactly: the result is the filter of the union of both sets.
func bloomUnion(dst, src *krpc.ScrapeBloomFilter) {
	if src == nil {
		return
	}
	for i := range dst {
		dst[i] |= src[i]
	}
}

// bloomCount is the filter's estimated member count; an empty filter is 0
// (EstimateCount's formula yields a fraction above zero for it).
func bloomCount(f *krpc.ScrapeBloomFilter) int {
	if *f == (krpc.ScrapeBloomFilter{}) {
		return 0
	}
	return int(math.Round(f.EstimateCount()))
}

// WriteStatus implements types.EngineManager with anacrolix's own status dump.
func (m *manager) WriteStatus(w io.Writer) {
	m.client.WriteStatus(w)
}

// liveSwarm implements liveSwarmFunc from the engine's torrent stats.
func (m *manager) liveSwarm(ih string) (seeders, leechers int, ok bool) {
	m.mu.RLock()
	e, found := m.engines[ih]
	var t *torrent.Torrent
	if found {
		t = e.t
	}
	m.mu.RUnlock()
	if t == nil {
		return 0, 0, false
	}
	st := t.Stats()
	return st.ConnectedSeeders, max(st.ActivePeers-st.ConnectedSeeders, 0), true
}

// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package engine

import (
	"sync"
	"time"

	"github.com/anacrolix/dht/v2/krpc"
	"github.com/anacrolix/torrent"
)

const (
	// peerHintTTL is how long peers learnt by a scrape are offered to a
	// torrent opened later. Swarm members come and go, but most stay for
	// many minutes: long enough to cover browsing a stream list, then
	// playing from it.
	peerHintTTL = 10 * time.Minute
	// peerHintMax caps the addresses kept per torrent (newest kept).
	peerHintMax = 200
)

// peerHints remembers peer addresses DHT scrapes found for torrents that are
// not open yet, so opening one starts with known peers instead of a cold
// lookup: the difference between seconds and a minute to metadata on small
// swarms.
type peerHints struct {
	mu      sync.Mutex
	entries map[string]peerHintEntry
}

type peerHintEntry struct {
	addrs []krpc.NodeAddr
	at    time.Time
}

// add merges addrs into ih's hints (deduplicated, newest last, capped) and
// drops expired entries so the cache only holds recently scraped torrents.
func (h *peerHints) add(ih string, addrs []krpc.NodeAddr, now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.entries == nil {
		h.entries = make(map[string]peerHintEntry)
	}
	for k, e := range h.entries {
		if now.Sub(e.at) > peerHintTTL {
			delete(h.entries, k)
		}
	}
	e := h.entries[ih]
	seen := make(map[string]struct{}, len(e.addrs)+len(addrs))
	for _, a := range e.addrs {
		seen[a.String()] = struct{}{}
	}
	for _, a := range addrs {
		if a.Port == 0 || len(a.IP) == 0 {
			continue
		}
		if _, dup := seen[a.String()]; dup {
			continue
		}
		seen[a.String()] = struct{}{}
		e.addrs = append(e.addrs, a)
	}
	if len(e.addrs) > peerHintMax {
		e.addrs = e.addrs[len(e.addrs)-peerHintMax:]
	}
	e.at = now
	h.entries[ih] = e
}

// get returns ih's unexpired hints.
func (h *peerHints) get(ih string, now time.Time) []krpc.NodeAddr {
	h.mu.Lock()
	defer h.mu.Unlock()
	e, ok := h.entries[ih]
	if !ok || now.Sub(e.at) > peerHintTTL {
		return nil
	}
	return append([]krpc.NodeAddr(nil), e.addrs...)
}

// peerInfos converts DHT peer addresses the way anacrolix does for its own
// DHT announces (Torrent.consumeDhtAnnouncePeers).
func peerInfos(addrs []krpc.NodeAddr) []torrent.PeerInfo {
	out := make([]torrent.PeerInfo, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, torrent.PeerInfo{Addr: a, Source: torrent.PeerSourceDhtGetPeers})
	}
	return out
}

// addPeerHints stores peers a scrape found for ih and, when the torrent is
// already open, hands them to it straight away.
func (m *manager) addPeerHints(ih string, addrs []krpc.NodeAddr) {
	m.peerHints.add(ih, addrs, time.Now())
	m.mu.RLock()
	e, ok := m.engines[ih]
	var t *torrent.Torrent
	if ok {
		t = e.t
	}
	m.mu.RUnlock()
	if t != nil {
		t.AddPeers(peerInfos(addrs))
	}
}

// seedPeerHints gives a newly opened torrent the peers recent scrapes found.
func (m *manager) seedPeerHints(ih string, t *torrent.Torrent) {
	if hints := m.peerHints.get(ih, time.Now()); len(hints) > 0 {
		t.AddPeers(peerInfos(hints))
	}
}

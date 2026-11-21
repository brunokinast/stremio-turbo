// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package engine

import (
	"maps"
	"time"

	"github.com/anacrolix/torrent"

	"github.com/rivulet-kodi/rivulet-engine/internal/types"
)

// The trace's supply section (types.SupplyTrace): where a torrent's peer
// addresses came from, the dial pipeline second by second, tracker and DHT
// replies, and why connections closed. It explains a torrent that connects to
// few peers next to a client that connects to many on the same swarm.
const (
	supplySampleCap   = 2000
	supplyAnnounceCap = 2000
	supplyDhtCap      = 2000
	supplyCloseCap    = 5000

	// The supply sampler runs every second for this long after a torrent is
	// added (start-up is where peers are won or lost), then every 10 s.
	supplyFastFor    = 3 * time.Minute
	supplySlowPeriod = 10 * time.Second
)

// supplyState is a torrent trace's supply record.
type supplyState struct {
	samples   ring[types.SupplySample]
	announces ring[types.TrackerAnnounce]
	dht       ring[types.DhtBatch]
	closes    ring[types.ConnClose]
	offered   map[string]int // new or changed addresses, by source
	repeated  map[string]int // addresses already known, by source
	rejected  map[string]int // banned or unusable, by source
}

func newSupplyState() supplyState {
	return supplyState{
		samples:   newRing[types.SupplySample](supplySampleCap),
		announces: newRing[types.TrackerAnnounce](supplyAnnounceCap),
		dht:       newRing[types.DhtBatch](supplyDhtCap),
		closes:    newRing[types.ConnClose](supplyCloseCap),
		offered:   map[string]int{},
		repeated:  map[string]int{},
		rejected:  map[string]int{},
	}
}

func (s *supplyState) trace() types.SupplyTrace {
	return types.SupplyTrace{
		Samples:   s.samples.items(),
		Announces: s.announces.items(),
		Dht:       s.dht.items(),
		Closes:    s.closes.items(),
	}
}

func installSupplyTracer(cc *torrent.ClientConfig, tr *tracer) {
	cc.Callbacks.PeerAdded = append(cc.Callbacks.PeerAdded, tr.onPeerAdded)
	cc.Callbacks.TrackerAnnounced = append(cc.Callbacks.TrackerAnnounced, tr.onTrackerAnnounced)
	cc.Callbacks.DhtPeersReceived = append(cc.Callbacks.DhtPeersReceived, tr.onDhtPeersReceived)
	cc.Callbacks.PeerConnClosedReason = append(cc.Callbacks.PeerConnClosedReason, tr.onPeerConnClosed)
}

// Callbacks run with the client lock held: record only.

func (tr *tracer) onPeerAdded(ev torrent.PeerAddedEvent) {
	src := string(ev.Peer.Source)
	tr.with(ev.Torrent.InfoHash().HexString(), func(tt *torrentTrace) {
		switch ev.Outcome {
		case torrent.PeerAddedNew, torrent.PeerAddedReplaced:
			tt.supply.offered[src]++
		case torrent.PeerAddedDuplicate:
			tt.supply.repeated[src]++
		default:
			tt.supply.rejected[src]++
		}
	})
}

func (tr *tracer) onTrackerAnnounced(ev torrent.TrackerAnnouncedEvent) {
	rec := types.TrackerAnnounce{
		URL:        ev.URL,
		NumWant:    ev.NumWant,
		DurationMs: ev.Duration.Milliseconds(),
		Peers:      ev.Peers,
		Added:      ev.Added,
		Seeders:    ev.Seeders,
		Leechers:   ev.Leechers,
		IntervalS:  int64(ev.Interval / time.Second),
	}
	if ev.Event != 0 {
		rec.Event = ev.Event.String()
	}
	if ev.Err != nil {
		rec.Error = ev.Err.Error()
	}
	tr.with(ev.Torrent.InfoHash().HexString(), func(tt *torrentTrace) {
		rec.AtMs = ev.Started.Sub(tt.created).Milliseconds()
		tt.supply.announces.add(rec)
	})
}

func (tr *tracer) onDhtPeersReceived(ev torrent.DhtPeersReceivedEvent) {
	now := time.Now()
	tr.with(ev.Torrent.InfoHash().HexString(), func(tt *torrentTrace) {
		tt.supply.dht.add(types.DhtBatch{AtMs: now.Sub(tt.created).Milliseconds(), Peers: ev.Peers, Added: ev.Added})
	})
}

func (tr *tracer) onPeerConnClosed(ev torrent.PeerConnClosedEvent) {
	t := ev.Conn.Torrent()
	if t == nil {
		return // closed before its handshake named a torrent
	}
	now := time.Now()
	rec := types.ConnClose{
		Address:    ev.Conn.RemoteAddr.String(),
		Source:     string(ev.Conn.Discovery),
		Network:    ev.Conn.Network,
		Outgoing:   ev.Conn.Outgoing(),
		Reason:     ev.Reason,
		Downloaded: ev.Downloaded,
		Uploaded:   ev.Uploaded,
	}
	if ev.Err != nil {
		rec.Error = ev.Err.Error()
	}
	if !ev.Handshaken.IsZero() {
		rec.LifetimeMs = now.Sub(ev.Handshaken).Milliseconds()
	}
	tr.with(t.InfoHash().HexString(), func(tt *torrentTrace) {
		rec.AtMs = now.Sub(tt.created).Milliseconds()
		tt.supply.closes.add(rec)
	})
}

// sampleSupply records one supply sample.
func (tr *tracer) sampleSupply(ih string, t *torrent.Torrent, now time.Time) {
	st := t.SupplyStats()
	pending := make(map[string]int, len(st.PendingBySource))
	for src, n := range st.PendingBySource {
		pending[string(src)] = n
	}
	tr.with(ih, func(tt *torrentTrace) {
		s := types.SupplySample{
			AtMs:             now.Sub(tt.created).Milliseconds(),
			PendingBySource:  pending,
			HalfOpen:         st.HalfOpen,
			MaxHalfOpen:      st.MaxHalfOpen,
			Established:      st.Established,
			MaxEstablished:   st.MaxEstablished,
			WantPeers:        st.WantPeers,
			DialTimeoutMs:    st.DialTimeout.Milliseconds(),
			OfferedBySource:  maps.Clone(tt.supply.offered),
			RepeatedBySource: maps.Clone(tt.supply.repeated),
		}
		if len(tt.supply.rejected) > 0 {
			s.RejectedBySource = maps.Clone(tt.supply.rejected)
		}
		tt.supply.samples.add(s)
	})
}

// sampleSupply samples the torrent's supply from when it is added until it
// closes: every second at first, then every supplySlowPeriod.
func (e *engine) sampleSupply(created time.Time) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	slow := false
	for {
		select {
		case <-e.t.Closed():
			return
		case now := <-tick.C:
			e.trace.sampleSupply(e.infoHash, e.t, now)
			if !slow && now.Sub(created) >= supplyFastFor {
				slow = true
				tick.Reset(supplySlowPeriod)
			}
		}
	}
}

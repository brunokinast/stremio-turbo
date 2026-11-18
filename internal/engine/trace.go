// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package engine

import (
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/go-utp/purego"
	"github.com/anacrolix/torrent"
	pp "github.com/anacrolix/torrent/peer_protocol"

	"github.com/rivulet-kodi/rivulet-engine/internal/types"
)

// Bounds for each torrent's trace (GET /debug/trace/:infoHash). A two-minute
// 4K play with ~30 peers yields a few thousand peer samples; older entries drop
// off first.
const (
	traceEventCap     = 2000
	traceStallCap     = 2000
	traceSampleCap    = 10000
	traceSlowWriteCap = 1000
	traceCompleteCap  = 2000
	lockHoldCap       = 1000

	// A read that waits this long for data counts as a stall.
	stallThreshold = 250 * time.Millisecond
	// Chunk writes at least this slow are kept individually.
	slowWriteThreshold = 10 * time.Millisecond
	// Client lock holds at least this long are reported.
	lockHoldThreshold = 50 * time.Millisecond
)

// ring is a bounded log that keeps the newest entries.
type ring[T any] struct {
	buf  []T
	next int
	cap  int
}

func newRing[T any](n int) ring[T] { return ring[T]{cap: n} }

func (r *ring[T]) add(v T) {
	if len(r.buf) < r.cap {
		r.buf = append(r.buf, v)
		return
	}
	r.buf[r.next] = v
	r.next = (r.next + 1) % r.cap
}

// items returns the entries oldest first.
func (r *ring[T]) items() []T {
	out := make([]T, 0, len(r.buf))
	out = append(out, r.buf[r.next:]...)
	return append(out, r.buf[:r.next]...)
}

// tracer records, per torrent, the milestones, read stalls, peer samples and
// storage timings of a play, plus client-wide lock holds. Everything is
// in-memory and bounded; anacrolix callbacks only record (some run with the
// client lock held).
type tracer struct {
	mu       sync.Mutex
	torrents map[string]*torrentTrace
	locks    ring[types.LockHold]
}

type torrentTrace struct {
	created    time.Time
	seen       map[string]bool // milestones already recorded
	events     ring[types.TraceEvent]
	stalls     ring[types.ReadStall]
	samples    ring[types.PeerSample]
	slowWrites ring[types.SlowWrite]
	completes  ring[types.PieceTiming]
	writes     int64
	writeTime  time.Duration
}

func newTracer() *tracer {
	return &tracer{torrents: map[string]*torrentTrace{}, locks: newRing[types.LockHold](lockHoldCap)}
}

// begin starts (or restarts) the trace for a torrent being added.
func (tr *tracer) begin(ih string, now time.Time) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tt := &torrentTrace{
		created:    now,
		seen:       map[string]bool{},
		events:     newRing[types.TraceEvent](traceEventCap),
		stalls:     newRing[types.ReadStall](traceStallCap),
		samples:    newRing[types.PeerSample](traceSampleCap),
		slowWrites: newRing[types.SlowWrite](traceSlowWriteCap),
		completes:  newRing[types.PieceTiming](traceCompleteCap),
	}
	tt.events.add(types.TraceEvent{Name: "created"})
	tr.torrents[ih] = tt
}

// forget drops a removed torrent's trace.
func (tr *tracer) forget(ih string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	delete(tr.torrents, ih)
}

// with runs f on a torrent's trace under the lock; a torrent never begun is
// ignored.
func (tr *tracer) with(ih string, f func(tt *torrentTrace)) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tt, ok := tr.torrents[ih]; ok {
		f(tt)
	}
}

func (tt *torrentTrace) ms(at time.Time) int64 { return at.Sub(tt.created).Milliseconds() }

// once records the milestone name the first time it happens.
func (tr *tracer) once(ih, name, detail string, at time.Time) {
	tr.with(ih, func(tt *torrentTrace) {
		if tt.seen[name] {
			return
		}
		tt.seen[name] = true
		tt.events.add(types.TraceEvent{AtMs: tt.ms(at), Name: name, Detail: detail})
	})
}

// stall records a read that waited d for data, ending at end.
func (tr *tracer) stall(ih string, s types.ReadStall, end time.Time, d time.Duration) {
	tr.with(ih, func(tt *torrentTrace) {
		s.AtMs = tt.ms(end.Add(-d))
		s.DurationMs = d.Milliseconds()
		tt.stalls.add(s)
	})
}

// onPeerConnAdded marks the first connected peer from each source.
func (tr *tracer) onPeerConnAdded(pc *torrent.PeerConn) {
	t := pc.Torrent()
	if t == nil {
		return
	}
	tr.once(t.InfoHash().HexString(), "first-connected:"+string(pc.Discovery), pc.Network, time.Now())
}

// onReadMessage marks the first unchoke. It runs for every message with the
// client lock held, so anything but an unchoke returns at once.
func (tr *tracer) onReadMessage(pc *torrent.PeerConn, msg *pp.Message) {
	if msg.Type != pp.Unchoke {
		return
	}
	t := pc.Torrent()
	if t == nil {
		return
	}
	tr.once(t.InfoHash().HexString(), "first-unchoke", pc.RemoteAddr.String(), time.Now())
}

// onChunkWritten counts storage writes, keeps the slow ones, and marks the
// first chunk received.
func (tr *tracer) onChunkWritten(ev torrent.ChunkWrittenEvent) {
	now := time.Now()
	ih := ev.Torrent.InfoHash().HexString()
	tr.with(ih, func(tt *torrentTrace) {
		tt.writes++
		tt.writeTime += ev.Duration
		if !tt.seen["first-chunk"] {
			tt.seen["first-chunk"] = true
			tt.events.add(types.TraceEvent{AtMs: tt.ms(now.Add(-ev.Duration)), Name: "first-chunk"})
		}
		if ev.Duration < slowWriteThreshold && ev.Err == nil {
			return
		}
		sw := types.SlowWrite{AtMs: tt.ms(now.Add(-ev.Duration)), Piece: ev.Piece, Begin: ev.Begin, DurationMs: msf(ev.Duration)}
		if ev.Err != nil {
			sw.Error = ev.Err.Error()
		}
		tt.slowWrites.add(sw)
	})
}

// onPieceMarkedComplete times each verified piece's MarkComplete.
func (tr *tracer) onPieceMarkedComplete(ev torrent.PieceMarkedCompleteEvent) {
	now := time.Now()
	tr.with(ev.Torrent.InfoHash().HexString(), func(tt *torrentTrace) {
		if !tt.seen["first-piece-verified"] {
			tt.seen["first-piece-verified"] = true
			tt.events.add(types.TraceEvent{AtMs: tt.ms(now), Name: "first-piece-verified"})
		}
		tt.completes.add(types.PieceTiming{AtMs: tt.ms(now.Add(-ev.Duration)), Piece: ev.Piece, DurationMs: msf(ev.Duration)})
	})
}

// onLockHold is the anacrolix LockHoldCallback.
func (tr *tracer) onLockHold(held time.Duration, lockedAt string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.locks.add(types.LockHold{At: time.Now().Add(-held), HeldMs: msf(held), LockedAt: lockedAt})
}

// samplePeers records one sample of every connection that is useful or could
// be: delivering, unchoking us, or holding requests.
func (tr *tracer) samplePeers(ih string, t *torrent.Torrent, now time.Time) {
	conns := t.PeerConns()
	samples := make([]types.PeerSample, 0, len(conns))
	for _, pc := range conns {
		st := pc.Stats()
		rs := pc.RequestStats()
		downloaded := st.BytesReadUsefulData.Int64()
		uc, isUTP := pc.NetConn().(*purego.Conn)
		// Idle peers are left out, except uTP ones: their transport counters show whether a peer
		// that never unchoked us is choking or its messages aren't getting through.
		if downloaded == 0 && rs.PeerChoking && rs.Outstanding == 0 && !isUTP {
			continue
		}
		client, _ := pc.PeerClientName.Load().(string)
		s := types.PeerSample{
			Address:        pc.RemoteAddr.String(),
			Client:         client,
			Network:        pc.Network,
			Source:         string(pc.Discovery),
			Outgoing:       rs.Outgoing,
			Downloaded:     downloaded,
			Pieces:         st.RemotePieceCount,
			Outstanding:    rs.Outstanding,
			Cancelled:      rs.Cancelled,
			MaxRequests:    rs.MaxRequests,
			Interested:     rs.Interested,
			PeerChoking:    rs.PeerChoking,
			PeerInterested: rs.PeerInterested,
			Choking:        rs.Choking,
			Snubbed:        rs.Snubbed,
		}
		if isUTP {
			s.UTP = utpSample(uc.Stats())
		}
		samples = append(samples, s)
	}
	tr.with(ih, func(tt *torrentTrace) {
		at := tt.ms(now)
		for _, s := range samples {
			s.AtMs = at
			tt.samples.add(s)
		}
	})
}

func utpSample(c purego.ConnStats) *types.UTPSample {
	return &types.UTPSample{
		RTTMs:                  c.RTT.Milliseconds(),
		RTOMs:                  c.RTO.Milliseconds(),
		InOrder:                c.InOrderPackets,
		Duplicates:             c.DuplicatePackets,
		ReorderInserts:         c.ReorderInserts,
		ReorderTooFar:          c.ReorderTooFar,
		ReorderPeak:            c.ReorderPeak,
		ReorderCount:           c.ReorderCount,
		ReorderBytes:           c.ReorderBytes,
		ReorderSpan:            c.ReorderSpan,
		PayloadEstimate:        c.PayloadEstimate,
		AcksSent:               c.AcksSent,
		SelectiveAcks:          c.SelectiveAcksSent,
		DuplicateAcks:          c.DuplicateAcksSent,
		WindowUpdatesImmediate: c.WindowUpdatesImmediate,
		WindowUpdatesScheduled: c.WindowUpdatesScheduled,
		CurWindow:              c.CurWindow,
		MaxWindow:              c.MaxWindow,
		LastRcvWindow:          c.LastRcvWindow,
		ReadBuffered:           c.ReadBuffered,
	}
}

// get returns a copy of a torrent's trace.
func (tr *tracer) get(ih string) (types.Trace, bool) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tt, ok := tr.torrents[ih]
	if !ok {
		return types.Trace{}, false
	}
	return types.Trace{
		Created:     tt.created,
		Events:      tt.events.items(),
		Stalls:      tt.stalls.items(),
		Requests:    []types.TraceRequest{},
		PeerSamples: tt.samples.items(),
		Storage: types.StorageTrace{
			ChunkWrites:     tt.writes,
			ChunkWriteMs:    msf(tt.writeTime),
			SlowWrites:      tt.slowWrites.items(),
			MarkCompletions: tt.completes.items(),
		},
	}, true
}

func (tr *tracer) lockHolds() []types.LockHold {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.locks.items()
}

func msf(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// Trace implements types.EngineManager.
func (m *manager) Trace(infoHash string) (types.Trace, bool) {
	return m.trace.get(strings.ToLower(infoHash))
}

// LockHolds implements types.EngineManager.
func (m *manager) LockHolds() []types.LockHold {
	return m.trace.lockHolds()
}

// installTracer registers the tracer's anacrolix callbacks on cc.
func installTracer(cc *torrent.ClientConfig, tr *tracer) {
	cc.Callbacks.PeerConnAdded = append(cc.Callbacks.PeerConnAdded, tr.onPeerConnAdded)
	cc.Callbacks.ReadMessage = tr.onReadMessage
	cc.Callbacks.ChunkWritten = append(cc.Callbacks.ChunkWritten, tr.onChunkWritten)
	cc.Callbacks.PieceMarkedComplete = append(cc.Callbacks.PieceMarkedComplete, tr.onPieceMarkedComplete)
	cc.LockHoldCallback = tr.onLockHold
	cc.LockHoldThreshold = lockHoldThreshold
}

// samplePeers samples the torrent's peers once a second while a stream reader
// is open (started by NewReader; at most one per engine).
func (e *engine) samplePeers() {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-e.t.Closed():
			e.sampling.Store(false)
			return
		case now := <-tick.C:
			e.mu.Lock()
			idle := e.openReaders == 0
			if idle {
				// Cleared under e.mu so a NewReader racing with this exit
				// either sees the flag cleared and starts a new sampler, or
				// was counted above.
				e.sampling.Store(false)
			}
			e.mu.Unlock()
			if idle {
				return
			}
			e.trace.samplePeers(e.infoHash, e.t, now)
		}
	}
}

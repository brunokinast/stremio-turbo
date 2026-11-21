// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/anacrolix/torrent"

	"github.com/rivulet-kodi/rivulet-engine/internal/types"
)

// dialLogCap bounds each torrent's connection log. A 4K swarm yields a few
// hundred attempts in the first minutes; older entries drop off first.
const dialLogCap = 2000

// dialLog records, per torrent, every outgoing connection attempt's outcome and
// every accepted incoming connection (GET /debug/dials/:infoHash), so peer
// acquisition can be diagnosed: which addresses fail, how, and how fast.
type dialLog struct {
	mu       sync.Mutex
	torrents map[string]*torrentDials
	// Accepted incoming connections since start, and the last one's time (Unix
	// ms): none while torrents are active means peers can't reach the listening
	// port (GET /network-info), which on a thin swarm decides the speed.
	incoming     atomic.Int64
	lastIncoming atomic.Int64
}

type torrentDials struct {
	created time.Time
	records []types.DialRecord // ring: next is the oldest once full
	next    int
}

func newDialLog() *dialLog {
	return &dialLog{torrents: map[string]*torrentDials{}}
}

// begin starts (or restarts) the log for a torrent being added.
func (d *dialLog) begin(ih string, now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.torrents[ih] = &torrentDials{created: now}
}

// forget drops a removed torrent's log.
func (d *dialLog) forget(ih string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.torrents, ih)
}

func (d *dialLog) add(ih string, started time.Time, rec types.DialRecord) {
	d.mu.Lock()
	defer d.mu.Unlock()
	td, ok := d.torrents[ih]
	if !ok {
		return
	}
	rec.StartedMs = started.Sub(td.created).Milliseconds()
	if len(td.records) < dialLogCap {
		td.records = append(td.records, rec)
		return
	}
	td.records[td.next] = rec
	td.next = (td.next + 1) % dialLogCap
}

// get returns a torrent's records, oldest first.
func (d *dialLog) get(ih string) ([]types.DialRecord, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	td, ok := d.torrents[ih]
	if !ok {
		return nil, false
	}
	out := make([]types.DialRecord, 0, len(td.records))
	out = append(out, td.records[td.next:]...)
	out = append(out, td.records[:td.next]...)
	return out, true
}

// onOutgoing is the anacrolix OutgoingConnResult callback. The client lock is
// held, so it only records.
func (d *dialLog) onOutgoing(ev torrent.OutgoingConnResultEvent) {
	rec := types.DialRecord{
		Address:    ev.Peer.Addr.String(),
		Source:     string(ev.Peer.Source),
		Network:    ev.Network,
		Outgoing:   true,
		DurationMs: ev.Duration.Milliseconds(),
		WaitMs:     ev.DialWait.Milliseconds(),
		Result:     dialResult(ev.Err),

		TimeoutMs:       ev.DialTimeout.Milliseconds(),
		HalfOpenTorrent: ev.HalfOpenTorrent,
		HalfOpenClient:  ev.HalfOpenClient,
		PendingPeers:    ev.PendingPeers,
	}
	if ev.Err != nil {
		rec.Error = ev.Err.Error()
	}
	for _, l := range ev.Dials {
		leg := types.DialLeg{Network: l.Network, DurationMs: l.Duration.Milliseconds(), Result: dialResult(l.Err)}
		if l.Err != nil {
			leg.Error = l.Err.Error()
		}
		rec.Legs = append(rec.Legs, leg)
	}
	// "all initial dials failed" says nothing on its own: classify the attempt
	// by its transports' outcome (every one timed out, or was refused).
	if rec.Result == types.DialOther && len(rec.Legs) > 0 {
		same := rec.Legs[0].Result
		for _, l := range rec.Legs[1:] {
			if l.Result != same {
				same = ""
				break
			}
		}
		if same != "" && same != types.DialConnected {
			rec.Result = same
		}
	}
	for _, h := range ev.Handshakes {
		leg := types.HandshakeLeg{Network: h.Network, Obfuscated: h.Obfuscated, DurationMs: h.Duration.Milliseconds()}
		if h.Err != nil {
			leg.Error = h.Err.Error()
		}
		rec.Handshakes = append(rec.Handshakes, leg)
	}
	d.add(ev.InfoHash.HexString(), ev.Started, rec)
}

// onPeerConnAdded records accepted incoming connections (outgoing ones are
// already logged by onOutgoing).
func (d *dialLog) onPeerConnAdded(pc *torrent.PeerConn) {
	if pc.Discovery != torrent.PeerSourceIncoming {
		return
	}
	d.noteIncoming(time.Now())
	t := pc.Torrent()
	if t == nil {
		return
	}
	d.add(t.InfoHash().HexString(), time.Now(), types.DialRecord{
		Address: pc.RemoteAddr.String(),
		Source:  string(pc.Discovery),
		Network: pc.Network,
		Result:  types.DialConnected,
	})
}

// wsaECONNREFUSED is Windows' "connection refused" socket error, which
// syscall.ECONNREFUSED does not match there.
const wsaECONNREFUSED syscall.Errno = 10061

// dialResult classifies a connection attempt's error.
func dialResult(err error) string {
	if err == nil {
		return types.DialConnected
	}
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return types.DialTimeout
	}
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, wsaECONNREFUSED) {
		return types.DialRefused
	}
	if strings.Contains(strings.ToLower(err.Error()), "handshake") {
		return types.DialHandshake
	}
	return types.DialOther
}

// Dials implements types.EngineManager.
func (m *manager) Dials(infoHash string) ([]types.DialRecord, bool) {
	return m.dials.get(strings.ToLower(infoHash))
}

// noteIncoming counts an accepted incoming connection.
func (d *dialLog) noteIncoming(now time.Time) {
	d.incoming.Add(1)
	d.lastIncoming.Store(now.UnixMilli())
}

// activeSince is when the longest-loaded torrent was added (zero when none is).
func (d *dialLog) activeSince() time.Time {
	d.mu.Lock()
	defer d.mu.Unlock()
	var since time.Time
	for _, td := range d.torrents {
		if since.IsZero() || td.created.Before(since) {
			since = td.created
		}
	}
	return since
}

// Incoming reports accepted incoming peer connections since start, the
// listening port, and since when torrents have been loaded, so a client can
// tell an unreachable port (no incoming connections after a while).
func (m *manager) Incoming() types.IncomingStats {
	st := types.IncomingStats{Count: m.dials.incoming.Load(), ListenPort: m.client.LocalPort()}
	if ms := m.dials.lastIncoming.Load(); ms > 0 {
		at := time.UnixMilli(ms)
		st.LastAt = &at
	}
	if since := m.dials.activeSince(); !since.IsZero() {
		st.ActiveSince = &since
	}
	return st
}

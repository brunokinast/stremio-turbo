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
		Result:     dialResult(ev.Err),
	}
	if ev.Err != nil {
		rec.Error = ev.Err.Error()
	}
	d.add(ev.InfoHash.HexString(), ev.Started, rec)
}

// onPeerConnAdded records accepted incoming connections (outgoing ones are
// already logged by onOutgoing).
func (d *dialLog) onPeerConnAdded(pc *torrent.PeerConn) {
	if pc.Discovery != torrent.PeerSourceIncoming {
		return
	}
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

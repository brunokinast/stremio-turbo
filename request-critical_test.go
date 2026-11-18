package torrent

import (
	"net"
	"slices"
	"testing"
	"time"
	"weak"

	"github.com/go-quicktest/qt"
)

func TestRTTQueueTimeClamp(t *testing.T) {
	ms := time.Millisecond
	qt.Check(t, qt.Equals(rttQueueTime(100*ms, 4, 500*ms, 3*time.Second), 500*ms))
	qt.Check(t, qt.Equals(rttQueueTime(300*ms, 4, 500*ms, 3*time.Second), 1200*ms))
	qt.Check(t, qt.Equals(rttQueueTime(2*time.Second, 4, 500*ms, 3*time.Second), 3*time.Second))
	qt.Check(t, qt.Equals(rttQueueTime(2*time.Second, 4, 500*ms, 0), 8*time.Second))
}

func TestRequestQueueTimeFromTransportRTT(t *testing.T) {
	_, p := offsetFirstSetup(t, false)
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	p.conn = c1
	cfg := p.t.cl.config
	cfg.RequestQueueTime = 3 * time.Second
	rtt, known, calls := 200*time.Millisecond, true, 0
	cfg.PeerConnRTT = func(c net.Conn) (time.Duration, bool) {
		calls++
		qt.Check(t, qt.Equals(c, c1))
		return rtt, known
	}
	qt.Check(t, qt.Equals(p.requestQueueTime(), 3*time.Second), qt.Commentf("option off"))
	cfg.RequestQueueRTTs = 4
	cfg.RequestQueueMinTime = 500 * time.Millisecond
	qt.Check(t, qt.Equals(p.requestQueueTime(), 800*time.Millisecond))
	rtt = 50 * time.Millisecond
	qt.Check(t, qt.Equals(p.requestQueueTime(), 800*time.Millisecond), qt.Commentf("reading reused for a second"))
	qt.Check(t, qt.Equals(calls, 1))
	p.reqRTT.checked = time.Time{}
	qt.Check(t, qt.Equals(p.requestQueueTime(), 500*time.Millisecond), qt.Commentf("floor"))
	p.reqRTT.checked, known = time.Time{}, false
	qt.Check(t, qt.Equals(p.requestQueueTime(), 3*time.Second), qt.Commentf("unknown round trip"))
}

func TestCriticalStealPays(t *testing.T) {
	ms := time.Millisecond
	for _, c := range []struct {
		mine, holder time.Duration
		want         bool
	}{
		{500 * ms, 1000 * ms, true},       // 2x and 500 ms sooner
		{800 * ms, 1000 * ms, false},      // only 1.25x
		{50 * ms, 150 * ms, false},        // 3x but only 100 ms sooner
		{100 * ms, unknownDelivery, true}, // the holder has no recent rate
		{unknownDelivery, 10 * time.Second, false},
	} {
		qt.Check(t, qt.Equals(criticalStealPays(c.mine, c.holder), c.want), qt.Commentf("%v vs %v", c.mine, c.holder))
	}
}

// Gives p a recent rate of bytesPerSec.
func setRecentRate(p *PeerConn, now time.Time, bytesPerSec int) {
	p.reqQueue.reset(32)
	p.reqQueue.onUsefulChunk(now, bytesPerSec, 32)
}

// Adds requests to p in order, as if sent.
func holdRequests(p *PeerConn, rs ...RequestIndex) {
	for _, r := range rs {
		p.requestState.Requests.Add(r)
		p.t.requestState[r] = requestState{peer: weak.Make(p), when: time.Now()}
	}
}

func TestHeldRequestDeliveryCountsRequestsAhead(t *testing.T) {
	tor, p := offsetFirstSetup(t, false)
	now := time.Now()
	setRecentRate(p, now, 16<<10) // one chunk per second
	tor.cl.lock()
	defer tor.cl.unlock()
	holdRequests(p, req(1, 5), req(1, 2), req(0, 9), req(1, 0))
	d, ahead := tor.heldRequestDelivery(p, req(0, 9), now)
	qt.Check(t, qt.Equals(ahead, 2))
	qt.Check(t, qt.Equals(d, 3*time.Second))
	qt.Check(t, qt.Equals(p.heldRequestDeliveryBound(now), 4*time.Second))
	qt.Check(t, qt.Equals(p.newRequestDelivery(now), 5*time.Second))
	p.reqRTT.rtt = 200 * time.Millisecond
	qt.Check(t, qt.Equals(p.newRequestDelivery(now), 5*time.Second+100*time.Millisecond))
	for _, r := range []RequestIndex{req(1, 5), req(1, 2), req(0, 9), req(1, 0)} {
		p.requestState.Requests.CheckedRemove(r)
		delete(tor.requestState, r)
	}
}

// A second peer with everything, unchoked, in the torrent's connections.
func addSecondPeer(tor *Torrent) *PeerConn {
	q := tor.cl.newConnection(nil, newConnectionOpts{network: "test"})
	q.setTorrent(tor)
	q.onPeerHasAllPiecesNoTriggers()
	q.peerChoking = false
	return q
}

func TestShouldStealCriticalByDelivery(t *testing.T) {
	tor, fast := offsetFirstSetup(t, false)
	slow := addSecondPeer(tor)
	now := time.Now()
	setRecentRate(fast, now, 4<<20)
	setRecentRate(slow, now, 64<<10)
	tor.cl.lock()
	defer tor.cl.unlock()
	// 20 requests ahead on a 4-chunks-per-second peer: ~5 s.
	for i := range 4 {
		holdRequests(slow, req(1, i), req(1, i+4), req(1, i+8), req(1, i+12), req(2, i))
	}
	holdRequests(slow, req(0, 3))
	ok, from, to := fast.shouldStealCritical(slow, req(0, 3), now)
	qt.Check(t, qt.IsTrue(ok))
	qt.Check(t, qt.Equals(from, deliveryTime(21, defaultChunkSize, 64<<10)))
	qt.Check(t, qt.Equals(to, deliveryTime(1, defaultChunkSize, 4<<20)))
	// The other way round never pays.
	ok, _, _ = slow.shouldStealCritical(fast, req(0, 3), now)
	qt.Check(t, qt.IsFalse(ok))
}

// A blocked reader's next chunks go first, ahead of another reader's piece that would otherwise
// interleave with it.
func TestCriticalRequestsSortFirst(t *testing.T) {
	tor, p := offsetFirstSetup(t, true)
	tor.cl.config.ReaderCriticalBytes = 2 * defaultChunkSize
	addPositionedReader(tor, 3*offsetFirstPieceLength)
	addWaitingReader(tor, 8*defaultChunkSize+10)
	reqs := sortedDesired(p)
	qt.Assert(t, qt.IsTrue(len(reqs) >= 3))
	qt.Check(t, qt.Equals(reqs[0], req(0, 8)))
	qt.Check(t, qt.Equals(reqs[1], req(0, 9)))
}

// A critical chunk another peer holds stays a candidate even when the candidate list is full.
func TestCriticalStealCandidatesSurviveTheLimit(t *testing.T) {
	tor, p := offsetFirstSetup(t, false)
	tor.cl.config.ReaderCriticalBytes = defaultChunkSize
	q := addSecondPeer(tor)
	addWaitingReader(tor, 3*offsetFirstPieceLength+15*defaultChunkSize)
	tor.cl.lock()
	holdRequests(q, req(3, 15))
	tor.cl.unlock()
	p.t.cl.lock()
	d := p.getDesiredRequestState().Requests
	p.t.cl.unlock()
	qt.Check(t, qt.IsTrue(slices.Contains(d.requestIndexes, req(3, 15))))
	qt.Check(t, qt.Equals(len(d.critical), 1))
	qt.Check(t, qt.Equals(d.critical[0], requestRange{req(3, 15), req(3, 16)}))
}

func TestCriticalOffChangesNothing(t *testing.T) {
	tor, p := offsetFirstSetup(t, false)
	addWaitingReader(tor, 0)
	tor.cl.lock()
	qt.Check(t, qt.IsNil(tor.readerCriticalRanges()))
	qt.Check(t, qt.Equals(tor.criticalExtraRequests(), maxRequests(0)))
	tor.cl.unlock()
	qt.Check(t, qt.IsNil(p.getDesiredRequestStateLocked(t).Requests.critical))
}

func (p *PeerConn) getDesiredRequestStateLocked(t *testing.T) desiredRequestState {
	p.t.cl.lock()
	defer p.t.cl.unlock()
	return p.getDesiredRequestState()
}

// Registers a reader blocked on torrent offset off.
func addWaitingReader(tor *Torrent, off int64) *reader {
	r := &reader{t: tor, length: tor.length()}
	piece := pieceIndex(off / offsetFirstPieceLength)
	r.pieces = pieceRange{piece, piece + 1}
	r.torrentPos.Store(off)
	r.waitingAt.Store(off)
	tor.cl.lock()
	if tor.readers == nil {
		tor.readers = map[*reader]struct{}{}
	}
	tor.readers[r] = struct{}{}
	tor.cl.unlock()
	return r
}

func TestReaderWaitedReportsTheHolder(t *testing.T) {
	tor, p := offsetFirstSetup(t, false)
	var got []ReaderWaitedEvent
	tor.cl.config.Callbacks.ReaderWaited = append(tor.cl.config.Callbacks.ReaderWaited, func(ev ReaderWaitedEvent) {
		got = append(got, ev)
	})
	now := time.Now()
	setRecentRate(p, now, 16<<10)
	tor.cl.lock()
	holdRequests(p, req(2, 1), req(2, 4))
	tor.cl.unlock()
	r := &reader{t: tor, length: tor.length()}
	r.waitingAt.Store(-1)
	w := r.beginWait(2*offsetFirstPieceLength + 4*defaultChunkSize)
	qt.Check(t, qt.Equals(r.waitingAt.Load(), 2*offsetFirstPieceLength+4*defaultChunkSize))
	r.endWait(w)
	qt.Check(t, qt.HasLen(got, 0), qt.Commentf("waits under %v aren't reported", readerWaitReportMin))
	w = r.beginWait(2*offsetFirstPieceLength + 4*defaultChunkSize)
	w.start = w.start.Add(-time.Second)
	r.endWait(w)
	qt.Assert(t, qt.HasLen(got, 1))
	ws := got[0].Wait
	qt.Check(t, qt.IsTrue(ws.Requested))
	qt.Check(t, qt.Equals(ws.Ahead, 1))
	qt.Check(t, qt.Equals(ws.HolderNetwork, "test"))
	qt.Check(t, qt.IsTrue(got[0].Waited >= time.Second))
	qt.Check(t, qt.Equals(r.waitingAt.Load(), int64(-1)))
}

// A full queue still takes critical requests, at most criticalRequestsOverLimit of them, and
// nothing else.
func TestCriticalRequestsMayExceedTheLimitByAFew(t *testing.T) {
	tor, p := offsetFirstSetup(t, false)
	tor.cl.config.InitialRequestQueue = 4
	tor.cl.config.ReaderCriticalBytes = 32 * defaultChunkSize
	tor.cl.lock()
	defer tor.cl.unlock()
	tor.conns[p] = struct{}{}
	defer delete(tor.conns, p)
	p.requestState.Interested = true
	p.initMessageWriter()
	holdRequests(p, req(1, 0), req(1, 1), req(1, 2), req(1, 3))
	qt.Assert(t, qt.Equals(p.nominalMaxRequests(), maxRequests(4)))
	// Pieces 2 and 3 (32 chunks) are critical.
	r := &reader{t: tor, length: tor.length()}
	r.waitingAt.Store(2 * offsetFirstPieceLength)
	if tor.readers == nil {
		tor.readers = map[*reader]struct{}{}
	}
	tor.readers[r] = struct{}{}
	defer delete(tor.readers, r)
	p.applyRequestState(p.getDesiredRequestState())
	qt.Check(t, qt.Equals(p.requestState.Requests.GetCardinality(), uint64(4+criticalRequestsOverLimit)))
	var extra int
	p.requestState.Requests.Iterate(func(x RequestIndex) bool {
		if x >= req(2, 0) {
			extra++
		}
		return true
	})
	qt.Check(t, qt.Equals(extra, criticalRequestsOverLimit))
}

func TestReaderWaitReplanOnlyWhenAStealCouldPay(t *testing.T) {
	tor, p := offsetFirstSetup(t, false)
	now := time.Now()
	setRecentRate(p, now, 1<<20) // 64 chunks per second
	tor.cl.lock()
	defer tor.cl.unlock()
	qt.Check(t, qt.IsTrue(tor.readerWaitMayNeedReplan(0, now)), qt.Commentf("unrequested"))
	holdRequests(p, req(0, 0))
	for i := range 15 {
		holdRequests(p, req(1, i), req(2, i))
	}
	qt.Check(t, qt.IsFalse(tor.readerWaitMayNeedReplan(0, now)), qt.Commentf("first in the queue: 16 ms"))
	qt.Check(t, qt.IsTrue(tor.readerWaitMayNeedReplan(2*offsetFirstPieceLength+14*defaultChunkSize, now)),
		qt.Commentf("31st: ~480 ms"))
}

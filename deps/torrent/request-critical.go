package torrent

import (
	"time"
)

// Critical requests for ClientConfig.ReaderCriticalBytes: the chunks a blocked reader needs next.
//
// Peers serve requests in the order they get them, so a chunk that becomes urgent after the queues
// are full waits behind them: the reader's wait is the holder's queue, not its rate. Critical
// chunks are moved by expected delivery time instead of by request counts.

const (
	// A peer takes a critical request only if it is expected to deliver it at least this many times
	// sooner than the holder. Two similar peers then never trade requests back and forth (each
	// steal cancels on the holder).
	criticalStealFactor = 2
	// ... and at least this much sooner, so a steal pays for its cancel.
	criticalStealMinGain = 200 * time.Millisecond
	// How far a peer may exceed its request limit with critical requests.
	criticalRequestsOverLimit = 16
	// A critical request outstanding longer than this, twice its holder's estimate and twice its
	// holder's round trip is overdue: the holder most likely dropped it (a choke without the fast
	// extension, a lost message) though its queue says it's next.
	criticalOverdueMin = 500 * time.Millisecond
	// How often a blocked read looks for its chunk's request becoming overdue.
	criticalOverdueCheck = 250 * time.Millisecond
	// A holder of a blocked read's chunk that has delivered nothing useful for this long, with the
	// chunk requested at least this long ago, is snubbed at once (PieceTimeout is far longer).
	criticalSnubAfter = 2 * time.Second
	// Shorter reader waits aren't reported.
	readerWaitReportMin = 20 * time.Millisecond
	unknownDelivery     = time.Duration(1<<63 - 1)
)

type requestRange struct {
	begin, end RequestIndex
}

func (t *Torrent) criticalExtraRequests() maxRequests {
	if t.cl.config.ReaderCriticalBytes > 0 {
		return criticalRequestsOverLimit
	}
	return 0
}

// The request index of the chunk holding torrent offset off.
func (t *Torrent) requestIndexAtOffset(off int64) RequestIndex {
	piece := pieceIndex(off / t.info.PieceLength)
	return t.pieceRequestIndexBegin(piece) + RequestIndex((off-int64(piece)*t.info.PieceLength)/int64(t.chunkSize))
}

// The critical request ranges: ReaderCriticalBytes from each blocked reader's position, within the
// reader. Nil when the option is off or no reader is blocked.
func (t *Torrent) readerCriticalRanges() (ret []requestRange) {
	n := t.cl.config.ReaderCriticalBytes
	if n <= 0 || !t.haveInfo() {
		return nil
	}
	for r := range t.readers {
		off := r.waitingAt.Load()
		if off < 0 {
			continue
		}
		end := min(off+n, r.offset+r.length, t.length())
		if end <= off {
			continue
		}
		ret = append(ret, requestRange{t.requestIndexAtOffset(off), t.requestIndexAtOffset(end-1) + 1})
	}
	return
}

func isCriticalRequest(ranges []requestRange, r RequestIndex) bool {
	for _, rr := range ranges {
		if r >= rr.begin && r < rr.end {
			return true
		}
	}
	return false
}

func deliveryTime(requests int, chunkSize int, rate float64) time.Duration {
	if rate <= 0 {
		return unknownDelivery
	}
	return time.Duration(float64(requests) * float64(chunkSize) / rate * float64(time.Second))
}

// Expected time until holder q delivers r: the requests it got before r and hasn't delivered, plus
// r, at its recent rate. Also returns how many were ahead.
func (t *Torrent) heldRequestDelivery(q *PeerConn, r RequestIndex, now time.Time) (time.Duration, int) {
	ahead := 0
	// Iterates in the order the requests were added.
	q.requestState.Requests.Iterate(func(x RequestIndex) bool {
		if x == r {
			return false
		}
		ahead++
		return true
	})
	return deliveryTime(ahead+1, int(t.chunkSize), q.reqQueue.shortRate(now)), ahead
}

// An upper bound of heldRequestDelivery that doesn't walk the queue.
func (q *PeerConn) heldRequestDeliveryBound(now time.Time) time.Duration {
	return deliveryTime(int(q.requestState.Requests.GetCardinality()), int(q.t.chunkSize), q.reqQueue.shortRate(now))
}

// Expected time until p would deliver a request added now: its queue plus the request, at its
// recent rate, plus half a round trip for the request to reach it.
func (p *PeerConn) newRequestDelivery(now time.Time) time.Duration {
	d := deliveryTime(int(p.requestState.Requests.GetCardinality())+1, int(p.t.chunkSize), p.reqQueue.shortRate(now))
	if d == unknownDelivery {
		return d
	}
	return d + p.reqRTT.rtt/2
}

func criticalStealPays(mine, holder time.Duration) bool {
	if mine == unknownDelivery {
		return false
	}
	if holder == unknownDelivery {
		return true
	}
	return mine*criticalStealFactor <= holder && holder-mine >= criticalStealMinGain
}

// Whether holder's request r is overdue (see criticalOverdueMin), given its expected delivery.
func (t *Torrent) requestOverdue(holder *PeerConn, r RequestIndex, est time.Duration, now time.Time) (overdue bool, age time.Duration) {
	age = now.Sub(t.requestState[r].when)
	limit := max(criticalOverdueMin, 2*holder.reqRTT.rtt)
	if est != unknownDelivery {
		limit = max(limit, 2*est)
	}
	return age > limit, age
}

// Whether p should take critical request r from holder, and the two estimates. An overdue request's
// holder is expected to take at least as long again as the request has waited, whatever its queue
// says.
func (p *PeerConn) shouldStealCritical(holder *PeerConn, r RequestIndex, now time.Time) (ok bool, from, to time.Duration, overdue bool) {
	to = p.newRequestDelivery(now)
	if to == unknownDelivery {
		return false, 0, to, false
	}
	// Cheap refusal first, for a request too young to be overdue: the bound is never below the
	// exact estimate.
	if now.Sub(p.t.requestState[r].when) <= max(criticalOverdueMin, 2*holder.reqRTT.rtt) &&
		!criticalStealPays(to, holder.heldRequestDeliveryBound(now)) {
		return false, 0, to, false
	}
	from, _ = p.t.heldRequestDelivery(holder, r, now)
	overdue, age := p.t.requestOverdue(holder, r, from, now)
	if overdue && from != unknownDelivery {
		from = max(from, age)
	}
	return criticalStealPays(to, from), from, to, overdue
}

func estimateOrUnknown(d time.Duration) time.Duration {
	if d == unknownDelivery {
		return -1
	}
	return d
}

// What the chunk holding torrent offset off is waiting on. Called with the Client lock held.
func (t *Torrent) chunkWaitState(off int64, now time.Time) (ret ChunkWaitState) {
	r := t.requestIndexAtOffset(off)
	piece := t.pieceIndexOfRequestIndex(r)
	holder := t.requestingPeer(r)
	if holder != nil {
		ret.Requested = true
		ret.Holder, ret.HolderNetwork = remoteAddrString(&holder.Peer), holder.Network
		ret.RequestedAgo = now.Sub(t.requestState[r].when)
		var est time.Duration
		est, ret.Ahead = t.heldRequestDelivery(holder, r, now)
		ret.HolderEstimate = estimateOrUnknown(est)
		holder.requestState.Requests.Iterate(func(x RequestIndex) bool {
			if x == r {
				return false
			}
			if x > r {
				ret.AheadLater++
			}
			if t.pieceIndexOfRequestIndex(x) != piece {
				ret.AheadOtherPieces++
			}
			return true
		})
		ret.HolderRate = holder.reqQueue.shortRate(now)
	}
	best := unknownDelivery
	for c := range t.conns {
		if c == holder || c.closed.IsSet() || !c.peerHasPiece(piece) {
			continue
		}
		if c.peerChoking && !c.peerAllowedFast.Contains(piece) {
			continue
		}
		if d := c.newRequestDelivery(now); d < best {
			best = d
			ret.BestOther, ret.BestOtherNetwork = remoteAddrString(&c.Peer), c.Network
		}
	}
	ret.BestOtherEstimate = estimateOrUnknown(best)
	return
}

func remoteAddrString(p *Peer) string {
	if p.RemoteAddr == nil {
		return ""
	}
	return p.RemoteAddr.String()
}

// A blocked read's wait, from when it began.
type readerWait struct {
	start time.Time
	off   int64
	state ChunkWaitState
	// When the request last found overdue was made, so each request triggers one re-plan.
	replanned time.Time
}

// Marks r blocked on torrent offset off: records what the chunk is waiting on for
// Callbacks.ReaderWaited, and with ReaderCriticalBytes has every peer with the piece reconsider
// its requests, since nothing else does while the reader waits inside a piece it already gave
// priority.
func (r *reader) beginWait(off int64) *readerWait {
	t := r.t
	cfg := t.cl.config
	w := &readerWait{start: time.Now(), off: off}
	if cfg.ReaderCriticalBytes <= 0 && len(cfg.Callbacks.ReaderWaited) == 0 {
		return w
	}
	t.cl.lock()
	defer t.cl.unlock()
	if !t.haveInfo() || off >= t.length() {
		return w
	}
	r.waitingAt.Store(off)
	if len(cfg.Callbacks.ReaderWaited) != 0 {
		w.state = t.chunkWaitState(off, w.start)
	}
	if cfg.ReaderCriticalBytes > 0 && t.readerWaitMayNeedReplan(off, w.start) {
		t.clearWayForCritical(t.requestIndexAtOffset(off), w.start)
		t.replanPeersWithPieceAt(off)
	}
	return w
}

// Clears the way for a blocked read's chunk r on its holder. Peers serve requests in the order
// they got them, so a chunk that became urgent after the holder's queue filled waits behind it:
// after a seek, behind requests for the old position or for the rest of a file being downloaded
// whole (a resume in a 4K film waited behind 230 such requests, an estimated 10 s). The holder's
// requests ahead of r in less urgent pieces are cancelled, so r moves up (a peer drops cancelled
// requests it hasn't sent yet) and the cancelled ones return to the pool. A holder that has
// delivered nothing useful for criticalSnubAfter is snubbed instead, so every request it holds can
// move, not only the one the reader reached. Called with the Client lock held.
func (t *Torrent) clearWayForCritical(r RequestIndex, now time.Time) {
	holder := t.requestingPeer(r)
	if holder == nil {
		return
	}
	if !holder.snubbed && now.Sub(holder.lastUsefulChunkReceived) >= criticalSnubAfter && now.Sub(t.requestState[r].when) >= criticalSnubAfter {
		holder.snub()
		return
	}
	prio := t.piece(t.pieceIndexOfRequestIndex(r)).effectivePriority()
	var behind []RequestIndex
	holder.requestState.Requests.Iterate(func(x RequestIndex) bool {
		if x == r {
			return false
		}
		if t.piece(t.pieceIndexOfRequestIndex(x)).effectivePriority() < prio {
			behind = append(behind, x)
		}
		return true
	})
	for _, x := range behind {
		holder.cancel(x)
	}
}

func (t *Torrent) replanPeersWithPieceAt(off int64) {
	piece := pieceIndex(off / t.info.PieceLength)
	for c := range t.conns {
		if c.peerHasPiece(piece) {
			c.onNeedUpdateRequests(peerUpdateRequestsReaderWaitingReason)
		}
	}
}

// During a blocked read: if the chunk's request has become overdue, every peer with the piece
// reconsiders its requests (once per request), so a request the holder dropped can move.
func (r *reader) checkWaitOverdue(w *readerWait) {
	t := r.t
	t.cl.lock()
	defer t.cl.unlock()
	if !t.haveInfo() || w.off >= t.length() {
		return
	}
	req := t.requestIndexAtOffset(w.off)
	holder := t.requestingPeer(req)
	if holder == nil {
		return
	}
	now := time.Now()
	est, _ := t.heldRequestDelivery(holder, req, now)
	if overdue, _ := t.requestOverdue(holder, req, est, now); !overdue || w.replanned == t.requestState[req].when {
		return
	}
	w.replanned = t.requestState[req].when
	t.clearWayForCritical(req, now)
	t.replanPeersWithPieceAt(w.off)
}

// Whether a wait on torrent offset off is worth every peer reconsidering its requests: the chunk
// isn't requested, it is far enough back in its holder's queue that a steal could pay, or its
// request is overdue. A reader
// at the download frontier blocks many times a second on chunks about to arrive; those don't
// qualify, and the queue walk stops at the chunk, so it's short for them.
func (t *Torrent) readerWaitMayNeedReplan(off int64, now time.Time) bool {
	r := t.requestIndexAtOffset(off)
	holder := t.requestingPeer(r)
	if holder == nil {
		return true
	}
	est, _ := t.heldRequestDelivery(holder, r, now)
	overdue, _ := t.requestOverdue(holder, r, est, now)
	return est >= criticalStealMinGain || overdue
}

func (r *reader) endWait(w *readerWait) {
	r.waitingAt.Store(-1)
	waited := time.Since(w.start)
	if waited < readerWaitReportMin {
		return
	}
	for _, f := range r.t.cl.config.Callbacks.ReaderWaited {
		f(ReaderWaitedEvent{Torrent: r.t, Offset: w.off, Waited: waited, Wait: w.state})
	}
}

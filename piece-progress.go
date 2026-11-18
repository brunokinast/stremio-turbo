package torrent

import (
	"sync/atomic"
	"time"
)

// PieceProgress is a snapshot of a piece's download, for diagnostics.
type PieceProgress struct {
	Chunks int
	// Chunks received (written, or being written).
	DirtyChunks int
	// Chunks requested from a peer and not yet received, and how many peers hold them.
	Outstanding int
	Requesters  int
	Priority    PiecePriority
	// Peers known to have the piece.
	Availability int
	Complete     bool
}

// Returns the piece's progress. Requires the info.
func (t *Torrent) PieceProgress(piece int) (ret PieceProgress) {
	t.cl.rLock()
	defer t.cl.rUnlock()
	if !t.haveInfo() || piece < 0 || piece >= t.numPieces() {
		return
	}
	p := t.piece(piece)
	ret.Chunks = int(p.numChunks())
	ret.DirtyChunks = int(p.numDirtyChunks())
	ret.Priority = p.effectivePriority()
	ret.Availability = p.availability()
	ret.Complete = t.pieceComplete(piece)
	begin := t.pieceRequestIndexBegin(piece)
	var peers map[*PeerConn]struct{}
	for r := begin; r < begin+RequestIndex(ret.Chunks); r++ {
		rs, ok := t.requestState[r]
		if !ok {
			continue
		}
		ret.Outstanding++
		if pc := rs.peer.Value(); pc != nil {
			if peers == nil {
				peers = make(map[*PeerConn]struct{})
			}
			peers[pc] = struct{}{}
		}
	}
	ret.Requesters = len(peers)
	return
}

// PendingWriteWaits sums the time reads spent waiting for a piece to have no write in flight
// before reading it from storage. Responsive reads wait for writes anywhere in the piece, not just
// in the range they read.
type PendingWriteWaits struct {
	Count      int64
	Total, Max time.Duration
}

type pendingWriteWaitCounters struct {
	count, total, max atomic.Int64
}

func (c *pendingWriteWaitCounters) add(started time.Time) {
	d := int64(time.Since(started))
	c.count.Add(1)
	c.total.Add(d)
	for {
		m := c.max.Load()
		if d <= m || c.max.CompareAndSwap(m, d) {
			return
		}
	}
}

func (t *Torrent) PendingWriteWaitStats() PendingWriteWaits {
	c := &t.pendingWriteWaits
	return PendingWriteWaits{
		Count: c.count.Load(),
		Total: time.Duration(c.total.Load()),
		Max:   time.Duration(c.max.Load()),
	}
}

package torrent

import (
	"slices"
	"testing"
	"weak"

	"github.com/go-quicktest/qt"

	"github.com/anacrolix/torrent/metainfo"
)

const offsetFirstPieceLength = 256 << 10 // 16 chunks

// A torrent of 4 pieces with pieces 0 and 3 at Now, and a peer that has everything.
func offsetFirstSetup(t *testing.T, enabled bool) (*Torrent, *PeerConn) {
	cl := newTestingClient(t)
	cl.config.ReaderOffsetFirst = enabled
	tor, _ := cl.AddTorrentOpt(AddTorrentOpts{InfoHash: testingTorrentInfoHash, Storage: &storageClient{}})
	qt.Assert(t, qt.IsNil(tor.setInfoUnlocked(&metainfo.Info{
		Pieces:      make([]byte, 4*metainfo.HashSize),
		PieceLength: offsetFirstPieceLength,
		Length:      4 * offsetFirstPieceLength,
	})))
	p := cl.newConnection(nil, newConnectionOpts{network: "test"})
	p.setTorrent(tor)
	p.onPeerHasAllPiecesNoTriggers()
	p.peerChoking = false
	tor.cl.lock()
	for i := range tor.numPieces() {
		prio := PiecePriorityNormal
		if i == 0 || i == 3 {
			prio = PiecePriorityNow
		}
		tor.pieces[i].priority.Raise(prio)
		tor.updatePiecePriorityNoRequests(i)
	}
	tor.cl.unlock()
	return tor, p
}

// Registers a reader that has read up to torrent offset off.
func addPositionedReader(tor *Torrent, off int64) {
	r := &reader{t: tor, length: tor.length()}
	piece := pieceIndex(off / offsetFirstPieceLength)
	r.pieces = pieceRange{piece, piece + 1}
	r.torrentPos.Store(off)
	tor.cl.lock()
	if tor.readers == nil {
		tor.readers = map[*reader]struct{}{}
	}
	tor.readers[r] = struct{}{}
	tor.cl.unlock()
}

func sortedDesired(p *PeerConn) []RequestIndex {
	p.t.cl.lock()
	defer p.t.cl.unlock()
	d := p.getDesiredRequestState().Requests
	reqs := slices.Clone(d.requestIndexes)
	slices.SortStableFunc(reqs, func(a, b RequestIndex) int {
		if d.lessByValue(a, b) {
			return -1
		}
		if d.lessByValue(b, a) {
			return 1
		}
		return 0
	})
	return reqs
}

func req(piece, chunk int) RequestIndex { return RequestIndex(piece*16 + chunk) }

// A head reader at the start of piece 0 and a tail reader near the end of piece 3: their chunks
// interleave, each from its reader's offset, and the tail piece wraps to its start afterwards.
func TestReaderOffsetFirstInterleavesFromOffsets(t *testing.T) {
	tor, p := offsetFirstSetup(t, true)
	addPositionedReader(tor, 0)
	addPositionedReader(tor, 3*offsetFirstPieceLength+14*defaultChunkSize+100)
	reqs := sortedDesired(p)
	qt.Assert(t, qt.IsTrue(len(reqs) >= 6))
	pairs := [][]RequestIndex{{req(0, 0), req(3, 14)}, {req(0, 1), req(3, 15)}, {req(0, 2), req(3, 0)}}
	for i, want := range pairs {
		got := []RequestIndex{reqs[2*i], reqs[2*i+1]}
		slices.Sort(got)
		qt.Check(t, qt.DeepEquals(got, want), qt.Commentf("pair %d", i))
	}
}

// Without the option, one Now piece is requested in full, from its first chunk, before the other.
func TestReaderOffsetFirstOffKeepsPieceOrder(t *testing.T) {
	tor, p := offsetFirstSetup(t, false)
	addPositionedReader(tor, 0)
	addPositionedReader(tor, 3*offsetFirstPieceLength+14*defaultChunkSize)
	reqs := sortedDesired(p)
	first := tor.pieceIndexOfRequestIndex(reqs[0])
	for i := range 16 {
		qt.Check(t, qt.Equals(reqs[i], req(first, i)))
	}
}

func TestReaderStartChunksLowestReaderWins(t *testing.T) {
	tor, _ := offsetFirstSetup(t, true)
	addPositionedReader(tor, 2*offsetFirstPieceLength+9*defaultChunkSize)
	addPositionedReader(tor, 2*offsetFirstPieceLength+5*defaultChunkSize)
	tor.cl.lock()
	starts := tor.readerStartChunks()
	tor.cl.unlock()
	qt.Check(t, qt.DeepEquals(starts, map[pieceIndex]chunkIndexType{2: 5}))
	qt.Check(t, qt.Equals(tor.chunkDistanceFromStart(req(2, 4), 2, 5), chunkIndexType(15)))
	qt.Check(t, qt.Equals(tor.chunkDistanceFromStart(req(2, 7), 2, 5), chunkIndexType(2)))
}

func TestPieceProgressAndPendingWriteStats(t *testing.T) {
	tor, p := offsetFirstSetup(t, true)
	tor.cl.lock()
	tor.requestState[req(3, 2)] = requestState{peer: weak.Make(p)}
	tor.cl.unlock()
	pp := tor.PieceProgress(3)
	qt.Check(t, qt.Equals(pp.Chunks, 16))
	qt.Check(t, qt.Equals(pp.Outstanding, 1))
	qt.Check(t, qt.Equals(pp.Requesters, 1))
	qt.Check(t, qt.Equals(pp.Priority, PiecePriorityNow))
	tor.cl.lock()
	delete(tor.requestState, req(3, 2))
	tor.cl.unlock()
	qt.Check(t, qt.Equals(tor.PendingWriteWaitStats().Count, int64(0)))
}

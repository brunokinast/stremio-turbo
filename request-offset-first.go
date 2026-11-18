package torrent

import (
	g "github.com/anacrolix/generics"

	requestStrategy "github.com/anacrolix/torrent/internal/request-strategy"
	typedRoaring "github.com/anacrolix/torrent/typed-roaring"
)

// The chunk to request first in each piece a reader is positioned in, for
// ClientConfig.ReaderOffsetFirst: the chunk holding the reader's position (the lowest, when several
// readers share a piece). Readers that haven't read since their last seek don't count; they don't
// give their piece priority either. Nil when the option is off or no reader is reading.
func (t *Torrent) readerStartChunks() (ret map[pieceIndex]chunkIndexType) {
	if !t.cl.config.ReaderOffsetFirst || !t.haveInfo() {
		return nil
	}
	for r := range t.readers {
		if r.pieces.begin >= r.pieces.end {
			continue
		}
		off := r.torrentPos.Load()
		if off < 0 || off >= t.length() {
			continue
		}
		piece := pieceIndex(off / t.info.PieceLength)
		chunk := chunkIndexType((off - int64(piece)*t.info.PieceLength) / int64(t.chunkSize))
		if prev, ok := ret[piece]; ok && prev <= chunk {
			continue
		}
		g.MakeMapIfNil(&ret)
		ret[piece] = chunk
	}
	return
}

// How many chunks after start, wrapping within the piece, the request lies.
func (t *Torrent) chunkDistanceFromStart(r RequestIndex, piece pieceIndex, start chunkIndexType) chunkIndexType {
	n := t.pieceNumChunks(piece)
	chunk := chunkIndexType(r - t.pieceRequestIndexBegin(piece))
	return (chunk + n - start%n) % n
}

// Like iterUndirtiedRequestIndexesInPiece, but from chunk start to the piece end and then from the
// piece's first chunk, when haveStart.
func (t *Torrent) iterUndirtiedRequestIndexesInPieceFrom(
	it *typedRoaring.Iterator[RequestIndex],
	piece pieceIndex,
	start chunkIndexType,
	haveStart bool,
	f func(requestStrategy.RequestIndex),
) {
	if !haveStart || start == 0 {
		t.iterUndirtiedRequestIndexesInPiece(it, piece, f)
		return
	}
	begin := t.pieceRequestIndexBegin(piece)
	end := begin + t.pieceNumChunks(piece)
	mid := min(begin+start, end)
	it.Initialize(&t.dirtyChunks)
	iterBitmapUnsetInRange(it, mid, end, f)
	it.Initialize(&t.dirtyChunks)
	iterBitmapUnsetInRange(it, begin, mid, f)
}

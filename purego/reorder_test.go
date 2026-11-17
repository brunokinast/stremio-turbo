package purego

import (
	"testing"

	"github.com/go-quicktest/qt"
)

// Delivers a forged data packet to c, as if from its peer, sequenceAhead past the next one c expects.
func injectDataAhead(c *Conn, sequenceAhead uint16) {
	b := make([]byte, headerSize+10)
	c.s.mu.Lock()
	h := header{
		Type:    stData,
		Version: protocolVersion,
		ConnID:  c.connIDRecv,
		SeqNr:   c.ackNr + 1 + sequenceAhead,
		AckNr:   c.seqNr - 1,
	}
	addr := c.addr
	c.s.mu.Unlock()
	h.marshalTo(b)
	c.s.processReceivedMessage(b, addr)
}

// A data packet beyond the reorder buffer is counted as dropped; one inside it is buffered.
func TestReorderTooFarCounted(t *testing.T) {
	_, c2, stop := connPair(t, "inproc", "")
	defer stop()
	c := c2.(*Conn)
	injectDataAhead(c, 5)
	injectDataAhead(c, reorderBufferMaxSize+100)
	s := c.Stats()
	qt.Check(t, qt.Equals(s.ReorderInserts, uint64(1)))
	qt.Check(t, qt.Equals(s.ReorderCount, 1))
	qt.Check(t, qt.Equals(s.ReorderTooFar, uint64(1)))
	qt.Check(t, qt.Equals(s.DuplicatePackets, uint64(0)))
}

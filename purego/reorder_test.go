package purego

import (
	"io"
	"testing"
	"time"

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

// Delivers a forged data packet with n payload bytes, sequenceAhead past the next one c expects.
func injectPayloadAhead(c *Conn, sequenceAhead uint16, n int) {
	b := make([]byte, headerSize+n)
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

// Waits for the window advertised in an ack to reach want.
func waitAdvertised(t *testing.T, c *Conn, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		s := c.Stats()
		if s.LastRcvWindow == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("advertised window %d, want %d (read buffered %d, reorder bytes %d)", s.LastRcvWindow, want, s.ReadBuffered, s.ReorderBytes)
		}
		time.Sleep(time.Millisecond)
	}
}

// Bytes held behind a hole count against the advertised window, as in libtorrent, so a sender can't
// run past the reorder buffer while the hole is open.
func TestReorderBytesShrinkTheWindow(t *testing.T) {
	_, c2, stop := connPair(t, "inproc", "")
	defer stop()
	c := c2.(*Conn)
	const buf = 64 << 10
	const n = 1000
	c.SetReadBufferLen(buf)
	for _, ahead := range []uint16{1, 2, 3} {
		injectPayloadAhead(c, ahead, n)
	}
	qt.Assert(t, qt.Equals(c.Stats().ReorderBytes, 3*n))
	waitAdvertised(t, c, buf-3*n)

	// The hole arrives: everything moves to the read buffer.
	injectPayloadAhead(c, 0, n)
	s := c.Stats()
	qt.Check(t, qt.Equals(s.ReorderBytes, 0))
	qt.Check(t, qt.Equals(s.ReadBuffered, 4*n))
	waitAdvertised(t, c, buf-4*n)

	// Reading it all reopens the window.
	_, err := io.ReadFull(c2, make([]byte, 4*n))
	qt.Assert(t, qt.IsNil(err))
	waitAdvertised(t, c, buf)
}

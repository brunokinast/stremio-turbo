package purego

import (
	"crypto/rand"
	"io"
	"testing"
	"time"

	"github.com/go-quicktest/qt"
)

// A read that reopens the receive window tells the peer soon, even when no packet arrives to carry
// the update.
func TestWindowUpdateAfterReadIsSent(t *testing.T) {
	c1, c2, stop := connPair(t, "inproc", "")
	defer stop()
	const buf = 64 << 10
	recv := c2.(*Conn)
	recv.SetReadBufferLen(buf)
	// Less than the buffer, so the window never closes and the update after the read is the
	// scheduled kind.
	data := make([]byte, buf/2)
	_, err := rand.Read(data)
	qt.Assert(t, qt.IsNil(err))
	_, err = c1.Write(data)
	qt.Assert(t, qt.IsNil(err))
	// Wait until everything has arrived and been acked, so the sender is idle.
	deadline := time.Now().Add(5 * time.Second)
	for recv.Stats().ReadBuffered < len(data) {
		qt.Assert(t, qt.IsTrue(time.Now().Before(deadline)), qt.Commentf("data did not arrive"))
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	before := recv.Stats()
	qt.Assert(t, qt.IsTrue(before.LastRcvWindow < buf), qt.Commentf("advertised %d", before.LastRcvWindow))

	_, err = io.ReadFull(c2, make([]byte, len(data)))
	qt.Assert(t, qt.IsNil(err))
	deadline = time.Now().Add(200 * time.Millisecond)
	for {
		s := recv.Stats()
		if s.LastRcvWindow == buf {
			qt.Check(t, qt.Equals(s.WindowUpdatesScheduled, before.WindowUpdatesScheduled+1))
			qt.Check(t, qt.Equals(s.AcksSent, before.AcksSent+1))
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("window update not sent within 200 ms: advertised %d, %d acks sent (%d before the read)",
				s.LastRcvWindow, s.AcksSent, before.AcksSent)
		}
		time.Sleep(time.Millisecond)
	}
}

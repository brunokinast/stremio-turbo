package purego

import (
	"crypto/rand"
	"io"
	"testing"

	"github.com/go-quicktest/qt"
)

// A lossless in-process transfer: every data packet arrives in order, nothing is buffered out of
// order or duplicated, and both ends have measured a round trip.
func TestStatsAfterTransfer(t *testing.T) {
	c1, c2, stop := connPair(t, "inproc", "")
	defer stop()
	const n = 1 << 20
	want := make([]byte, n)
	_, err := rand.Read(want)
	qt.Assert(t, qt.IsNil(err))
	done := make(chan int64)
	go func() {
		got, _ := io.Copy(io.Discard, c2)
		done <- got
	}()
	_, err = c1.Write(want)
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.IsNil(c1.Close()))
	qt.Assert(t, qt.Equals(<-done, int64(n)))

	recv := c2.(*Conn).Stats()
	send := c1.(*Conn).Stats()
	// At least one packet per maximum payload; the exact count depends on MTU discovery.
	qt.Check(t, qt.IsTrue(recv.InOrderPackets >= n/packetSize), qt.Commentf("in order: %d", recv.InOrderPackets))
	qt.Check(t, qt.Equals(recv.DuplicatePackets, uint64(0)))
	qt.Check(t, qt.Equals(recv.ReorderInserts, uint64(0)))
	qt.Check(t, qt.Equals(recv.ReorderTooFar, uint64(0)))
	qt.Check(t, qt.Equals(recv.ReorderCount, 0))
	qt.Check(t, qt.IsTrue(recv.AcksSent > 0))
	qt.Check(t, qt.Equals(recv.SelectiveAcksSent, uint64(0)))
	qt.Check(t, qt.IsTrue(send.RTO > 0), qt.Commentf("sender rto: %v", send.RTO))
	c2.Close()
}

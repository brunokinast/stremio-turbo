package purego

import (
	"io"
	"testing"
	"time"

	"github.com/anacrolix/missinggo"
	"github.com/go-quicktest/qt"
)

// TestRTTWithoutSocketLock: RTT reads the smoothed RTT while the socket lock is held (as when the
// socket reader is inside the firewall callback), and agrees with Stats.
func TestRTTWithoutSocketLock(t *testing.T) {
	s1, err := NewSocket("udp", "localhost:0")
	qt.Assert(t, qt.IsNil(err))
	defer s1.Close()
	s2, err := NewSocket("udp", "localhost:0")
	qt.Assert(t, qt.IsNil(err))
	defer s2.Close()
	accepted := make(chan *Conn, 1)
	go func() {
		c, err := s2.Accept()
		qt.Check(t, qt.IsNil(err))
		accepted <- c.(*Conn)
	}()
	c1, err := s1.Dial(s2.Addr().String())
	qt.Assert(t, qt.IsNil(err))
	defer c1.Close()
	c2 := <-accepted
	defer c2.Close()
	go io.Copy(io.Discard, c2)
	_, err = io.CopyN(c1, missinggo.ZeroReader, 1<<20)
	qt.Assert(t, qt.IsNil(err))
	uc := c1.(*Conn)
	qt.Assert(t, qt.Equals(uc.RTT(), uc.Stats().RTT))

	s1.mu.Lock()
	got := make(chan time.Duration, 1)
	go func() { got <- uc.RTT() }()
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("RTT blocked on the socket lock")
	}
	s1.mu.Unlock()
}

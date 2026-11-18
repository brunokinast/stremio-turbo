// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package engine

import (
	"net"
	"runtime"
	"testing"
	"time"
)

// A loopback TCP connection reports a small positive RTT where the platform
// exposes one.
func TestConnRTTOverLoopbackTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			accepted <- c
		}
	}()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s := <-accepted
	defer s.Close()
	// Exchange a little data so the kernel has RTT samples.
	for range 5 {
		if _, err := c.Write([]byte("ping")); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 4)
		if _, err := s.Read(buf); err != nil {
			t.Fatal(err)
		}
	}
	rtt, ok := connRTT(c)
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		if ok {
			t.Fatalf("unexpected RTT %v", rtt)
		}
		return
	}
	if !ok || rtt <= 0 || rtt > time.Second {
		t.Fatalf("connRTT = %v, %v", rtt, ok)
	}
	if _, ok := connRTT(nil); ok {
		t.Fatal("nil conn reported an RTT")
	}
}

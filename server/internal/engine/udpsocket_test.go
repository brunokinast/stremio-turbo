// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package engine

import (
	"errors"
	"net"
	"testing"
)

type bufferRecordingConn struct {
	net.PacketConn
	set []int
	err error
}

func (c *bufferRecordingConn) SetReadBuffer(n int) error {
	c.set = append(c.set, n)
	return c.err
}

func TestBufferedListenPacketSetsReadBuffer(t *testing.T) {
	for _, setErr := range []error{nil, errors.New("not permitted")} {
		rc := &bufferRecordingConn{err: setErr}
		lp := bufferedListenPacket(func(network, addr string) (net.PacketConn, error) {
			if network != "udp4" || addr != ":0" {
				t.Fatalf("listen(%q, %q)", network, addr)
			}
			return rc, nil
		})
		pc, err := lp("udp4", ":0")
		if err != nil || pc != rc {
			t.Fatalf("got %v, %v; want the listened conn (set error %v)", pc, err, setErr)
		}
		if len(rc.set) != 1 || rc.set[0] != udpReadBuffer {
			t.Fatalf("SetReadBuffer calls = %v; want [%d]", rc.set, udpReadBuffer)
		}
	}
}

func TestBufferedListenPacketPassesListenErrors(t *testing.T) {
	want := errors.New("address in use")
	lp := bufferedListenPacket(func(string, string) (net.PacketConn, error) { return nil, want })
	if _, err := lp("udp", ":1"); !errors.Is(err, want) {
		t.Fatalf("err = %v; want %v", err, want)
	}
}

// A real socket accepts the size (Windows and Linux both allow 4 MiB to an
// unprivileged process; Linux may cap it at rmem_max, which is not an error).
func TestBufferedListenPacketRealSocket(t *testing.T) {
	pc, err := bufferedListenPacket(net.ListenPacket)("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	if err := pc.(*net.UDPConn).SetReadBuffer(udpReadBuffer); err != nil {
		t.Fatalf("SetReadBuffer(%d): %v", udpReadBuffer, err)
	}
}

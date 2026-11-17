// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package engine

import (
	"net"
	"sync"

	"github.com/rivulet-kodi/rivulet-engine/internal/logging"
)

// udpReadBuffer is the OS receive buffer for the uTP (and DHT) socket. The
// default (64 KiB on Windows) overflows when a far peer's burst arrives while
// the socket reader is busy: the lost packets then cost a round trip each to
// repair. In the lab, loss clumps after the relay appeared in 9 of 13 runs with
// the default buffer and in none of 8 with 4 MiB, and Windows did not count the
// drops as receive errors (streaming-research findings/10 §C).
const udpReadBuffer = 4 << 20

// readBufferSetter is the part of *net.UDPConn the buffer needs.
type readBufferSetter interface {
	SetReadBuffer(bytes int) error
}

// bufferedListenPacket wraps listen so that every UDP socket it opens gets a
// udpReadBuffer receive buffer. Failing to grow the buffer is logged once and
// the socket is used as it is.
func bufferedListenPacket(listen func(network, addr string) (net.PacketConn, error)) func(network, addr string) (net.PacketConn, error) {
	var warn sync.Once
	return func(network, addr string) (net.PacketConn, error) {
		pc, err := listen(network, addr)
		if err != nil {
			return nil, err
		}
		if s, ok := pc.(readBufferSetter); ok {
			if err := s.SetReadBuffer(udpReadBuffer); err != nil {
				warn.Do(func() {
					logging.For("engine").Warn("could not grow the UDP receive buffer", "bytes", udpReadBuffer, "err", err)
				})
			}
		}
		return pc, nil
	}
}

// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package engine

import (
	"net"
	"time"

	"github.com/anacrolix/go-utp/purego"
)

// connRTT is anacrolix's PeerConnRTT: a peer connection's transport round
// trip, which sizes its request queue (ClientConfig.RequestQueueRTTs). uTP
// measures it on every packet; for TCP the kernel's smoothed RTT is read where
// the platform exposes it (tcpRTT).
func connRTT(c net.Conn) (time.Duration, bool) {
	switch c := c.(type) {
	case *purego.Conn:
		rtt := c.Stats().RTT
		return rtt, rtt > 0
	case *net.TCPConn:
		return tcpRTT(c)
	}
	return 0, false
}

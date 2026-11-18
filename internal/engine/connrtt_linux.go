// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package engine

import (
	"net"
	"time"

	"golang.org/x/sys/unix"
)

// tcpRTT reads the socket's smoothed RTT with TCP_INFO.
func tcpRTT(c *net.TCPConn) (time.Duration, bool) {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, false
	}
	var info *unix.TCPInfo
	var ioErr error
	err = raw.Control(func(fd uintptr) {
		info, ioErr = unix.GetsockoptTCPInfo(int(fd), unix.IPPROTO_TCP, unix.TCP_INFO)
	})
	if err != nil || ioErr != nil || info.Rtt == 0 {
		return 0, false
	}
	return time.Duration(info.Rtt) * time.Microsecond, true
}

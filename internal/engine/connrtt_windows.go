// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package engine

import (
	"net"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// SIO_TCP_INFO (mstcpip.h, Windows 10 1703 and later): _WSAIORW(IOC_VENDOR, 39).
const sioTCPInfo = 0xD8000027

// tcpInfoV0 is mstcpip.h's TCP_INFO_v0; Go's alignment matches the C layout.
type tcpInfoV0 struct {
	State             uint32
	Mss               uint32
	ConnectionTimeMs  uint64
	TimestampsEnabled uint8
	RttUs             uint32
	MinRttUs          uint32
	BytesInFlight     uint32
	Cwnd              uint32
	SndWnd            uint32
	RcvWnd            uint32
	RcvBuf            uint32
	BytesOut          uint64
	BytesIn           uint64
	BytesReordered    uint32
	BytesRetrans      uint32
	FastRetrans       uint32
	DupAcksIn         uint32
	TimeoutEpisodes   uint32
	SynRetrans        uint8
}

// tcpRTT reads the socket's smoothed RTT with SIO_TCP_INFO.
func tcpRTT(c *net.TCPConn) (time.Duration, bool) {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, false
	}
	var info tcpInfoV0
	var ioErr error
	err = raw.Control(func(fd uintptr) {
		version := uint32(0)
		var n uint32
		ioErr = windows.WSAIoctl(windows.Handle(fd), sioTCPInfo,
			(*byte)(unsafe.Pointer(&version)), uint32(unsafe.Sizeof(version)),
			(*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), &n, nil, 0)
	})
	if err != nil || ioErr != nil || info.RttUs == 0 {
		return 0, false
	}
	return time.Duration(info.RttUs) * time.Microsecond, true
}

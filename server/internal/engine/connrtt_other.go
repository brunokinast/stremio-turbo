// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

//go:build !(linux || windows)

package engine

import (
	"net"
	"time"

	"github.com/rivulet-kodi/rivulet-engine/internal/types"
)

// tcpRTT is unknown on this platform; the request queue then keeps
// RequestQueueTime for TCP peers.
func tcpRTT(*net.TCPConn) (time.Duration, bool) { return 0, false }

// tcpSample: no kernel TCP state in the trace on this platform.
func tcpSample(*net.TCPConn) *types.TCPSample { return nil }

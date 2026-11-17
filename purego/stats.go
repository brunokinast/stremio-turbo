package purego

import "time"

// connCounters are the receive and ack counters a Conn keeps for Stats. They are plain integers:
// every update and every read happens under the socket mutex.
type connCounters struct {
	inOrderPackets    uint64
	duplicatePackets  uint64
	reorderInserts    uint64
	reorderTooFar     uint64
	reorderPeak       uint16
	acksSent          uint64
	selectiveAcksSent uint64
	duplicateAcksSent uint64
	windowUpdatesNow  uint64
	windowUpdatesLate uint64

	lastAckNr   uint16
	haveLastAck bool
}

// ConnStats is a snapshot of one connection's receive path and congestion state. It is meant for
// diagnostics: telling path loss, receive-buffer loss and slow recovery apart on a live transfer.
type ConnStats struct {
	// Data packets that arrived in order and were delivered to the read buffer.
	InOrderPackets uint64
	// Data packets we already had: behind the ack point, or already in the reorder buffer.
	// On a lossless path every one of them is a needless resend by the peer.
	DuplicatePackets uint64
	// Data packets that arrived ahead of a hole and were buffered.
	ReorderInserts uint64
	// Data packets dropped because they were too far ahead of the ack point to buffer.
	ReorderTooFar uint64
	// The most packets that sat in the reorder buffer at once.
	ReorderPeak int
	// Packets currently in the reorder buffer.
	ReorderCount int

	// State packets sent, those carrying a selective ack, and those that repeated the previous
	// ack number without a selective ack (a sender may count these as duplicate acks).
	AcksSent          uint64
	SelectiveAcksSent uint64
	DuplicateAcksSent uint64
	// Receive-window updates after reads: sent at once (the window had been closed), or
	// scheduled to ride on the next ack.
	WindowUpdatesImmediate uint64
	WindowUpdatesScheduled uint64

	// Smoothed round-trip time, its variance and the retransmit timeout.
	RTT, RTTVar, RTO time.Duration
	// Bytes in flight and the congestion window, in bytes.
	CurWindow, MaxWindow int
	// The receive window last advertised to the peer, and the bytes waiting to be read.
	LastRcvWindow, ReadBuffered int
}

// Stats returns a snapshot of the connection's counters and congestion state.
func (c *Conn) Stats() ConnStats {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	k := &c.stats
	return ConnStats{
		InOrderPackets:         k.inOrderPackets,
		DuplicatePackets:       k.duplicatePackets,
		ReorderInserts:         k.reorderInserts,
		ReorderTooFar:          k.reorderTooFar,
		ReorderPeak:            int(k.reorderPeak),
		ReorderCount:           int(c.reorderCount),
		AcksSent:               k.acksSent,
		SelectiveAcksSent:      k.selectiveAcksSent,
		DuplicateAcksSent:      k.duplicateAcksSent,
		WindowUpdatesImmediate: k.windowUpdatesNow,
		WindowUpdatesScheduled: k.windowUpdatesLate,
		RTT:                    time.Duration(c.rtt) * time.Millisecond,
		RTTVar:                 time.Duration(c.rttVar) * time.Millisecond,
		RTO:                    time.Duration(c.rto) * time.Millisecond,
		CurWindow:              c.curWindow,
		MaxWindow:              c.maxWindow,
		LastRcvWindow:          c.lastRcvWin,
		ReadBuffered:           c.readBuf.len(),
	}
}

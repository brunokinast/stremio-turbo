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
	// Packets currently in the reorder buffer, and their payload bytes (which the advertised
	// window leaves out).
	ReorderCount int
	ReorderBytes int
	// How far the reorder buffer reaches past the next expected packet, in packets.
	ReorderSpan int
	// Moving average of the peer's data payload size, in bytes: 528 is libtorrent's MTU floor,
	// which it keeps until one of its MTU probes is acked.
	PayloadEstimate int

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

// RTT is the smoothed round-trip time (0 before the first sample). Unlike Stats it doesn't take
// the socket lock, so it's safe to call while holding a lock the socket's callbacks take: the
// socket reader holds the socket lock while it asks the firewall callback, which in anacrolix
// takes the client lock, so reading Stats under the client lock deadlocks.
func (c *Conn) RTT() time.Duration {
	return time.Duration(c.rttNow.Load()) * time.Millisecond
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
		ReorderBytes:           c.reorderBytes,
		ReorderSpan:            int(c.reorderSpan()),
		PayloadEstimate:        c.payloadEst,
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

// socketCounters are the Socket-wide counters behind Counters, updated under the socket mutex.
type socketCounters struct {
	SocketCounters
	lastWriteErr error
}

// SocketCounters is a snapshot of a Socket's packet and handshake counters. It is meant for
// diagnosing outgoing connections that never complete: whether the SYN left, whether anything
// came back from the peer, and whether a reply arrived that matched no connection.
type SocketCounters struct {
	// Datagrams written, and writes that failed (LastWriteError holds the latest error).
	Writes, WriteErrors uint64
	LastWriteError      string
	// uTP datagrams received (anything parsePacket accepted).
	Received uint64
	// Outgoing connections started (first SYN), SYNs resent after a timeout, SYN-ACKs that
	// completed an outgoing connection, and outgoing connections that gave up with no reply.
	SynsSent, SynResends, SynAcks, SynTimeouts uint64
	// Resets received, those that matched a connection, and those that refused a connection
	// still in syn-sent.
	Resets, ResetsMatched, ResetsRefused uint64
	// Non-SYN packets matching no connection, and those among them from an IP we have an
	// outgoing connection in syn-sent to (a reply from another port, or with another ID).
	Unmatched, UnmatchedFromDialedIP uint64
	// Packets dropped because their ack number was outside the window, and those dropped
	// while the connection was still in syn-sent (a SYN-ACK we refused).
	AckOutOfRange, AckOutOfRangeSynSent uint64
	// Incoming connections: SYNs that created one.
	IncomingSyns uint64
}

// Counters returns a snapshot of the Socket's counters.
func (s *Socket) Counters() SocketCounters {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.counters.SocketCounters
	if s.counters.lastWriteErr != nil {
		c.LastWriteError = s.counters.lastWriteErr.Error()
	}
	return c
}

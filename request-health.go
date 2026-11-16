package torrent

import (
	"time"
)

// Request health, modelled on libtorrent: per-peer request queues sized by measured rate
// (ClientConfig.RequestQueueTime), snubbing of peers that stop delivering
// (ClientConfig.PieceTimeout), and a periodic retry of unchoked peers left without requests.
//
// Without these, on large pieces and thin swarms (typical of 4K streaming), a fast peer could
// queue nearly a whole 16 MiB piece, a stalled peer kept its blocks indefinitely, and a peer that
// once rejected requests was not asked again until it re-unchoked us.

const (
	defaultRequestQueueTime = 3 * time.Second
	defaultPieceTimeout     = 20 * time.Second
	// How often connections are checked. Cheap: a few field reads per connection, and a request
	// update only for peers that need one.
	requestHealthInterval = time.Second
	// Minimum time since a peer's last request update before an idle retry.
	idleRetryAfter = 3 * time.Second
	// Floor of the rate-based queue, so a slow peer still has a request in flight while the
	// previous one is answered (libtorrent's min_request_queue).
	minRateRequestQueue maxRequests = 2
)

// rateRequestQueue returns the request queue that covers queueTime of a peer's download rate, and
// whether it applies (a rate is known and the cap is enabled).
func rateRequestQueue(rate float64, queueTime time.Duration, chunkSize int) (maxRequests, bool) {
	if queueTime <= 0 || rate <= 0 || chunkSize <= 0 {
		return 0, false
	}
	n := rate * queueTime.Seconds() / float64(chunkSize)
	if n > float64(maxLocalToRemoteRequests) {
		return maxLocalToRemoteRequests, true
	}
	return max(minRateRequestQueue, maxRequests(n)), true
}

func (cl *Client) requestHealthTimerFunc() {
	if cl.closed.IsSet() {
		return
	}
	cl.lock()
	now := time.Now()
	for t := range cl.torrents {
		t.checkRequestHealth(now)
	}
	cl.unlock()
	if !cl.closed.IsSet() {
		cl.requestHealthTimer.Reset(requestHealthInterval)
	}
}

func (t *Torrent) checkRequestHealth(now time.Time) {
	if !t.haveInfo() || t.closed.IsSet() {
		return
	}
	pieceTimeout := t.cl.config.PieceTimeout
	for c := range t.conns {
		if c.closed.IsSet() {
			continue
		}
		if pieceTimeout > 0 && c.deliveryOverdue(now, pieceTimeout) {
			c.snub()
			continue
		}
		if c.idleButUseful(now) {
			c.onNeedUpdateRequests(peerUpdateRequestsIdleReason)
		}
	}
}

// deliveryOverdue reports whether the peer has had a request outstanding for longer than timeout
// and delivered no useful chunk in that time.
func (c *PeerConn) deliveryOverdue(now time.Time, timeout time.Duration) bool {
	if c.snubbed || c.requestState.Requests.IsEmpty() || now.Sub(c.lastUsefulChunkReceived) < timeout {
		return false
	}
	overdue := false
	c.requestState.Requests.Iterate(func(r RequestIndex) bool {
		if now.Sub(c.t.requestState[r].when) >= timeout {
			overdue = true
			return false
		}
		return true
	})
	return overdue
}

// snub cancels the peer's requests so other peers can take them, and limits it to one request
// until it delivers a useful chunk again.
func (c *PeerConn) snub() {
	c.snubbed = true
	c.cancelAllRequests()
	c.t.iterPeers(func(p *Peer) {
		if p != &c.Peer {
			p.onNeedUpdateRequests(peerUpdateRequestsSnubbedReason)
		}
	})
}

// idleButUseful reports whether the peer is unchoking us and has pieces we want, but has no
// requests and has not had a request update for a while: typically after it rejected requests,
// which otherwise are not retried until it unchokes us again.
func (c *PeerConn) idleButUseful(now time.Time) bool {
	return !c.peerChoking &&
		c.requestState.Interested &&
		c.isLowOnRequests() &&
		c.needRequestUpdate == "" &&
		now.Sub(c.lastRequestUpdate) >= idleRetryAfter
}

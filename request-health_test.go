package torrent

import (
	"net"
	"testing"
	"time"

	qt "github.com/go-quicktest/qt"
)

func TestRateRequestQueue(t *testing.T) {
	const chunk = 16 << 10
	for _, c := range []struct {
		name      string
		rate      float64
		queueTime time.Duration
		want      maxRequests
		applies   bool
	}{
		{"disabled", 1 << 20, 0, 0, false},
		{"no rate yet", 0, 3 * time.Second, 0, false},
		// 500 KiB/s for 3 s is about 94 chunks: enough to stay busy, not a whole 16 MiB piece.
		{"fast peer keeps about 3 s queued", 500 << 10, 3 * time.Second, 93, true},
		{"slow peer keeps the floor", 13 << 10, 3 * time.Second, minRateRequestQueue, true},
		{"capped at the local limit", 1 << 30, 3 * time.Second, maxLocalToRemoteRequests, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, ok := rateRequestQueue(c.rate, c.queueTime, chunk)
			qt.Check(t, qt.Equals(ok, c.applies))
			if c.applies {
				qt.Check(t, qt.Equals(got, c.want))
			}
		})
	}
}

// testHealthConn builds the minimum connection the request-health checks read.
func testHealthConn(cfg *ClientConfig) *PeerConn {
	tor := &Torrent{
		cl:           &Client{config: cfg},
		chunkSize:    16 << 10,
		requestState: map[RequestIndex]requestState{},
	}
	c := &PeerConn{}
	c.initRequestState()
	c.t = tor
	c.PeerMaxRequests = 2000
	c.peakRequests = 1000
	return c
}

func TestNominalMaxRequestsUsesRateAndSnub(t *testing.T) {
	cfg := &ClientConfig{RequestQueueTime: 3 * time.Second}
	c := testHealthConn(cfg)
	// No rate measured yet: slow start (twice the peak, within the local write-buffer limit).
	qt.Check(t, qt.Equals(c.nominalMaxRequests(), maxLocalToRemoteRequests))

	// 1 MiB delivered over 2 s of expecting chunks: 512 KiB/s, so 3 s is 96 chunks.
	c._stats.BytesReadUsefulData.Add(1 << 20)
	c.cumulativeExpectedToReceiveChunks = 2 * time.Second
	qt.Check(t, qt.Equals(c.nominalMaxRequests(), maxRequests(96)))

	c.snubbed = true
	qt.Check(t, qt.Equals(c.nominalMaxRequests(), maxRequests(1)))

	cfg.RequestQueueTime = 0
	c.snubbed = false
	qt.Check(t, qt.Equals(c.nominalMaxRequests(), maxLocalToRemoteRequests))
}

func TestDeliveryOverdue(t *testing.T) {
	const timeout = 20 * time.Second
	now := time.Now()
	for _, c := range []struct {
		name       string
		requestAge time.Duration // 0: no request outstanding
		lastChunk  time.Duration // how long ago the last useful chunk arrived
		snubbed    bool
		want       bool
	}{
		{"no requests", 0, time.Hour, false, false},
		{"request not yet overdue", 5 * time.Second, time.Hour, false, false},
		{"recent chunk keeps the peer healthy", time.Minute, time.Second, false, false},
		{"overdue request and no recent chunk", 21 * time.Second, time.Hour, false, true},
		{"already snubbed", time.Minute, time.Hour, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			pc := testHealthConn(&ClientConfig{PieceTimeout: timeout})
			pc.lastUsefulChunkReceived = now.Add(-c.lastChunk)
			pc.snubbed = c.snubbed
			if c.requestAge > 0 {
				const r RequestIndex = 3
				pc.requestState.Requests.Add(r)
				pc.t.requestState[r] = requestState{when: now.Add(-c.requestAge)}
			}
			qt.Check(t, qt.Equals(pc.deliveryOverdue(now, timeout), c.want))
		})
	}
}

func TestIdleButUseful(t *testing.T) {
	now := time.Now()
	idle := func() *PeerConn {
		pc := testHealthConn(&ClientConfig{})
		pc.peerChoking = false
		pc.requestState.Interested = true
		pc.lastRequestUpdate = now.Add(-time.Minute)
		return pc
	}
	qt.Check(t, qt.IsTrue(idle().idleButUseful(now)))

	choking := idle()
	choking.peerChoking = true
	qt.Check(t, qt.IsFalse(choking.idleButUseful(now)))

	notInterested := idle()
	notInterested.requestState.Interested = false
	qt.Check(t, qt.IsFalse(notInterested.idleButUseful(now)))

	busy := idle()
	busy.requestState.Requests.Add(1)
	qt.Check(t, qt.IsFalse(busy.idleButUseful(now)))

	justUpdated := idle()
	justUpdated.lastRequestUpdate = now.Add(-time.Second)
	qt.Check(t, qt.IsFalse(justUpdated.idleButUseful(now)))

	pending := idle()
	pending.needRequestUpdate = peerUpdateRequestsIdleReason
	qt.Check(t, qt.IsFalse(pending.idleButUseful(now)))
}

func TestNewDefaultClientConfigRequestHealth(t *testing.T) {
	cfg := NewDefaultClientConfig()
	qt.Check(t, qt.Equals(cfg.RequestQueueTime, defaultRequestQueueTime))
	qt.Check(t, qt.Equals(cfg.PieceTimeout, defaultPieceTimeout))
}

func TestOutgoingConnResultCallback(t *testing.T) {
	cfg := TestingConfig(t)
	results := make(chan OutgoingConnResultEvent, 4)
	cfg.Callbacks.OutgoingConnResult = append(cfg.Callbacks.OutgoingConnResult, func(ev OutgoingConnResultEvent) {
		results <- ev
	})
	cl, err := NewClient(cfg)
	qt.Assert(t, qt.IsNil(err))
	defer cl.Close()
	tor, _ := cl.AddTorrentInfoHash(testingTorrentInfoHash)
	// Nothing listens on port 1, so the dial fails quickly.
	addr := ipPortAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}
	tor.AddPeers([]PeerInfo{{Addr: addr, Source: PeerSourceDirect}})
	select {
	case ev := <-results:
		qt.Check(t, qt.Equals(ev.InfoHash, testingTorrentInfoHash))
		qt.Check(t, qt.Equals(ev.Peer.Addr.String(), addr.String()))
		qt.Check(t, qt.Equals(ev.Peer.Source, PeerSourceDirect))
		qt.Check(t, qt.IsNotNil(ev.Err))
		qt.Check(t, qt.Equals(ev.Network, ""))
		qt.Check(t, qt.IsFalse(ev.Started.IsZero()))
	case <-time.After(30 * time.Second):
		t.Fatal("no OutgoingConnResult event")
	}
}

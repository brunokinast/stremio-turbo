package torrent

import (
	"testing"
	"time"

	"github.com/go-quicktest/qt"
)

const testChunk = 16 << 10

// Delivers n chunks spread evenly over d, starting at start.
func deliver(q *peerRequestQueue, start time.Time, d time.Duration, n int) time.Time {
	for i := range n {
		q.onUsefulChunk(start.Add(d*time.Duration(i)/time.Duration(n)), testChunk, 32)
	}
	return start.Add(d)
}

func TestRequestQueueSlowStartGrowsPerChunk(t *testing.T) {
	var q peerRequestQueue
	now := time.Unix(1000, 0)
	qt.Check(t, qt.Equals(q.nominal(now, 32, 3*time.Second, testChunk), maxRequests(32)))
	// Rising rate: 10, 20, 40 chunks in successive seconds; still in slow start, +1 per chunk.
	now = deliver(&q, now, time.Second, 10)
	now = deliver(&q, now, time.Second, 20)
	now = deliver(&q, now, time.Second, 40)
	qt.Check(t, qt.IsTrue(q.slowStart))
	qt.Check(t, qt.Equals(q.nominal(now, 32, 3*time.Second, testChunk), maxRequests(32+70)))
}

func TestRequestQueueLeavesSlowStartWhenRateIsFlat(t *testing.T) {
	var q peerRequestQueue
	now := time.Unix(1000, 0)
	now = deliver(&q, now, time.Second, 64)
	now = deliver(&q, now, time.Second, 64)
	now = deliver(&q, now, time.Second, 64)
	now = deliver(&q, now, time.Second, 64)
	q.onUsefulChunk(now, testChunk, 32) // a chunk in a new second triggers the check
	qt.Assert(t, qt.IsFalse(q.slowStart))
	// 64 chunks/s = 1 MiB/s; 3 s of that is 192 chunks.
	got := q.nominal(now, 32, 3*time.Second, testChunk)
	qt.Check(t, qt.IsTrue(got >= 190 && got <= 195), qt.Commentf("limit %d", got))
}

func TestRequestQueueFloorAndSilence(t *testing.T) {
	var q peerRequestQueue
	now := time.Unix(1000, 0)
	// A slow peer: one chunk a second, flat, so slow start ends with a tiny rate.
	for range 5 {
		now = deliver(&q, now, time.Second, 1)
	}
	q.onUsefulChunk(now, testChunk, 32)
	qt.Assert(t, qt.IsFalse(q.slowStart))
	// 16 KiB/s × 3 s = 3 chunks, floored at 4 while it is delivering.
	qt.Check(t, qt.Equals(q.nominal(now, 32, 3*time.Second, testChunk), minRecentRequestQueue))
	// Silent for longer than the window: the rate is 0 and the floor drops to 2.
	later := now.Add(6 * time.Second)
	qt.Check(t, qt.Equals(q.shortRate(later), 0.0))
	qt.Check(t, qt.Equals(q.nominal(later, 32, 3*time.Second, testChunk), minRateRequestQueue))
	// Without a rate cap, the queue stays where slow start left it.
	qt.Check(t, qt.Equals(q.nominal(later, 32, 0, testChunk), q.limit))
}

func TestRequestQueueShortRateYoungConnection(t *testing.T) {
	var q peerRequestQueue
	now := time.Unix(1000, 0)
	// 2 MiB over the first 2 s: 1 MiB/s, not diluted over the 5 s window.
	now = deliver(&q, now, 2*time.Second, 128)
	qt.Check(t, qt.Equals(q.shortRate(now), float64(1<<20)))
}

func TestRequestQueueReset(t *testing.T) {
	var q peerRequestQueue
	now := deliver(&q, time.Unix(1000, 0), time.Second, 50)
	qt.Check(t, qt.Equals(q.limit, maxRequests(32+50)))
	q.reset(32)
	qt.Check(t, qt.Equals(q.nominal(now, 32, 3*time.Second, testChunk), maxRequests(32)))
	qt.Check(t, qt.IsTrue(q.slowStart))
}

func testQueueConn(cfg *ClientConfig) *PeerConn {
	c := testHealthConn(cfg)
	c.PeerMaxRequests = 250
	return c
}

// With InitialRequestQueue, the limit comes from the queue sizing, capped by the peer's reqq; the
// snub still wins. Without it, nothing changes (see TestNominalMaxRequestsUsesRateAndSnub).
func TestNominalMaxRequestsInitialQueue(t *testing.T) {
	cfg := &ClientConfig{RequestQueueTime: 3 * time.Second, InitialRequestQueue: 32}
	c := testQueueConn(cfg)
	qt.Check(t, qt.Equals(c.nominalMaxRequests(), maxRequests(32)))
	c.reqQueue.limit = 400
	qt.Check(t, qt.Equals(c.nominalMaxRequests(), maxRequests(250)))
	c.snubbed = true
	qt.Check(t, qt.Equals(c.nominalMaxRequests(), maxRequests(1)))
}

func addTestRequests(c *PeerConn, n int) {
	for i := range n {
		c.requestState.Requests.Add(RequestIndex(i))
	}
}

func TestLowWaterRefill(t *testing.T) {
	cfg := &ClientConfig{InitialRequestQueue: 32, RequestLowWater: 0.5}
	c := testQueueConn(cfg)
	addTestRequests(c, 17)
	qt.Check(t, qt.IsFalse(c.isLowOnRequests())) // 17 of 32
	c.requestState.Requests.CheckedRemove(16)
	qt.Check(t, qt.IsTrue(c.isLowOnRequests())) // 16 of 32
	// A chunk right after an update doesn't re-plan; one 100 ms later does.
	c.lastRequestUpdate = time.Now()
	qt.Check(t, qt.IsFalse(c.chunkShouldUpdateRequests()))
	c.lastRequestUpdate = time.Now().Add(-lowWaterUpdateInterval)
	qt.Check(t, qt.IsTrue(c.chunkShouldUpdateRequests()))
	// An empty queue always refills.
	c.initRequestState()
	c.lastRequestUpdate = time.Now()
	qt.Check(t, qt.IsTrue(c.chunkShouldUpdateRequests()))

	// Default: only an empty queue is low.
	cfg.RequestLowWater = 0
	addTestRequests(c, 1)
	qt.Check(t, qt.IsFalse(c.isLowOnRequests()))
	c.lastRequestUpdate = time.Time{}
	qt.Check(t, qt.IsFalse(c.chunkShouldUpdateRequests()))
}

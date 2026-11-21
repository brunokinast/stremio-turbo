package torrent

import (
	"time"
)

// Per-peer request queue sizing for ClientConfig.InitialRequestQueue, modelled on libtorrent's slow
// start: a new or newly unchoked peer gets InitialRequestQueue requests at once, the queue grows by
// one request per useful block received (so it doubles every round trip) until the peer's rate
// stops rising, and from then on it covers RequestQueueTime of the peer's recent rate.
//
// Without it the queue only doubles each time it drains completely (twice the requests added at
// the previous update), starting from 2: at 300 ms RTT it took ~15 s to grow from 2 to ~200 and
// ~11 s to reach full speed, and the rate cap computed from the connection's lifetime average kept
// a slowly started peer's queue short.

const (
	// The window of the short-term rate.
	shortRateBuckets = 5
	// A slow-start second counts as flat when its bytes are at most this much above the previous
	// second's. Two flat seconds in a row end slow start.
	slowStartGrowth = 1.1
	slowStartFlat   = 2
	// Floor of the rate-based queue for a peer that delivered in the last shortRateBuckets seconds,
	// so a briefly slow but alive peer keeps enough in flight to recover.
	minRecentRequestQueue maxRequests = 4
	// With ClientConfig.RequestLowWater, how often a received chunk may trigger a request update
	// for one peer. A full queue is refilled at once regardless.
	lowWaterUpdateInterval = 100 * time.Millisecond
)

type peerRequestQueue struct {
	// Useful bytes per second, indexed by unix second modulo shortRateBuckets; bucketSec says which
	// second a bucket holds.
	bytes     [shortRateBuckets]int64
	bucketSec [shortRateBuckets]int64
	// First useful chunk since the queue was last reset, to measure young connections fairly.
	firstUseful time.Time
	lastUseful  time.Time

	// The current queue limit while sizing by slow start; 0 until first used.
	limit     maxRequests
	slowStart bool
	// The last complete second that slow start looked at, and how many flat seconds in a row.
	checkedSec int64
	flat       int
}

// Starts slow start over at initial requests: for a new connection, after an unchoke or a snub.
func (q *peerRequestQueue) reset(initial maxRequests) {
	*q = peerRequestQueue{limit: initial, slowStart: true}
}

// Records a useful block of n bytes received at now.
func (q *peerRequestQueue) onUsefulChunk(now time.Time, n int, initial maxRequests) {
	if q.limit == 0 {
		q.reset(initial)
	}
	sec := now.Unix()
	i := sec % shortRateBuckets
	if q.bucketSec[i] != sec {
		q.bucketSec[i] = sec
		q.bytes[i] = 0
	}
	q.bytes[i] += int64(n)
	if q.firstUseful.IsZero() {
		q.firstUseful = now
		q.checkedSec = sec
	}
	q.lastUseful = now
	if !q.slowStart {
		return
	}
	q.limit = min(q.limit+1, maxLocalToRemoteRequests)
	if q.limit >= maxLocalToRemoteRequests {
		q.slowStart = false
		return
	}
	// Judge each complete second once, comparing it with the one before.
	for ; q.checkedSec < sec-1; q.checkedSec++ {
		cur, prev := q.secondBytes(q.checkedSec+1), q.secondBytes(q.checkedSec)
		if prev > 0 && float64(cur) <= slowStartGrowth*float64(prev) {
			q.flat++
		} else {
			q.flat = 0
		}
		if q.flat >= slowStartFlat {
			q.slowStart = false
			return
		}
	}
}

func (q *peerRequestQueue) secondBytes(sec int64) int64 {
	i := sec % shortRateBuckets
	if q.bucketSec[i] != sec {
		return 0
	}
	return q.bytes[i]
}

// The useful download rate over the last shortRateBuckets seconds (or since the first useful
// chunk, when that is more recent), in bytes per second. Zero after shortRateBuckets seconds of
// silence.
func (q *peerRequestQueue) shortRate(now time.Time) float64 {
	if q.firstUseful.IsZero() {
		return 0
	}
	sec := now.Unix()
	var sum int64
	for i := range shortRateBuckets {
		if s := q.bucketSec[i]; s > sec-shortRateBuckets && s <= sec {
			sum += q.bytes[i]
		}
	}
	span := min(now.Sub(q.firstUseful), shortRateBuckets*time.Second)
	span = max(span, time.Second)
	return float64(sum) / span.Seconds()
}

// The queue limit by slow start or by rate, before the peer's and the write buffer's caps.
func (q *peerRequestQueue) nominal(now time.Time, initial maxRequests, queueTime time.Duration, chunkSize int) maxRequests {
	if q.limit == 0 {
		q.reset(initial)
	}
	if q.slowStart {
		return q.limit
	}
	if queueTime <= 0 {
		// No rate cap configured: keep what slow start reached.
		return q.limit
	}
	n, ok := rateRequestQueue(q.shortRate(now), queueTime, chunkSize)
	if !ok {
		// Nothing delivered recently.
		return minRateRequestQueue
	}
	if now.Sub(q.lastUseful) < shortRateBuckets*time.Second {
		n = max(n, minRecentRequestQueue)
	}
	return n
}

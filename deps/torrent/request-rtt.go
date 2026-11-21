package torrent

import (
	"time"
)

// Per-peer round trip for ClientConfig.RequestQueueRTTs, from the transport via
// ClientConfig.PeerConnRTT.
//
// Estimating it from request latencies doesn't work while queues are deep: a sample is the latency
// minus the time the peer spent sending what was queued ahead, two large numbers (seconds each)
// whose difference is the round trip (a fraction of one), and the recent rate that converts bytes
// ahead to time lags. In the lab (300 ms path) such an estimator settled at 1 ms, the queue was
// sized below two round trips, the sender idled, and since the limit is rate x queue time it shrank
// with the rate: 500 -> 13 requests, half the throughput. The transport measures the round trip
// directly (uTP timestamps every packet; TCP keeps a smoothed RTT).

// How long a transport round trip reading is reused.
const peerRTTRefresh = time.Second

type peerRTT struct {
	// Zero when unknown.
	rtt     time.Duration
	checked time.Time
}

// The connection's round trip, refreshed from ClientConfig.PeerConnRTT at most once per
// peerRTTRefresh. Zero when unknown.
func (cn *PeerConn) transportRTT(now time.Time) time.Duration {
	f := cn.t.cl.config.PeerConnRTT
	if f == nil || cn.conn == nil {
		return 0
	}
	if now.Sub(cn.reqRTT.checked) >= peerRTTRefresh {
		cn.reqRTT.checked = now
		cn.reqRTT.rtt = 0
		if d, ok := f(cn.conn); ok && d > 0 {
			cn.reqRTT.rtt = d
		}
	}
	return cn.reqRTT.rtt
}

// The queue time for a peer with round trip rtt: rtts round trips, at least minTime and at most
// ceiling (when set).
func rttQueueTime(rtt time.Duration, rtts float64, minTime, ceiling time.Duration) time.Duration {
	d := max(time.Duration(rtts*float64(rtt)), minTime)
	if ceiling > 0 {
		d = min(d, ceiling)
	}
	return d
}

// How much of the peer's rate its request queue should cover.
func (cn *PeerConn) requestQueueTime() time.Duration {
	cfg := cn.t.cl.config
	if cfg.RequestQueueRTTs > 0 {
		if rtt := cn.transportRTT(time.Now()); rtt > 0 {
			return rttQueueTime(rtt, cfg.RequestQueueRTTs, cfg.RequestQueueMinTime, cfg.RequestQueueTime)
		}
	}
	return cfg.RequestQueueTime
}

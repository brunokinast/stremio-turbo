package purego

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
)

// Lab counters (research only; one connection assumed). See streaming-research/plans/01.
var Lab struct {
	InOrder, DupAncient, DupReorder, ReorderInserts, ReorderTooFar          atomic.Int64
	AcksSent, SackAcks, DupAcksSent, WinUpdateImmediate, WinUpdateScheduled atomic.Int64
	RcvWinMin, ReadBufPeak, RcvBuf                                          atomic.Int64
	lastAck                                                                 atomic.Int64
}

func init() { Lab.RcvWinMin.Store(1 << 40); Lab.lastAck.Store(-1) }

func labMin(a *atomic.Int64, v int64) {
	if v < a.Load() {
		a.Store(v)
	}
}
func labMax(a *atomic.Int64, v int64) {
	if v > a.Load() {
		a.Store(v)
	}
}

// Per-connection counters, keyed by the remote address.
type ConnLab struct {
	InOrder, Dup, ReorderInserts, ReorderPeak, TooFar, Acks, Sack, DupAck atomic.Int64
	RTTms                                                                 atomic.Int64
	lastAck                                                               atomic.Int64
}

var LabConns sync.Map

func labFor(addr any) *ConnLab {
	l := &ConnLab{}
	l.lastAck.Store(-1)
	v, _ := LabConns.LoadOrStore(fmt.Sprint(addr), l)
	return v.(*ConnLab)
}

// LabBigSack makes selective acks cover the whole reorder buffer instead of 30 packets.
var LabBigSack = os.Getenv("UTPLAB_BIGSACK") == "1"

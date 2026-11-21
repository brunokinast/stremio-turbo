package torrent

import (
	"context"
	"time"
)

type dialPool struct {
	resCh chan DialResult
	addr  string
	left  int
	// Every result getFirst has received, in arrival order (results the drainer closes aren't
	// included). Only touched by the goroutine calling getFirst.
	results []DialResult
}

func (me *dialPool) getFirst() (res DialResult) {
	for me.left > 0 && res.Conn == nil {
		res = <-me.resCh
		me.left--
		me.results = append(me.results, res)
	}
	return
}

func (me *dialPool) add(ctx context.Context, dialer Dialer) {
	me.left++
	go func() {
		started := time.Now()
		conn, err := dialFromSocketErr(ctx, dialer, me.addr)
		me.resCh <- DialResult{
			Conn:     conn,
			Dialer:   dialer,
			Err:      err,
			Duration: time.Since(started),
		}
	}()
}

func (me *dialPool) startDrainer() {
	go me.drainAndCloseRemainingDials()
}

func (me *dialPool) drainAndCloseRemainingDials() {
	for me.left > 0 {
		conn := (<-me.resCh).Conn
		me.left--
		if conn != nil {
			conn.Close()
		}
	}
}

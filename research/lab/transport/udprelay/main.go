// udprelay: a one-client UDP relay for the transport lab. The downloader talks to -listen; the
// relay talks to the sender (-to) from -from. Adds one-way delay, random loss and burst drops on
// the sender->downloader direction, and an optional bottleneck (rate + queue, tail drop).
package main

import (
	"flag"
	"fmt"
	"math/rand"
	"net"
	"sync/atomic"
	"time"
)

type pkt struct {
	due time.Time
	b   []byte
}

func main() {
	listen := flag.String("listen", "127.0.0.1:52010", "downloader side")
	from := flag.String("from", "127.0.0.1:52011", "local address used towards the sender")
	to := flag.String("to", "127.0.0.1:52001", "sender")
	delay := flag.Duration("delay", 150*time.Millisecond, "one-way delay, both directions")
	loss := flag.Float64("loss", 0, "random loss probability, sender->downloader")
	burstN := flag.Int("burst", 0, "drop this many consecutive sender->downloader data packets")
	burstAt := flag.Duration("burst-at", 20*time.Second, "when the first burst happens, after the first packet")
	burstEvery := flag.Duration("burst-every", 0, "repeat the burst with this period (0 = once)")
	stride := flag.Int("stride", 0, "drop one data packet in every this many ...")
	strideN := flag.Int("stride-n", 10, "... this many times ...")
	strideAt := flag.Duration("stride-at", 20*time.Second, "... starting at this time")
	rate := flag.Int("rate", 0, "bottleneck bytes/s sender->downloader (0 = none)")
	queue := flag.Int("queue", 64*1024, "bottleneck queue in bytes (tail drop)")
	mtu := flag.Int("mtu", 0, "drop sender->downloader datagrams larger than this (0 = none); 548 models a 576-byte IPv4 path, which keeps libtorrent at its 528-byte uTP payload floor")
	flag.Parse()

	la, _ := net.ResolveUDPAddr("udp4", *listen)
	fa, _ := net.ResolveUDPAddr("udp4", *from)
	ta, _ := net.ResolveUDPAddr("udp4", *to)
	down, err := net.ListenUDP("udp4", la)
	if err != nil {
		panic(err)
	}
	up, err := net.ListenUDP("udp4", fa)
	if err != nil {
		panic(err)
	}
	for _, c := range []*net.UDPConn{down, up} {
		c.SetReadBuffer(8 << 20)
		c.SetWriteBuffer(8 << 20)
	}

	var client atomic.Pointer[net.UDPAddr]
	var toDlPk, toDlBytes, toSnPk, dropped, qdrops, mtuDropped, inGaps, inRetrans atomic.Int64

	pipe := func(out func([]byte)) chan pkt {
		ch := make(chan pkt, 1<<16)
		go func() {
			for p := range ch {
				if d := time.Until(p.due); d > 0 {
					time.Sleep(d)
				}
				out(p.b)
			}
		}()
		return ch
	}
	toDl := pipe(func(b []byte) {
		if c := client.Load(); c != nil {
			down.WriteToUDP(b, c)
		}
	})
	toSn := pipe(func(b []byte) { up.WriteToUDP(b, ta) })

	// downloader -> sender
	go func() {
		buf := make([]byte, 65536)
		for {
			n, addr, err := down.ReadFromUDP(buf)
			if err != nil {
				return
			}
			client.Store(addr)
			toSnPk.Add(1)
			toSn <- pkt{time.Now().Add(*delay), append([]byte(nil), buf[:n]...)}
		}
	}()
	// sender -> downloader
	go func() {
		buf := make([]byte, 65536)
		var start, qFree time.Time
		burstLeft := 0
		var maxSeq uint16
		haveSeq := false
		strideLeft, strideCount := *strideN, 0
		mtuSeen := map[uint16]bool{}
		nextBurst := time.Duration(-1)
		if *burstN > 0 {
			nextBurst = *burstAt
		}
		for {
			n, _, err := up.ReadFromUDP(buf)
			if err != nil {
				return
			}
			now := time.Now()
			if start.IsZero() {
				start = now
			}
			data := n > 200
			// A real 576-byte path drops libtorrent's MTU probes (sent with DF) but fragments and
			// delivers the resend, which libtorrent sends at the same size without the probe flag.
			// So drop only the first oversize copy of each uTP sequence number.
			if *mtu > 0 && n > *mtu && n >= 20 && buf[0]>>4 == 0 {
				seq := uint16(buf[16])<<8 | uint16(buf[17])
				if !mtuSeen[seq] {
					mtuSeen[seq] = true
					mtuDropped.Add(1)
					continue
				}
				delete(mtuSeen, seq)
			}
			if n >= 20 && buf[0]>>4 == 0 {
				// A uTP data packet: watch the sequence numbers as they arrive from the sender.
				seq := uint16(buf[16])<<8 | uint16(buf[17])
				if !haveSeq {
					haveSeq, maxSeq = true, seq
				} else if d := seq - maxSeq; d != 0 && d < 0x8000 {
					inGaps.Add(int64(d - 1))
					maxSeq = seq
				} else if d != 0 {
					inRetrans.Add(1)
				}
			}
			if *stride > 0 && data && strideLeft > 0 && now.Sub(start) >= *strideAt {
				strideCount++
				if strideCount%*stride == 0 {
					strideLeft--
					dropped.Add(1)
					continue
				}
			}
			if nextBurst >= 0 && now.Sub(start) >= nextBurst && data {
				burstLeft = *burstN
				if *burstEvery > 0 {
					nextBurst += *burstEvery
				} else {
					nextBurst = -1
				}
			}
			if burstLeft > 0 && data {
				burstLeft--
				dropped.Add(1)
				continue
			}
			if *loss > 0 && data && rand.Float64() < *loss {
				dropped.Add(1)
				continue
			}
			due := now
			if *rate > 0 {
				if qFree.Before(now) {
					qFree = now
				}
				backlog := qFree.Sub(now).Seconds() * float64(*rate)
				if int(backlog)+n > *queue {
					qdrops.Add(1)
					continue
				}
				qFree = qFree.Add(time.Duration(float64(n) / float64(*rate) * float64(time.Second)))
				due = qFree
			}
			toDlPk.Add(1)
			toDlBytes.Add(int64(n))
			toDl <- pkt{due.Add(*delay), append([]byte(nil), buf[:n]...)}
		}
	}()
	fmt.Printf("relay %s <-> %s -> %s delay=%v loss=%g burst=%d@%v every %v rate=%d queue=%d\n", *listen, *from, *to, *delay, *loss, *burstN, *burstAt, *burstEvery, *rate, *queue)
	var lp, lb int64
	for range time.Tick(time.Second) {
		p, b := toDlPk.Load(), toDlBytes.Load()
		if p != lp {
			fmt.Printf("relay: to-downloader %5d pk %7.0f KiB/s | to-sender %d pk total | dropped %d (random/burst) %d (queue) %d (mtu) | from sender: seq gaps %d, resends %d\n", p-lp, float64(b-lb)/1024, toSnPk.Load(), dropped.Load(), qdrops.Load(), mtuDropped.Load(), inGaps.Load(), inRetrans.Load())
		}
		lp, lb = p, b
	}
}

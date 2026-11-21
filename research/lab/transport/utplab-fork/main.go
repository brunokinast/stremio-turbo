// utplab-fork: utplab built against the go-utp fork (C:\Users\bruno\Documents\git\go-utp) instead of
// the lab copy, with the 4 MiB UDP receive buffer Rivulet uses. Same flags and output layout as
// utplab, but the counters come from the connection's Conn.Stats() (the fork has no lab globals),
// so duplicates are one column and the window columns are the values at each tick.
package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/go-utp/purego"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

var reqqRe = regexp.MustCompile(`reqq: ([^,]+),`)

func main() {
	tf := flag.String("torrent", "lab.torrent", ".torrent file")
	peer := flag.String("peer", "127.0.0.1:52010", "the one peer to dial")
	dir := flag.String("dir", "", "download folder (emptied first)")
	secs := flag.Int("secs", 60, "run length")
	stall := flag.Duration("stall", 0, "sleep this long in the peer read loop ...")
	stallEvery := flag.Duration("stall-every", 8*time.Second, "... once per this period")
	rqt := flag.Duration("rqt", -1, "RequestQueueTime override (0 disables the request-health cap)")
	port := flag.Int("port", 52020, "listen port")
	osbuf := flag.Int("osbuf", 4<<20, "UDP receive buffer (0: OS default)")
	iqueue := flag.Int("iqueue", 32, "ClientConfig.InitialRequestQueue (0: the old doubling-when-empty queue)")
	lowWater := flag.Float64("lowwater", 0.5, "ClientConfig.RequestLowWater (0: refill only an empty queue)")
	reads := flag.String("reads", "", "comma list of delay@offset+length reads, mpv-style: the first starts when the info arrives and keeps reading sequentially after its range (the player's head connection); the others start delay after the first chunk. Each reports its time to bytes from the first chunk")
	responsive := flag.Bool("responsive", false, "responsive readers (serve chunks before the piece verifies)")
	offsetFirst := flag.Bool("offsetfirst", false, "ClientConfig.ReaderOffsetFirst")
	qmin := flag.Duration("qmin", 0, "ClientConfig.RequestQueueMinTime")
	qrtts := flag.Float64("qrtts", 0, "ClientConfig.RequestQueueRTTs (0: queue sized by RequestQueueTime)")
	critical := flag.Int64("critical", 0, "ClientConfig.ReaderCriticalBytes")
	closeHead := flag.Bool("closehead", false, "close the head reader when a later read starts, as mpv does on a seek")
	flag.Parse()

	os.RemoveAll(*dir)
	os.MkdirAll(*dir, 0o755)
	cfg := torrent.NewDefaultClientConfig()
	cfg.ListenHost = func(string) string { return "127.0.0.1" }
	cfg.ListenPort = *port
	cfg.DisableTCP = true
	cfg.NoDHT = true
	cfg.DisablePEX = true
	cfg.DisableTrackers = true
	cfg.DisableIPv6 = true
	cfg.NoDefaultPortForwarding = true
	cfg.AcceptPeerConnections = false // only our connection through the relay
	cfg.DataDir = *dir
	cfg.Seed = false
	if *osbuf > 0 {
		cfg.ListenPacket = func(network, addr string) (net.PacketConn, error) {
			pc, err := net.ListenPacket(network, addr)
			if err == nil {
				err = pc.(*net.UDPConn).SetReadBuffer(*osbuf)
			}
			return pc, err
		}
	}
	if *rqt >= 0 {
		cfg.RequestQueueTime = *rqt
	}
	cfg.InitialRequestQueue = *iqueue
	cfg.RequestLowWater = *lowWater
	cfg.ReaderOffsetFirst = *offsetFirst
	cfg.RequestQueueMinTime = *qmin
	cfg.RequestQueueRTTs = *qrtts
	cfg.ReaderCriticalBytes = *critical
	cfg.PeerConnRTT = func(c net.Conn) (time.Duration, bool) {
		if uc, ok := c.(*purego.Conn); ok {
			rtt := uc.Stats().RTT
			return rtt, rtt > 0
		}
		return 0, false
	}
	t0 := time.Now()
	cfg.Callbacks.ReaderWaited = append(cfg.Callbacks.ReaderWaited, func(ev torrent.ReaderWaitedEvent) {
		w := ev.Wait
		fmt.Printf("WAIT at=%.2fs off=%d waited=%dms requested=%v ago=%dms ahead=%d rate=%.0fKiB/s est=%dms\n",
			time.Since(t0).Seconds()-ev.Waited.Seconds(), ev.Offset, ev.Waited.Milliseconds(), w.Requested,
			w.RequestedAgo.Milliseconds(), w.Ahead, w.HolderRate/1024, w.HolderEstimate.Milliseconds())
	})
	firstChunk := make(chan struct{})
	var firstChunkAt time.Time
	var firstOnce sync.Once
	cfg.Callbacks.ReceivedUsefulData = append(cfg.Callbacks.ReceivedUsefulData, func(torrent.ReceivedUsefulDataEvent) {
		firstOnce.Do(func() { firstChunkAt = time.Now(); close(firstChunk) })
	})
	var mu sync.Mutex
	var lastStall time.Time
	stalls := 0
	if *stall > 0 {
		cfg.Callbacks.ReceivedUsefulData = append(cfg.Callbacks.ReceivedUsefulData, func(torrent.ReceivedUsefulDataEvent) {
			mu.Lock()
			due := time.Since(lastStall) >= *stallEvery
			if due {
				lastStall = time.Now()
				stalls++
			}
			mu.Unlock()
			if due {
				time.Sleep(*stall)
			}
		})
		lastStall = time.Now()
	}
	cl, err := torrent.NewClient(cfg)
	if err != nil {
		panic(err)
	}
	mi, err := metainfo.LoadFromFile(*tf)
	if err != nil {
		panic(err)
	}
	t, err := cl.AddTorrent(mi)
	if err != nil {
		panic(err)
	}
	t.AddPeers([]torrent.PeerInfo{{Addr: torrent.StringAddr(*peer), Trusted: true}})
	t.DownloadAll()
	var headMu sync.Mutex
	var head torrent.Reader
	if *reads != "" {
		<-t.GotInfo()
		for i, spec := range strings.Split(*reads, ",") {
			var delay time.Duration
			var off, n int64
			at, rng, _ := strings.Cut(spec, "@")
			delay, err := time.ParseDuration(at)
			if err != nil {
				panic(err)
			}
			if _, err := fmt.Sscanf(rng, "%d+%d", &off, &n); err != nil {
				panic(err)
			}
			go func() {
				if i > 0 {
					<-firstChunk
					time.Sleep(delay)
				}
				r := t.NewReader()
				if *responsive {
					r.SetResponsive()
				}
				headMu.Lock()
				if i == 0 {
					head = r
				} else if *closeHead && head != nil {
					head.Close()
					head = nil
					fmt.Printf("HEAD closed at %+.2fs\n", time.Since(firstChunkAt).Seconds())
				}
				headMu.Unlock()
				r.Seek(off, io.SeekStart)
				began := time.Now()
				if _, err := io.ReadFull(r, make([]byte, n)); err != nil {
					fmt.Printf("READ %d failed: %v\n", i, err)
					return
				}
				<-firstChunk
				fmt.Printf("READ %d off=%d len=%d: done %.2fs after the first chunk (started %+.2fs, took %.2fs)\n",
					i, off, n, time.Since(firstChunkAt).Seconds(), began.Sub(firstChunkAt).Seconds(), time.Since(began).Seconds())
				if i == 0 {
					io.Copy(io.Discard, r)
				}
			}()
		}
	}
	start := time.Now()
	var last int64
	var prev, s purego.ConnStats
	fmt.Println("    t   MiB  KiB/s | inorder    dup reoIns tooFar reoNow | acks  sack dupAck winImm winSch | rcvWin readBuf rttMs")
	for i := 1; i <= *secs*2; i++ {
		time.Sleep(time.Until(start.Add(time.Duration(i) * 500 * time.Millisecond)))
		st := t.Stats()
		got := st.BytesReadUsefulData.Int64()
		for _, pc := range t.PeerConns() {
			if c, ok := pc.NetConn().(*purego.Conn); ok {
				s = c.Stats()
			}
		}
		fmt.Printf("%5.1f %5.0f %6.0f | %7d %6d %6d %6d %6d | %4d %5d %6d %6d %6d | %6d %7d %5d\n", float64(i)/2, float64(got)/1048576, float64(got-last)/512,
			s.InOrderPackets-prev.InOrderPackets, s.DuplicatePackets-prev.DuplicatePackets, s.ReorderInserts-prev.ReorderInserts,
			s.ReorderTooFar-prev.ReorderTooFar, s.ReorderCount,
			s.AcksSent-prev.AcksSent, s.SelectiveAcksSent-prev.SelectiveAcksSent, s.DuplicateAcksSent-prev.DuplicateAcksSent,
			s.WindowUpdatesImmediate-prev.WindowUpdatesImmediate, s.WindowUpdatesScheduled-prev.WindowUpdatesScheduled,
			s.LastRcvWindow, s.ReadBuffered, s.RTT.Milliseconds())
		var sb strings.Builder
		cl.WriteStatus(&sb)
		if m := reqqRe.FindStringSubmatch(sb.String()); m != nil {
			fmt.Printf("      reqq %s\n", m[1])
		}
		for _, pc := range t.PeerConns() {
			rs := pc.RequestStats()
			fmt.Printf("      rq out=%d max=%d qt=%dms rtt=%dms\n", rs.Outstanding, rs.MaxRequests, rs.QueueTime.Milliseconds(), rs.RTTEstimate.Milliseconds())
		}
		prev = s
		last = got
		if t.BytesMissing() == 0 {
			fmt.Println("complete at", time.Since(start))
			break
		}
	}
	el := time.Since(start).Seconds()
	fmt.Printf("TOTAL %.1f MiB in %.1fs = %.0f KiB/s; osbuf=%d iqueue=%d lowwater=%g qmin=%v qrtts=%g critical=%d stalls=%d totals: inorder=%d dup=%d reoIns=%d tooFar=%d reoPeak=%d acks=%d sack=%d dupAck=%d winImm=%d winSch=%d\n",
		float64(last)/1048576, el, float64(last)/1024/el, *osbuf, *iqueue, *lowWater, *qmin, *qrtts, *critical, stalls,
		s.InOrderPackets, s.DuplicatePackets, s.ReorderInserts, s.ReorderTooFar, s.ReorderPeak, s.AcksSent, s.SelectiveAcksSent,
		s.DuplicateAcksSent, s.WindowUpdatesImmediate, s.WindowUpdatesScheduled)
	os.Exit(0)
}

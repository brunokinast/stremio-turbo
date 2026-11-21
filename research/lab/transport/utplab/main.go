// utplab: downloads one torrent from one peer over uTP only, with the anacrolix fork and the
// lab copy of go-utp, and prints rate and receive-path counters twice a second.
package main

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/go-utp/purego"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

var reqqRe = regexp.MustCompile(`reqq: ([^,]+),`)

func main() {
	mk := flag.String("make", "", "make a .torrent for this file and exit")
	tf := flag.String("torrent", "lab.torrent", ".torrent file")
	peer := flag.String("peer", "127.0.0.1:52010", "the one peer to dial")
	dir := flag.String("dir", "", "download folder (emptied first)")
	secs := flag.Int("secs", 60, "run length")
	stall := flag.Duration("stall", 0, "sleep this long in the peer read loop ...")
	stallEvery := flag.Duration("stall-every", 8*time.Second, "... once per this period")
	rqt := flag.Duration("rqt", -1, "RequestQueueTime override (0 disables the request-health cap)")
	tcp := flag.Bool("tcp", false, "use TCP instead of uTP")
	port := flag.Int("port", 52020, "listen port")
	statusAt := flag.Int("status-at", 0, "dump client status at this second")
	flag.Parse()

	if *mk != "" {
		info := metainfo.Info{PieceLength: 16 << 20}
		if err := info.BuildFromFilePath(*mk); err != nil {
			panic(err)
		}
		var mi metainfo.MetaInfo
		mi.InfoBytes, _ = bencode.Marshal(info)
		f, _ := os.Create(*tf)
		mi.Write(f)
		f.Close()
		fmt.Println("infohash", mi.HashInfoBytes().HexString())
		return
	}
	os.RemoveAll(*dir)
	os.MkdirAll(*dir, 0o755)
	cfg := torrent.NewDefaultClientConfig()
	cfg.ListenHost = func(string) string { return "127.0.0.1" }
	cfg.ListenPort = *port
	cfg.DisableTCP = !*tcp
	cfg.DisableUTP = *tcp
	cfg.NoDHT = true
	cfg.DisablePEX = true
	cfg.DisableTrackers = true
	cfg.DisableIPv6 = true
	cfg.NoDefaultPortForwarding = true
	// The sender learns our listen address and would otherwise dial us directly, bypassing the
	// relay (seen in runs B4, B5, O3, O4 of step 1). Only our outgoing connection may exist.
	cfg.AcceptPeerConnections = false
	cfg.DataDir = *dir
	cfg.Seed = false
	if *rqt >= 0 {
		cfg.RequestQueueTime = *rqt
	}
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
	start := time.Now()
	var last int64
	L := &purego.Lab
	fmt.Println("    t   MiB  KiB/s | inorder dupOld dupReo reoIns tooFar |  acks  sack dupAck winImm winSch | rcvWinMin readBufPeak")
	var p [10]int64
	for i := 1; i <= *secs*2; i++ {
		time.Sleep(time.Until(start.Add(time.Duration(i) * 500 * time.Millisecond)))
		st := t.Stats()
		got := st.BytesReadUsefulData.Int64()
		c := [10]int64{L.InOrder.Load(), L.DupAncient.Load(), L.DupReorder.Load(), L.ReorderInserts.Load(), L.ReorderTooFar.Load(), L.AcksSent.Load(), L.SackAcks.Load(), L.DupAcksSent.Load(), L.WinUpdateImmediate.Load(), L.WinUpdateScheduled.Load()}
		fmt.Printf("%5.1f %5.0f %6.0f | %7d %6d %6d %6d %6d | %5d %5d %6d %6d %6d | %9d %9d\n", float64(i)/2, float64(got)/1048576, float64(got-last)/512,
			c[0]-p[0], c[1]-p[1], c[2]-p[2], c[3]-p[3], c[4]-p[4], c[5]-p[5], c[6]-p[6], c[7]-p[7], c[8]-p[8], c[9]-p[9], L.RcvWinMin.Load(), L.ReadBufPeak.Load())
		var sb strings.Builder
		cl.WriteStatus(&sb)
		if m := reqqRe.FindStringSubmatch(sb.String()); m != nil {
			fmt.Printf("      reqq %s"+string(rune(10)), m[1])
		}
		p = c
		last = got
		L.RcvWinMin.Store(1 << 40)
		L.ReadBufPeak.Store(0)
		if *statusAt > 0 && i == *statusAt*2 {
			f, _ := os.Create("status.txt")
			cl.WriteStatus(f)
			f.Close()
		}
		if t.BytesMissing() == 0 {
			fmt.Println("complete at", time.Since(start))
			break
		}
	}
	el := time.Since(start).Seconds()
	fmt.Printf("TOTAL %.1f MiB in %.1fs = %.0f KiB/s; rcvBuf=%d stalls=%d totals: inorder=%d dupOld=%d dupReo=%d reoIns=%d tooFar=%d acks=%d sack=%d dupAck=%d winImm=%d winSch=%d\n",
		float64(last)/1048576, el, float64(last)/1024/el, L.RcvBuf.Load(), stalls,
		p[0], p[1], p[2], p[3], p[4], p[5], p[6], p[7], p[8], p[9])
	os.Exit(0)
}

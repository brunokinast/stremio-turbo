// swarmprobe: streams one file of a real torrent like a player reading at unlimited speed, with
// the anacrolix fork and the lab copy of go-utp, and prints per-peer transport counters.
// Research tool: it answers "what does our uTP receive path see from real peers".
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/anacrolix/go-utp/purego"
	"github.com/anacrolix/torrent"
)

var (
	blockRe = regexp.MustCompile(`(?m)^- (\S+)-(\S+)\n((?:  .*\n)+)`)
	reqqRe  = regexp.MustCompile(`reqq: ([^,]+), flags: (\S+)`)
	drRe    = regexp.MustCompile(`good chunks: (\d+)/\d+, dr: ([\d.]+) KiB/s`)
	haveRe  = regexp.MustCompile(`(\d+)/(\d+) completed`)
	cliRe   = regexp.MustCompile(`peer id: "\\"-?([A-Za-z]{2}[0-9A-Za-z]{4})`)
	errRe   = regexp.MustCompile(`(?m)^\s*(?:Erros de recebimento|Receive Errors)\s*=\s*(\d+)`)
)

func udpErrors() int64 {
	out, err := exec.Command("netstat", "-s", "-p", "udp").Output()
	if err != nil {
		return -1
	}
	if m := errRe.FindSubmatch(out); m != nil {
		n, _ := strconv.ParseInt(string(m[1]), 10, 64)
		return n
	}
	return -1
}

func main() {
	ih := flag.String("ih", "", "info hash (hex)")
	trf := flag.String("trackers", "", "file with one tracker URL per line")
	idx := flag.Int("file", 0, "file index to stream")
	dir := flag.String("dir", "", "download folder (emptied first)")
	secs := flag.Int("secs", 150, "run length")
	port := flag.Int("port", 49883, "listen port")
	every := flag.Int("every", 2, "seconds between samples")
	noTCP := flag.Bool("no-tcp", false, "uTP only")
	noUTP := flag.Bool("no-utp", false, "TCP only")
	rqt := flag.Duration("rqt", -1, "RequestQueueTime override (0 disables the request-health cap)")
	flag.Parse()

	os.RemoveAll(*dir)
	os.MkdirAll(*dir, 0o755)
	cfg := torrent.NewDefaultClientConfig()
	cfg.ListenPort = *port
	cfg.DataDir = *dir
	cfg.Seed = true
	cfg.DisableTCP = *noTCP
	cfg.DisableUTP = *noUTP
	cfg.DisableWebtorrent = true
	if *rqt >= 0 {
		cfg.RequestQueueTime = *rqt
	}
	cl, err := torrent.NewClient(cfg)
	if err != nil {
		panic(err)
	}
	magnet := "magnet:?xt=urn:btih:" + *ih
	if *trf != "" {
		b, _ := os.ReadFile(*trf)
		for _, l := range strings.Fields(string(b)) {
			magnet += "&tr=" + l
		}
	}
	start := time.Now()
	t, err := cl.AddMagnet(magnet)
	if err != nil {
		panic(err)
	}
	fmt.Printf("%6.1fs added\n", time.Since(start).Seconds())
	<-t.GotInfo()
	fmt.Printf("%6.1fs got info: %d pieces of %d MiB\n", time.Since(start).Seconds(), t.NumPieces(), t.Info().PieceLength>>20)
	t.CancelPieces(0, t.NumPieces())
	f := t.Files()[*idx]
	fmt.Printf("        file %d: %s (%d MiB)\n", *idx, f.DisplayPath(), f.Length()>>20)
	r := f.NewReader()
	r.SetReadahead(256 << 20)
	r.SetResponsive()
	var read int64
	go func() {
		buf := make([]byte, 1<<20)
		for {
			n, err := r.Read(buf)
			read += int64(n)
			if err != nil && err != io.EOF {
				return
			}
		}
	}()

	type prev struct{ in, reo, dup, sack, dupack, chunks int64 }
	last := map[string]prev{}
	var lastUseful, firstRead int64
	e0 := udpErrors()
	firstByte := -1.0
	for el := *every; el <= *secs; el += *every {
		time.Sleep(time.Until(start.Add(time.Duration(el) * time.Second)))
		var sb strings.Builder
		cl.WriteStatus(&sb)
		st := t.Stats()
		useful := st.BytesReadUsefulData.Int64()
		if firstByte < 0 && read > 0 {
			firstByte = float64(el)
			firstRead = read
		}
		_ = firstRead
		fmt.Printf("%4ds net %7.1f MiB %6.0f KiB/s | reader %7.1f MiB | peers %d conn / %d known / %d half-open | uTP global: reorderIns %d dup %d sackAcks %d dupAcks %d winUpd %d | OS udp rx errors +%d\n",
			el, float64(useful)/1048576, float64(useful-lastUseful)/1024/float64(*every), float64(read)/1048576,
			st.ActivePeers, st.TotalPeers, st.HalfOpenPeers,
			purego.Lab.ReorderInserts.Load(), purego.Lab.DupAncient.Load()+purego.Lab.DupReorder.Load(), purego.Lab.SackAcks.Load(), purego.Lab.DupAcksSent.Load(),
			purego.Lab.WinUpdateScheduled.Load()+purego.Lab.WinUpdateImmediate.Load(), udpErrors()-e0)
		lastUseful = useful
		var rows []string
		for _, m := range blockRe.FindAllStringSubmatch(sb.String(), -1) {
			local, remote, body := m[1], m[2], m[3]
			dr := drRe.FindStringSubmatch(body)
			rq := reqqRe.FindStringSubmatch(body)
			hv := haveRe.FindStringSubmatch(body)
			if dr == nil || rq == nil {
				continue
			}
			chunks, _ := strconv.ParseInt(dr[1], 10, 64)
			p := last[remote]
			cli := ""
			if c := cliRe.FindStringSubmatch(body); c != nil {
				cli = c[1]
			}
			have := ""
			if hv != nil {
				have = hv[1]
			}
			row := fmt.Sprintf("       %-28s %-7s have=%-5s +%4d chunks dr=%7s KiB/s reqq=%-22s flags=%-14s", remote, cli, have, chunks-p.chunks, dr[2], rq[1], rq[2])
			var cur prev
			cur.chunks = chunks
			if v, ok := purego.LabConns.Load(remote); ok && strings.Contains(rq[2], "U") {
				l := v.(*purego.ConnLab)
				cur.in, cur.reo, cur.dup, cur.sack, cur.dupack = l.InOrder.Load(), l.ReorderInserts.Load(), l.Dup.Load(), l.Sack.Load(), l.DupAck.Load()
				row += fmt.Sprintf(" uTP rtt=%4dms pk+%-5d holes+%-4d dup+%-4d sack+%-4d dupAck+%-3d reorderPeak=%d", l.RTTms.Load(), cur.in-p.in, cur.reo-p.reo, cur.dup-p.dup, cur.sack-p.sack, cur.dupack-p.dupack, l.ReorderPeak.Swap(0))
			}
			_ = local
			if chunks-p.chunks > 0 || el%10 == 0 {
				rows = append(rows, row)
			}
			last[remote] = cur
		}
		sort.Strings(rows)
		for _, r := range rows {
			fmt.Println(r)
		}
	}
	fmt.Printf("DONE first reader byte by %.0fs; reader %.1f MiB in %ds\n", firstByte, float64(read)/1048576, *secs)
	os.Exit(0)
}

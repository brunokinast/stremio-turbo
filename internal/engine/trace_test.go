// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package engine

import (
	"io"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"golang.org/x/time/rate"

	"github.com/rivulet-kodi/rivulet-engine/internal/types"
)

func TestRingKeepsNewestOldestFirst(t *testing.T) {
	r := newRing[int](3)
	for i := 1; i <= 5; i++ {
		r.add(i)
	}
	got := r.items()
	if len(got) != 3 || got[0] != 3 || got[1] != 4 || got[2] != 5 {
		t.Fatalf("items = %v, want [3 4 5]", got)
	}
}

func TestTracerRecordsRelativeToCreation(t *testing.T) {
	tr := newTracer()
	t0 := time.Now()
	ih := "0123456789abcdef0123456789abcdef01234567"
	tr.begin(ih, t0)
	tr.once(ih, "metadata", "", t0.Add(150*time.Millisecond))
	tr.once(ih, "metadata", "", t0.Add(900*time.Millisecond)) // only the first counts
	tr.stall(ih, types.ReadStall{File: 1, Offset: 42, Piece: 0}, t0.Add(2*time.Second), 500*time.Millisecond)
	tr.once("ffffffffffffffffffffffffffffffffffffffff", "metadata", "", t0) // never begun: ignored
	tr.onLockHold(80*time.Millisecond, "somewhere")

	got, ok := tr.get(ih)
	if !ok {
		t.Fatal("trace missing")
	}
	if len(got.Events) != 2 || got.Events[0].Name != "created" || got.Events[1].Name != "metadata" || got.Events[1].AtMs != 150 {
		t.Fatalf("events = %+v", got.Events)
	}
	if len(got.Stalls) != 1 || got.Stalls[0].AtMs != 1500 || got.Stalls[0].DurationMs != 500 || got.Stalls[0].Offset != 42 {
		t.Fatalf("stalls = %+v", got.Stalls)
	}
	if holds := tr.lockHolds(); len(holds) != 1 || holds[0].HeldMs != 80 || holds[0].LockedAt != "somewhere" {
		t.Fatalf("lock holds = %+v", holds)
	}
	tr.forget(ih)
	if _, ok := tr.get(ih); ok {
		t.Fatal("trace kept after forget")
	}
}

// A real stream from a loopback seeder fills the trace: milestones, storage
// timings, the first read and peer samples.
func TestTraceFromLoopbackStream(t *testing.T) {
	if testing.Short() {
		t.Skip("starts two torrent clients")
	}
	const pieceLen, numPieces = 256 << 10, 16
	dir := t.TempDir()
	want, mi := newPayloadTorrent(t, dir, pieceLen, numPieces)

	scfg := torrent.NewDefaultClientConfig()
	scfg.DataDir = dir
	scfg.Seed = true
	scfg.NoDHT = true
	scfg.DisableTrackers = true
	scfg.DisableIPv6 = true
	scfg.DisableUTP = true
	scfg.NoDefaultPortForwarding = true
	scfg.ListenPort = 0
	scfg.ListenHost = func(string) string { return "127.0.0.1" }
	// ~4 s for the payload, so the once-a-second peer sampler sees the transfer.
	scfg.UploadRateLimiter = rate.NewLimiter(1<<20, 256<<10)
	seeder, err := torrent.NewClient(scfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { seeder.Close() })
	st, err := seeder.AddTorrent(mi)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.VerifyDataContext(t.Context()); err != nil {
		t.Fatal(err)
	}

	m, e := newTestEngine(t, 0, mi)
	e.t.AddClientPeer(seeder)
	r, _, err := e.NewReader(0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	timer := time.AfterFunc(2*time.Minute, func() { _ = r.Close() })
	t.Cleanup(func() { timer.Stop() })
	got, err := io.ReadAll(io.LimitReader(r, int64(len(want))))
	if err != nil || len(got) != len(want) {
		t.Fatalf("read %d bytes, err %v", len(got), err)
	}
	tr, ok := m.Trace(e.infoHash)
	if !ok {
		t.Fatal("no trace")
	}
	names := map[string]bool{}
	for _, ev := range tr.Events {
		names[ev.Name] = true
	}
	// AddClientPeer gives the peer no source code, so its milestone is "first-connected:".
	for _, n := range []string{"created", "metadata", "first-connected:", "first-unchoke", "first-chunk", "first-piece-verified", "first-byte-read"} {
		if !names[n] {
			t.Errorf("missing event %q in %+v", n, tr.Events)
		}
	}
	if tr.Storage.ChunkWrites == 0 || len(tr.Storage.MarkCompletions) == 0 {
		t.Errorf("storage = %+v", tr.Storage)
	}
	var first bool
	for _, s := range tr.Stalls {
		first = first || s.First
	}
	if !first {
		t.Errorf("first read not recorded: %+v", tr.Stalls)
	}
	if len(tr.PeerSamples) == 0 || tr.PeerSamples[0].Downloaded == 0 {
		t.Errorf("peer samples = %+v", tr.PeerSamples)
	}
}

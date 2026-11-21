// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package engine

import (
	"testing"
	"time"

	"github.com/anacrolix/torrent"
)

func TestResumeSpan(t *testing.T) {
	const mib = 1 << 20
	for _, tc := range []struct {
		name               string
		off, length, piece int64
		frac               float64
		want               pieceSpan
		ok                 bool
	}{
		{"no resume", 0, 1024 * mib, 16 * mib, 0, pieceSpan{}, false},
		{"finished", 0, 1024 * mib, 16 * mib, 1, pieceSpan{}, false},
		// pos 512 MiB: [496, 528) MiB is pieces 31 and 32.
		{"middle", 0, 1024 * mib, 16 * mib, 0.5, pieceSpan{31, 33}, true},
		// Clamped to the file's start; a second file starting at 100 MiB.
		{"near the start", 100 * mib, 1024 * mib, 16 * mib, 0.001, pieceSpan{6, 8}, true},
	} {
		got, ok := resumeSpan(tc.off, tc.length, tc.piece, tc.frac)
		if ok != tc.ok || got != tc.want {
			t.Errorf("%s: got %v %v, want %v %v", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}

// PrimeFile with a resume point pins the pieces around it with the head and
// tail; a probe reader keeps them, the player's reader releases them, and a
// moved resume point releases the old window.
func TestPrimeFileResumeWindow(t *testing.T) {
	const pieceLen, numPieces = 2 << 20, 64 // 128 MiB: the 32 MiB window is pieces 24-39 at 0.5
	_, mi := newPayloadTorrent(t, t.TempDir(), pieceLen, numPieces)
	_, e := newTestEngine(t, 0, mi)
	prio := func(i int) torrent.PiecePriority { return e.t.Piece(i).State().Priority }
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		settled := true
		for i := 0; settled && i < numPieces; i++ {
			st := e.t.Piece(i).State()
			settled = st.Ok && !st.Checking
		}
		if settled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pieces still checking after 30 s")
		}
	}

	e.PrimeFile(0, 0.5)
	for _, i := range []int{0, 24, 32, 39, numPieces - 1} {
		if prio(i) != torrent.PiecePriorityNow {
			t.Fatalf("piece %d after PrimeFile(0, 0.5) = %v, want Now", i, prio(i))
		}
	}
	if prio(20) == torrent.PiecePriorityNow || prio(40) == torrent.PiecePriorityNow {
		t.Fatalf("pieces outside the window raised: 20 %v, 40 %v", prio(20), prio(40))
	}

	// The resume point moves: the old window is released, the new one raised.
	e.PrimeFile(0, 0.25)
	if prio(16) != torrent.PiecePriorityNow || prio(36) == torrent.PiecePriorityNow {
		t.Fatalf("after moving to 0.25: piece 16 %v (want Now), piece 36 %v (want released)", prio(16), prio(36))
	}

	r, _, err := e.NewProbeReader(0)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if prio(16) != torrent.PiecePriorityNow {
		t.Fatalf("resume piece after a probe = %v, want Now", prio(16))
	}

	p, _, err := e.NewReader(0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Close() }()
	if prio(16) == torrent.PiecePriorityNow {
		t.Fatal("the player's reader left the resume window pinned at Now")
	}
}

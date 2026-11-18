// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package engine

import (
	"testing"
	"time"

	"github.com/anacrolix/torrent"
)

// A probe reader (the web client's race) leaves the file prepared for the
// player: the boundary pins survive it opening and closing, and the file stays
// selected. The player's reader then releases the pins as before.
func TestProbeReaderKeepsFilePreparedForThePlayer(t *testing.T) {
	const pieceLen, numPieces = 256 << 10, 16
	_, mi := newPayloadTorrent(t, t.TempDir(), pieceLen, numPieces)
	_, e := newTestEngine(t, 0, mi)
	last := numPieces - 1
	prio := func() torrent.PiecePriority { return e.t.Piece(last).State().Priority }
	// A piece reads as priority none until its storage completion is known.
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if st := e.t.Piece(last).State(); st.Ok && !st.Checking {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pieces still checking after 30 s")
		}
	}
	e.PrimeFile(0)
	if prio() != torrent.PiecePriorityNow {
		t.Fatalf("tail piece after PrimeFile = %v, want Now", prio())
	}

	r, _, err := e.NewProbeReader(0)
	if err != nil {
		t.Fatal(err)
	}
	if prio() != torrent.PiecePriorityNow {
		t.Fatalf("tail piece with a probe open = %v, want Now", prio())
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	_, selected := e.selected[0]
	e.mu.Unlock()
	if prio() != torrent.PiecePriorityNow || !selected {
		t.Fatalf("after the probe closed: tail %v, selected %v; want Now, true", prio(), selected)
	}

	p, _, err := e.NewReader(0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Close() }()
	if prio() == torrent.PiecePriorityNow {
		t.Fatal("the player's reader left the tail pinned at Now")
	}
}

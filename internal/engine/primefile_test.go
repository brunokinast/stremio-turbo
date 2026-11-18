// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package engine

import (
	"math/rand/v2"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// newSeasonPackTorrent builds a two-episode pack whose second file is the
// larger, so the largest-file guess picks the episode that is NOT played first.
func newSeasonPackTorrent(t *testing.T, pieceLen int64) *metainfo.MetaInfo {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "pack")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	rng := rand.NewChaCha8([32]byte{7})
	for name, pieces := range map[string]int64{"Show.S01E01.mkv": 8, "Show.S01E02.mkv": 12} {
		data := make([]byte, pieceLen*pieces)
		_, _ = rng.Read(data)
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	info := metainfo.Info{PieceLength: pieceLen}
	if err := info.BuildFromFilePath(dir); err != nil {
		t.Fatal(err)
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	return &metainfo.MetaInfo{InfoBytes: infoBytes}
}

func fileIndex(t *testing.T, e *engine, name string) int {
	t.Helper()
	for i, f := range e.t.Files() {
		if filepath.Base(f.DisplayPath()) == name {
			return i
		}
	}
	t.Fatalf("no file %s", name)
	return -1
}

func primedFiles(e *engine) map[int]struct{} {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := map[int]struct{}{}
	for i := range e.primed {
		out[i] = struct{}{}
	}
	return out
}

// TestPrimeFileReplacesGuess: once the played file is named, the guessed
// (largest) episode's head stops being top priority and the named one's starts.
func TestPrimeFileReplacesGuess(t *testing.T) {
	_, e := newTestEngine(t, 0, newSeasonPackTorrent(t, 256<<10))
	applyBoundaryPriorities(e) // waits for the automatic run to finish
	e1, e2 := fileIndex(t, e, "Show.S01E01.mkv"), fileIndex(t, e, "Show.S01E02.mkv")
	if _, ok := primedFiles(e)[e2]; !ok {
		t.Fatalf("guess should prime the largest file %d; primed %v", e2, primedFiles(e))
	}

	e.PrimeFile(e1, 0)
	primed := primedFiles(e)
	if _, ok := primed[e1]; !ok || len(primed) != 1 {
		t.Fatalf("primed = %v; want only %d", primed, e1)
	}
	files := e.t.Files()
	// A piece reports no priority while its initial completion check runs.
	prio := func(i int) torrent.PiecePriority {
		for range 200 {
			if st := e.t.Piece(i).State(); !st.Marking && !st.Checking {
				return st.Priority
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("piece %d still being checked", i)
		return 0
	}
	if p := prio(files[e1].BeginPieceIndex()); p != torrent.PiecePriorityNow {
		t.Errorf("named file head priority = %v; want Now", p)
	}
	if p := prio(files[e2].BeginPieceIndex()); p == torrent.PiecePriorityNow {
		t.Errorf("guessed file head is still Now after PrimeFile")
	}
}

// TestGuessSkippedWhenFileChosen: the guess never primes once a stream or
// caller has chosen the file (it used to race the stream's selection).
func TestGuessSkippedWhenFileChosen(t *testing.T) {
	_, e := newTestEngine(t, 0, newSeasonPackTorrent(t, 256<<10))
	applyBoundaryPriorities(e)
	e1 := fileIndex(t, e, "Show.S01E01.mkv")
	e.PrimeFile(e1, 0) // releases the guess

	// Re-run the guess as if it had lost the race with the stream.
	e.onceMetaPriority = sync.Once{}
	e.fileMu.Lock()
	e.chosenFile = -1
	e.fileMu.Unlock()
	applyBoundaryPriorities(e)
	primed := primedFiles(e)
	if _, ok := primed[e1]; !ok || len(primed) != 1 {
		t.Fatalf("primed = %v; want only the chosen file %d", primed, e1)
	}
}

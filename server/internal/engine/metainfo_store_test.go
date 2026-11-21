// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package engine

import (
	"os"
	"testing"
	"time"

	"github.com/rivulet-kodi/rivulet-engine/internal/types"
)

// TestMetainfoSavedAndReused: a torrent's metadata is saved beside its data
// once known, a reload without metadata uses it (the file list is there at
// once, no swarm), and it's deleted with the data.
func TestMetainfoSavedAndReused(t *testing.T) {
	m, e := newTestEngine(t, 0, newSeasonPackTorrent(t, 256<<10))
	p := m.metainfoPath(e.infoHash)
	var saved bool
	for range 200 {
		if _, err := os.Stat(p); err == nil {
			saved = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !saved {
		t.Fatalf("metadata not saved at %s", p)
	}

	m.mu.Lock()
	m.unloadLocked(e.infoHash, e)
	m.mu.Unlock()
	eng, err := m.EnsureEngine(e.infoHash, types.AddOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !eng.(*engine).hasInfo() {
		t.Fatal("reload without metadata had no file list: the saved one wasn't used")
	}

	// A saved file that doesn't match the torrent is dropped, not used.
	m.mu.Lock()
	m.unloadLocked(e.infoHash, eng.(*engine))
	m.mu.Unlock()
	if err := os.WriteFile(p, []byte("not a torrent"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.EnsureEngine(e.infoHash, types.AddOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err == nil {
		// Dropped, then possibly saved again once the (magnet) torrent got
		// metadata; it must not be the garbage any more.
		if raw, _ := os.ReadFile(p); string(raw) == "not a torrent" {
			t.Fatal("invalid saved metadata kept")
		}
	}

	if err := m.RemoveTorrentData(e.infoHash); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("saved metadata kept after the data was deleted: %v", err)
	}
}

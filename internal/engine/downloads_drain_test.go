// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package engine

import (
	"sync"
	"testing"
	"time"

	"github.com/rivulet-kodi/rivulet-engine/internal/types"
)

// TestKeepAwakeAndExitWhenDone: the system is kept awake while a download is
// being fetched, and exit-when-done signals once nothing is left to fetch and
// nothing is being read.
func TestKeepAwakeAndExitWhenDone(t *testing.T) {
	m, e := newTestEngine(t, 0, newSeasonPackTorrent(t, 256<<10))
	var mu sync.Mutex
	var calls []bool
	m.dl.mu.Lock()
	m.dl.awake = func(on bool) { mu.Lock(); calls = append(calls, on); mu.Unlock() }
	m.dl.awakeOn = false
	m.dl.mu.Unlock()
	last := func() (bool, int) {
		mu.Lock()
		defer mu.Unlock()
		if len(calls) == 0 {
			return false, 0
		}
		return calls[len(calls)-1], len(calls)
	}
	drained := func() bool {
		select {
		case <-m.Drained():
			return true
		default:
			return false
		}
	}

	e1 := fileIndex(t, e, "Show.S01E01.mkv")
	if _, err := m.AddDownload(e.infoHash, e1, types.AddOptions{}); err != nil {
		t.Fatal(err)
	}
	m.SetExitWhenDone(true)
	m.tickDownloads()
	if on, n := last(); !on || n != 1 {
		t.Fatalf("awake calls = %v; want [true]", calls)
	}
	if drained() {
		t.Fatal("drained while a download is incomplete")
	}

	m.dl.mu.Lock()
	m.dl.entries[downloadKey{e.infoHash, e1}].Complete = true
	m.dl.mu.Unlock()
	r, _, err := e.NewReader(e1)
	if err != nil {
		t.Fatal(err)
	}
	m.tickDownloads()
	if on, _ := last(); on {
		t.Fatalf("still kept awake with no download to fetch: %v", calls)
	}
	if drained() {
		t.Fatal("drained while a torrent is being read")
	}
	_ = r.Close()
	m.tickDownloads()
	select {
	case <-m.Drained():
	case <-time.After(5 * time.Second):
		t.Fatal("not drained once downloads were done and nothing read")
	}
}

// TestExitWhenDoneOff: without exit-when-done, finishing never signals.
func TestExitWhenDoneOff(t *testing.T) {
	m, _ := newTestEngine(t, 0, newSeasonPackTorrent(t, 256<<10))
	m.tickDownloads()
	select {
	case <-m.Drained():
		t.Fatal("drained with exit-when-done off")
	default:
	}
}

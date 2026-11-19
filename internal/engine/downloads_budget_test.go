// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	g "github.com/anacrolix/generics"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/rivulet-kodi/rivulet-engine/internal/types"
)

// TestMakeRoomEvictionOrder: room is made from completed downloads, watched
// ones first, oldest first; incomplete downloads are never deleted, and a
// download that can't fit is refused.
func TestMakeRoomEvictionOrder(t *testing.T) {
	em, err := New(newJanitorTestCfg(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = em.Close() })
	m := em.(*manager)
	m.SetDownloadBudgetFn(func() int64 { return 40 })
	ih := func(n int) string { return fmt.Sprintf("%040x", n) }
	now := time.Now()
	for _, en := range []downloadEntry{
		{InfoHash: ih(1), Size: 10, Complete: true, AddedAt: now.Add(-3 * time.Hour)},                // unwatched, oldest
		{InfoHash: ih(2), Size: 10, Complete: true, Watched: true, AddedAt: now.Add(-1 * time.Hour)}, // watched, newer
		{InfoHash: ih(3), Size: 10, Complete: true, Watched: true, AddedAt: now.Add(-2 * time.Hour)}, // watched, older
		{InfoHash: ih(4), Size: 10, AddedAt: now.Add(-4 * time.Hour)},                                // incomplete
	} {
		e := en
		m.dl.entries[downloadKey{e.InfoHash, 0}] = &e
		if err := os.MkdirAll(filepath.Join(m.cfg.CacheRoot, e.InfoHash), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	exists := func(n int) bool {
		_, ok := m.dl.entries[downloadKey{ih(n), 0}]
		_, err := os.Stat(filepath.Join(m.cfg.CacheRoot, ih(n)))
		if ok != (err == nil) {
			t.Fatalf("download %d: entry %v but data dir err %v", n, ok, err)
		}
		return ok
	}

	if err := m.makeRoom(10, downloadKey{ih(9), 0}); err != nil {
		t.Fatalf("10 more: %v", err)
	}
	if exists(3) || !exists(2) || !exists(1) || !exists(4) {
		t.Fatalf("after freeing 10: want only the older watched download deleted")
	}
	if err := m.makeRoom(30, downloadKey{ih(9), 0}); err != nil {
		t.Fatalf("30 more: %v", err)
	}
	if exists(2) || exists(1) || !exists(4) {
		t.Fatalf("after freeing 20 more: want watched then unwatched deleted, incomplete kept")
	}
	if err := m.makeRoom(31, downloadKey{ih(9), 0}); !errors.Is(err, types.ErrDownloadBudget) {
		t.Fatalf("31 more = %v; want ErrDownloadBudget", err)
	}
	if !exists(4) {
		t.Fatal("incomplete download deleted")
	}
	m.SetDownloadBudgetFn(func() int64 { return -1 })
	if err := m.makeRoom(1<<40, downloadKey{ih(9), 0}); err != nil {
		t.Fatalf("unlimited budget: %v", err)
	}
}

// TestDeleteOneFileInPack: removing one episode of a season pack deletes only
// that file and marks only its pieces incomplete.
func TestDeleteOneFileInPack(t *testing.T) {
	m, e := newTestEngine(t, 0, newSeasonPackTorrent(t, 256<<10))
	e1, e2 := fileIndex(t, e, "Show.S01E01.mkv"), fileIndex(t, e, "Show.S01E02.mkv")
	for _, idx := range []int{e1, e2} {
		if _, err := m.AddDownload(e.infoHash, idx, types.AddOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	m.dl.mu.Lock()
	en1, en2 := *m.dl.entries[downloadKey{e.infoHash, e1}], *m.dl.entries[downloadKey{e.infoHash, e2}]
	m.dl.mu.Unlock()
	if en1.Path == "" || en1.PieceEnd <= en1.PieceBegin || en2.PieceBegin < en1.PieceEnd {
		t.Fatalf("layouts: %+v / %+v", en1, en2)
	}
	write := func(en downloadEntry) string {
		p := filepath.Join(e.path, filepath.FromSlash(en.Path))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		// Full size (sparse), as a completed file is: a wrong size makes the
		// storage treat the file as incomplete when the torrent reloads.
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(p, en.Size); err != nil {
			t.Fatal(err)
		}
		return p
	}
	p1, p2 := write(en1), write(en2)
	h := metainfo.NewHashFromHex(e.infoHash)
	for i := 0; i < en2.PieceEnd; i++ {
		if err := m.completion.Set(metainfo.PieceKey{InfoHash: h, Index: i}, g.Some(true)); err != nil {
			t.Fatal(err)
		}
	}
	complete := func(i int) bool {
		c, err := m.completion.Get(metainfo.PieceKey{InfoHash: h, Index: i})
		if err != nil {
			t.Fatal(err)
		}
		return c.Ok && c.Complete
	}

	if err := m.RemoveDownload(e.infoHash, e1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p1); !os.IsNotExist(err) {
		t.Fatalf("removed episode's file still there: %v", err)
	}
	if _, err := os.Stat(p2); err != nil {
		t.Fatalf("other episode's file gone: %v", err)
	}
	for i := en1.PieceBegin; i < en1.PieceEnd; i++ {
		if complete(i) {
			t.Fatalf("piece %d of the removed episode still complete", i)
		}
	}
	for i := en2.PieceBegin; i < en2.PieceEnd; i++ {
		if !complete(i) {
			t.Fatalf("piece %d of the other episode lost its completion", i)
		}
	}
	if !m.dl.hasTorrent(e.infoHash) {
		t.Fatal("the other episode's download was dropped")
	}
}

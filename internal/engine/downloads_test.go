// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package engine

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/anacrolix/torrent"

	"github.com/rivulet-kodi/rivulet-engine/internal/types"
)

// fakeData stands in for downloaded pieces under the torrent's cache dir.
func fakeData(t *testing.T, e *engine) {
	t.Helper()
	if err := os.MkdirAll(e.path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.path+"/piece.data", []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func waitBackground(t *testing.T, e *engine, idx int) {
	t.Helper()
	for range 500 {
		e.mu.Lock()
		ok := e.isBackgroundLocked(idx)
		e.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("file %d never became a background download", idx)
}

// TestDownloadsRegistryPersists: entries survive a reload; bad ones are dropped.
func TestDownloadsRegistryPersists(t *testing.T) {
	dir := t.TempDir()
	d := newDownloads(dir)
	d.mu.Lock()
	d.entries[downloadKey{scanIhOld, 3}] = &downloadEntry{InfoHash: scanIhOld, FileIdx: 3, Watched: true, Trackers: []string{"udp://t:1"}, AddedAt: time.Now()}
	d.saveLocked()
	d.mu.Unlock()
	got := newDownloads(dir)
	en, ok := got.entries[downloadKey{scanIhOld, 3}]
	if !ok || !en.Watched || len(en.Trackers) != 1 {
		t.Fatalf("reloaded %+v", got.entries)
	}
	if err := os.WriteFile(got.path, []byte(`[{"infoHash":"nothex","fileIdx":0}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if n := len(newDownloads(dir).entries); n != 0 {
		t.Fatalf("invalid entry kept: %d", n)
	}
}

// TestBackgroundFileSurvivesStreams: a download keeps its file downloading
// after its stream closes and while another file of the torrent is played,
// where a plain stream's file is dropped to priority None.
func TestBackgroundFileSurvivesStreams(t *testing.T) {
	m, e := newTestEngine(t, 0, newSeasonPackTorrent(t, 256<<10))
	e1, e2 := fileIndex(t, e, "Show.S01E01.mkv"), fileIndex(t, e, "Show.S01E02.mkv")
	if _, err := m.AddDownload(e.infoHash, e1, types.AddOptions{}); err != nil {
		t.Fatal(err)
	}
	waitBackground(t, e, e1)
	files := e.t.Files()

	r, _, err := e.NewReader(e1)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	if p := files[e1].Priority(); p == torrent.PiecePriorityNone {
		t.Fatalf("download dropped after its stream closed")
	}
	r2, _, err := e.NewReader(e2)
	if err != nil {
		t.Fatal(err)
	}
	if p := files[e1].Priority(); p == torrent.PiecePriorityNone {
		t.Fatalf("download demoted when another file was played")
	}
	_ = r2.Close()
	if p := files[e2].Priority(); p != torrent.PiecePriorityNone {
		t.Fatalf("plain stream file priority = %v after close; want None", p)
	}
}

// TestDownloadsKeepData: the idle janitor, the engine cap and RemoveEngine
// keep a torrent with downloads and its data; removing the last download
// deletes it.
func TestDownloadsKeepData(t *testing.T) {
	m, e := newTestEngine(t, 0, newSeasonPackTorrent(t, 256<<10))
	e1 := fileIndex(t, e, "Show.S01E01.mkv")
	if _, err := m.AddDownload(e.infoHash, e1, types.AddOptions{}); err != nil {
		t.Fatal(err)
	}
	fakeData(t, e)
	e.mu.Lock()
	e.lastAccess = time.Now().Add(-time.Hour)
	e.mu.Unlock()

	m.evictIdle(time.Second)
	if _, ok := m.GetEngine(e.infoHash); !ok {
		t.Fatal("idle janitor removed a torrent with downloads")
	}
	m.mu.Lock()
	victim := m.oldestIdleLocked()
	m.mu.Unlock()
	if victim != "" {
		t.Fatalf("engine cap would evict %s", victim)
	}
	if err := m.RemoveEngine(e.infoHash); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.GetEngine(e.infoHash); !ok {
		t.Fatal("RemoveEngine removed a torrent with downloads")
	}
	if _, err := os.Stat(e.path); err != nil {
		t.Fatalf("data dir gone: %v", err)
	}

	if err := m.RemoveDownload(e.infoHash, e1); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.GetEngine(e.infoHash); ok {
		t.Fatal("torrent kept after its last download was removed")
	}
	if _, err := os.Stat(e.path); !os.IsNotExist(err) {
		t.Fatalf("data dir kept after removal: %v", err)
	}
	if err := m.RemoveDownload(e.infoHash, e1); !errors.Is(err, ErrNoDownload) {
		t.Fatalf("second remove = %v; want ErrNoDownload", err)
	}
}

// TestCompletedDownloadsUnloadKeepingData: once every download of a torrent is
// complete and nothing reads it, the torrent is unloaded but its data stays.
func TestCompletedDownloadsUnloadKeepingData(t *testing.T) {
	m, e := newTestEngine(t, 0, newSeasonPackTorrent(t, 256<<10))
	e1 := fileIndex(t, e, "Show.S01E01.mkv")
	if _, err := m.AddDownload(e.infoHash, e1, types.AddOptions{}); err != nil {
		t.Fatal(err)
	}
	m.dl.mu.Lock()
	m.dl.entries[downloadKey{e.infoHash, e1}].Complete = true
	m.dl.mu.Unlock()
	fakeData(t, e)
	e.mu.Lock()
	e.lastAccess = time.Now().Add(-time.Hour)
	e.mu.Unlock()

	m.tickDownloads()
	if _, ok := m.GetEngine(e.infoHash); ok {
		t.Fatal("completed torrent still loaded")
	}
	if _, err := os.Stat(e.path); err != nil {
		t.Fatalf("data dir deleted on unload: %v", err)
	}
	list := m.Downloads()
	if len(list) != 1 || list[0].State != types.DownloadComplete {
		t.Fatalf("downloads = %+v", list)
	}
}

// TestPausedDownloadIsNotBackground: pausing stops the background download.
func TestPausedDownloadIsNotBackground(t *testing.T) {
	m, e := newTestEngine(t, 0, newSeasonPackTorrent(t, 256<<10))
	e1 := fileIndex(t, e, "Show.S01E01.mkv")
	if _, err := m.AddDownload(e.infoHash, e1, types.AddOptions{}); err != nil {
		t.Fatal(err)
	}
	waitBackground(t, e, e1)
	paused := true
	d, err := m.UpdateDownload(e.infoHash, e1, types.DownloadPatch{Paused: &paused})
	if err != nil || d.State != types.DownloadPaused {
		t.Fatalf("pause = %+v, %v", d, err)
	}
	if p := e.t.Files()[e1].Priority(); p != torrent.PiecePriorityNone {
		t.Fatalf("paused file priority = %v; want None", p)
	}
}

// TestDownloadsNeedDisk: the in-RAM cache refuses downloads.
func TestDownloadsNeedDisk(t *testing.T) {
	m, e := newTestEngine(t, 32<<20, newSeasonPackTorrent(t, 256<<10))
	if _, err := m.AddDownload(e.infoHash, 0, types.AddOptions{}); !errors.Is(err, ErrDownloadsNeedDisk) {
		t.Fatalf("err = %v; want ErrDownloadsNeedDisk", err)
	}
}

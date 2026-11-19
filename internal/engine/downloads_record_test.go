// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent"

	"github.com/rivulet-kodi/rivulet-engine/internal/types"
)

func recordOf(t *testing.T, m *manager, ih string, idx int) (types.Download, bool) {
	t.Helper()
	for _, d := range m.Downloads() {
		if d.InfoHash == ih && d.FileIdx == idx {
			return d, true
		}
	}
	return types.Download{}, false
}

// TestPlayedFileIsKept: a player's reader records its file as kept (origin
// "play"), so it keeps downloading after the stream closes and the idle
// janitor leaves the torrent and its data alone.
func TestPlayedFileIsKept(t *testing.T) {
	m, e := newTestEngine(t, 0, newSeasonPackTorrent(t, 256<<10))
	e2 := fileIndex(t, e, "Show.S01E02.mkv")
	r, _, err := e.NewReader(e2)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	d, ok := recordOf(t, m, e.infoHash, e2)
	if !ok || d.Kind != types.DownloadKept || len(d.Origins) != 1 || d.Origins[0] != "play" || d.LastPlayed == nil {
		t.Fatalf("record = %+v (found %v)", d, ok)
	}
	waitBackground(t, e, e2)
	if p := e.t.Files()[e2].Priority(); p == torrent.PiecePriorityNone {
		t.Fatal("played file dropped to None after its stream closed")
	}
	fakeData(t, e)
	e.mu.Lock()
	e.lastAccess = time.Now().Add(-time.Hour)
	e.mu.Unlock()
	m.evictIdle(time.Second)
	if _, ok := m.GetEngine(e.infoHash); !ok {
		t.Fatal("idle janitor removed a played (kept) torrent")
	}
}

// TestProbeReaderIsNotKept: a probe (the web client's measurement) records
// nothing.
func TestProbeReaderIsNotKept(t *testing.T) {
	m, e := newTestEngine(t, 0, newSeasonPackTorrent(t, 256<<10))
	e1 := fileIndex(t, e, "Show.S01E01.mkv")
	r, _, err := e.NewProbeReader(e1)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	if d, ok := recordOf(t, m, e.infoHash, e1); ok {
		t.Fatalf("probe recorded %+v", d)
	}
}

// TestTemporaryLeaseLapses: a temporary file is held (primed, not
// downloaded whole) until its lease lapses; then it and its data are deleted.
// Renewing it pushes the lease forward.
func TestTemporaryLeaseLapses(t *testing.T) {
	m, e := newTestEngine(t, 0, newSeasonPackTorrent(t, 256<<10))
	e1 := fileIndex(t, e, "Show.S01E01.mkv")
	req := types.DownloadRequest{Kind: types.DownloadTemporary, Origin: "picker", StartFraction: 0.5, Meta: &types.DownloadMeta{MetaID: "tt1", VideoID: "tt1:1:1"}}
	if _, err := m.AddDownload(e.infoHash, e1, types.AddOptions{}, req); err != nil {
		t.Fatal(err)
	}
	d, _ := recordOf(t, m, e.infoHash, e1)
	if d.Kind != types.DownloadTemporary || d.Meta == nil || d.Meta.VideoID != "tt1:1:1" {
		t.Fatalf("record = %+v", d)
	}
	e.mu.Lock()
	_, held := e.held[e1]
	bg := e.isBackgroundLocked(e1)
	e.mu.Unlock()
	if !held || bg {
		t.Fatalf("held=%v background=%v; want held, not background", held, bg)
	}
	fakeData(t, e)

	m.expireTemporary()
	if _, ok := recordOf(t, m, e.infoHash, e1); !ok {
		t.Fatal("temporary file deleted before its lease lapsed")
	}
	m.dl.mu.Lock()
	m.dl.entries[downloadKey{e.infoHash, e1}].leaseUntil = time.Now().Add(-time.Second)
	m.dl.mu.Unlock()
	m.expireTemporary()
	if _, ok := recordOf(t, m, e.infoHash, e1); ok {
		t.Fatal("temporary file kept after its lease lapsed")
	}
	if _, err := os.Stat(e.path); !os.IsNotExist(err) {
		t.Fatalf("temporary file's data kept: %v", err)
	}
}

// TestTemporaryPromotedByPlay: playing a temporary file makes it kept, and
// adding a temporary request for a kept file doesn't demote it.
func TestTemporaryPromotedByPlay(t *testing.T) {
	m, e := newTestEngine(t, 0, newSeasonPackTorrent(t, 256<<10))
	e1 := fileIndex(t, e, "Show.S01E01.mkv")
	tmp := types.DownloadRequest{Kind: types.DownloadTemporary, Origin: "picker"}
	if _, err := m.AddDownload(e.infoHash, e1, types.AddOptions{}, tmp); err != nil {
		t.Fatal(err)
	}
	r, _, err := e.NewReader(e1)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	d, _ := recordOf(t, m, e.infoHash, e1)
	if d.Kind != types.DownloadKept || strings.Join(d.Origins, ",") != "picker,play" {
		t.Fatalf("after play: %+v", d)
	}
	if _, err := m.AddDownload(e.infoHash, e1, types.AddOptions{}, tmp); err != nil {
		t.Fatal(err)
	}
	if d, _ := recordOf(t, m, e.infoHash, e1); d.Kind != types.DownloadKept {
		t.Fatalf("temporary request demoted a kept file: %+v", d)
	}
	e.mu.Lock()
	_, held := e.held[e1]
	e.mu.Unlock()
	if held {
		t.Fatal("kept file still held as temporary")
	}
}

// TestTemporaryNotDemotedByPlayingAnotherFile: measuring the next episode of
// the pack being played keeps its primed pieces (playing a file demotes the
// torrent's other unrecorded files).
func TestTemporaryNotDemotedByPlayingAnotherFile(t *testing.T) {
	m, e := newTestEngine(t, 0, newSeasonPackTorrent(t, 256<<10))
	e1, e2 := fileIndex(t, e, "Show.S01E01.mkv"), fileIndex(t, e, "Show.S01E02.mkv")
	if _, err := m.AddDownload(e.infoHash, e2, types.AddOptions{}, types.DownloadRequest{Kind: types.DownloadTemporary}); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	_, primed := e.primed[e2]
	e.mu.Unlock()
	if !primed {
		t.Fatal("temporary file not primed")
	}
	r, _, err := e.NewReader(e1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	e.mu.Lock()
	_, primed = e.primed[e2]
	e.mu.Unlock()
	if !primed {
		t.Fatal("playing another file demoted the temporary file")
	}
}

// TestTemporaryLapsedAfterRestart: a temporary record loaded from disk has no
// lease and is deleted on the first tick.
func TestTemporaryLapsedAfterRestart(t *testing.T) {
	dir := t.TempDir()
	d := newDownloads(dir)
	d.mu.Lock()
	d.entries[downloadKey{scanIhOld, 1}] = &downloadEntry{InfoHash: scanIhOld, FileIdx: 1, Kind: types.DownloadTemporary, AddedAt: time.Now()}
	d.saveLocked()
	d.mu.Unlock()
	en := newDownloads(dir).entries[downloadKey{scanIhOld, 1}]
	if en == nil || en.kind() != types.DownloadTemporary || !time.Now().After(en.leaseUntil) {
		t.Fatalf("reloaded temporary = %+v; want a lapsed lease", en)
	}
}

// TestLeftoverFolders: a cache folder with no record is listed as a leftover
// with its files and size, and deleted by info hash; a recorded torrent's
// folder isn't a leftover, and recording a leftover's torrent adopts it.
func TestLeftoverFolders(t *testing.T) {
	m, e := newTestEngine(t, 0, newSeasonPackTorrent(t, 256<<10))
	if _, err := m.AddDownload(e.infoHash, fileIndex(t, e, "Show.S01E01.mkv"), types.AddOptions{}, types.DownloadRequest{}); err != nil {
		t.Fatal(err)
	}
	fakeData(t, e)
	old := filepath.Join(m.cfg.CacheRoot, scanIhOld)
	if err := os.MkdirAll(filepath.Join(old, "Show"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "Show", "Show.S01E05.mkv.part"), make([]byte, 64<<10), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(m.cfg.CacheRoot, "downloads"), 0o700); err != nil {
		t.Fatal(err)
	}

	m.scanLeftovers()
	left, ok := recordOf(t, m, scanIhOld, leftoverIdx)
	if !ok || left.Kind != types.DownloadLeftover || left.BytesOnDisk <= 0 || len(left.Files) != 1 || left.Files[0] != "Show/Show.S01E05.mkv.part" || left.Name != "Show" {
		t.Fatalf("leftover = %+v (found %v)", left, ok)
	}
	if _, ok := recordOf(t, m, e.infoHash, leftoverIdx); ok {
		t.Fatal("a recorded torrent's folder listed as a leftover")
	}
	if disk := m.Disk(); disk.Used < left.BytesOnDisk {
		t.Fatalf("disk used %d < leftover %d", disk.Used, left.BytesOnDisk)
	}

	if err := m.RemoveDownload(scanIhOld, leftoverIdx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("leftover folder kept: %v", err)
	}
	if _, ok := recordOf(t, m, scanIhOld, leftoverIdx); ok {
		t.Fatal("leftover record kept after delete")
	}

	// A leftover whose torrent is recorded again is adopted.
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	m.scanLeftovers()
	if _, ok := recordOf(t, m, scanIhOld, leftoverIdx); !ok {
		t.Fatal("leftover not found again")
	}
	m.notePlayed(scanIhOld, 2)
	if _, ok := recordOf(t, m, scanIhOld, leftoverIdx); ok {
		t.Fatal("leftover kept after its torrent was recorded")
	}
}

// TestRemoveTorrentData deletes every record of a torrent and its folder.
func TestRemoveTorrentData(t *testing.T) {
	m, e := newTestEngine(t, 0, newSeasonPackTorrent(t, 256<<10))
	for _, name := range []string{"Show.S01E01.mkv", "Show.S01E02.mkv"} {
		if _, err := m.AddDownload(e.infoHash, fileIndex(t, e, name), types.AddOptions{}, types.DownloadRequest{}); err != nil {
			t.Fatal(err)
		}
	}
	fakeData(t, e)
	if err := m.RemoveTorrentData(e.infoHash); err != nil {
		t.Fatal(err)
	}
	if n := len(m.Downloads()); n != 0 {
		t.Fatalf("%d records left", n)
	}
	if _, ok := m.GetEngine(e.infoHash); ok {
		t.Fatal("torrent still loaded")
	}
	if _, err := os.Stat(e.path); !os.IsNotExist(err) {
		t.Fatalf("data kept: %v", err)
	}
}

// TestPatchMeta attaches a client's episode details to a recorded file.
func TestPatchMeta(t *testing.T) {
	m, e := newTestEngine(t, 0, newSeasonPackTorrent(t, 256<<10))
	e1 := fileIndex(t, e, "Show.S01E01.mkv")
	m.notePlayed(e.infoHash, e1)
	meta := types.DownloadMeta{MetaID: "tt1", VideoID: "tt1:1:1", Name: "Show", Poster: "https://img/p.jpg", Season: 1, Episode: 1, Title: "Pilot"}
	if _, err := m.UpdateDownload(e.infoHash, e1, types.DownloadPatch{Meta: &meta}); err != nil {
		t.Fatal(err)
	}
	d, _ := recordOf(t, m, e.infoHash, e1)
	if d.Meta == nil || *d.Meta != meta {
		t.Fatalf("meta = %+v", d.Meta)
	}
}

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

	g "github.com/anacrolix/generics"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"

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

// TestUnrecordedFolder: a cache folder with no record gets a marker (never
// listed); its files are unkept rows ("pending" without a file list), count
// toward the disk used, and deleting the last one removes the folder and the
// marker; recording its torrent drops the marker.
func TestUnrecordedFolder(t *testing.T) {
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

	m.scanLeftovers()
	if _, ok := m.dl.entries[downloadKey{scanIhOld, leftoverIdx}]; !ok {
		t.Fatal("no marker for the unrecorded folder")
	}
	var rows []types.Download
	for _, d := range m.Downloads() {
		if d.Kind == types.DownloadLeftover {
			t.Fatalf("a marker listed: %+v", d)
		}
		if d.InfoHash == scanIhOld {
			rows = append(rows, d)
		}
	}
	// 64 KiB is under the tiny-file floor, but it's all the folder holds.
	if len(rows) != 1 || rows[0].Kind != types.DownloadUnkept || rows[0].Path != "Show/Show.S01E05.mkv.part" ||
		rows[0].Info != types.DownloadInfoPending || rows[0].Size != 0 || rows[0].BytesOnDisk <= 0 {
		t.Fatalf("unkept rows = %+v", rows)
	}
	if disk := m.Disk(); disk.Used < rows[0].BytesOnDisk {
		t.Fatalf("disk used %d < unkept %d", disk.Used, rows[0].BytesOnDisk)
	}
	m.metaFetch.setMissing(scanIhOld, true)
	if d := m.unkeptViews(); len(d) != 1 || d[0].Info != types.DownloadInfoMissing {
		t.Fatalf("after a failed fetch: %+v", d)
	}

	if err := m.RemoveUnkept(scanIhOld, rows[0].Path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("emptied folder kept: %v", err)
	}
	if _, ok := m.dl.entries[downloadKey{scanIhOld, leftoverIdx}]; ok {
		t.Fatal("marker kept after its folder emptied")
	}

	// A marked folder whose torrent is recorded again loses its marker.
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	m.scanLeftovers()
	m.notePlayed(scanIhOld, 2)
	if _, ok := m.dl.entries[downloadKey{scanIhOld, leftoverIdx}]; ok {
		t.Fatal("marker kept after its torrent was recorded")
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

// unkeptOf returns the unkept row at path.
func unkeptOf(m *manager, ih, path string) (types.Download, bool) {
	for _, d := range m.Downloads() {
		if d.Kind == types.DownloadUnkept && d.InfoHash == ih && d.Path == path {
			return d, true
		}
	}
	return types.Download{}, false
}

// TestUnkeptFiles: an unrecorded file in a recorded torrent's folder is an
// unkept row (tiny files aren't) with its real index and size, loaded or not
// (the saved file list), and its progress: a .part file from the piece
// database, a file with its final name and full size complete. Recorded files
// and paths outside the folder can't be deleted as unkept. Resuming it
// records it as kept.
func TestUnkeptFiles(t *testing.T) {
	const pieceLen = 256 << 10
	m, e := newTestEngine(t, 0, newSeasonPackTorrent(t, pieceLen))
	e1 := fileIndex(t, e, "Show.S01E01.mkv")
	e2 := fileIndex(t, e, "Show.S01E02.mkv")
	if _, err := m.AddDownload(e.infoHash, e1, types.AddOptions{}, types.DownloadRequest{Meta: &types.DownloadMeta{MetaID: "tt1", Name: "Show", Season: 1, Episode: 1}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := recordOf(t, m, e.infoHash, e1); !ok { // fills the record's path
		t.Fatal("no record")
	}
	m.saveMetainfo(e.infoHash, e.t)
	pack := filepath.Join(m.cfg.CacheRoot, e.infoHash, "pack")
	if err := os.MkdirAll(pack, 0o700); err != nil {
		t.Fatal(err)
	}
	part := filepath.Join(pack, "Show.S01E02.mkv.part")
	if err := os.WriteFile(part, make([]byte, 2<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pack, "info.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	size := e.t.Files()[e2].Length()

	var unkept []types.Download
	for _, d := range m.Downloads() {
		if d.Kind == types.DownloadUnkept {
			unkept = append(unkept, d)
		}
	}
	if len(unkept) != 1 {
		t.Fatalf("unkept = %+v", unkept)
	}
	p := unkept[0]
	if p.Path != "pack/Show.S01E02.mkv.part" || p.FileIdx != e2 || p.Size != size || p.BytesOnDisk < 2<<20 || p.Info != "" ||
		p.Meta == nil || p.Meta.MetaID != "tt1" || p.Meta.Episode != 0 {
		t.Fatalf("unkept = %+v meta %+v", p, p.Meta)
	}
	if disk := m.Disk(); disk.Used < p.BytesOnDisk {
		t.Fatalf("disk used %d < unkept %d", disk.Used, p.BytesOnDisk)
	}

	// Unloaded: size from the saved list, progress from the piece database.
	_, begin, _ := e.fileLayout(e2)
	m.mu.Lock()
	m.unloadLocked(e.infoHash, e)
	m.mu.Unlock()
	ih := metainfo.NewHashFromHex(e.infoHash)
	for i := begin; i < begin+3; i++ {
		if err := m.completion.Set(metainfo.PieceKey{InfoHash: ih, Index: i}, g.Some(true)); err != nil {
			t.Fatal(err)
		}
	}
	p, ok := unkeptOf(m, e.infoHash, "pack/Show.S01E02.mkv.part")
	if !ok || p.FileIdx != e2 || p.Size != size || p.BytesCompleted != 3*pieceLen {
		t.Fatalf("unloaded .part = %+v (found %v); want %d done", p, ok, 3*pieceLen)
	}
	// Its final name at full size: complete, as anacrolix's part-file storage takes it.
	final := filepath.Join(pack, "Show.S01E02.mkv")
	if err := os.Rename(part, final); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(final, size); err != nil {
		t.Fatal(err)
	}
	if p, ok = unkeptOf(m, e.infoHash, "pack/Show.S01E02.mkv"); !ok || p.BytesCompleted != size {
		t.Fatalf("final-name file = %+v (found %v); want complete", p, ok)
	}

	for _, bad := range []string{"pack/Show.S01E01.mkv", "../" + e.infoHash, "pack/missing.mkv", "pack"} {
		if err := m.RemoveUnkept(e.infoHash, bad); err != ErrDownloadPath {
			t.Fatalf("RemoveUnkept(%q) = %v, want ErrDownloadPath", bad, err)
		}
	}

	// Resume = record it as kept (the saved list gives the index at once).
	if _, err := m.AddDownload(e.infoHash, e2, types.AddOptions{}, types.DownloadRequest{Origin: "resume"}); err != nil {
		t.Fatal(err)
	}
	if d, ok := recordOf(t, m, e.infoHash, e2); !ok || d.Kind != types.DownloadKept || len(d.Origins) != 1 || d.Origins[0] != "resume" {
		t.Fatalf("resumed = %+v (found %v)", d, ok)
	}
	if _, ok := unkeptOf(m, e.infoHash, "pack/Show.S01E02.mkv"); ok {
		t.Fatal("resumed file still listed as unkept")
	}
}

// TestUnkeptDelete: deleting one unkept file leaves the rest; deleting a
// torrent's last record keeps the unkept files beside it under a marker, and
// deleting the last of those removes the folder, marker and saved list.
func TestUnkeptDelete(t *testing.T) {
	m, e := newTestEngine(t, 0, newSeasonPackTorrent(t, 256<<10))
	e1 := fileIndex(t, e, "Show.S01E01.mkv")
	if _, err := m.AddDownload(e.infoHash, e1, types.AddOptions{}, types.DownloadRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := recordOf(t, m, e.infoHash, e1); !ok {
		t.Fatal("no record")
	}
	m.saveMetainfo(e.infoHash, e.t)
	pack := filepath.Join(m.cfg.CacheRoot, e.infoHash, "pack")
	_ = os.MkdirAll(pack, 0o700)
	for _, name := range []string{"Show.S01E01.mkv.part", "Show.S01E02.mkv.part", "Extra.mkv"} {
		if err := os.WriteFile(filepath.Join(pack, name), make([]byte, 2<<20), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.RemoveUnkept(e.infoHash, "pack/Extra.mkv"); err != nil {
		t.Fatal(err)
	}
	if _, ok := unkeptOf(m, e.infoHash, "pack/Show.S01E02.mkv.part"); !ok {
		t.Fatal("the other unkept file went too")
	}

	if err := m.RemoveDownload(e.infoHash, e1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(pack, "Show.S01E01.mkv.part")); !os.IsNotExist(err) {
		t.Fatalf("deleted record's file kept: %v", err)
	}
	if _, ok := unkeptOf(m, e.infoHash, "pack/Show.S01E02.mkv.part"); !ok {
		t.Fatal("unkept file deleted with the last record")
	}
	if _, ok := m.dl.entries[downloadKey{e.infoHash, leftoverIdx}]; !ok {
		t.Fatal("no marker for the folder's unkept data")
	}

	if err := m.RemoveUnkept(e.infoHash, "pack/Show.S01E02.mkv.part"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(m.cfg.CacheRoot, e.infoHash)); !os.IsNotExist(err) {
		t.Fatalf("emptied folder kept: %v", err)
	}
	if _, err := os.Stat(m.metainfoPath(e.infoHash)); !os.IsNotExist(err) {
		t.Fatalf("saved list kept: %v", err)
	}
	if _, ok := m.dl.entries[downloadKey{e.infoHash, leftoverIdx}]; ok {
		t.Fatal("marker kept")
	}
}

// TestRecordWithoutPath: a record whose path isn't stored yet still hides its
// file from the unkept rows (its index's path from the file list).
func TestRecordWithoutPath(t *testing.T) {
	m, e := newTestEngine(t, 0, newSeasonPackTorrent(t, 256<<10))
	e1 := fileIndex(t, e, "Show.S01E01.mkv")
	m.saveMetainfo(e.infoHash, e.t)
	m.mu.Lock()
	m.unloadLocked(e.infoHash, e)
	m.mu.Unlock()
	m.dl.mu.Lock()
	m.dl.entries[downloadKey{e.infoHash, e1}] = &downloadEntry{InfoHash: e.infoHash, FileIdx: e1, Kind: types.DownloadKept, AddedAt: time.Now()}
	m.dl.mu.Unlock()
	pack := filepath.Join(m.cfg.CacheRoot, e.infoHash, "pack")
	_ = os.MkdirAll(pack, 0o700)
	if err := os.WriteFile(filepath.Join(pack, "Show.S01E01.mkv.part"), make([]byte, 2<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	if rows := m.unkeptViews(); len(rows) != 0 {
		t.Fatalf("recorded file listed as unkept: %+v", rows)
	}
}

// TestFirstFileOrder: of a torrent's incomplete kept files, the lowest order
// is at High priority and the rest at Normal; pausing it moves High to the
// next one.
func TestFirstFileOrder(t *testing.T) {
	m, e := newTestEngine(t, 0, newSeasonPackTorrent(t, 256<<10))
	e1, e2 := fileIndex(t, e, "Show.S01E01.mkv"), fileIndex(t, e, "Show.S01E02.mkv")
	for _, f := range []struct{ idx, order int }{{e2, 1002}, {e1, 1001}} {
		if _, err := m.AddDownload(e.infoHash, f.idx, types.AddOptions{}, types.DownloadRequest{Origin: "manual", Order: f.order}); err != nil {
			t.Fatal(err)
		}
	}
	m.tickDownloads()
	files := e.t.Files()
	if got := files[e1].Priority(); got != torrent.PiecePriorityHigh {
		t.Fatalf("E1 priority %v, want High", got)
	}
	if got := files[e2].Priority(); got != torrent.PiecePriorityNormal {
		t.Fatalf("E2 priority %v, want Normal", got)
	}
	if d, _ := recordOf(t, m, e.infoHash, e1); !d.First || d.Order != 1001 {
		t.Fatalf("E1 view: %+v", d)
	}
	paused := true
	if _, err := m.UpdateDownload(e.infoHash, e1, types.DownloadPatch{Paused: &paused}); err != nil {
		t.Fatal(err)
	}
	m.tickDownloads()
	if got := files[e2].Priority(); got != torrent.PiecePriorityHigh {
		t.Fatalf("after pausing E1, E2 priority %v, want High", got)
	}
	if d, _ := recordOf(t, m, e.infoHash, e1); d.First {
		t.Fatalf("paused E1 still first: %+v", d)
	}
}

// A pause landing while setFirst is between its check and its SetPriority
// must not leave the paused file at High.
func TestFirstFilePausedNotHigh(t *testing.T) {
	m, e := newTestEngine(t, 0, newSeasonPackTorrent(t, 256<<10))
	e1 := fileIndex(t, e, "Show.S01E01.mkv")
	if _, err := m.AddDownload(e.infoHash, e1, types.AddOptions{}, types.DownloadRequest{Origin: "manual", Order: 1001}); err != nil {
		t.Fatal(err)
	}
	paused := make(chan struct{})
	testHookFirstChecked = func() {
		testHookFirstChecked = nil
		go func() {
			e.setBackground(e1, false)
			close(paused)
		}()
		select {
		case <-paused:
		case <-time.After(100 * time.Millisecond):
		}
	}
	defer func() { testHookFirstChecked = nil }()
	e.setFirst(e1)
	<-paused
	if got := e.t.Files()[e1].Priority(); got == torrent.PiecePriorityHigh {
		t.Fatalf("paused file left at High")
	}
}

func TestFirstFileRule(t *testing.T) {
	t0 := time.Now()
	cases := []struct {
		name string
		rs   []fileRank
		want int
	}{
		{"none", nil, -1},
		{"lowest order", []fileRank{{idx: 3, order: 1003}, {idx: 1, order: 1001}, {idx: 2, order: 1002}}, 1},
		{"unranked after ranked", []fileRank{{idx: 5, added: t0}, {idx: 4, order: 2001, added: t0.Add(time.Hour)}}, 4},
		{"oldest when unranked", []fileRank{{idx: 5, added: t0.Add(time.Minute)}, {idx: 6, added: t0}}, 6},
		{"playing first", []fileRank{{idx: 1, order: 1001}, {idx: 7, reading: true}}, 7},
	}
	for _, c := range cases {
		if got := firstFile(c.rs); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}

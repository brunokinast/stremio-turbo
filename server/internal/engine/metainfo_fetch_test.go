// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/rivulet-kodi/rivulet-engine/internal/types"
)

// TestFetchMetainfo: a marked folder with data and no saved file list gets its
// metadata from the swarm (a local seeder here); the list is saved, the
// torrent unloaded again, and its unkept file listed with its size. One that
// nobody answers for reads "missing".
func TestFetchMetainfo(t *testing.T) {
	const pieceLen = 256 << 10
	src := t.TempDir()
	pack := filepath.Join(src, "pack")
	_ = os.MkdirAll(pack, 0o700)
	data := make([]byte, pieceLen*6)
	for i := range data {
		data[i] = byte(i * 13)
	}
	if err := os.WriteFile(filepath.Join(pack, "Show.S01E01.mkv"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	info := metainfo.Info{PieceLength: pieceLen}
	if err := info.BuildFromFilePath(pack); err != nil {
		t.Fatal(err)
	}
	ib, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	mi := &metainfo.MetaInfo{InfoBytes: ib}
	ih := mi.HashInfoBytes().HexString()

	scfg := torrent.NewDefaultClientConfig()
	scfg.DataDir = src
	scfg.Seed = true
	scfg.NoDHT = true
	scfg.DisableTrackers = true
	scfg.DisableIPv6 = true
	scfg.DisableUTP = true
	scfg.NoDefaultPortForwarding = true
	scfg.ListenPort = 0
	scfg.ListenHost = func(string) string { return "127.0.0.1" }
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

	cfg := newJanitorTestCfg(t)
	em, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = em.Close() })
	m := em.(*manager)
	// Two folders from an earlier run: one with a seeder, one with none.
	dead := scanIhOld
	for _, dir := range []string{filepath.Join(cfg.CacheRoot, ih, "pack"), filepath.Join(cfg.CacheRoot, dead)} {
		_ = os.MkdirAll(dir, 0o700)
		if err := os.WriteFile(filepath.Join(dir, "Show.S01E01.mkv.part"), make([]byte, 2<<20), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m.scanLeftovers()
	todo := m.metainfoTodo()
	if len(todo) != 2 {
		t.Fatalf("todo = %+v", todo)
	}
	if rows := m.unkeptViews(); len(rows) != 2 || rows[0].Info != types.DownloadInfoPending {
		t.Fatalf("before: %+v", rows)
	}

	old := metaFetchTimeout
	metaFetchTimeout = 2 * time.Second
	t.Cleanup(func() { metaFetchTimeout = old })
	done := make(chan struct{})
	go func() { m.fetchMetainfo(todo); close(done) }()
	// Point the fetched torrent at the seeder once the job has loaded it.
	for deadline := time.Now().Add(10 * time.Second); ; {
		m.mu.RLock()
		e, ok := m.engines[ih]
		m.mu.RUnlock()
		if ok {
			e.t.AddClientPeer(seeder)
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the job never loaded the torrent")
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("fetch job didn't finish")
	}

	if _, err := os.Stat(m.metainfoPath(ih)); err != nil {
		t.Fatalf("file list not saved: %v", err)
	}
	m.mu.RLock()
	_, ih1Loaded := m.engines[ih]
	_, deadLoaded := m.engines[dead]
	m.mu.RUnlock()
	if ih1Loaded || deadLoaded {
		t.Fatalf("left loaded: fetched %v, dead %v", ih1Loaded, deadLoaded)
	}
	got := map[string]types.Download{}
	for _, d := range m.unkeptViews() {
		got[d.InfoHash] = d
	}
	if d := got[ih]; d.Info != "" || d.FileIdx != 0 || d.Size != int64(len(data)) {
		t.Fatalf("fetched folder's row = %+v", d)
	}
	if d := got[dead]; d.Info != types.DownloadInfoMissing || d.Size != 0 {
		t.Fatalf("dead folder's row = %+v", d)
	}
}

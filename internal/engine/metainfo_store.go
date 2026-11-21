// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/rivulet-kodi/rivulet-engine/internal/logging"
)

// A torrent's metadata (its file list) is saved beside its data folder as
// <cache>/<infoHash>.torrent once known, and reused whenever the torrent is
// loaded again. Without it, an unloaded torrent's files have no known size
// (only bytes on disk), and reloading it waits for the swarm to send the
// metadata again. It's deleted with the data.

// metainfoPath is where ih's metadata is saved, "" when nothing is kept on
// disk (memory storage, or no cache root).
func (m *manager) metainfoPath(ih string) string {
	if m.mem != nil || m.cfg.CacheRoot == "" {
		return ""
	}
	return filepath.Join(m.cfg.CacheRoot, ih+".torrent")
}

// savedMetainfo returns ih's saved metadata, nil when there's none.
func (m *manager) savedMetainfo(ih string) []byte {
	p := m.metainfoPath(ih)
	if p == "" {
		return nil
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	return raw
}

// saveMetainfo writes t's metadata for ih (temp file, then rename), unless
// it's already saved.
func (m *manager) saveMetainfo(ih string, t *torrent.Torrent) {
	p := m.metainfoPath(ih)
	if p == "" {
		return
	}
	if _, err := os.Stat(p); err == nil {
		return
	}
	mi := t.Metainfo()
	err := writeFileAtomic(p, func(tmp string) error {
		f, err := os.Create(tmp)
		if err != nil {
			return err
		}
		werr := mi.Write(f)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		return werr
	})
	if err != nil {
		logging.For("engine").Warn("saving torrent metadata", "info_hash", ih, "err", err)
	}
}

// forgetMetainfo deletes ih's saved metadata (its data is gone).
func (m *manager) forgetMetainfo(ih string) {
	if p := m.metainfoPath(ih); p != "" {
		_ = os.Remove(p)
	}
}

// savedFile is one file of a torrent, from its saved metadata.
type savedFile struct {
	path        string // relative to the torrent's cache folder, slash-separated
	display     string // what anacrolix shows (without the torrent's folder name)
	length      int64
	begin, end  int // pieces wholly inside the file ([begin, end))
	first, last int // pieces the file touches ([first, last])
}

// savedLayout is a torrent's file list and piece size from saved metadata.
type savedLayout struct {
	files    []savedFile
	pieceLen int64
	pieces   int
}

type layoutCacheEntry struct {
	mod    time.Time
	size   int64
	layout *savedLayout
}

// savedFileList returns ih's file list from its saved metadata (parsed once
// per version of the file), nil when there's none.
func (m *manager) savedFileList(ih string) *savedLayout {
	p := m.metainfoPath(ih)
	if p == "" {
		return nil
	}
	st, err := os.Stat(p)
	if err != nil {
		return nil
	}
	m.layoutMu.Lock()
	defer m.layoutMu.Unlock()
	if c, ok := m.layouts[ih]; ok && c.mod.Equal(st.ModTime()) && c.size == st.Size() {
		return c.layout
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	mi, err := metainfo.Load(bytes.NewReader(raw))
	if err != nil || mi.HashInfoBytes().HexString() != ih {
		return nil
	}
	info, err := mi.UnmarshalInfo()
	if err != nil || info.PieceLength <= 0 {
		return nil
	}
	l := &savedLayout{pieceLen: info.PieceLength, pieces: info.NumPieces()}
	total := info.TotalLength()
	for _, fi := range info.UpvertedFiles() {
		f := savedFile{
			path:    strings.Join(append([]string{info.BestName()}, fi.BestPath()...), "/"),
			display: fi.DisplayPath(&info),
			length:  fi.Length,
		}
		off, stop := fi.TorrentOffset, fi.TorrentOffset+fi.Length
		f.begin = int((off + l.pieceLen - 1) / l.pieceLen)
		f.end = int(stop / l.pieceLen)
		if stop == total {
			f.end = l.pieces
		}
		if f.end < f.begin {
			f.end = f.begin
		}
		f.first = int(off / l.pieceLen)
		f.last = f.first
		if fi.Length > 0 {
			f.last = int((stop - 1) / l.pieceLen)
		}
		l.files = append(l.files, f)
	}
	if m.layouts == nil {
		m.layouts = map[string]layoutCacheEntry{}
	}
	m.layouts[ih] = layoutCacheEntry{mod: st.ModTime(), size: st.Size(), layout: l}
	return l
}

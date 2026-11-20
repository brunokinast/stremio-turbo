// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package engine

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/rivulet-kodi/rivulet-engine/internal/logging"
	"github.com/rivulet-kodi/rivulet-engine/internal/types"
)

// Unkept files: data on disk with no record of its own (an episode that was
// streamed and never kept, a folder left by an earlier run). They're listed
// beside the records, one per file, with their real size and progress from
// the torrent's file list (loaded, or saved: metainfo_store.go), and stay
// until deleted on request. A folder with no record keeps a leftover marker
// (scanLeftovers, markFolder) so the janitor leaves its data alone.

// unkeptMinBytes hides tiny unrecorded files (a release's .txt or .nfo):
// nothing worth deleting on its own, unless they're all a folder holds.
const unkeptMinBytes = 1 << 20

// progressKey and progressEntry cache an unloaded unkept file's progress by
// its modification time and size (the piece database is read once per change).
type progressKey struct{ ih, path string }

type progressEntry struct {
	mod  time.Time
	size int64
	done int64
}

// unkeptFile is a torrent file as unkeptViews needs it.
type unkeptFile struct {
	path, display string
	length        int64
	begin, end    int   // pieces wholly inside it
	done          int64 // live progress (loaded torrent), -1 when unloaded
	streaming     bool
}

// torrentFiles returns ih's file list: the loaded torrent's (with live
// progress), else the saved one, nil when neither is known.
func (m *manager) torrentFiles(ih string) (files []unkeptFile, pieceLen int64) {
	m.mu.RLock()
	e, loaded := m.engines[ih]
	m.mu.RUnlock()
	if loaded && e.hasInfo() {
		info := e.t.Info()
		for i, f := range e.t.Files() {
			_, begin, end := e.fileLayout(i)
			files = append(files, unkeptFile{path: filepath.ToSlash(f.Path()), display: f.DisplayPath(), length: f.Length(),
				begin: begin, end: end, done: f.BytesCompleted(), streaming: e.isReading(i)})
		}
		return files, info.PieceLength
	}
	l := m.savedFileList(ih)
	if l == nil {
		return nil, 0
	}
	for _, f := range l.files {
		files = append(files, unkeptFile{path: f.path, display: f.display, length: f.length, begin: f.begin, end: f.end, done: -1})
	}
	return files, l.pieceLen
}

// unkeptViews lists the unkept files in the folders of torrents that have a
// record or a marker.
func (m *manager) unkeptViews() []types.Download {
	if m.mem != nil || m.cfg.CacheRoot == "" {
		return nil
	}
	type folder struct {
		paths     map[string]bool // recorded files
		idxs      []int           // recorded files whose path isn't stored yet
		meta      *types.DownloadMeta
		hasRecord bool
	}
	m.dl.mu.Lock()
	folders := map[string]*folder{}
	for k, en := range m.dl.entries {
		f := folders[k.ih]
		if f == nil {
			f = &folder{paths: map[string]bool{}}
			folders[k.ih] = f
		}
		if en.kind() == types.DownloadLeftover {
			continue
		}
		f.hasRecord = true
		if en.Path != "" {
			f.paths[filepath.ToSlash(en.Path)] = true
		} else {
			f.idxs = append(f.idxs, k.idx)
		}
		if f.meta == nil && en.Meta != nil {
			// The show, not the episode: an unkept file is another file of it.
			f.meta = &types.DownloadMeta{MetaID: en.Meta.MetaID, Type: en.Meta.Type, Name: en.Meta.Name, Poster: en.Meta.Poster}
		}
	}
	m.dl.mu.Unlock()
	var out []types.Download
	for ih, f := range folders {
		files, pieceLen := m.torrentFiles(ih)
		indexOf := make(map[string]int, len(files))
		for i, tf := range files {
			indexOf[tf.path] = i
		}
		for _, idx := range f.idxs {
			if idx >= 0 && idx < len(files) {
				f.paths[files[idx].path] = true
			}
		}
		type candidate struct {
			rel, base string
			info      fs.FileInfo
			onDisk    int64
		}
		var found []candidate
		dir := filepath.Join(m.cfg.CacheRoot, ih)
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(dir, p)
			if err != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)
			base := strings.TrimSuffix(rel, ".part")
			if f.paths[base] {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			found = append(found, candidate{rel, base, info, allocatedSize(p, info)})
			return nil
		})
		big := 0
		for _, c := range found {
			if c.onDisk >= unkeptMinBytes {
				big++
			}
		}
		for _, c := range found {
			// Tiny files show only when they're all an unrecorded folder holds,
			// so it can still be deleted.
			if c.onDisk < unkeptMinBytes && (big > 0 || f.hasRecord) {
				continue
			}
			v := types.Download{
				InfoHash: ih, FileIdx: leftoverIdx, Kind: types.DownloadUnkept, Name: c.base,
				BytesOnDisk: c.onDisk, State: types.DownloadIdle, AddedAt: c.info.ModTime(), Path: c.rel,
			}
			if i, ok := indexOf[c.base]; ok {
				tf := files[i]
				v.FileIdx, v.Name, v.Size, v.Streaming = i, tf.display, tf.length, tf.streaming
				v.BytesCompleted = tf.done
				if tf.done < 0 {
					v.BytesCompleted = m.unkeptProgress(ih, c.rel, c.info, tf, pieceLen)
				}
			} else if files == nil {
				v.Info = m.metaFetch.info(ih)
			}
			if f.meta != nil {
				meta := *f.meta
				v.Meta = &meta
			}
			out = append(out, v)
		}
	}
	return out
}

// unkeptProgress is an unloaded file's bytes done. A file with its final name
// and full size is complete: anacrolix's part-file storage renames a file
// once all of it is verified and, on load, takes such a file as complete
// (storage/file-torrent.go, setCompletionFromPartFiles). Otherwise the pieces
// wholly inside it that the piece database has as complete count.
func (m *manager) unkeptProgress(ih, rel string, info fs.FileInfo, f unkeptFile, pieceLen int64) int64 {
	if !strings.HasSuffix(rel, ".part") && info.Size() == f.length {
		return f.length
	}
	if m.completion == nil || pieceLen <= 0 {
		return 0
	}
	key := progressKey{ih, rel}
	m.layoutMu.Lock()
	if c, ok := m.progress[key]; ok && c.mod.Equal(info.ModTime()) && c.size == info.Size() {
		m.layoutMu.Unlock()
		return c.done
	}
	m.layoutMu.Unlock()
	h := metainfo.NewHashFromHex(ih)
	var n int64
	for i := f.begin; i < f.end; i++ {
		if c, err := m.completion.Get(metainfo.PieceKey{InfoHash: h, Index: i}); err == nil && c.Ok && c.Complete {
			n++
		}
	}
	done := min(n*pieceLen, f.length)
	m.layoutMu.Lock()
	if m.progress == nil {
		m.progress = map[progressKey]progressEntry{}
	}
	m.progress[key] = progressEntry{mod: info.ModTime(), size: info.Size(), done: done}
	m.layoutMu.Unlock()
	return done
}

// RemoveUnkept deletes one unkept file from a torrent's folder. The torrent
// is unloaded for it (and reloaded for its records); a missing file's pieces
// read as incomplete to the storage, so they're fetched again if the file is
// ever wanted. A folder left with no data and no record goes whole, with its
// marker and saved file list.
func (m *manager) RemoveUnkept(infoHash, path string) error {
	ih := strings.ToLower(infoHash)
	rel := filepath.Clean(filepath.FromSlash(path))
	if m.cfg.CacheRoot == "" || filepath.IsAbs(rel) || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ErrDownloadPath
	}
	full := filepath.Join(m.cfg.CacheRoot, ih, rel)
	info, err := os.Lstat(full)
	if err != nil || !info.Mode().IsRegular() {
		return ErrDownloadPath
	}
	base := strings.TrimSuffix(filepath.ToSlash(rel), ".part")
	m.dl.mu.Lock()
	for k, en := range m.dl.entries {
		if k.ih == ih && en.kind() != types.DownloadLeftover && filepath.ToSlash(en.Path) == base {
			m.dl.mu.Unlock()
			return ErrDownloadPath
		}
	}
	m.dl.mu.Unlock()
	m.mu.Lock()
	e, loaded := m.engines[ih]
	if loaded {
		e.mu.Lock()
		reading := e.openReaders > 0
		e.mu.Unlock()
		if reading {
			m.mu.Unlock()
			return ErrDownloadBusy
		}
		m.unloadLocked(ih, e)
	}
	m.mu.Unlock()
	if err := os.Remove(full); err != nil {
		return err
	}
	logging.For("engine").Info("removed unkept file on request", "info_hash", ih, "path", filepath.ToSlash(rel))
	if m.dropEmptyFolder(ih) {
		return nil
	}
	if loaded && m.hasRecords(ih) {
		go m.tickDownloads() // reload the torrent for its records
	}
	return nil
}

// hasRecords reports whether ih has a kept or temporary record (a marker
// doesn't count).
func (m *manager) hasRecords(ih string) bool {
	m.dl.mu.Lock()
	defer m.dl.mu.Unlock()
	for k, en := range m.dl.entries {
		if k.ih == ih && en.kind() != types.DownloadLeftover {
			return true
		}
	}
	return false
}

// folderHasOtherData reports whether ih's folder holds data (a file of at
// least unkeptMinBytes: a release's .txt alone doesn't keep a folder) other
// than path (a record's path, with or without .part; "" matches none).
func (m *manager) folderHasOtherData(ih, path string) bool {
	if m.cfg.CacheRoot == "" {
		return false
	}
	dir := filepath.Join(m.cfg.CacheRoot, ih)
	path = filepath.ToSlash(path)
	found := false
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || found {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil || (path != "" && strings.TrimSuffix(filepath.ToSlash(rel), ".part") == path) {
			return nil
		}
		if info, err := d.Info(); err == nil && allocatedSize(p, info) >= unkeptMinBytes {
			found = true
		}
		return nil
	})
	return found
}

// markFolder records ih's folder with a leftover marker (its data is unkept
// files now), unless it has one.
func (m *manager) markFolder(ih string) {
	m.dl.mu.Lock()
	defer m.dl.mu.Unlock()
	key := downloadKey{ih, leftoverIdx}
	if _, ok := m.dl.entries[key]; ok {
		return
	}
	m.dl.entries[key] = &downloadEntry{InfoHash: ih, FileIdx: leftoverIdx, Kind: types.DownloadLeftover, AddedAt: time.Now()}
	m.dl.saveLocked()
}

// dropEmptyFolder deletes ih's folder, marker and saved file list when the
// folder holds no data (tiny files go with it) and ih has no record. Reports
// whether it did.
func (m *manager) dropEmptyFolder(ih string) bool {
	if m.hasRecords(ih) || m.folderHasOtherData(ih, "") {
		return false
	}
	m.dl.mu.Lock()
	delete(m.dl.entries, downloadKey{ih, leftoverIdx})
	m.dl.saveLocked()
	m.dl.mu.Unlock()
	m.waitPurge(ih)
	_ = os.RemoveAll(filepath.Join(m.cfg.CacheRoot, ih))
	m.forgetMetainfo(ih)
	logging.For("engine").Info("removed emptied torrent folder", "info_hash", ih)
	return true
}

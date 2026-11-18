// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent"

	"github.com/rivulet-kodi/rivulet-engine/internal/logging"
	"github.com/rivulet-kodi/rivulet-engine/internal/types"
)

// Downloads: files a client asked to have downloaded whole in the background
// (download ahead: the next episode fetched while the current one plays, so it
// later plays from disk without buffering). A download is a per-file intent
// that outlives the file's readers: closing the last stream, or playing
// another file of the torrent, no longer drops it to priority None, and the
// janitor's idle and LRU passes (which delete a torrent's data) leave torrents
// with downloads alone. The registry is persisted, so incomplete downloads
// resume after a restart; a torrent whose downloads are all complete is
// unloaded without deleting its data, and piece completion (persisted per
// torrent directory) makes it play from disk when it's opened again.

// downloadsFile is the registry's file name under the app path.
const downloadsFile = "downloads.json"

// resumeReadyTimeout bounds the wait for a re-added torrent's metadata.
const resumeReadyTimeout = 10 * time.Minute

type downloadKey struct {
	ih  string
	idx int
}

// downloadEntry is one registry record, persisted as JSON.
type downloadEntry struct {
	InfoHash string    `json:"infoHash"`
	FileIdx  int       `json:"fileIdx"`
	Trackers []string  `json:"trackers,omitempty"`
	Paused   bool      `json:"paused,omitempty"`
	Watched  bool      `json:"watched,omitempty"`
	Complete bool      `json:"complete,omitempty"`
	AddedAt  time.Time `json:"addedAt"`
	// Name and Size are filled once the torrent's metadata is known.
	Name string `json:"name,omitempty"`
	Size int64  `json:"size,omitempty"`

	// Last progress sample, for the reported rate (not persisted).
	sampleAt    time.Time
	sampleBytes int64
	rate        float64
}

type downloads struct {
	mu      sync.Mutex
	path    string // "" keeps the registry in memory only
	entries map[downloadKey]*downloadEntry
}

func newDownloads(appPath string) *downloads {
	d := &downloads{entries: map[downloadKey]*downloadEntry{}}
	if appPath == "" {
		return d
	}
	d.path = filepath.Join(appPath, downloadsFile)
	raw, err := os.ReadFile(d.path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			logging.For("engine").Warn("reading downloads", "path", d.path, "err", err)
		}
		return d
	}
	var list []*downloadEntry
	if err := json.Unmarshal(raw, &list); err != nil {
		logging.For("engine").Warn("parsing downloads", "path", d.path, "err", err)
		return d
	}
	for _, en := range list {
		if !isHexInfoHash(en.InfoHash) || en.FileIdx < 0 {
			continue
		}
		en.InfoHash = strings.ToLower(en.InfoHash)
		d.entries[downloadKey{en.InfoHash, en.FileIdx}] = en
	}
	return d
}

func isHexInfoHash(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}

// saveLocked writes the registry atomically (temp file + rename). d.mu held.
func (d *downloads) saveLocked() {
	if d.path == "" {
		return
	}
	list := make([]*downloadEntry, 0, len(d.entries))
	for _, en := range d.entries {
		list = append(list, en)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].AddedAt.Before(list[j].AddedAt) })
	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return
	}
	tmp := d.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err == nil {
		err = os.Rename(tmp, d.path)
		if err != nil {
			logging.For("engine").Warn("saving downloads", "path", d.path, "err", err)
		}
	}
}

// hasTorrent reports whether any download belongs to infoHash.
func (d *downloads) hasTorrent(ih string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for k := range d.entries {
		if k.ih == ih {
			return true
		}
	}
	return false
}

// background reports whether file idx of infoHash is an active (unpaused) download.
func (d *downloads) background(ih string, idx int) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	en, ok := d.entries[downloadKey{ih, idx}]
	return ok && !en.Paused
}

// Download errors, aliased from types.
var (
	ErrDownloadsNeedDisk = types.ErrDownloadsNeedDisk
	ErrNoDownload        = types.ErrNoDownload
)

// AddDownload registers file idx of infoHash for a whole-file background
// download, adding the torrent if needed. Adding an existing download unpauses
// it.
func (m *manager) AddDownload(infoHash string, idx int, opts types.AddOptions) (types.Download, error) {
	if m.mem != nil {
		return types.Download{}, ErrDownloadsNeedDisk
	}
	if idx < 0 {
		return types.Download{}, ErrNoDownload
	}
	ih := strings.ToLower(infoHash)
	if _, err := m.EnsureEngine(ih, opts); err != nil {
		return types.Download{}, err
	}
	trackers := append(append([]string{}, opts.Trackers...), trackersFromSources(opts.Sources)...)
	m.dl.mu.Lock()
	key := downloadKey{ih, idx}
	en, ok := m.dl.entries[key]
	if !ok {
		en = &downloadEntry{InfoHash: ih, FileIdx: idx, AddedAt: time.Now()}
		m.dl.entries[key] = en
	}
	en.Paused = false
	if len(trackers) > 0 {
		en.Trackers = mergeStrings(en.Trackers, trackers)
	}
	m.dl.saveLocked()
	m.dl.mu.Unlock()
	m.startDownload(ih, idx)
	return m.downloadView(key), nil
}

// trackersFromSources keeps the tracker URLs of peerSearch sources ("tracker:<url>").
func trackersFromSources(sources []string) []string {
	var out []string
	for _, s := range sources {
		if u, ok := strings.CutPrefix(s, "tracker:"); ok && u != "" {
			out = append(out, u)
		}
	}
	return out
}

func mergeStrings(a, b []string) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, s := range append(append([]string{}, a...), b...) {
		if _, dup := seen[s]; !dup {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}

// startDownload marks the file for background download once the torrent's
// metadata is known (the wait runs in the background).
func (m *manager) startDownload(ih string, idx int) {
	m.mu.RLock()
	e, ok := m.engines[ih]
	m.mu.RUnlock()
	if !ok {
		return
	}
	e.mu.Lock()
	already := e.isBackgroundLocked(idx)
	e.mu.Unlock()
	if already {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), resumeReadyTimeout)
		defer cancel()
		select {
		case <-e.t.GotInfo():
		case <-ctx.Done():
			return
		case <-m.done:
			return
		}
		if m.dl.background(ih, idx) {
			e.setBackground(idx, true)
		}
	}()
}

// setBackground marks (or unmarks) a file as a background download: the whole
// file at Normal priority, which a playing file's readahead window still
// outranks. Unmarking leaves a file that is being played alone.
func (e *engine) setBackground(idx int, on bool) {
	files := e.t.Files()
	if idx < 0 || idx >= len(files) {
		return
	}
	e.mu.Lock()
	if e.background == nil {
		e.background = map[int]struct{}{}
	}
	if on {
		e.background[idx] = struct{}{}
	} else {
		delete(e.background, idx)
	}
	_, sel := e.selected[idx]
	busy := e.reading[idx] > 0 || sel
	e.mu.Unlock()
	switch {
	case on:
		files[idx].Download()
	case !busy:
		files[idx].SetPriority(torrent.PiecePriorityNone)
	}
}

// isBackgroundLocked reports whether file idx is a background download. e.mu held.
func (e *engine) isBackgroundLocked(idx int) bool {
	_, ok := e.background[idx]
	return ok
}

// Downloads lists every download with its progress.
func (m *manager) Downloads() []types.Download {
	m.dl.mu.Lock()
	keys := make([]downloadKey, 0, len(m.dl.entries))
	for k := range m.dl.entries {
		keys = append(keys, k)
	}
	m.dl.mu.Unlock()
	out := make([]types.Download, 0, len(keys))
	for _, k := range keys {
		out = append(out, m.downloadView(k))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AddedAt.Before(out[j].AddedAt) })
	return out
}

// downloadView refreshes an entry from its loaded torrent (if any) and
// returns its public form.
func (m *manager) downloadView(k downloadKey) types.Download {
	m.mu.RLock()
	e, loaded := m.engines[k.ih]
	m.mu.RUnlock()
	var done, size int64
	var name string
	known := false
	if loaded && e.hasInfo() {
		if files := e.t.Files(); k.idx < len(files) {
			f := files[k.idx]
			done, size, name, known = f.BytesCompleted(), f.Length(), f.DisplayPath(), true
		}
	}
	m.dl.mu.Lock()
	defer m.dl.mu.Unlock()
	en, ok := m.dl.entries[k]
	if !ok {
		return types.Download{InfoHash: k.ih, FileIdx: k.idx}
	}
	now := time.Now()
	if known {
		if en.Name != name || en.Size != size {
			en.Name, en.Size = name, size
			m.dl.saveLocked()
		}
		if !en.sampleAt.IsZero() && now.Sub(en.sampleAt) >= time.Second {
			en.rate = float64(done-en.sampleBytes) / now.Sub(en.sampleAt).Seconds()
			if en.rate < 0 {
				en.rate = 0
			}
		}
		if en.sampleAt.IsZero() || now.Sub(en.sampleAt) >= time.Second {
			en.sampleAt, en.sampleBytes = now, done
		}
		if !en.Complete && size > 0 && done >= size {
			en.Complete = true
			m.dl.saveLocked()
		}
	}
	if en.Complete {
		done = en.Size
	}
	state := types.DownloadQueued
	switch {
	case en.Complete:
		state = types.DownloadComplete
	case en.Paused:
		state = types.DownloadPaused
	case loaded && known:
		state = types.DownloadDownloading
	}
	rate := en.rate
	if state != types.DownloadDownloading {
		rate = 0
	}
	return types.Download{
		InfoHash: en.InfoHash, FileIdx: en.FileIdx, Name: en.Name, Size: en.Size,
		BytesCompleted: done, Rate: rate, State: state, Watched: en.Watched, AddedAt: en.AddedAt,
	}
}

// UpdateDownload pauses/resumes a download or marks it watched.
func (m *manager) UpdateDownload(infoHash string, idx int, patch types.DownloadPatch) (types.Download, error) {
	k := downloadKey{strings.ToLower(infoHash), idx}
	m.dl.mu.Lock()
	en, ok := m.dl.entries[k]
	if !ok {
		m.dl.mu.Unlock()
		return types.Download{}, ErrNoDownload
	}
	if patch.Watched != nil {
		en.Watched = *patch.Watched
	}
	resume := false
	pause := false
	if patch.Paused != nil && *patch.Paused != en.Paused {
		en.Paused = *patch.Paused
		pause, resume = en.Paused, !en.Paused
	}
	m.dl.saveLocked()
	trackers := append([]string{}, en.Trackers...)
	m.dl.mu.Unlock()
	switch {
	case pause:
		m.mu.RLock()
		e, loaded := m.engines[k.ih]
		m.mu.RUnlock()
		if loaded {
			e.setBackground(k.idx, false)
		}
	case resume:
		if _, err := m.EnsureEngine(k.ih, types.AddOptions{Trackers: trackers}); err != nil {
			return types.Download{}, err
		}
		m.startDownload(k.ih, k.idx)
	}
	return m.downloadView(k), nil
}

// RemoveDownload forgets a download. The torrent's data is deleted when no
// other download of it remains and nothing is reading it; otherwise the file
// just stops being a download (deleting one file inside a torrent is a later
// step's budget work).
func (m *manager) RemoveDownload(infoHash string, idx int) error {
	k := downloadKey{strings.ToLower(infoHash), idx}
	m.dl.mu.Lock()
	if _, ok := m.dl.entries[k]; !ok {
		m.dl.mu.Unlock()
		return ErrNoDownload
	}
	delete(m.dl.entries, k)
	m.dl.saveLocked()
	m.dl.mu.Unlock()
	others := m.dl.hasTorrent(k.ih)
	m.mu.Lock()
	e, loaded := m.engines[k.ih]
	if !loaded {
		m.mu.Unlock()
		if !others {
			m.waitPurge(k.ih)
			_ = os.RemoveAll(filepath.Join(m.cfg.CacheRoot, k.ih))
		}
		return nil
	}
	e.mu.Lock()
	idle := e.openReaders == 0
	e.mu.Unlock()
	if !others && idle {
		finish := m.beginPurge(k.ih, e)
		m.mu.Unlock()
		finish()
		return nil
	}
	m.mu.Unlock()
	e.setBackground(k.idx, false)
	return nil
}

// waitPurge blocks while infoHash's cache directory is being deleted.
func (m *manager) waitPurge(ih string) {
	m.mu.RLock()
	ch, ok := m.purging[ih]
	m.mu.RUnlock()
	if ok {
		<-ch
	}
}

// tickDownloads re-adds the torrents of incomplete, unpaused downloads
// (after a restart), and unloads torrents whose downloads are all complete
// and that nothing reads. Runs at start and on each janitor tick.
func (m *manager) tickDownloads() {
	if m.mem != nil {
		return
	}
	m.dl.mu.Lock()
	type torrentState struct {
		trackers   []string
		incomplete []int
	}
	byTorrent := map[string]*torrentState{}
	for k, en := range m.dl.entries {
		st := byTorrent[k.ih]
		if st == nil {
			st = &torrentState{}
			byTorrent[k.ih] = st
		}
		st.trackers = mergeStrings(st.trackers, en.Trackers)
		if !en.Complete && !en.Paused {
			st.incomplete = append(st.incomplete, k.idx)
		}
	}
	m.dl.mu.Unlock()
	for ih, st := range byTorrent {
		m.mu.RLock()
		e, loaded := m.engines[ih]
		m.mu.RUnlock()
		if len(st.incomplete) > 0 {
			if !loaded {
				if _, err := m.EnsureEngine(ih, types.AddOptions{Trackers: st.trackers}); err != nil {
					logging.For("engine").Warn("resuming download", "info_hash", ih, "err", err)
					continue
				}
				logging.For("engine").Info("resumed download", "info_hash", ih, "files", len(st.incomplete))
			}
			for _, idx := range st.incomplete {
				m.startDownload(ih, idx)
			}
			// Refresh completion so finished files are noticed.
			for _, idx := range st.incomplete {
				m.downloadView(downloadKey{ih, idx})
			}
			continue
		}
		if loaded {
			m.unloadIfDone(ih, e)
		}
	}
}

// unloadIfDone drops a torrent whose downloads are all complete and that has
// no readers, keeping its data on disk (unlike beginPurge).
func (m *manager) unloadIfDone(ih string, e *engine) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.engines[ih] != e {
		return
	}
	e.mu.Lock()
	busy := e.openReaders > 0 || time.Since(e.lastAccess) < time.Minute
	e.mu.Unlock()
	if busy {
		return
	}
	e.t.Drop()
	delete(m.engines, ih)
	m.dials.forget(ih)
	m.trace.forget(ih)
	logging.For("engine").Info("unloaded completed downloads", "info_hash", ih)
}

// stopNonBackground drops every file that is not a background download to
// priority None, unless it is being read: what removing the torrent would
// have stopped, short of deleting its data.
func (e *engine) stopNonBackground() {
	if !e.hasInfo() {
		return
	}
	files := e.t.Files()
	e.mu.Lock()
	var stop []int
	for i := range files {
		if e.reading[i] == 0 && !e.isBackgroundLocked(i) {
			stop = append(stop, i)
			delete(e.selected, i)
			delete(e.primed, i)
			delete(e.prefetched, i)
		}
	}
	e.mu.Unlock()
	for _, i := range stop {
		files[i].SetPriority(torrent.PiecePriorityNone)
	}
	e.releaseBoundary(stop)
}

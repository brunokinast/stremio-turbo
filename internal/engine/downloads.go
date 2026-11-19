// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	g "github.com/anacrolix/generics"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"

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

// addMetadataTimeout bounds AddDownload's wait for a new download's metadata
// (its size is needed for the budget).
const addMetadataTimeout = 30 * time.Second

// defaultDownloadBudget is the disk space downloads may use when no budget
// function is set (the downloadBudget setting's default).
const defaultDownloadBudget = int64(60e9)

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
	// Filled once the torrent's metadata is known: the display name, size,
	// path on disk (relative to the torrent's cache dir) and the pieces that
	// lie wholly inside the file, so one file of a season pack can be deleted
	// while the torrent is unloaded.
	Name       string `json:"name,omitempty"`
	Size       int64  `json:"size,omitempty"`
	Path       string `json:"path,omitempty"`
	PieceBegin int    `json:"pieceBegin,omitempty"`
	PieceEnd   int    `json:"pieceEnd,omitempty"`

	// Last progress sample, for the reported rate (not persisted).
	sampleAt    time.Time
	sampleBytes int64
	rate        float64
}

type downloads struct {
	mu      sync.Mutex
	path    string // "" keeps the registry in memory only
	entries map[downloadKey]*downloadEntry

	// Exit when done (SetExitWhenDone): drained is closed once no download
	// is left to fetch and nothing reads a torrent, and the process exits.
	exitWhenDone bool
	drained      chan struct{}
	drainOnce    sync.Once

	// awake keeps the system from sleeping while downloads are being
	// fetched (systemKeepAwake; replaced in tests). awakeOn is its state.
	awake   func(bool)
	awakeOn bool
}

func newDownloads(appPath string) *downloads {
	d := &downloads{entries: map[downloadKey]*downloadEntry{}, drained: make(chan struct{}), awake: systemKeepAwake}
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
	eng, err := m.EnsureEngine(ih, opts)
	if err != nil {
		return types.Download{}, err
	}
	key := downloadKey{ih, idx}
	m.dl.mu.Lock()
	_, exists := m.dl.entries[key]
	m.dl.mu.Unlock()
	if !exists {
		size, err := m.waitFileSize(eng.(*engine), idx)
		if err != nil {
			return types.Download{}, err
		}
		if err := m.makeRoom(size, key); err != nil {
			return types.Download{}, err
		}
	}
	trackers := append(append([]string{}, opts.Trackers...), trackersFromSources(opts.Sources)...)
	m.dl.mu.Lock()
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
		if en.Name != name || en.Size != size || en.Path == "" {
			en.Name, en.Size = name, size
			en.Path, en.PieceBegin, en.PieceEnd = e.fileLayout(k.idx)
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

// RemoveDownload forgets a download and deletes its data, unless the torrent
// is being read (then the file just stops being a download, and the janitor
// reclaims it later as usual).
func (m *manager) RemoveDownload(infoHash string, idx int) error {
	k := downloadKey{strings.ToLower(infoHash), idx}
	if !m.deleteDownload(k) {
		return ErrNoDownload
	}
	return nil
}

// deleteDownload forgets a download and deletes its data: the whole torrent
// directory when no other download of it remains, otherwise just the file
// (the torrent is unloaded for that, and the file's pieces are marked
// incomplete; boundary pieces shared with a neighbouring file stay). A torrent
// that is being read keeps its data. Reports false for an unknown download.
func (m *manager) deleteDownload(k downloadKey) bool {
	m.dl.mu.Lock()
	en, ok := m.dl.entries[k]
	if !ok {
		m.dl.mu.Unlock()
		return false
	}
	gone := *en
	delete(m.dl.entries, k)
	m.dl.saveLocked()
	m.dl.mu.Unlock()
	others := m.dl.hasTorrent(k.ih)
	m.mu.Lock()
	e, loaded := m.engines[k.ih]
	if loaded {
		e.mu.Lock()
		reading := e.openReaders > 0
		e.mu.Unlock()
		if reading {
			m.mu.Unlock()
			e.setBackground(k.idx, false)
			return true
		}
		if !others {
			finish := m.beginPurge(k.ih, e)
			m.mu.Unlock()
			finish()
			return true
		}
		m.unloadLocked(k.ih, e)
	}
	m.mu.Unlock()
	if !others {
		m.waitPurge(k.ih)
		_ = os.RemoveAll(filepath.Join(m.cfg.CacheRoot, k.ih))
		return true
	}
	m.deleteFileData(gone)
	if loaded {
		go m.tickDownloads() // reload the torrent for its other downloads
	}
	return true
}

// deleteFileData deletes one file's data from an unloaded torrent's cache dir
// and marks the pieces wholly inside it incomplete.
func (m *manager) deleteFileData(en downloadEntry) {
	if en.Path == "" {
		return
	}
	p := filepath.Join(m.cfg.CacheRoot, en.InfoHash, filepath.FromSlash(en.Path))
	for _, f := range []string{p, p + ".part"} {
		if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			logging.For("engine").Warn("deleting downloaded file", "path", f, "err", err)
		}
	}
	if m.completion == nil {
		return
	}
	ih := metainfo.NewHashFromHex(en.InfoHash)
	for i := en.PieceBegin; i < en.PieceEnd; i++ {
		if err := m.completion.Set(metainfo.PieceKey{InfoHash: ih, Index: i}, g.Some(false)); err != nil {
			logging.For("engine").Warn("marking piece incomplete", "info_hash", en.InfoHash, "piece", i, "err", err)
			return
		}
	}
}

// fileLayout returns file idx's path relative to the torrent's cache dir and
// the range of pieces lying wholly inside it ([begin, end)).
func (e *engine) fileLayout(idx int) (path string, begin, end int) {
	info := e.t.Info()
	files := e.t.Files()
	if info == nil || idx < 0 || idx >= len(files) || info.PieceLength <= 0 {
		return "", 0, 0
	}
	f := files[idx]
	pl := info.PieceLength
	off, stop := f.Offset(), f.Offset()+f.Length()
	begin = int((off + pl - 1) / pl)
	end = int(stop / pl)
	if stop == info.TotalLength() {
		end = info.NumPieces()
	}
	if end < begin {
		end = begin
	}
	return f.Path(), begin, end
}

// waitFileSize waits (bounded) for the torrent's metadata and returns file
// idx's size.
func (m *manager) waitFileSize(e *engine, idx int) (int64, error) {
	select {
	case <-e.t.GotInfo():
	case <-time.After(addMetadataTimeout):
		return 0, types.ErrDownloadNoMetadata
	case <-m.done:
		return 0, types.ErrDownloadNoMetadata
	}
	files := e.t.Files()
	if idx >= len(files) {
		return 0, ErrNoDownload
	}
	return files[idx].Length(), nil
}

// SetDownloadBudgetFn sets the function returning the download budget in
// bytes (<0 unlimited), read on every add.
func (m *manager) SetDownloadBudgetFn(fn func() int64) {
	m.dl.mu.Lock()
	m.dlBudget = fn
	m.dl.mu.Unlock()
}

// makeRoom deletes completed downloads until need more bytes fit the budget:
// watched ones first, then unwatched ones, oldest first within each. Neither
// incomplete downloads nor torrents being read are deleted. keep is the
// download being added.
func (m *manager) makeRoom(need int64, keep downloadKey) error {
	m.dl.mu.Lock()
	budget := defaultDownloadBudget
	if m.dlBudget != nil {
		budget = m.dlBudget()
	}
	var used int64
	var cands []*downloadEntry
	for k, en := range m.dl.entries {
		if k == keep {
			continue
		}
		used += en.Size
		if en.Complete {
			c := *en
			cands = append(cands, &c)
		}
	}
	m.dl.mu.Unlock()
	if budget < 0 || used+need <= budget {
		return nil
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].Watched != cands[j].Watched {
			return cands[i].Watched
		}
		return cands[i].AddedAt.Before(cands[j].AddedAt)
	})
	for _, c := range cands {
		if used+need <= budget {
			break
		}
		if m.torrentReading(c.InfoHash) {
			continue
		}
		if m.deleteDownload(downloadKey{c.InfoHash, c.FileIdx}) {
			used -= c.Size
			logging.For("engine").Info("deleted download for budget", "info_hash", c.InfoHash, "file", c.Name, "watched", c.Watched, "freed_bytes", c.Size)
		}
	}
	if used+need > budget {
		return fmt.Errorf("%w: needs %.1f GB, downloads that can't be deleted use %.1f of %.1f GB",
			types.ErrDownloadBudget, float64(need)/1e9, float64(used)/1e9, float64(budget)/1e9)
	}
	return nil
}

// torrentReading reports whether infoHash is loaded with open readers.
func (m *manager) torrentReading(ih string) bool {
	m.mu.RLock()
	e, ok := m.engines[ih]
	m.mu.RUnlock()
	if !ok {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.openReaders > 0
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
	active := false
	for _, st := range byTorrent {
		active = active || len(st.incomplete) > 0
	}
	m.setAwake(active)
	defer m.checkDrained(active)
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

// setAwake keeps the system awake while downloads are being fetched.
func (m *manager) setAwake(active bool) {
	m.dl.mu.Lock()
	changed := m.dl.awakeOn != active
	m.dl.awakeOn = active
	awake := m.dl.awake
	m.dl.mu.Unlock()
	if !changed {
		return
	}
	awake(active)
	if active {
		logging.For("engine").Info("keeping the system awake while downloads run")
	} else {
		logging.For("engine").Info("downloads idle; the system may sleep")
	}
}

// SetExitWhenDone turns exit-when-done on or off: when on, the process exits
// (Drained closes) once no download is left to fetch and no torrent is being
// read. Turning it on checks at once.
func (m *manager) SetExitWhenDone(on bool) {
	m.dl.mu.Lock()
	m.dl.exitWhenDone = on
	m.dl.mu.Unlock()
	if on {
		go m.tickDownloads()
	}
}

// Drained is closed when exit-when-done is on and the work is done.
func (m *manager) Drained() <-chan struct{} { return m.dl.drained }

// checkDrained closes drained when exit-when-done is on, no download is
// active and no torrent has an open reader.
func (m *manager) checkDrained(active bool) {
	m.dl.mu.Lock()
	on := m.dl.exitWhenDone
	m.dl.mu.Unlock()
	if !on || active {
		return
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, e := range m.engines {
		e.mu.Lock()
		reading := e.openReaders > 0
		e.mu.Unlock()
		if reading {
			return
		}
	}
	m.dl.drainOnce.Do(func() {
		logging.For("engine").Info("downloads done; exiting (exit when done)")
		close(m.dl.drained)
	})
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
	m.unloadLocked(ih, e)
	logging.For("engine").Info("unloaded completed downloads", "info_hash", ih)
}

// unloadLocked drops a torrent without deleting its data. m.mu held.
func (m *manager) unloadLocked(ih string, e *engine) {
	e.t.Drop()
	delete(m.engines, ih)
	m.dials.forget(ih)
	m.trace.forget(ih)
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

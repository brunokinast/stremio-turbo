// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"encoding/json"
	"errors"
	"math"
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

// Downloads: the record of every file on disk. Three kinds:
//
//   - kept: downloaded whole in the background and deleted only on request (a
//     client's DELETE). A file the player streams becomes kept (origin
//     "play"), so the episode being watched finishes downloading even after
//     it stops; a client adds the next episode as kept too. A kept download
//     outlives its readers: closing the last stream, or playing another file
//     of the torrent, doesn't drop it to priority None.
//   - temporary: a client measuring candidates. Its start, end and the window
//     at the resume point are primed; it holds a lease the client renews, and
//     is deleted once the lease lapses (also at startup), so a closed or
//     crashed client leaves nothing behind. Playing it makes it kept.
//   - leftover: a cache folder found at startup with no record (data from
//     before the record existed, or of a server that was stopped). Listed so
//     the user can delete it; never fetched.
//
// The janitor's idle and LRU passes (which delete a torrent's data) leave
// torrents with records alone; they only reclaim torrents nobody recorded. The
// registry is persisted, so incomplete kept files resume after a restart; a
// torrent whose kept files are all complete is unloaded without deleting its
// data, and piece completion makes it play from disk when it's opened again.

// downloadsFile is the registry's file name under the app path.
const downloadsFile = "downloads.json"

// resumeReadyTimeout bounds the wait for a re-added torrent's metadata.
const resumeReadyTimeout = 10 * time.Minute

// addMetadataTimeout bounds AddDownload's wait for a new kept file's
// metadata (to check the file index).
const addMetadataTimeout = 30 * time.Second

// temporaryLease is how long a temporary file lives without its client
// renewing it (re-adding it).
const temporaryLease = 2 * time.Minute

// leftoverIdx is a leftover's file index: the record covers the whole folder.
const leftoverIdx = -1

type downloadKey struct {
	ih  string
	idx int
}

// downloadEntry is one registry record, persisted as JSON.
type downloadEntry struct {
	InfoHash string              `json:"infoHash"`
	FileIdx  int                 `json:"fileIdx"`
	Kind     types.DownloadKind  `json:"kind,omitempty"` // "" (older registries) is kept
	Origins  []string            `json:"origins,omitempty"`
	Meta     *types.DownloadMeta `json:"meta,omitempty"`
	// LastPlayed is when a player last opened the file.
	LastPlayed *time.Time `json:"lastPlayed,omitempty"`
	// Files lists a leftover folder's files.
	Files    []string  `json:"files,omitempty"`
	Trackers []string  `json:"trackers,omitempty"`
	Paused   bool      `json:"paused,omitempty"`
	Watched  bool      `json:"watched,omitempty"`
	Complete bool      `json:"complete,omitempty"`
	AddedAt  time.Time `json:"addedAt"`
	// Order ranks the file among its torrent's kept files (DownloadRequest.Order).
	Order int `json:"order,omitempty"`
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

	// A temporary file's lease (not persisted: a temporary file found at
	// startup has lapsed).
	leaseUntil time.Time
}

func (en *downloadEntry) kind() types.DownloadKind {
	if en.Kind == "" {
		return types.DownloadKept
	}
	return en.Kind
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
		if !isHexInfoHash(en.InfoHash) || (en.FileIdx < 0 && (en.FileIdx != leftoverIdx || en.Kind != types.DownloadLeftover)) {
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

// background reports whether file idx of infoHash is an active (unpaused) kept download.
func (d *downloads) background(ih string, idx int) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	en, ok := d.entries[downloadKey{ih, idx}]
	return ok && en.kind() == types.DownloadKept && !en.Paused
}

// Download errors, aliased from types.
var (
	ErrDownloadsNeedDisk = types.ErrDownloadsNeedDisk
	ErrNoDownload        = types.ErrNoDownload
	ErrDownloadPath      = types.ErrDownloadPath
	ErrDownloadBusy      = types.ErrDownloadBusy
)

// AddDownload records file idx of infoHash, adding the torrent if needed.
// A kept file is downloaded whole in the background; re-adding one unpauses
// it. A temporary file has its start, end and the window at
// req.StartFraction primed, and its lease renewed; adding a temporary file
// that is already kept changes nothing but its origins, and adding a kept one
// that is temporary promotes it. A leftover record of the torrent is dropped:
// its folder is the new record's data.
func (m *manager) AddDownload(infoHash string, idx int, opts types.AddOptions, req types.DownloadRequest) (types.Download, error) {
	if m.mem != nil {
		return types.Download{}, ErrDownloadsNeedDisk
	}
	if idx < 0 {
		return types.Download{}, ErrNoDownload
	}
	kind := req.Kind
	switch kind {
	case "":
		kind = types.DownloadKept
	case types.DownloadKept, types.DownloadTemporary:
	default:
		return types.Download{}, types.ErrDownloadKind
	}
	ih := strings.ToLower(infoHash)
	eng, err := m.EnsureEngine(ih, opts)
	if err != nil {
		return types.Download{}, err
	}
	e := eng.(*engine)
	key := downloadKey{ih, idx}
	m.dl.mu.Lock()
	_, exists := m.dl.entries[key]
	m.dl.mu.Unlock()
	if !exists && kind == types.DownloadKept {
		if _, err := m.waitFileSize(e, idx); err != nil {
			return types.Download{}, err
		}
	}
	trackers := append(append([]string{}, opts.Trackers...), trackersFromSources(opts.Sources)...)
	m.dl.mu.Lock()
	delete(m.dl.entries, downloadKey{ih, leftoverIdx})
	en, ok := m.dl.entries[key]
	if !ok {
		en = &downloadEntry{InfoHash: ih, FileIdx: idx, Kind: kind, AddedAt: time.Now()}
		m.dl.entries[key] = en
	}
	if kind == types.DownloadKept {
		en.Kind = types.DownloadKept
		en.Paused = false
		en.leaseUntil = time.Time{}
	}
	keep := en.kind() == types.DownloadKept
	if !keep {
		en.leaseUntil = time.Now().Add(temporaryLease)
	}
	if req.Origin != "" {
		en.Origins = mergeStrings(en.Origins, []string{req.Origin})
	}
	if req.Order != 0 {
		en.Order = req.Order
	}
	if req.Meta != nil && (req.Meta.MetaID != "" || req.Meta.VideoID != "") {
		meta := *req.Meta
		en.Meta = &meta
	}
	if len(trackers) > 0 {
		en.Trackers = mergeStrings(en.Trackers, trackers)
	}
	m.dl.saveLocked()
	m.dl.mu.Unlock()
	e.setHeld(idx, !keep)
	if keep {
		m.startDownload(ih, idx)
	} else {
		e.PrimeFile(idx, req.StartFraction)
	}
	return m.downloadView(key), nil
}

// notePlayed records that a player opened file idx of infoHash: the file
// becomes kept (origin "play") and downloads whole, so an episode that is
// stopped still finishes. Called for every player reader (not probes).
func (m *manager) notePlayed(ih string, idx int) {
	if m.mem != nil || idx < 0 {
		return
	}
	now := time.Now()
	key := downloadKey{ih, idx}
	m.dl.mu.Lock()
	delete(m.dl.entries, downloadKey{ih, leftoverIdx})
	en, ok := m.dl.entries[key]
	if !ok {
		en = &downloadEntry{InfoHash: ih, FileIdx: idx, Kind: types.DownloadKept, AddedAt: now}
		m.dl.entries[key] = en
	}
	promoted := ok && en.kind() != types.DownloadKept
	en.Kind = types.DownloadKept
	en.leaseUntil = time.Time{}
	en.Origins = mergeStrings(en.Origins, []string{"play"})
	en.LastPlayed = &now
	m.dl.saveLocked()
	m.dl.mu.Unlock()
	if !ok || promoted {
		logging.For("engine").Info("keeping played file", "info_hash", ih, "file_idx", idx, "promoted", promoted)
	}
	m.mu.RLock()
	e, loaded := m.engines[ih]
	m.mu.RUnlock()
	if loaded {
		e.setHeld(idx, false)
	}
	m.startDownload(ih, idx)
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
	if e.hasInfo() {
		// Mark it now: a player's reader closing right after (notePlayed)
		// would otherwise demote the file before the goroutine ran.
		if m.dl.background(ih, idx) {
			e.setBackground(idx, true)
		}
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

// setHeld marks (or unmarks) file idx as a temporary record's file: its primed
// pieces are kept when another file of the torrent is played (a measurement of
// the next episode of the playing pack would otherwise read a demoted file).
func (e *engine) setHeld(idx int, on bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.held == nil {
		e.held = map[int]struct{}{}
	}
	if on {
		e.held[idx] = struct{}{}
	} else {
		delete(e.held, idx)
	}
}

// isWantedLocked reports whether file idx has a record that keeps it from
// being demoted: a background (kept) download or a temporary one. e.mu held.
func (e *engine) isWantedLocked(idx int) bool {
	_, held := e.held[idx]
	return held || e.isBackgroundLocked(idx)
}

// Downloads lists every download with its progress.
func (m *manager) Downloads() []types.Download {
	m.dl.mu.Lock()
	keys := make([]downloadKey, 0, len(m.dl.entries))
	for k, en := range m.dl.entries {
		if en.kind() != types.DownloadLeftover { // a folder marker: its files are listed as unkept
			keys = append(keys, k)
		}
	}
	m.dl.mu.Unlock()
	out := make([]types.Download, 0, len(keys))
	for _, k := range keys {
		out = append(out, m.downloadView(k))
	}
	out = append(out, m.unkeptViews()...)
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
	known, streaming, peers, first := false, false, 0, false
	if loaded && e.hasInfo() {
		if files := e.t.Files(); k.idx >= 0 && k.idx < len(files) {
			f := files[k.idx]
			done, size, name, known = f.BytesCompleted(), f.Length(), f.DisplayPath(), true
		}
		streaming, peers = e.isReading(k.idx), e.t.Stats().ActivePeers
		first = e.isFirst(k.idx)
	}
	m.dl.mu.Lock()
	defer m.dl.mu.Unlock()
	en, ok := m.dl.entries[k]
	if !ok {
		return types.Download{InfoHash: k.ih, FileIdx: k.idx}
	}
	if en.kind() == types.DownloadLeftover {
		return types.Download{InfoHash: k.ih, FileIdx: k.idx, Kind: types.DownloadLeftover}
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
	case en.kind() == types.DownloadTemporary:
		state = types.DownloadIdle
		if loaded && en.rate > 0 {
			state = types.DownloadDownloading
		}
	case loaded && known:
		state = types.DownloadDownloading
	}
	rate := en.rate
	if state != types.DownloadDownloading {
		rate = 0
	}
	d := types.Download{
		InfoHash: en.InfoHash, FileIdx: en.FileIdx, Kind: en.kind(), Origins: append([]string(nil), en.Origins...),
		Name: en.Name, Size: en.Size, BytesCompleted: done, BytesOnDisk: m.fileOnDisk(en),
		Rate: rate, State: state, Watched: en.Watched, AddedAt: en.AddedAt, LastPlayed: en.LastPlayed,
		Streaming: streaming, Order: en.Order, First: first && !en.Complete && !en.Paused,
	}
	if loaded {
		d.Peers = peers
	}
	if en.Meta != nil {
		meta := *en.Meta
		d.Meta = &meta
	}
	return d
}

// isReading reports whether a reader has file idx open.
func (e *engine) isReading(idx int) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.reading[idx] > 0
}

// fileOnDisk returns the allocated bytes of a recorded file (its final name
// or its .part), 0 while its path is unknown.
func (m *manager) fileOnDisk(en *downloadEntry) int64 {
	if en.Path == "" || m.cfg.CacheRoot == "" {
		return 0
	}
	p := filepath.Join(m.cfg.CacheRoot, en.InfoHash, filepath.FromSlash(en.Path))
	for _, f := range []string{p, p + ".part"} {
		if info, err := os.Stat(f); err == nil {
			return allocatedSize(f, info)
		}
	}
	return 0
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
	if patch.Meta != nil {
		meta := *patch.Meta
		en.Meta = &meta
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
	if idx == leftoverIdx {
		return m.RemoveTorrentData(k.ih)
	}
	if !m.deleteDownload(k) {
		return ErrNoDownload
	}
	// Removals by a client (DELETE /downloads): logged so a record that
	// disappears can be traced (lapsed leases log their own line).
	logging.For("engine").Info("removed download on request", "info_hash", k.ih, "file_idx", k.idx)
	return nil
}

// RemoveTorrentData forgets every record of a torrent and deletes its cache
// folder. A torrent being read keeps its data until the janitor reclaims it
// (it has no record left).
func (m *manager) RemoveTorrentData(infoHash string) error {
	ih := strings.ToLower(infoHash)
	m.dl.mu.Lock()
	found := false
	for k := range m.dl.entries {
		if k.ih == ih {
			delete(m.dl.entries, k)
			found = true
		}
	}
	if found {
		m.dl.saveLocked()
	}
	m.dl.mu.Unlock()
	m.mu.Lock()
	e, loaded := m.engines[ih]
	if loaded {
		e.mu.Lock()
		reading := e.openReaders > 0
		e.background = nil
		e.held = nil
		e.mu.Unlock()
		if reading {
			m.mu.Unlock()
			e.stopNonBackground()
			logging.For("engine").Info("removed torrent records on request (being read; data reclaimed later)", "info_hash", ih)
			return nil
		}
		finish := m.beginPurge(ih, e)
		m.mu.Unlock()
		finish()
		logging.For("engine").Info("removed torrent data on request", "info_hash", ih)
		return nil
	}
	m.mu.Unlock()
	dir := filepath.Join(m.cfg.CacheRoot, ih)
	if _, err := os.Stat(dir); err != nil {
		if found {
			return nil
		}
		return ErrNoDownload
	}
	m.waitPurge(ih)
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	m.forgetMetainfo(ih)
	logging.For("engine").Info("removed torrent data on request", "info_hash", ih)
	return nil
}

// Disk reports the cache volume's free and total space and the bytes the
// records' files occupy.
func (m *manager) Disk() types.DiskInfo {
	var used int64
	for _, d := range m.Downloads() {
		used += d.BytesOnDisk
	}
	free, total := diskSpace(m.cfg.CacheRoot)
	return types.DiskInfo{Free: free, Total: total, Used: used}
}

// scanLeftovers records every torrent cache folder that has no record and no
// loaded torrent as a leftover, and drops leftover records whose folder is
// gone. Runs at start.
func (m *manager) scanLeftovers() {
	if m.mem != nil || m.cfg.CacheRoot == "" {
		return
	}
	dirs, err := os.ReadDir(m.cfg.CacheRoot)
	if err != nil {
		return
	}
	present := map[string]os.DirEntry{}
	for _, d := range dirs {
		if d.IsDir() && isHexInfoHash(d.Name()) {
			present[strings.ToLower(d.Name())] = d
		}
	}
	m.mu.RLock()
	loaded := make(map[string]bool, len(m.engines))
	for ih := range m.engines {
		loaded[ih] = true
	}
	m.mu.RUnlock()
	m.dl.mu.Lock()
	defer m.dl.mu.Unlock()
	recorded := map[string]bool{}
	changed := false
	for k, en := range m.dl.entries {
		if en.kind() == types.DownloadLeftover {
			if _, ok := present[k.ih]; !ok {
				delete(m.dl.entries, k)
				changed = true
			}
			continue
		}
		recorded[k.ih] = true
	}
	for ih, d := range present {
		key := downloadKey{ih, leftoverIdx}
		if _, ok := m.dl.entries[key]; ok || recorded[ih] || loaded[ih] {
			continue
		}
		added := time.Now()
		if info, err := d.Info(); err == nil {
			added = info.ModTime()
		}
		name := ""
		if sub, err := os.ReadDir(filepath.Join(m.cfg.CacheRoot, d.Name())); err == nil && len(sub) == 1 {
			name = strings.TrimSuffix(sub[0].Name(), ".part")
		}
		m.dl.entries[key] = &downloadEntry{InfoHash: ih, FileIdx: leftoverIdx, Kind: types.DownloadLeftover, Name: name, AddedAt: added}
		changed = true
		logging.For("engine").Info("found leftover cache folder", "info_hash", ih)
	}
	if changed {
		m.dl.saveLocked()
	}
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
	// Unkept files beside it stay (deleted only on request): the folder
	// keeps them under a marker instead of going whole.
	if !others && m.folderHasOtherData(k.ih, gone.Path) {
		m.markFolder(k.ih)
		others = true
	}
	m.mu.Lock()
	e, loaded := m.engines[k.ih]
	if loaded {
		e.mu.Lock()
		reading := e.openReaders > 0
		e.mu.Unlock()
		if reading {
			m.mu.Unlock()
			e.setHeld(k.idx, false)
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
		m.forgetMetainfo(k.ih)
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
	m.expireTemporary()
	m.dl.mu.Lock()
	type torrentState struct {
		trackers   []string
		incomplete []int
	}
	byTorrent := map[string]*torrentState{}
	ranks := map[downloadKey]fileRank{}
	for k, en := range m.dl.entries {
		if en.kind() != types.DownloadKept {
			continue
		}
		st := byTorrent[k.ih]
		if st == nil {
			st = &torrentState{}
			byTorrent[k.ih] = st
		}
		st.trackers = mergeStrings(st.trackers, en.Trackers)
		if !en.Complete && !en.Paused {
			st.incomplete = append(st.incomplete, k.idx)
			ranks[k] = fileRank{idx: k.idx, order: en.Order, added: en.AddedAt}
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
			if e, ok := m.loadedEngine(ih); ok && e.hasInfo() {
				rs := make([]fileRank, 0, len(st.incomplete))
				for _, idx := range st.incomplete {
					r := ranks[downloadKey{ih, idx}]
					r.reading = e.isReading(idx)
					rs = append(rs, r)
				}
				e.setFirst(firstFile(rs))
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

// fileRank is what decides which of a torrent's incomplete kept files
// downloads first.
type fileRank struct {
	idx     int
	order   int
	added   time.Time
	reading bool
}

// firstFile picks the file of a torrent that downloads first: one being
// played, else the lowest order (0, unranked, after every ranked one), else
// the oldest record. -1 for none.
func firstFile(rs []fileRank) int {
	best := -1
	var b fileRank
	less := func(x, y fileRank) bool {
		if x.reading != y.reading {
			return x.reading
		}
		xo, yo := x.order, y.order
		if xo == 0 {
			xo = math.MaxInt
		}
		if yo == 0 {
			yo = math.MaxInt
		}
		if xo != yo {
			return xo < yo
		}
		if !x.added.Equal(y.added) {
			return x.added.Before(y.added)
		}
		return x.idx < y.idx
	}
	for _, r := range rs {
		if best < 0 || less(r, b) {
			best, b = r.idx, r
		}
	}
	return best
}

// setFirst gives background file idx High priority, so peers that have its
// pieces send those first and the other background files take what's left
// (a player's readahead still outranks it); the previous first file goes back
// to Normal. -1 clears it.
func (e *engine) setFirst(idx int) {
	files := e.t.Files()
	e.mu.Lock()
	prev, had := e.first, e.firstSet
	_, prevBackground := e.background[prev]
	_, bg := e.background[idx]
	if idx >= 0 && !bg {
		idx = -1
	}
	e.first, e.firstSet = idx, idx >= 0
	e.mu.Unlock()
	if had && prev != idx && prevBackground && prev >= 0 && prev < len(files) {
		files[prev].SetPriority(torrent.PiecePriorityNormal)
	}
	if idx >= 0 && idx < len(files) {
		files[idx].SetPriority(torrent.PiecePriorityHigh)
	}
}

// isFirst reports whether file idx is its torrent's first file (setFirst).
func (e *engine) isFirst(idx int) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.firstSet && e.first == idx
}

// loadedEngine returns ih's engine if it's loaded.
func (m *manager) loadedEngine(ih string) (*engine, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.engines[ih]
	return e, ok
}

// expireTemporary deletes temporary files whose lease lapsed, unless a reader
// has the torrent open (the player is about to make it kept).
func (m *manager) expireTemporary() {
	now := time.Now()
	m.dl.mu.Lock()
	var lapsed []downloadKey
	for k, en := range m.dl.entries {
		if en.kind() == types.DownloadTemporary && now.After(en.leaseUntil) {
			lapsed = append(lapsed, k)
		}
	}
	m.dl.mu.Unlock()
	for _, k := range lapsed {
		if m.torrentReading(k.ih) {
			continue
		}
		if m.deleteDownload(k) {
			logging.For("engine").Info("removed temporary file (lease lapsed)", "info_hash", k.ih, "file_idx", k.idx)
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
		if e.reading[i] == 0 && !e.isWantedLocked(i) {
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

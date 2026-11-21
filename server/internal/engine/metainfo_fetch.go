// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package engine

import (
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/rivulet-kodi/rivulet-engine/internal/logging"
	"github.com/rivulet-kodi/rivulet-engine/internal/types"
)

// Data on disk from before file lists were saved (metainfo_store.go) has no
// known size or progress. At start, the torrents with data and no saved list
// are loaded one at a time to get their metadata from the swarm (DHT, the
// engine's trackers and the records' own), which saves it; each is unloaded
// again unless a download wants it. Until then their unkept files read
// "pending"; one nobody answers for reads "missing" until the next start.

// metaFetchTimeout bounds the wait for one torrent's metadata.
var metaFetchTimeout = 2 * time.Minute

// metaFetchState is which torrents' metadata nobody answered for.
type metaFetchState struct {
	mu      sync.Mutex
	missing map[string]bool
}

// info is an unkept file's Info while its torrent has no file list.
func (s *metaFetchState) info(ih string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.missing[ih] {
		return types.DownloadInfoMissing
	}
	return types.DownloadInfoPending
}

func (s *metaFetchState) setMissing(ih string, on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.missing == nil {
		s.missing = map[string]bool{}
	}
	if on {
		s.missing[ih] = true
	} else {
		delete(s.missing, ih)
	}
}

// metaFetch is one torrent to fetch metadata for, with its records' trackers.
type metaFetch struct {
	ih       string
	trackers []string
}

// metainfoTodo lists the torrents with a record or marker, data on disk and no
// saved file list. Taken at start, before anything is loaded.
func (m *manager) metainfoTodo() []metaFetch {
	if m.mem != nil || m.cfg.CacheRoot == "" {
		return nil
	}
	m.dl.mu.Lock()
	byIh := map[string][]string{}
	for k, en := range m.dl.entries {
		byIh[k.ih] = mergeStrings(byIh[k.ih], en.Trackers)
	}
	m.dl.mu.Unlock()
	var todo []metaFetch
	for ih, trackers := range byIh {
		if _, err := os.Stat(m.metainfoPath(ih)); err == nil {
			continue
		}
		if !m.folderHasOtherData(ih, "") {
			continue
		}
		todo = append(todo, metaFetch{ih, trackers})
	}
	sort.Slice(todo, func(i, j int) bool { return todo[i].ih < todo[j].ih })
	return todo
}

// fetchMetainfo gets each torrent's metadata in turn (see above).
func (m *manager) fetchMetainfo(todo []metaFetch) {
	for _, f := range todo {
		select {
		case <-m.done:
			return
		default:
		}
		if _, err := os.Stat(m.metainfoPath(f.ih)); err == nil {
			continue // saved meanwhile (played or resumed)
		}
		if _, err := os.Stat(filepath.Join(m.cfg.CacheRoot, f.ih)); err != nil {
			continue // deleted meanwhile
		}
		m.mu.RLock()
		_, loaded := m.engines[f.ih]
		m.mu.RUnlock()
		if loaded {
			continue // loaded for something else: its metadata is saved when known
		}
		eng, err := m.EnsureEngine(f.ih, types.AddOptions{Trackers: f.trackers})
		if err != nil {
			m.metaFetch.setMissing(f.ih, true)
			logging.For("engine").Warn("fetching torrent metadata", "info_hash", f.ih, "err", err)
			continue
		}
		e := eng.(*engine)
		start := time.Now()
		select {
		case <-e.t.GotInfo():
			m.saveMetainfo(f.ih, e.t)
			m.metaFetch.setMissing(f.ih, false)
			logging.For("engine").Info("fetched torrent metadata for data on disk", "info_hash", f.ih, "after", time.Since(start).Round(time.Millisecond))
		case <-time.After(metaFetchTimeout):
			m.metaFetch.setMissing(f.ih, true)
			logging.For("engine").Info("no metadata for data on disk (retried at the next start)", "info_hash", f.ih)
		case <-m.done:
			return
		}
		m.releaseFetched(f.ih, e)
	}
}

// releaseFetched unloads a torrent loaded only for its metadata, unless a
// reader has it or a kept download is fetching it.
func (m *manager) releaseFetched(ih string, e *engine) {
	m.dl.mu.Lock()
	wanted := false
	for k, en := range m.dl.entries {
		if k.ih == ih && en.kind() == types.DownloadKept && !en.Complete && !en.Paused {
			wanted = true
		}
	}
	m.dl.mu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if wanted || m.engines[ih] != e {
		return
	}
	e.mu.Lock()
	reading := e.openReaders > 0
	e.mu.Unlock()
	if !reading {
		m.unloadLocked(ih, e)
	}
}

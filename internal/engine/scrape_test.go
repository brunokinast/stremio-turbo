// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"errors"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/anacrolix/dht/v2/krpc"
	"github.com/anacrolix/torrent/types/infohash"

	"github.com/rivulet-kodi/rivulet-engine/internal/types"
)

const (
	ihA = "aabbccddeeff00112233445566778899aabbccdd"
	ihB = "00112233445566778899aabbccddeeff00112233"
)

// fakeScrape answers from a per-tracker table and records each call.
type fakeScrape struct {
	mu    sync.Mutex
	calls map[string][][]string // tracker → batches of hex hashes
	table map[string]map[string]scrapeCounts
	fail  map[string]bool
}

func (f *fakeScrape) fn(_ context.Context, tr string, ihs []infohash.T) ([]scrapeCounts, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls == nil {
		f.calls = make(map[string][][]string)
	}
	batch := make([]string, len(ihs))
	for i, ih := range ihs {
		batch[i] = ih.HexString()
	}
	f.calls[tr] = append(f.calls[tr], batch)
	if f.fail[tr] {
		return nil, errors.New("tracker down")
	}
	out := make([]scrapeCounts, len(ihs))
	for i, ih := range batch {
		out[i] = f.table[tr][ih]
	}
	return out, nil
}

func TestScrapeAll_MergesTrackersAndBatches(t *testing.T) {
	const (
		trA     = "udp://a.example.com:1337/announce"
		trB     = "http://b.example.com/announce"
		trExtra = "udp://extra.example.com:6969/announce"
		trDown  = "udp://down.example.com:1/announce"
	)
	f := &fakeScrape{
		table: map[string]map[string]scrapeCounts{
			trA:     {ihA: {seeders: 10, leechers: 3, completed: 100}},
			trB:     {ihA: {seeders: 25, leechers: 1}, ihB: {seeders: 4}},
			trExtra: {ihA: {seeders: 7, leechers: 9}},
		},
		fail: map[string]bool{trDown: true},
	}
	reqs := []types.ScrapeRequest{
		{InfoHash: ihA, Trackers: []string{trA, trB, "wss://ws.example.com", trDown}},
		{InfoHash: ihB, Trackers: []string{trB}},
		{InfoHash: "not-a-hash"},
		{InfoHash: ihA}, // duplicate: answered from the same scrape
	}
	got := scrapeAll(context.Background(), reqs, scrapeSources{extraTrackers: []string{trExtra}, tracker: f.fn}, &scrapeCache{}, time.Now(), nil)

	want := []types.ScrapeResult{
		{InfoHash: ihA, Seeders: 25, Leechers: 9, Completed: 100, Trackers: 3},
		{InfoHash: ihB, Seeders: 4, Trackers: 2}, // trB + trExtra (zeros)
		{InfoHash: "not-a-hash"},
		{InfoHash: ihA, Seeders: 25, Leechers: 9, Completed: 100, Trackers: 3},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("results =\n%+v\nwant\n%+v", got, want)
	}
	// Each tracker asked once, with every hash it serves in one batch; ws
	// trackers are not scrapeable and are skipped.
	if b := f.calls[trB]; len(b) != 1 || !slices.Equal(b[0], []string{ihA, ihB}) {
		t.Errorf("trB calls = %v", b)
	}
	if _, ok := f.calls["wss://ws.example.com"]; ok {
		t.Error("ws tracker was scraped")
	}
}

func TestScrapeAll_Cache(t *testing.T) {
	const tr = "udp://a.example.com:1337/announce"
	f := &fakeScrape{table: map[string]map[string]scrapeCounts{tr: {ihA: {seeders: 5}}}}
	cache := &scrapeCache{}
	now := time.Now()
	reqs := []types.ScrapeRequest{{InfoHash: ihA, Trackers: []string{tr}}, {InfoHash: ihB}}

	scrapeAll(context.Background(), reqs, scrapeSources{tracker: f.fn}, cache, now, nil)
	scrapeAll(context.Background(), reqs, scrapeSources{tracker: f.fn}, cache, now.Add(time.Minute), nil)
	// ihA answered → cached; ihB had no trackers → nothing to scrape or cache.
	if n := len(f.calls[tr]); n != 1 {
		t.Errorf("fresh cache: tracker called %d times; want 1", n)
	}

	got := scrapeAll(context.Background(), reqs, scrapeSources{tracker: f.fn}, cache, now.Add(scrapeCacheTTL+time.Second), nil)
	if n := len(f.calls[tr]); n != 2 {
		t.Errorf("expired cache: tracker called %d times; want 2", n)
	}
	if got[0].Seeders != 5 || got[0].Trackers != 1 {
		t.Errorf("result = %+v", got[0])
	}
}

func TestScrapeAll_UnansweredNotCached(t *testing.T) {
	const tr = "udp://down.example.com:1/announce"
	f := &fakeScrape{fail: map[string]bool{tr: true}}
	cache := &scrapeCache{}
	reqs := []types.ScrapeRequest{{InfoHash: ihA, Trackers: []string{tr}}}
	now := time.Now()
	scrapeAll(context.Background(), reqs, scrapeSources{tracker: f.fn}, cache, now, nil)
	scrapeAll(context.Background(), reqs, scrapeSources{tracker: f.fn}, cache, now, nil)
	if n := len(f.calls[tr]); n != 2 {
		t.Errorf("tracker called %d times; want 2 (failures are retried)", n)
	}
}

func TestScrapeTrackerList_CapsRequestTrackers(t *testing.T) {
	var many []string
	for i := range scrapeMaxTrackersPerTorrent + 5 {
		many = append(many, "udp://t"+string(rune('a'+i))+".example.com:1/announce")
	}
	got := scrapeTrackerList(many, []string{"http://extra.example.com/announce", many[0]})
	if len(got) != scrapeMaxTrackersPerTorrent+1 {
		t.Errorf("len = %d; want %d (capped + 1 extra, deduped)", len(got), scrapeMaxTrackersPerTorrent+1)
	}
}

func TestScrapeAll_StreamsAndMergesSources(t *testing.T) {
	const tr = "udp://a.example.com:1337/announce"
	f := &fakeScrape{table: map[string]map[string]scrapeCounts{tr: {ihA: {seeders: 5, leechers: 2}, ihB: {}}}}
	src := scrapeSources{
		tracker: f.fn,
		dht: func(_ context.Context, ih infohash.T, report func(seeds, peers int)) {
			if ih.HexString() == ihA {
				report(30, 10)
				report(80, 12)
			}
		},
		live: func(ih string) (int, int, bool) {
			if ih == ihB {
				return 3, 1, true
			}
			return 0, 0, false
		},
	}
	var (
		mu      sync.Mutex
		updates []types.ScrapeUpdate
	)
	reqs := []types.ScrapeRequest{{InfoHash: ihA, Trackers: []string{tr}}, {InfoHash: ihB, Trackers: []string{tr}}}
	got := scrapeAll(context.Background(), reqs, src, &scrapeCache{}, time.Now(), func(u types.ScrapeUpdate) {
		mu.Lock()
		defer mu.Unlock()
		updates = append(updates, u)
	})

	want := []types.ScrapeResult{
		{InfoHash: ihA, Seeders: 80, Leechers: 12, Trackers: 1, DHT: true},
		{InfoHash: ihB, Seeders: 3, Leechers: 1, Trackers: 1, Live: true},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("results =\n%+v\nwant\n%+v", got, want)
	}
	// Live answers come first; each torrent's last update carries its final
	// figures; a zero tracker answer below the live figure adds no update
	// beyond the first-answer one.
	if len(updates) == 0 || updates[0].Source != "live" || updates[0].InfoHash != ihB {
		t.Fatalf("first update = %+v", updates)
	}
	last := map[string]types.ScrapeResult{}
	for _, u := range updates {
		last[u.InfoHash] = u.ScrapeResult
	}
	if last[ihA] != want[0] || last[ihB] != want[1] {
		t.Errorf("last updates = %+v", last)
	}
}

func TestScrapeAll_CachedStreamsOnce(t *testing.T) {
	const tr = "udp://a.example.com:1337/announce"
	f := &fakeScrape{table: map[string]map[string]scrapeCounts{tr: {ihA: {seeders: 5}}}}
	cache := &scrapeCache{}
	now := time.Now()
	reqs := []types.ScrapeRequest{{InfoHash: ihA, Trackers: []string{tr}}}
	scrapeAll(context.Background(), reqs, scrapeSources{tracker: f.fn}, cache, now, nil)

	var updates []types.ScrapeUpdate
	scrapeAll(context.Background(), reqs, scrapeSources{tracker: f.fn}, cache, now, func(u types.ScrapeUpdate) {
		updates = append(updates, u)
	})
	if len(updates) != 1 || updates[0].Source != "cache" || updates[0].Seeders != 5 {
		t.Errorf("updates = %+v", updates)
	}
}

func TestBloomUnionAndCount(t *testing.T) {
	var a, b, u krpc.ScrapeBloomFilter
	if n := bloomCount(&u); n != 0 {
		t.Errorf("empty filter count = %d; want 0", n)
	}
	for i := range 40 {
		ip := net.IPv4(10, 0, byte(i/256), byte(i))
		if i < 25 {
			a.AddIp(ip)
		}
		if i >= 15 {
			b.AddIp(ip)
		}
	}
	bloomUnion(&u, &a)
	bloomUnion(&u, &b)
	bloomUnion(&u, nil)
	// 40 distinct addresses overall, 10 of them in both filters.
	if n := bloomCount(&u); n < 36 || n > 44 {
		t.Errorf("union count = %d; want about 40", n)
	}
}

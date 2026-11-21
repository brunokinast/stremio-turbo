// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rivulet-kodi/rivulet-engine/internal/types"
)

const (
	// scrapeMaxTorrents caps one request; a stream list has a few dozen.
	scrapeMaxTorrents = 200
	// scrapeRequestTimeout bounds a plain JSON call (sources run in parallel,
	// each with its own shorter timeout; slower DHT answers are cut off).
	scrapeRequestTimeout = 8 * time.Second
	// scrapeStreamTimeout bounds a streamed call, which reports figures as
	// they arrive and so can wait for the slower DHT scrapes to finish.
	scrapeStreamTimeout = 60 * time.Second
	// ndjsonType is the streamed response's media type.
	ndjsonType = "application/x-ndjson"
)

type scrapeBody struct {
	Torrents []types.ScrapeRequest `json:"torrents"`
}

// handleScrape serves POST /scrape: swarm sizes for a batch of torrents, from
// trackers, DHT scrapes and open engines, without adding them as engines. Body
// {"torrents":[{"infoHash":"…","trackers":["udp://…"]}]} → {"results":[…]}
// in request order. With "Accept: application/x-ndjson" the figures stream
// instead: one JSON line per improvement as sources answer, then
// {"done":true}.
func (s *server) handleScrape(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body scrapeBody
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body: " + err.Error()})
		return
	}
	if len(body.Torrents) > scrapeMaxTorrents {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "too many torrents"})
		return
	}
	reqs := make([]types.ScrapeRequest, len(body.Torrents))
	for i, t := range body.Torrents {
		if !isInfoHash(t.InfoHash) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid infoHash: " + t.InfoHash})
			return
		}
		reqs[i] = types.ScrapeRequest{
			InfoHash: strings.ToLower(t.InfoHash),
			// Accept bare announce URLs and peerSearch-style sources
			// ("tracker:<url>", "dht:<ih>"), as stremio-core forwards them.
			Trackers: filterRequestTrackers(stripTrackerPrefixes(t.Trackers)),
		}
	}
	if strings.Contains(r.Header.Get("Accept"), ndjsonType) {
		s.streamScrape(w, r, reqs)
		return
	}
	ctx, cancel := withTimeout(r, scrapeRequestTimeout)
	defer cancel()
	writeJSON(w, http.StatusOK, map[string]any{"results": s.em.Scrape(ctx, reqs, nil)})
}

// streamScrape writes each ScrapeUpdate as an NDJSON line, flushed at once so
// the client can show figures while slower sources are still asked.
func (s *server) streamScrape(w http.ResponseWriter, r *http.Request, reqs []types.ScrapeRequest) {
	ctx, cancel := withTimeout(r, scrapeStreamTimeout)
	defer cancel()
	w.Header().Set("Content-Type", ndjsonType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	flusher, _ := w.(http.Flusher)
	writeLine := func(v any) {
		_ = enc.Encode(v) // a gone client also cancels ctx, ending the scrape
		if flusher != nil {
			flusher.Flush()
		}
	}
	s.em.Scrape(ctx, reqs, func(u types.ScrapeUpdate) { writeLine(u) })
	writeLine(map[string]bool{"done": true})
}

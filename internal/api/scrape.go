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
	// scrapeRequestTimeout bounds the whole call (tracker scrapes run in
	// parallel, each with its own shorter timeout).
	scrapeRequestTimeout = 8 * time.Second
)

type scrapeBody struct {
	Torrents []types.ScrapeRequest `json:"torrents"`
}

// handleScrape serves POST /scrape: tracker-reported swarm sizes for a batch
// of torrents, without adding them as engines. Body
// {"torrents":[{"infoHash":"…","trackers":["udp://…"]}]} → {"results":[…]}
// in request order.
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
	ctx, cancel := withTimeout(r, scrapeRequestTimeout)
	defer cancel()
	writeJSON(w, http.StatusOK, map[string]any{"results": s.em.Scrape(ctx, reqs)})
}

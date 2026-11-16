// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package api

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/rivulet-kodi/rivulet-engine/internal/types"
)

func TestHandlerScrape(t *testing.T) {
	const other = "00112233445566778899aabbccddeeff00112233"
	em := newFakeEM()
	em.scrapeSeeders = map[string]int{testIH: 42}
	h := New(em, &fakeSS{}, &fakeProber{}, types.Config{HTTPPort: 11470})

	body := `{"torrents":[
		{"infoHash":"` + strings.ToUpper(testIH) + `","trackers":["tracker:udp://t.example.com:80/announce","dht:` + testIH + `","http://127.0.0.1:9/announce"]},
		{"infoHash":"` + other + `"}
	]}`
	rec := serve(t, h, http.MethodPost, "/scrape", strings.NewReader(body))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", rec.Code, rec.Body)
	}
	var resp struct {
		Results []types.ScrapeResult `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	want := []types.ScrapeResult{
		{InfoHash: testIH, Seeders: 42, Trackers: 1},
		{InfoHash: other},
	}
	if !slices.Equal(resp.Results, want) {
		t.Errorf("results = %+v; want %+v", resp.Results, want)
	}
	// Info hash lower-cased; "tracker:" unwrapped, "dht:" dropped, loopback
	// tracker removed by the SSRF filter.
	if got := em.scrapeReqs[0]; got.InfoHash != testIH ||
		!slices.Equal(got.Trackers, []string{"udp://t.example.com:80/announce"}) {
		t.Errorf("engine request = %+v", got)
	}
}

func TestHandlerScrape_Rejects(t *testing.T) {
	h := newHandler(t)
	tests := []struct {
		name, method, body string
		want               int
	}{
		{"get", http.MethodGet, "", http.StatusMethodNotAllowed},
		{"bad json", http.MethodPost, "{", http.StatusBadRequest},
		{"bad hash", http.MethodPost, `{"torrents":[{"infoHash":"xyz"}]}`, http.StatusBadRequest},
		{"too many", http.MethodPost, `{"torrents":[` + strings.Repeat(`{"infoHash":"`+testIH+`"},`, scrapeMaxTorrents) + `{"infoHash":"` + testIH + `"}]}`, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serve(t, h, tt.method, "/scrape", strings.NewReader(tt.body))
			if rec.Code != tt.want {
				t.Errorf("status = %d; want %d", rec.Code, tt.want)
			}
		})
	}
}

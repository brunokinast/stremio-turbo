// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestHandlerScrape_Stream(t *testing.T) {
	em := newFakeEM()
	em.scrapeSeeders = map[string]int{testIH: 42}
	h := New(em, &fakeSS{}, &fakeProber{}, types.Config{HTTPPort: 11470})

	req := httptest.NewRequest(http.MethodPost, "/scrape", strings.NewReader(`{"torrents":[{"infoHash":"`+testIH+`"}]}`))
	req.Header.Set("Accept", "application/x-ndjson")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/x-ndjson" {
		t.Errorf("Content-Type = %q", ct)
	}
	lines := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %q; want one update and done", lines)
	}
	var u types.ScrapeUpdate
	if err := json.Unmarshal([]byte(lines[0]), &u); err != nil {
		t.Fatal(err)
	}
	if u.InfoHash != testIH || u.Seeders != 42 || u.Source != "tracker" {
		t.Errorf("update = %+v", u)
	}
	if lines[1] != `{"done":true}` {
		t.Errorf("last line = %q", lines[1])
	}
}

func TestHandlerDebugStatus(t *testing.T) {
	h := newHandler(t)
	rec := serve(t, h, http.MethodGet, "/debug/status", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "status" {
		t.Errorf("status = %d, body %q; want 200 with the client status", rec.Code, rec.Body)
	}
	if rec := serve(t, h, http.MethodGet, "/debug/other", nil); rec.Code != http.StatusNotFound {
		t.Errorf("/debug/other = %d; want 404", rec.Code)
	}
}

func TestHandlerDebugDials(t *testing.T) {
	h := newHandler(t, testEngine())
	rec := serve(t, h, http.MethodGet, "/debug/dials/"+strings.ToUpper(testIH), nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"result":"refused"`) {
		t.Errorf("known: status %d body %s", rec.Code, rec.Body)
	}
	if rec := serve(t, h, http.MethodGet, "/debug/dials/00112233445566778899aabbccddeeff00112233", nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown torrent = %d; want 404", rec.Code)
	}
	if rec := serve(t, h, http.MethodGet, "/debug/dials/nothex", nil); rec.Code != http.StatusNotFound {
		t.Errorf("bad hash = %d; want 404", rec.Code)
	}
}

// A ranged stream request shows up in the torrent's trace with its range,
// user agent, size and first-byte time; stats polls are not logged.
func TestHandlerDebugTraceLogsStreamRequests(t *testing.T) {
	h := newHandler(t, testEngine())
	req := httptest.NewRequest(http.MethodGet, "/"+testIH+"/0", nil)
	req.Header.Set("Range", "bytes=0-3")
	req.Header.Set("User-Agent", "libmpv")
	h.ServeHTTP(httptest.NewRecorder(), req)
	serve(t, h, http.MethodGet, "/"+testIH+"/stats.json", nil)

	rec := serve(t, h, http.MethodGet, "/debug/trace/"+strings.ToUpper(testIH), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body)
	}
	var tr types.Trace
	if err := json.Unmarshal(rec.Body.Bytes(), &tr); err != nil {
		t.Fatal(err)
	}
	if len(tr.Requests) != 1 {
		t.Fatalf("requests = %+v; want only the stream request", tr.Requests)
	}
	got := tr.Requests[0]
	if got.Class != "stream" || got.Range != "bytes=0-3" || got.UserAgent != "libmpv" ||
		got.Status != http.StatusPartialContent || got.Bytes != 4 || got.FirstByteMs < 0 || got.End != "done" || got.ID == 0 {
		t.Errorf("request = %+v", got)
	}
	if rec := serve(t, h, http.MethodGet, "/debug/trace/00112233445566778899aabbccddeeff00112233", nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown torrent = %d; want 404", rec.Code)
	}
	if rec := serve(t, h, http.MethodGet, "/debug/locks", nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"lockedAt":"here"`) {
		t.Errorf("locks: status %d body %s", rec.Code, rec.Body)
	}
}

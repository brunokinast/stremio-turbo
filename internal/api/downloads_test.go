// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package api

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rivulet-kodi/rivulet-engine/internal/types"
)

// fakeDownloadsEM adds an in-memory types.Downloader to fakeEM.
type fakeDownloadsEM struct {
	*fakeEM
	added   []types.AddOptions
	entries map[string]types.Download
}

func dlKey(ih string, idx int) string { return ih + "/" + strconv.Itoa(idx) }

func (f *fakeDownloadsEM) AddDownload(ih string, idx int, opts types.AddOptions) (types.Download, error) {
	f.added = append(f.added, opts)
	d := types.Download{InfoHash: ih, FileIdx: idx, State: types.DownloadQueued, AddedAt: time.Now()}
	f.entries[dlKey(ih, idx)] = d
	return d, nil
}

func (f *fakeDownloadsEM) Downloads() []types.Download {
	out := []types.Download{}
	for _, d := range f.entries {
		out = append(out, d)
	}
	return out
}

func (f *fakeDownloadsEM) UpdateDownload(ih string, idx int, p types.DownloadPatch) (types.Download, error) {
	d, ok := f.entries[dlKey(ih, idx)]
	if !ok {
		return types.Download{}, types.ErrNoDownload
	}
	if p.Watched != nil {
		d.Watched = *p.Watched
	}
	if p.Paused != nil && *p.Paused {
		d.State = types.DownloadPaused
	}
	f.entries[dlKey(ih, idx)] = d
	return d, nil
}

func (f *fakeDownloadsEM) RemoveDownload(ih string, idx int) error {
	if _, ok := f.entries[dlKey(ih, idx)]; !ok {
		return types.ErrNoDownload
	}
	delete(f.entries, dlKey(ih, idx))
	return nil
}

const dlIH = "0123456789abcdef0123456789abcdef01234567"

func doDL(t *testing.T, h http.Handler, method, path, body string, loopback bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if loopback {
		req.RemoteAddr = "127.0.0.1:5555"
	} else {
		req.RemoteAddr = "192.0.2.7:5555"
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestDownloadsAPI walks add → list → patch → delete, and the error paths.
func TestDownloadsAPI(t *testing.T) {
	em := &fakeDownloadsEM{fakeEM: newFakeEM(), entries: map[string]types.Download{}}
	h := New(em, &fakeSS{}, &fakeProber{}, types.Config{HTTPPort: 11470, AppPath: t.TempDir()})

	add := `{"infoHash":"` + strings.ToUpper(dlIH) + `","fileIdx":2,"sources":["tracker:udp://t.example:1337","dht:` + dlIH + `"]}`
	if rec := doDL(t, h, http.MethodPost, "/downloads", add, true); rec.Code != http.StatusOK {
		t.Fatalf("POST = %d %s", rec.Code, rec.Body)
	}
	if len(em.added) != 1 || len(em.added[0].Sources) != 2 {
		t.Fatalf("add options = %+v", em.added)
	}
	if _, ok := em.entries[dlKey(dlIH, 2)]; !ok {
		t.Fatalf("info hash not lower-cased: %v", em.entries)
	}
	if rec := doDL(t, h, http.MethodGet, "/downloads", "", true); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"fileIdx":2`) {
		t.Fatalf("GET = %d %s", rec.Code, rec.Body)
	}
	if rec := doDL(t, h, http.MethodPatch, "/downloads/"+dlIH+"/2", `{"watched":true}`, true); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"watched":true`) {
		t.Fatalf("PATCH = %d %s", rec.Code, rec.Body)
	}
	if rec := doDL(t, h, http.MethodDelete, "/downloads/"+dlIH+"/2", "", true); rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s", rec.Code, rec.Body)
	}
	if rec := doDL(t, h, http.MethodDelete, "/downloads/"+dlIH+"/2", "", true); rec.Code != http.StatusNotFound {
		t.Fatalf("second DELETE = %d; want 404", rec.Code)
	}
	for _, c := range []struct {
		method, path, body string
		loopback           bool
		want               int
	}{
		{http.MethodPost, "/downloads", `{"infoHash":"zz","fileIdx":0}`, true, http.StatusBadRequest},
		{http.MethodPost, "/downloads", `{"infoHash":"` + dlIH + `"}`, true, http.StatusBadRequest},
		{http.MethodPost, "/downloads", `{"infoHash":"` + dlIH + `","fileIdx":0}`, false, http.StatusForbidden},
		{http.MethodDelete, "/downloads/" + dlIH + "/0", "", false, http.StatusForbidden},
		{http.MethodPut, "/downloads", "", true, http.StatusMethodNotAllowed},
		{http.MethodPatch, "/downloads/" + dlIH + "/x", "{}", true, http.StatusNotFound},
		{http.MethodPatch, "/downloads/" + dlIH + "/0", "{}", true, http.StatusNotFound},
	} {
		if rec := doDL(t, h, c.method, c.path, c.body, c.loopback); rec.Code != c.want {
			t.Errorf("%s %s = %d; want %d", c.method, c.path, rec.Code, c.want)
		}
	}
}

// TestDownloadsAPIUnsupported: an engine manager without downloads answers 501.
func TestDownloadsAPIUnsupported(t *testing.T) {
	h := newHandler(t)
	if rec := doDL(t, h, http.MethodGet, "/downloads", "", true); rec.Code != http.StatusNotImplemented {
		t.Fatalf("GET = %d; want 501", rec.Code)
	}
}

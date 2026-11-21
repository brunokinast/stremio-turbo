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
	added        []types.AddOptions
	requests     []types.DownloadRequest
	removed      []string
	entries      map[string]types.Download
	exitWhenDone *bool
}

func dlKey(ih string, idx int) string { return ih + "/" + strconv.Itoa(idx) }

func (f *fakeDownloadsEM) AddDownload(ih string, idx int, opts types.AddOptions, req types.DownloadRequest) (types.Download, error) {
	if req.Kind != "" && req.Kind != types.DownloadKept && req.Kind != types.DownloadTemporary {
		return types.Download{}, types.ErrDownloadKind
	}
	f.added = append(f.added, opts)
	f.requests = append(f.requests, req)
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

func (f *fakeDownloadsEM) SetExitWhenDone(on bool) { f.exitWhenDone = &on }

func (f *fakeDownloadsEM) RemoveTorrentData(ih string) error {
	f.removed = append(f.removed, ih)
	return nil
}

func (f *fakeDownloadsEM) RemoveUnkept(ih, path string) error {
	if path == "busy.mkv" {
		return types.ErrDownloadBusy
	}
	f.removed = append(f.removed, ih+":"+path)
	return nil
}

func (f *fakeDownloadsEM) Disk() types.DiskInfo { return types.DiskInfo{Free: 7, Total: 9, Used: 1} }

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
		{http.MethodPost, "/downloads", `{"infoHash":"` + dlIH + `","fileIdx":0,"kind":"leftover"}`, true, http.StatusBadRequest},
		{http.MethodDelete, "/downloads/" + dlIH, "", false, http.StatusForbidden},
		{http.MethodGet, "/downloads/" + dlIH, "", true, http.StatusMethodNotAllowed},
		{http.MethodPost, "/downloads/disk", "", true, http.StatusMethodNotAllowed},
	} {
		if rec := doDL(t, h, c.method, c.path, c.body, c.loopback); rec.Code != c.want {
			t.Errorf("%s %s = %d; want %d", c.method, c.path, rec.Code, c.want)
		}
	}
}

// TestDownloadsAPIRecord: a temporary add carries its request, a torrent's
// data is deleted by info hash, and /downloads/disk reports the volume.
func TestDownloadsAPIRecord(t *testing.T) {
	em := &fakeDownloadsEM{fakeEM: newFakeEM(), entries: map[string]types.Download{}}
	h := New(em, &fakeSS{}, &fakeProber{}, types.Config{HTTPPort: 11470, AppPath: t.TempDir()})

	add := `{"infoHash":"` + dlIH + `","fileIdx":1,"kind":"temporary","origin":"picker","startFraction":0.4,"meta":{"metaId":"tt1","videoId":"tt1:2:3","type":"series"}}`
	if rec := doDL(t, h, http.MethodPost, "/downloads", add, true); rec.Code != http.StatusOK {
		t.Fatalf("POST = %d %s", rec.Code, rec.Body)
	}
	req := em.requests[0]
	if req.Kind != types.DownloadTemporary || req.Origin != "picker" || req.StartFraction != 0.4 || req.Meta == nil || req.Meta.VideoID != "tt1:2:3" {
		t.Fatalf("request = %+v", req)
	}
	if rec := doDL(t, h, http.MethodDelete, "/downloads/"+strings.ToUpper(dlIH), "", true); rec.Code != http.StatusNoContent || len(em.removed) != 1 || em.removed[0] != dlIH {
		t.Fatalf("DELETE torrent = %d, removed %v", rec.Code, em.removed)
	}
	if rec := doDL(t, h, http.MethodDelete, "/downloads/"+dlIH+"?path=pack%2FE02.mkv.part", "", true); rec.Code != http.StatusNoContent || len(em.removed) != 2 || em.removed[1] != dlIH+":pack/E02.mkv.part" {
		t.Fatalf("DELETE partial = %d, removed %v", rec.Code, em.removed)
	}
	if rec := doDL(t, h, http.MethodDelete, "/downloads/"+dlIH+"?path=busy.mkv", "", true); rec.Code != http.StatusConflict {
		t.Fatalf("DELETE busy partial = %d; want 409", rec.Code)
	}
	if rec := doDL(t, h, http.MethodGet, "/downloads/disk", "", true); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"free":7`) {
		t.Fatalf("GET disk = %d %s", rec.Code, rec.Body)
	}
}

// TestExitWhenDoneAPI: POST /downloads/exit-when-done toggles drain mode.
func TestExitWhenDoneAPI(t *testing.T) {
	em := &fakeDownloadsEM{fakeEM: newFakeEM(), entries: map[string]types.Download{}}
	h := New(em, &fakeSS{}, &fakeProber{}, types.Config{HTTPPort: 11470, AppPath: t.TempDir()})
	if rec := doDL(t, h, http.MethodPost, "/downloads/exit-when-done", `{"on":true}`, true); rec.Code != http.StatusOK || em.exitWhenDone == nil || !*em.exitWhenDone {
		t.Fatalf("on = %d %s", rec.Code, rec.Body)
	}
	if rec := doDL(t, h, http.MethodPost, "/downloads/exit-when-done", `{}`, true); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty body = %d; want 400", rec.Code)
	}
	if rec := doDL(t, h, http.MethodPost, "/downloads/exit-when-done", `{"on":false}`, false); rec.Code != http.StatusForbidden {
		t.Fatalf("remote = %d; want 403", rec.Code)
	}
	if rec := doDL(t, h, http.MethodGet, "/downloads/exit-when-done", "", true); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET = %d; want 405", rec.Code)
	}
}

// TestDownloadsAPIUnsupported: an engine manager without downloads answers 501.
func TestDownloadsAPIUnsupported(t *testing.T) {
	h := newHandler(t)
	if rec := doDL(t, h, http.MethodGet, "/downloads", "", true); rec.Code != http.StatusNotImplemented {
		t.Fatalf("GET = %d; want 501", rec.Code)
	}
}

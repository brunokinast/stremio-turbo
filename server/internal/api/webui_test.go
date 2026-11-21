// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/rivulet-kodi/rivulet-engine/internal/types"
)

func TestWebUIServesBundledFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "abc", "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"index.html":          "<html>ui</html>",
		"abc/scripts/main.js": "main()",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := &server{cfg: types.Config{WebUIDir: dir}}
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.route(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}

	if rec := get("/web"); rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/web/" {
		t.Errorf("/web = %d %q; want a redirect to /web/", rec.Code, rec.Header().Get("Location"))
	}
	rec := get("/web/")
	if rec.Code != http.StatusOK || rec.Body.String() != "<html>ui</html>" {
		t.Errorf("/web/ = %d %q; want index.html", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("index.html Cache-Control = %q; want no-cache", rec.Header().Get("Cache-Control"))
	}
	rec = get("/web/abc/scripts/main.js")
	if rec.Code != http.StatusOK || rec.Body.String() != "main()" {
		t.Errorf("asset = %d %q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "" {
		t.Errorf("asset Cache-Control = %q; want none", rec.Header().Get("Cache-Control"))
	}
	if rec := get("/web/../go.mod"); rec.Code == http.StatusOK {
		t.Errorf("path escape served a file")
	}
}

func TestWebUIOffWithoutDir(t *testing.T) {
	s := &server{cfg: types.Config{}}
	rec := httptest.NewRecorder()
	s.route(rec, httptest.NewRequest(http.MethodGet, "/web/", nil))
	if rec.Code == http.StatusOK {
		t.Errorf("/web/ served without RIVULET_WEBUI_DIR")
	}
}

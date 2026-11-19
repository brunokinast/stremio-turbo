// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/rivulet-kodi/rivulet-engine/internal/types"
)

// downloadsMaxBody caps a /downloads request body.
const downloadsMaxBody = 64 << 10

// addDownloadBody is a POST /downloads body.
type addDownloadBody struct {
	InfoHash string   `json:"infoHash"`
	FileIdx  *int     `json:"fileIdx"`
	Trackers []string `json:"trackers"`
	Sources  []string `json:"sources"`
	types.DownloadRequest
}

// handleDownloads serves the record of every file on disk: /downloads (list,
// add), /downloads/disk (space), /downloads/:infoHash (delete a torrent's
// data) and /downloads/:infoHash/:fileIdx (change, delete one file).
func (s *server) handleDownloads(w http.ResponseWriter, r *http.Request) {
	dl, ok := s.em.(types.Downloader)
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "downloads are not supported by this engine"})
		return
	}
	rest := strings.Trim(strings.TrimPrefix(strings.Trim(r.URL.Path, "/"), "downloads"), "/")
	if rest == "" {
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			writeJSON(w, http.StatusOK, dl.Downloads())
		case http.MethodPost:
			s.addDownload(w, r, dl)
		default:
			w.Header().Set("Allow", "GET, HEAD, POST")
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		}
		return
	}
	if rest == "exit-when-done" {
		s.exitWhenDone(w, r, dl)
		return
	}
	if rest == "disk" {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		writeJSON(w, http.StatusOK, dl.Disk())
		return
	}
	seg := strings.Split(rest, "/")
	if len(seg) == 1 && isInfoHash(seg[0]) {
		if r.Method != http.MethodDelete {
			w.Header().Set("Allow", "DELETE")
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		if !s.settingsWriteAllowed(r) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "downloads write requires a browser origin, a loopback peer, or api_password"})
			return
		}
		if err := dl.RemoveTorrentData(strings.ToLower(seg[0])); err != nil {
			writeDownloadErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if len(seg) != 2 || !isInfoHash(seg[0]) {
		http.NotFound(w, r)
		return
	}
	idx, err := strconv.Atoi(seg[1])
	if err != nil || idx < -1 {
		http.NotFound(w, r)
		return
	}
	ih := strings.ToLower(seg[0])
	switch r.Method {
	case http.MethodPatch:
		if !s.settingsWriteAllowed(r) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "downloads write requires a browser origin, a loopback peer, or api_password"})
			return
		}
		var patch types.DownloadPatch
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, downloadsMaxBody)).Decode(&patch); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body: " + err.Error()})
			return
		}
		d, err := dl.UpdateDownload(ih, idx, patch)
		if err != nil {
			writeDownloadErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, d)
	case http.MethodDelete:
		if !s.settingsWriteAllowed(r) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "downloads write requires a browser origin, a loopback peer, or api_password"})
			return
		}
		if err := dl.RemoveDownload(ih, idx); err != nil {
			writeDownloadErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Allow", "PATCH, DELETE")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
	}
}

func (s *server) addDownload(w http.ResponseWriter, r *http.Request, dl types.Downloader) {
	if !s.settingsWriteAllowed(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "downloads write requires a browser origin, a loopback peer, or api_password"})
		return
	}
	var body addDownloadBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, downloadsMaxBody)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body: " + err.Error()})
		return
	}
	if !isInfoHash(body.InfoHash) || body.FileIdx == nil || *body.FileIdx < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "infoHash (40 hex characters) and fileIdx (>= 0) are required"})
		return
	}
	opts := types.AddOptions{Trackers: body.Trackers, Sources: body.Sources}
	d, err := dl.AddDownload(strings.ToLower(body.InfoHash), *body.FileIdx, opts, body.DownloadRequest)
	if err != nil {
		writeDownloadErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// exitWhenDone serves POST /downloads/exit-when-done {"on": bool}.
func (s *server) exitWhenDone(w http.ResponseWriter, r *http.Request, dl types.Downloader) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	if !s.settingsWriteAllowed(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "downloads write requires a browser origin, a loopback peer, or api_password"})
		return
	}
	var body struct {
		On *bool `json:"on"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, downloadsMaxBody)).Decode(&body); err != nil || body.On == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": `body must be {"on": true|false}`})
		return
	}
	dl.SetExitWhenDone(*body.On)
	writeJSON(w, http.StatusOK, map[string]any{"on": *body.On})
}

func writeDownloadErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, types.ErrNoDownload):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
	case errors.Is(err, types.ErrDownloadKind):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
	case errors.Is(err, types.ErrDownloadsNeedDisk):
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
	case errors.Is(err, types.ErrTooManyEngines), errors.Is(err, types.ErrDownloadNoMetadata):
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
	}
}

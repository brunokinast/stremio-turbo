// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package api

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rivulet-kodi/rivulet-engine/internal/types"
)

// Bounds for the per-torrent request log served in GET /debug/trace/:infoHash.
const (
	reqLogCap      = 1000
	reqLogTorrents = 64
)

// reqLog records the HTTP requests under each torrent's routes: what the
// player and the server's own helpers (probe, subtitle hash) read, and how long
// each waited for its first byte. Stats and peer polls are not recorded.
type reqLog struct {
	nextID   atomic.Uint64
	mu       sync.Mutex
	torrents map[string]*torrentReqs
}

type torrentReqs struct {
	lastUse time.Time
	recs    []reqRecord // ring: next is the oldest once full
	next    int
}

// reqRecord is a request with absolute times; the trace converts them to the
// torrent's time base.
type reqRecord struct {
	start time.Time
	types.TraceRequest
}

func newReqLog() *reqLog { return &reqLog{torrents: map[string]*torrentReqs{}} }

func (l *reqLog) add(ih string, rec reqRecord) {
	l.mu.Lock()
	defer l.mu.Unlock()
	tr := l.torrents[ih]
	if tr == nil {
		if len(l.torrents) >= reqLogTorrents {
			l.dropOldestLocked()
		}
		tr = &torrentReqs{}
		l.torrents[ih] = tr
	}
	tr.lastUse = rec.start
	if len(tr.recs) < reqLogCap {
		tr.recs = append(tr.recs, rec)
		return
	}
	tr.recs[tr.next] = rec
	tr.next = (tr.next + 1) % reqLogCap
}

func (l *reqLog) dropOldestLocked() {
	var oldest string
	var at time.Time
	for ih, tr := range l.torrents {
		if oldest == "" || tr.lastUse.Before(at) {
			oldest, at = ih, tr.lastUse
		}
	}
	delete(l.torrents, oldest)
}

// get returns a torrent's requests since created, oldest first, with times
// relative to it. Requests from before created belong to an earlier engine for
// the same torrent (removed or evicted since) and are left out.
func (l *reqLog) get(ih string, created time.Time) []types.TraceRequest {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := []types.TraceRequest{}
	tr := l.torrents[ih]
	if tr == nil {
		return out
	}
	for _, r := range append(append([]reqRecord(nil), tr.recs[tr.next:]...), tr.recs[:tr.next]...) {
		// A create or stream request starts just before the engine it makes,
		// so allow a little slack.
		if r.start.Before(created.Add(-time.Second)) {
			continue
		}
		r.AtMs = r.start.Sub(created).Milliseconds()
		out = append(out, r.TraceRequest)
	}
	return out
}

type reqIDKey struct{}

// traceRequestID returns the request-log id carried by r's context, or 0.
func traceRequestID(ctx context.Context) uint64 {
	id, _ := ctx.Value(reqIDKey{}).(uint64)
	return id
}

// traceWriter records a response's status, size and first-byte time. It
// forwards Flush and exposes the wrapped writer through Unwrap, so
// http.ResponseController (write deadlines) still works.
type traceWriter struct {
	http.ResponseWriter
	status    int
	bytes     int64
	firstByte time.Time
}

func (w *traceWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *traceWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.firstByte.IsZero() && len(b) > 0 {
		w.firstByte = time.Now()
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

func (w *traceWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *traceWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// traced runs h for a request under torrent ih and records it in the log.
func (s *server) traced(w http.ResponseWriter, r *http.Request, ih, class string, h func(http.ResponseWriter, *http.Request)) {
	id := s.reqs.nextID.Add(1)
	tw := &traceWriter{ResponseWriter: w}
	start := time.Now()
	h(tw, r.WithContext(context.WithValue(r.Context(), reqIDKey{}, id)))
	rec := reqRecord{start: start, TraceRequest: types.TraceRequest{
		ID:          id,
		Class:       class,
		Method:      r.Method,
		Range:       r.Header.Get("Range"),
		UserAgent:   r.UserAgent(),
		Status:      tw.status,
		FirstByteMs: -1,
		Bytes:       tw.bytes,
		DurationMs:  time.Since(start).Milliseconds(),
		End:         "done",
	}}
	if rec.Status == 0 {
		rec.Status = http.StatusOK
	}
	if !tw.firstByte.IsZero() {
		rec.FirstByteMs = tw.firstByte.Sub(start).Milliseconds()
	}
	switch {
	case r.Context().Err() != nil:
		rec.End = "client-closed"
	case rec.Status >= http.StatusBadRequest:
		rec.End = "error"
	}
	s.reqs.add(ih, rec)
}

// handleDebugTrace serves GET /debug/trace/:infoHash: the engine's trace plus
// the request log.
func (s *server) handleDebugTrace(w http.ResponseWriter, ih string) {
	tr, ok := s.em.Trace(ih)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "unknown torrent"})
		return
	}
	tr.Requests = s.reqs.get(ih, tr.Created)
	writeJSON(w, http.StatusOK, tr)
}

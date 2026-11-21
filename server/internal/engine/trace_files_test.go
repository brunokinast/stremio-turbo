// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package engine

import (
	"testing"
	"time"
)

// The trace knows a torrent's files once metadata is in, before any reader,
// so a preloaded or racing torrent's downloads are counted per file.
func TestTraceFilesSetOnMetadata(t *testing.T) {
	_, mi := newPayloadTorrent(t, t.TempDir(), 256<<10, 8)
	_, e := newTestEngine(t, 0, mi)
	if e.trace == nil {
		t.Skip("trace disabled")
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		var begins []int
		e.trace.with(e.infoHash, func(tt *torrentTrace) { begins = tt.fileBegins })
		if begins != nil {
			if len(begins) != 1 || begins[0] != 0 {
				t.Fatalf("file begins = %v, want [0]", begins)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("trace file begins not set 10 s after metadata, with no reader")
		}
	}
}

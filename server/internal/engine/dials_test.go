// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"errors"
	"fmt"
	"syscall"
	"testing"
	"time"

	"github.com/rivulet-kodi/rivulet-engine/internal/types"
)

func TestDialResult(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{nil, types.DialConnected},
		{context.DeadlineExceeded, types.DialTimeout},
		{fmt.Errorf("dial: %w", syscall.ECONNREFUSED), types.DialRefused},
		{fmt.Errorf("dial: %w", wsaECONNREFUSED), types.DialRefused},
		{errors.New("error during handshake: EOF"), types.DialHandshake},
		{errors.New("no route"), types.DialOther},
	} {
		if got := dialResult(tc.err); got != tc.want {
			t.Errorf("dialResult(%v) = %q; want %q", tc.err, got, tc.want)
		}
	}
}

func TestDialLogRingAndTimes(t *testing.T) {
	d := newDialLog()
	start := time.Unix(1000, 0)
	d.add("ih", start, types.DialRecord{Address: "ignored: torrent not begun"})
	if _, ok := d.get("ih"); ok {
		t.Fatal("unknown torrent must report !ok")
	}
	d.begin("ih", start)
	for i := range dialLogCap + 5 {
		d.add("ih", start.Add(time.Duration(i)*time.Millisecond), types.DialRecord{Address: fmt.Sprint(i)})
	}
	got, ok := d.get("ih")
	if !ok || len(got) != dialLogCap {
		t.Fatalf("len = %d ok = %v; want %d", len(got), ok, dialLogCap)
	}
	// Oldest first, with the 5 earliest dropped; times relative to begin.
	if got[0].Address != "5" || got[0].StartedMs != 5 || got[len(got)-1].Address != fmt.Sprint(dialLogCap+4) {
		t.Errorf("first %+v last %+v", got[0], got[len(got)-1])
	}
	d.forget("ih")
	if _, ok := d.get("ih"); ok {
		t.Error("forgotten torrent still logged")
	}
}

// Incoming connections are counted with the last one's time, and activeSince
// is the longest-loaded torrent's add time (zero once none is loaded).
func TestDialLogIncomingAndActiveSince(t *testing.T) {
	d := newDialLog()
	if d.incoming.Load() != 0 || !d.activeSince().IsZero() {
		t.Fatalf("fresh log: count %d, activeSince %v", d.incoming.Load(), d.activeSince())
	}
	t0 := time.Unix(1_800_000_000, 0)
	d.begin("aa", t0.Add(time.Minute))
	d.begin("bb", t0)
	if got := d.activeSince(); !got.Equal(t0) {
		t.Errorf("activeSince = %v; want the older torrent's %v", got, t0)
	}
	d.noteIncoming(t0.Add(2 * time.Minute))
	d.noteIncoming(t0.Add(3 * time.Minute))
	if d.incoming.Load() != 2 || d.lastIncoming.Load() != t0.Add(3*time.Minute).UnixMilli() {
		t.Errorf("count %d last %d; want 2 and the second's time", d.incoming.Load(), d.lastIncoming.Load())
	}
	d.forget("bb")
	if got := d.activeSince(); !got.Equal(t0.Add(time.Minute)) {
		t.Errorf("after forgetting the older: activeSince = %v", got)
	}
	d.forget("aa")
	if !d.activeSince().IsZero() {
		t.Errorf("no torrents: activeSince = %v; want zero", d.activeSince())
	}
}

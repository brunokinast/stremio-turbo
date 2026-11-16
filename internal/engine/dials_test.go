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

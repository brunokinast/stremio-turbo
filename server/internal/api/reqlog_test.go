// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package api

import (
	"testing"
	"time"

	"github.com/rivulet-kodi/rivulet-engine/internal/types"
)

// Requests made for an earlier engine of the same torrent (since removed) are
// not reported against the new one.
func TestReqLogSkipsRequestsBeforeTheEngine(t *testing.T) {
	l := newReqLog()
	created := time.Now()
	l.add(testIH, reqRecord{start: created.Add(-10 * time.Minute), TraceRequest: types.TraceRequest{ID: 1}})
	l.add(testIH, reqRecord{start: created.Add(-200 * time.Millisecond), TraceRequest: types.TraceRequest{ID: 2}})
	l.add(testIH, reqRecord{start: created.Add(3 * time.Second), TraceRequest: types.TraceRequest{ID: 3}})
	got := l.get(testIH, created)
	if len(got) != 2 || got[0].ID != 2 || got[1].ID != 3 || got[1].AtMs != 3000 {
		t.Fatalf("requests = %+v; want ids 2 and 3", got)
	}
}

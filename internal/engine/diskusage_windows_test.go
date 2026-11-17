// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

//go:build windows

package engine

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

// makeSparse marks f sparse, as the torrent storage does with its files on
// Windows; NTFS otherwise allocates (and zero-fills) every byte up to the end.
func makeSparse(t *testing.T, f *os.File) {
	t.Helper()
	var returned uint32
	if err := windows.DeviceIoControl(windows.Handle(f.Fd()), windows.FSCTL_SET_SPARSE, nil, 0, nil, 0, &returned, nil); err != nil {
		t.Skipf("filesystem does not support sparse files: %v", err)
	}
}

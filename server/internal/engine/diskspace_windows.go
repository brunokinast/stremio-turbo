// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package engine

import "golang.org/x/sys/windows"

// diskSpace returns the free (available to this user) and total bytes of the
// volume holding dir; 0, 0 when it can't be read.
func diskSpace(dir string) (free, total int64) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, 0
	}
	var avail, tot, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &avail, &tot, &totalFree); err != nil {
		return 0, 0
	}
	return int64(avail), int64(tot)
}

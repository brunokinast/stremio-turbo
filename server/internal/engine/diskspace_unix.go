// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

//go:build linux || darwin || freebsd

package engine

import "golang.org/x/sys/unix"

// diskSpace returns the free (available to this user) and total bytes of the
// volume holding dir; 0, 0 when it can't be read.
func diskSpace(dir string) (free, total int64) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, 0
	}
	return int64(st.Bavail) * int64(st.Bsize), int64(st.Blocks) * int64(st.Bsize)
}

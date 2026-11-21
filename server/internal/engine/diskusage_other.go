// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly || windows)

package engine

import "io/fs"

// allocatedSize falls back to the apparent size where the allocated block
// count is not available; see the unix
// variant for why the distinction matters to evict().
func allocatedSize(_ string, info fs.FileInfo) int64 {
	return info.Size()
}

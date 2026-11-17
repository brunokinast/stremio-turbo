// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

//go:build windows

package engine

import (
	"io/fs"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procGetCompressedFileSizeW = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetCompressedFileSizeW")

// allocatedSize reports the bytes a file occupies on disk: GetCompressedFileSize
// leaves out the holes of a sparse file (and counts compressed files at their
// compressed size). The torrent storage creates its files sparse on Windows, so
// a half-downloaded 10 GB episode whose tail was written has an apparent size
// of 10 GB while using a fraction of it; see the unix variant for why evict()
// must not charge the apparent size. NTFS counts newly written data once it is
// flushed (seconds; the storage syncs completed pieces about once a second), so
// the figure can lag the latest writes slightly. Capped at the apparent size;
// falls back to it when the call fails.
func allocatedSize(path string, info fs.FileInfo) int64 {
	size := info.Size()
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return size
	}
	var high uint32
	low, _, callErr := procGetCompressedFileSizeW.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&high)))
	if uint32(low) == 0xFFFFFFFF && callErr != windows.ERROR_SUCCESS {
		return size
	}
	if alloc := int64(high)<<32 | int64(uint32(low)); alloc < size {
		return alloc
	}
	return size
}

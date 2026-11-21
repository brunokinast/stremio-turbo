package storage

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"github.com/go-quicktest/qt"
	"golang.org/x/sys/windows"
)

var procGetCompressedFileSizeW = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetCompressedFileSizeW")

// The bytes the file occupies on disk (holes in a sparse file don't count).
func allocatedBytes(t *testing.T, path string) int64 {
	t.Helper()
	p, err := windows.UTF16PtrFromString(path)
	qt.Assert(t, qt.IsNil(err))
	var high uint32
	low, _, callErr := procGetCompressedFileSizeW.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&high)))
	if uint32(low) == 0xFFFFFFFF && callErr != windows.ERROR_SUCCESS {
		t.Fatal(callErr)
	}
	return int64(high)<<32 | int64(uint32(low))
}

// Cache files are sparse on Windows: a write far past the end leaves a hole rather than zero-filling
// (and allocating) everything before it.
func TestOpenFileExtraCreatesSparseFiles(t *testing.T) {
	dir := t.TempDir()
	const off = 1 << 30
	write := func(f *os.File) {
		_, err := f.WriteAt(make([]byte, 16<<10), off)
		qt.Assert(t, qt.IsNil(err))
		qt.Assert(t, qt.IsNil(f.Close()))
	}
	sparse := filepath.Join(dir, "sub", "sparse.bin") // also checks the directory is created
	f, err := openFileExtra(sparse, os.O_RDWR)
	qt.Assert(t, qt.IsNil(err))
	attrs, err := windows.GetFileAttributes(windows.StringToUTF16Ptr(sparse))
	qt.Assert(t, qt.IsNil(err))
	qt.Check(t, qt.Not(qt.Equals(attrs&windows.FILE_ATTRIBUTE_SPARSE_FILE, 0)))
	write(f)

	plain := filepath.Join(dir, "plain.bin")
	f, err = os.OpenFile(plain, os.O_CREATE|os.O_RDWR, 0o644)
	qt.Assert(t, qt.IsNil(err))
	write(f)

	qt.Check(t, qt.IsTrue(allocatedBytes(t, sparse) < 1<<20), qt.Commentf("sparse allocated %d", allocatedBytes(t, sparse)))
	qt.Check(t, qt.IsTrue(allocatedBytes(t, plain) >= off), qt.Commentf("plain allocated %d", allocatedBytes(t, plain)))
}

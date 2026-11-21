package storage

import (
	"os"

	"github.com/edsrzf/mmap-go"
	"golang.org/x/sys/windows"
)

func msync(mm mmap.MMap, offset, nbytes int) error {
	// Fuck you Windows you suck. TODO: Use windows.FlushViewOfFile.
	return mm.Flush()
}

// Marks the file sparse, so writing far past its end leaves a hole instead of zero-filling up to the
// write. Without it, the first write to the last piece of a new 10 GB file took 7.5 s, writing 10 GB
// of zeros, and other writes to the file waited for it, blocking every peer's read loop at the start
// of a stream. Unix filesystems leave holes by themselves. The attribute is idempotent and harmless on
// a file that is already written; filesystems without sparse support (FAT, exFAT, some shares) refuse
// it, and the file is used as before.
func setSparse(f *os.File) {
	var returned uint32
	_ = windows.DeviceIoControl(windows.Handle(f.Fd()), windows.FSCTL_SET_SPARSE, nil, 0, nil, 0, &returned, nil)
}

//go:build !windows

package storage

import "os"

// Unix filesystems leave holes for unwritten ranges by themselves. See the Windows variant.
func setSparse(*os.File) {}

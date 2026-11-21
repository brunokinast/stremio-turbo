package engine

import (
	"os"
	"path/filepath"
)

// writeFileAtomic replaces path with what write puts in a temporary file
// beside it (write gets that file's name), so a reader never sees a half
// written file. The temporary file is removed when anything fails.
func writeFileAtomic(path string, write func(tmp string) error) error {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := write(tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

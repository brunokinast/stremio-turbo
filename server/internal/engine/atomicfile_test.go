package engine

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := writeFileAtomic(path, func(tmp string) error { return os.WriteFile(tmp, []byte("one"), 0o600) }); err != nil {
		t.Fatal(err)
	}
	failed := errors.New("write failed")
	if err := writeFileAtomic(path, func(tmp string) error {
		_ = os.WriteFile(tmp, []byte("half"), 0o600)
		return failed
	}); !errors.Is(err, failed) {
		t.Fatalf("err = %v, want the write's error", err)
	}
	if raw, _ := os.ReadFile(path); string(raw) != "one" {
		t.Fatalf("file = %q after a failed write, want the old content", raw)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("%d files left, want only the target (no temporary file)", len(entries))
	}
}

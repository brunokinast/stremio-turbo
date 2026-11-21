// goexec runs the binaries `go test` and `go run` build, from one of a fixed set of paths.
//
// The Windows firewall approves programs by exact path, and go builds each test or run binary in a
// new random temp folder, so every run of anything that listens raised a new firewall prompt. Set as
// the go command's -exec program (go env -w GOFLAGS=-exec=<this exe>), goexec links or copies the
// binary to <slots>\slotNN.exe, a path with a standing allow rule (allow-testbins.ps1), and runs it
// there with the same arguments, working directory, environment and exit code.
//
// A slot is held through a lock on slotNN.lock for as long as the binary runs; the OS drops the lock
// if goexec dies, so a crash never leaks a slot. The slot's exe is deleted when the binary exits. Parallel package tests take different slots and
// wait when all are busy.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

// Slots is the number of fixed paths; allow-testbins.ps1 creates a rule for each.
const slots = 16

func slotDir() string {
	exe, err := os.Executable()
	if err != nil {
		panic(err)
	}
	// lab\tools\goexec.exe -> lab\testbin\slots
	return filepath.Join(filepath.Dir(filepath.Dir(exe)), "testbin", "slots")
}

// Takes a free slot: an exclusive, non-blocking lock on its lock file.
func acquire(dir string) (n int, lock *os.File) {
	for {
		for n = range slots {
			f, err := os.OpenFile(filepath.Join(dir, fmt.Sprintf("slot%02d.lock", n)), os.O_CREATE|os.O_RDWR, 0o644)
			if err != nil {
				continue
			}
			ol := new(windows.Overlapped)
			err = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
			if err == nil {
				return n, f
			}
			f.Close()
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func place(src, dst string) error {
	// The previous binary in this slot is not running (we hold the slot), so it can go.
	if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if os.Link(src, dst) == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: goexec <binary> [args...] (meant as go's -exec program)")
		os.Exit(2)
	}
	dir := slotDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "goexec:", err)
		os.Exit(2)
	}
	n, lock := acquire(dir)
	slot := filepath.Join(dir, fmt.Sprintf("slot%02d.exe", n))
	if err := place(os.Args[1], slot); err != nil {
		fmt.Fprintln(os.Stderr, "goexec: placing binary:", err)
		os.Exit(2)
	}
	cmd := exec.Command(slot, os.Args[2:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	// Leave nothing behind: the slot path keeps its firewall rule without the file.
	os.Remove(slot)
	lock.Close()
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		os.Exit(ee.ExitCode())
	default:
		fmt.Fprintln(os.Stderr, "goexec:", err)
		os.Exit(2)
	}
}

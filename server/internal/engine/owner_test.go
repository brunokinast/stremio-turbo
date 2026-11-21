// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package engine

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestOwnerHelperProcess is the app process the owner tests watch: it lives
// for RIVULET_OWNER_HELPER_LIFE, then exits.
func TestOwnerHelperProcess(t *testing.T) {
	if os.Getenv("RIVULET_OWNER_HELPER") != "1" {
		t.Skip("helper process")
	}
	d, _ := time.ParseDuration(os.Getenv("RIVULET_OWNER_HELPER_LIFE"))
	time.Sleep(d)
}

func startOwner(t *testing.T, life time.Duration) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestOwnerHelperProcess$")
	cmd.Env = append(os.Environ(), "RIVULET_OWNER_HELPER=1", "RIVULET_OWNER_HELPER_LIFE="+life.String())
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return cmd
}

func exitWhenDoneOn(m *manager) bool {
	m.dl.mu.Lock()
	defer m.dl.mu.Unlock()
	return m.dl.exitWhenDone
}

func TestOwnerExitDrainsServer(t *testing.T) {
	m, _ := newTestEngine(t, 0, newSeasonPackTorrent(t, 256<<10))
	owner := startOwner(t, 300*time.Millisecond)
	if err := m.SetOwner(owner.Process.Pid); err != nil {
		t.Fatal(err)
	}
	if exitWhenDoneOn(m) {
		t.Fatal("exit-when-done on while the app runs")
	}
	_ = owner.Wait()
	select {
	case <-m.Drained():
	case <-time.After(10 * time.Second):
		t.Fatal("not drained after the app exited (nothing downloading)")
	}
}

func TestNewOwnerKeepsServer(t *testing.T) {
	m, _ := newTestEngine(t, 0, newSeasonPackTorrent(t, 256<<10))
	first := startOwner(t, 300*time.Millisecond)
	if err := m.SetOwner(first.Process.Pid); err != nil {
		t.Fatal(err)
	}
	m.SetExitWhenDone(true) // as if the first app had exited during downloads
	second := startOwner(t, 10*time.Second)
	if err := m.SetOwner(second.Process.Pid); err != nil {
		t.Fatal(err)
	}
	if exitWhenDoneOn(m) {
		t.Fatal("a new owner left exit-when-done on")
	}
	_ = first.Wait()
	time.Sleep(1500 * time.Millisecond) // the replaced watcher's poll
	if exitWhenDoneOn(m) {
		t.Fatal("the replaced owner's exit turned exit-when-done on")
	}
	_ = second.Process.Kill()
	_ = second.Wait()
}

func TestOwnerUnknownProcess(t *testing.T) {
	m, _ := newTestEngine(t, 0, newSeasonPackTorrent(t, 256<<10))
	cmd := startOwner(t, 0)
	pid := cmd.Process.Pid
	_ = cmd.Wait()
	time.Sleep(100 * time.Millisecond)
	if err := m.SetOwner(pid); err == nil {
		t.Log("pid reused by the OS; not an error") // rare, but legal
	}
}

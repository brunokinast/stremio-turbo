// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package engine

import "github.com/rivulet-kodi/rivulet-engine/internal/logging"

// SetOwner ties the server's lifetime to the app process pid. When that
// process exits, exit-when-done turns on: the server exits at once when
// nothing is downloading or playing, or after the downloads finish. A new
// owner (the app started again while downloads finish) turns exit-when-done
// off. pid 0 clears the owner.
func (m *manager) SetOwner(pid int) error {
	var wait func(done <-chan struct{}) bool
	if pid != 0 {
		w, err := watchProcess(pid)
		if err != nil {
			return err
		}
		wait = w
	}
	m.ownerMu.Lock()
	m.ownerGen++
	gen := m.ownerGen
	m.ownerMu.Unlock()
	m.SetExitWhenDone(false)
	if wait == nil {
		return nil
	}
	go func() {
		if !wait(m.done) {
			return // server closing
		}
		m.ownerMu.Lock()
		current := m.ownerGen == gen
		m.ownerMu.Unlock()
		if current {
			logging.For("engine").Info("app exited; exiting when downloads are done", "pid", pid)
			m.SetExitWhenDone(true)
		}
	}()
	return nil
}

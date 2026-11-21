// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

//go:build !windows

package engine

import (
	"errors"
	"syscall"
	"time"
)

// watchProcess checks pid exists and returns a wait that reports true when it
// no longer does, false when done closes first.
func watchProcess(pid int) (func(done <-chan struct{}) bool, error) {
	if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		return nil, err
	}
	return func(done <-chan struct{}) bool {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return false
			case <-t.C:
				if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
					return true
				}
			}
		}
	}, nil
}

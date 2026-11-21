// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package engine

import "golang.org/x/sys/windows"

// watchProcess opens pid now (so a reused pid can't be mistaken for it) and
// returns a wait that reports true when the process exits, false when done
// closes first.
func watchProcess(pid int) (func(done <-chan struct{}) bool, error) {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return nil, err
	}
	return func(done <-chan struct{}) bool {
		defer windows.CloseHandle(h) //nolint:errcheck // nothing to do on failure
		for {
			ev, err := windows.WaitForSingleObject(h, 1000)
			if err != nil || ev == windows.WAIT_OBJECT_0 {
				return true
			}
			select {
			case <-done:
				return false
			default:
			}
		}
	}, nil
}

// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

package engine

import (
	"runtime"
	"sync"
	"syscall"
)

const (
	esSystemRequired = 0x00000001
	esContinuous     = 0x80000000
)

var (
	awakeOnce sync.Once
	awakeCh   chan bool
)

// systemKeepAwake keeps Windows from sleeping while on (ES_SYSTEM_REQUIRED;
// the display may still turn off and the session lock). The execution state
// belongs to the calling thread, so one goroutine locked to its OS thread
// holds it for the whole process.
func systemKeepAwake(on bool) {
	awakeOnce.Do(func() {
		awakeCh = make(chan bool, 1)
		proc := syscall.NewLazyDLL("kernel32.dll").NewProc("SetThreadExecutionState")
		go func() {
			runtime.LockOSThread()
			for on := range awakeCh {
				flags := uintptr(esContinuous)
				if on {
					flags |= esSystemRequired
				}
				_, _, _ = proc.Call(flags)
			}
		}()
	})
	awakeCh <- on
}

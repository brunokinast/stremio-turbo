// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

//go:build !windows && !linux && !darwin && !freebsd

package engine

// diskSpace is unknown on this platform: 0, 0.
func diskSpace(string) (free, total int64) { return 0, 0 }

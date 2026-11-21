// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

//go:build !windows

package engine

// systemKeepAwake is a no-op outside Windows.
func systemKeepAwake(bool) {}

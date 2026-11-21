// SPDX-FileCopyrightText: 2026 The rivulet-engine Authors
//
// SPDX-License-Identifier: MIT

//go:build !windows

package engine

import (
	"os"
	"testing"
)

// makeSparse is a no-op: unix filesystems leave holes by themselves.
func makeSparse(*testing.T, *os.File) {}

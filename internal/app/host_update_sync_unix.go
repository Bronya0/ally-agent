//go:build !windows

// SPDX-License-Identifier: GPL-3.0-only
package app

import "os"

func syncUpdateDirFile(f *os.File) error { return f.Sync() }

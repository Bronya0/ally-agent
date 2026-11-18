// SPDX-License-Identifier: GPL-3.0-only
//go:build !windows

package uikit

import "github.com/egoist/mygo"

// macOS keeps its menu-bar icon anchor; Linux uses the system-menu fallback.
func capturePosition() func(*mygo.Window) { return nil }

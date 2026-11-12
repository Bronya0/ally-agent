// SPDX-License-Identifier: GPL-3.0-only
//go:build !windows

package desktop

import "github.com/egoist/mygo"

// macOS keeps its menu-bar icon anchor; Linux uses the system-menu fallback.
func captureTrayMenuPosition() func(*mygo.Window) { return nil }

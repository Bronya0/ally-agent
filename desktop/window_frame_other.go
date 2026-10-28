// SPDX-License-Identifier: GPL-3.0-only
//go:build !windows

package desktop

import "github.com/egoist/mygo"

func configureWindowFrame(win *mygo.Window) {}

func showWindowAfterFirstFrame(win *mygo.Window) { win.Show() }

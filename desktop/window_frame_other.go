// SPDX-License-Identifier: GPL-3.0-only
//go:build !windows

package desktop

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func configureWindowFrame(win *mygo.Window, header ui.Color) {}

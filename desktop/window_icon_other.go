// SPDX-License-Identifier: GPL-3.0-only
//go:build !windows

package desktop

import (
	"runtime"

	"github.com/egoist/mygo"
)

func configureWindowIcon(win *mygo.Window, icon []byte) error {
	if err := win.SetIcon(icon); err != nil {
		return err
	}
	// Packaged macOS apps already have a multi-resolution ICNS. Do not replace
	// it with a single PNG; plain executable development still needs a Dock icon.
	if runtime.GOOS == "darwin" && !mygo.App.IsPackaged() {
		return mygo.App.Dock.SetIcon(icon)
	}
	return nil
}

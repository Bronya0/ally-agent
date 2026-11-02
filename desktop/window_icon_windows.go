// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"bytes"
	"image/png"
	"log"

	"github.com/egoist/mygo"
)

// MyGo accepts one PNG for both window icons and owns their native handles.
// Pre-filter at its large-icon size so the taskbar does not shrink the full source.
func configureWindowIcon(win *mygo.Window, data []byte) error {
	lastSize := 0
	apply := func() error {
		size := systemIconSize(win.NativeHandle(), smCxIcon, 32)
		if size == lastSize {
			return nil
		}
		src, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			return err
		}
		icon, err := encodePNG(fitSquare(src, size, 100))
		if err != nil {
			return err
		}
		if err := win.SetIcon(icon); err != nil {
			return err
		}
		lastSize = size
		return nil
	}
	if err := apply(); err != nil {
		return err
	}
	refresh := func() {
		if err := apply(); err != nil {
			log.Printf("window icon refresh failed: %v", err)
		}
	}
	offResize := win.OnResize(refresh)
	offMove := win.OnMove(refresh)
	offDisplays := mygo.Screen.OnDisplaysChanged(refresh)
	win.OnClosed(func() {
		offResize()
		offMove()
		offDisplays()
	})
	return nil
}

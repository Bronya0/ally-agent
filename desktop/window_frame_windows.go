// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"time"
	"unsafe"

	"github.com/egoist/mygo"
	"golang.org/x/sys/windows"
)

func configureWindowFrame(win *mygo.Window) {
	// Windows 11 draws a light DWM outline even with the title bar hidden.
	// COLOR_NONE suppresses only that outline, preserving native resizing,
	// rounded corners and the shadow. Older Windows ignore this attribute.
	const colorNone uint32 = 0xfffffffe
	color := colorNone
	_ = windows.DwmSetWindowAttribute(windows.HWND(win.NativeHandle()),
		windows.DWMWA_BORDER_COLOR, unsafe.Pointer(&color), uint32(unsafe.Sizeof(color)))
}

// Draw the native GPU surface while DWM keeps it off-screen. Hidden windows
// may not paint, so merely delaying Show cannot guarantee a painted first frame.
func showWindowAfterFirstFrame(win *mygo.Window) {
	const dwmwaCloak = 13
	setCloak := func(value uint32) {
		_ = windows.DwmSetWindowAttribute(windows.HWND(win.NativeHandle()), dwmwaCloak, unsafe.Pointer(&value), 4)
	}
	setCloak(1)
	revealed := false
	reveal := func() {
		if !revealed {
			revealed = true
			setCloak(0)
		}
	}
	win.OnReadyToShow(reveal)
	win.Show()
	// Retain visibility even on drivers that do not deliver a first-frame event.
	time.AfterFunc(500*time.Millisecond, func() { win.Update(reveal) })
}

// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
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

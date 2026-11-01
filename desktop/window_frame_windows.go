// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"unsafe"

	"github.com/egoist/mygo"
	"golang.org/x/sys/windows"
)

// configureWindowFrame suppresses the light DWM outline Windows 11 draws
// around a window whose title bar is hidden. COLOR_NONE takes only that
// outline away, keeping the native resize edges, rounded corners and the
// shadow; older Windows ignore the attribute. MyGo keeps the accent off the
// caption itself (plainCaption), and this is the outline it leaves behind.
func configureWindowFrame(win *mygo.Window) {
	const colorNone uint32 = 0xfffffffe
	color := colorNone
	_ = windows.DwmSetWindowAttribute(windows.HWND(win.NativeHandle()),
		windows.DWMWA_BORDER_COLOR, unsafe.Pointer(&color), uint32(unsafe.Sizeof(color)))
}

// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"unsafe"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"golang.org/x/sys/windows"
)

// configureWindowFrame suppresses the DWM outline and colors the one-pixel
// caption MyGo retains on Windows 11 to match the app header. Older Windows
// ignore the caption color attribute.
func configureWindowFrame(win *mygo.Window, header ui.Color) {
	const colorNone uint32 = 0xfffffffe
	borderColor := colorNone
	_ = windows.DwmSetWindowAttribute(windows.HWND(win.NativeHandle()),
		windows.DWMWA_BORDER_COLOR, unsafe.Pointer(&borderColor), uint32(unsafe.Sizeof(borderColor)))
	captionColor := uint32(header.R) | uint32(header.G)<<8 | uint32(header.B)<<16 // COLORREF is 0x00BBGGRR.
	_ = windows.DwmSetWindowAttribute(windows.HWND(win.NativeHandle()),
		windows.DWMWA_CAPTION_COLOR, unsafe.Pointer(&captionColor), uint32(unsafe.Sizeof(captionColor)))
}

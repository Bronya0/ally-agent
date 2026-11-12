// SPDX-License-Identifier: GPL-3.0-only
//go:build windows

package desktop

import (
	"ally-dev/desktop/uikit"
	"image"
	"unsafe"

	"github.com/egoist/mygo"
)

var (
	procTrayMenuGetCursorPos     = user32.NewProc("GetCursorPos")
	procTrayMenuGetWindowRect    = user32.NewProc("GetWindowRect")
	procTrayMenuMonitorFromPoint = user32.NewProc("MonitorFromPoint")
	procTrayMenuGetMonitorInfo   = user32.NewProc("GetMonitorInfoW")
	procTrayMenuSetWindowPos     = user32.NewProc("SetWindowPos")
)

type trayMenuPoint struct{ X, Y int32 }
type trayMenuRect struct{ Left, Top, Right, Bottom int32 }
type trayMenuMonitorInfo struct {
	Size          uint32
	Monitor, Work trayMenuRect
	Flags         uint32
}

// Capture once in the tray callback, before creating/laying out the flyout.
// Later pointer movement must not move the open menu or its FitContent anchor.
func captureTrayMenuPosition() func(*mygo.Window) {
	var at trayMenuPoint
	if r, ok := callProc(procTrayMenuGetCursorPos, uintptr(unsafe.Pointer(&at))); !ok || r == 0 {
		return nil // retain the platform icon anchor if coordinates are unavailable
	}
	return func(win *mygo.Window) {
		bindTrayMenuPosition(win, image.Pt(int(at.X), int(at.Y)))
	}
}

// Keep MyGo's tray flyout lifecycle, but override its icon-based placement.
// Native resize/move callbacks also run before a hidden FitContent flyout shows.
func bindTrayMenuPosition(win *mygo.Window, at image.Point) {
	placing := false
	place := func() {
		if placing || win.IsDestroyed() {
			return
		}
		placing = true
		defer func() { placing = false }()
		hwnd := win.NativeHandle()
		var rect trayMenuRect
		if r, ok := callProc(procTrayMenuGetWindowRect, hwnd, uintptr(unsafe.Pointer(&rect))); !ok || r == 0 {
			return
		}
		const monitorDefaultToNearest = 2
		packed := uintptr(uint64(uint32(at.X)) | uint64(uint32(at.Y))<<32)
		monitor, ok := callProc(procTrayMenuMonitorFromPoint, packed, monitorDefaultToNearest)
		if !ok || monitor == 0 {
			return
		}
		info := trayMenuMonitorInfo{Size: uint32(unsafe.Sizeof(trayMenuMonitorInfo{}))}
		if r, ok := callProc(procTrayMenuGetMonitorInfo, monitor, uintptr(unsafe.Pointer(&info))); !ok || r == 0 {
			return
		}
		dpi := 96
		if d, ok := callProc(procGetDpiForWindow, hwnd); ok && d >= 48 && d <= 960 {
			dpi = int(d)
		}
		inset := (int(uikit.TrayMenuShadowInset)*dpi + 48) / 96
		// A tray click is over the taskbar, outside rcWork. Use the monitor
		// bounds so the panel can actually touch that click, as a context menu.
		bounds := image.Rect(int(info.Monitor.Left), int(info.Monitor.Top), int(info.Monitor.Right), int(info.Monitor.Bottom))
		size := image.Pt(int(rect.Right-rect.Left), int(rect.Bottom-rect.Top))
		origin := trayMenuPosition(at, size, bounds, inset)
		if origin.X == int(rect.Left) && origin.Y == int(rect.Top) {
			return
		}
		const swpNoSize, swpNoZOrder, swpNoActivate = 0x0001, 0x0004, 0x0010
		callProc(procTrayMenuSetWindowPos, hwnd, 0, uintptr(origin.X), uintptr(origin.Y), 0, 0, swpNoSize|swpNoZOrder|swpNoActivate)
	}
	offMove := win.OnMove(place)
	offResize := win.OnResize(place)
	win.OnClosed(func() {
		offMove()
		offResize()
	})
	place()
}

// All coordinates are physical pixels from the same monitor. Position the
// visible panel, not the transparent shadow footprint, against the click.
func trayMenuPosition(at, size image.Point, bounds image.Rectangle, inset int) image.Point {
	x, y := at.X-inset, at.Y-size.Y+inset
	if x+size.X > bounds.Max.X {
		if left := at.X - size.X + inset; left >= bounds.Min.X {
			x = left // not enough room to the right: align the panel's right edge
		}
	}
	if y < bounds.Min.Y {
		if below := at.Y - inset; below+size.Y <= bounds.Max.Y {
			y = below // not enough room above: align the panel's top edge
		}
	}
	x = min(max(x, bounds.Min.X), max(bounds.Min.X, bounds.Max.X-size.X))
	y = min(max(y, bounds.Min.Y), max(bounds.Min.Y, bounds.Max.Y-size.Y))
	return image.Pt(x, y)
}

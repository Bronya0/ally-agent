// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
//go:build windows

package screenshot

import (
	"errors"
	"fmt"
	"image"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Win32 surface for screen capture. Everything goes through direct syscalls:
// no PowerShell child process (antivirus/EDR friendly), no cgo.
var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	dwmapi   = windows.NewLazySystemDLL("dwmapi.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procGetDC                      = user32.NewProc("GetDC")
	procReleaseDC                  = user32.NewProc("ReleaseDC")
	procCreateCompatibleDC         = gdi32.NewProc("CreateCompatibleDC")
	procDeleteDC                   = gdi32.NewProc("DeleteDC")
	procCreateDIBSection           = gdi32.NewProc("CreateDIBSection")
	procDeleteObject               = gdi32.NewProc("DeleteObject")
	procSelectObject               = gdi32.NewProc("SelectObject")
	procBitBlt                     = gdi32.NewProc("BitBlt")
	procEnumWindows                = user32.NewProc("EnumWindows")
	procEnumDisplayMonitors        = user32.NewProc("EnumDisplayMonitors")
	procIsWindowVisible            = user32.NewProc("IsWindowVisible")
	procIsIconic                   = user32.NewProc("IsIconic")
	procShowWindow                 = user32.NewProc("ShowWindow")
	procSetForegroundWindow        = user32.NewProc("SetForegroundWindow")
	procGetForegroundWindow        = user32.NewProc("GetForegroundWindow")
	procGetWindowTextLengthW       = user32.NewProc("GetWindowTextLengthW")
	procGetWindowTextW             = user32.NewProc("GetWindowTextW")
	procGetWindowRect              = user32.NewProc("GetWindowRect")
	procGetWindowThreadProcessId   = user32.NewProc("GetWindowThreadProcessId")
	procGetSystemMetrics           = user32.NewProc("GetSystemMetrics")
	procSetThreadDpiAwareness      = user32.NewProc("SetThreadDpiAwarenessContext")
	procDwmGetWindowAttribute      = dwmapi.NewProc("DwmGetWindowAttribute")
	procOpenProcess                = kernel32.NewProc("OpenProcess")
	procQueryFullProcessImageNameW = kernel32.NewProc("QueryFullProcessImageNameW")
)

const (
	srccopy                  = 0x00CC0020
	dwmwaExtendedFrameBounds = 9
	dwmwaCloaked             = 14
	processQueryLimitedInfo  = 0x1000
	swRestore                = 9
	// SM_XVIRTUALSCREEN / SM_YVIRTUALSCREEN / SM_CXVIRTUALSCREEN / SM_CYVIRTUALSCREEN
	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCxVirtualScreen = 78
	smCyVirtualScreen = 79
	// (DPI_AWARENESS_CONTEXT)-4: per-monitor-v2 thread awareness, so
	// GetWindowRect / DWM bounds come back in physical pixels regardless of
	// the process-wide awareness the WebView host sets.
	dpiAwarenessPerMonitorV2 = ^uintptr(3)
)

type winRect struct {
	Left, Top, Right, Bottom int32
}

func winRectToRect(r winRect) Rect {
	return Rect{X: int(r.Left), Y: int(r.Top), Width: int(r.Right - r.Left), Height: int(r.Bottom - r.Top)}
}

// setThreadDpiAware pins the calling thread to per-monitor-v2 awareness for
// the duration of fn and restores the previous context afterwards, so pooled
// OS threads keep their state even when fn panics. On Windows releases where
// the API is absent the body still runs; coordinates then follow whatever
// process awareness is in effect.
func setThreadDpiAware(fn func()) {
	if err := procSetThreadDpiAwareness.Find(); err == nil {
		prev, _, _ := procSetThreadDpiAwareness.Call(dpiAwarenessPerMonitorV2)
		defer procSetThreadDpiAwareness.Call(prev)
	}
	fn()
}

// listWindows enumerates visible, uncloaked, titled top-level windows in
// z-order (front to back). The focused flag is exact (GetForegroundWindow).
func listWindows() ([]Window, error) {
	var (
		windows []Window
		enumErr error
	)
	setThreadDpiAware(func() {
		var foreground uintptr
		if hwnd, _, _ := procGetForegroundWindow.Call(); hwnd != 0 {
			foreground = hwnd
		}
		cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
			if r1, _, _ := procIsWindowVisible.Call(hwnd); r1 == 0 {
				return 1
			}
			// UWP apps keep phantom "visible" windows behind their shell
			// host; the cloak attribute is the documented way to skip them.
			if cloaked, ok := dwmAttributeUint32(hwnd, dwmwaCloaked); ok && cloaked != 0 {
				return 1
			}
			title := windowTitle(hwnd)
			if title == "" {
				return 1
			}
			rect, ok := windowBounds(hwnd)
			if !ok || rect.Width <= 0 || rect.Height <= 0 {
				return 1
			}
			windows = append(windows, Window{
				ID:      int(hwnd),
				Title:   SanitizeTitle(title),
				Process: windowProcessName(hwnd),
				X:       rect.X,
				Y:       rect.Y,
				Width:   rect.Width,
				Height:  rect.Height,
				Focused: hwnd == foreground,
			})
			return 1
		})
		if r1, _, callErr := procEnumWindows.Call(cb, 0); r1 == 0 {
			enumErr = fmt.Errorf("%w: EnumWindows failed: %v", ErrCaptureFailed, callErr)
		}
	})
	if enumErr != nil {
		return nil, enumErr
	}
	return windows, nil
}

func windowTitle(hwnd uintptr) string {
	length, _, _ := procGetWindowTextLengthW.Call(hwnd)
	if length == 0 {
		return ""
	}
	buf := make([]uint16, length+1)
	procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(length+1))
	return windows.UTF16ToString(buf)
}

// windowBounds prefers DWM's extended frame bounds, which exclude the
// invisible resize borders GetWindowRect includes on Windows 10+.
func windowBounds(hwnd uintptr) (Rect, bool) {
	if rect, ok := dwmWindowRect(hwnd); ok {
		return rect, true
	}
	var r winRect
	if r1, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); r1 == 0 {
		return Rect{}, false
	}
	return winRectToRect(r), true
}

func dwmWindowRect(hwnd uintptr) (Rect, bool) {
	if err := procDwmGetWindowAttribute.Find(); err != nil {
		return Rect{}, false
	}
	var r winRect
	if r1, _, _ := procDwmGetWindowAttribute.Call(hwnd, dwmwaExtendedFrameBounds, uintptr(unsafe.Pointer(&r)), unsafe.Sizeof(r)); r1 != 0 {
		return Rect{}, false
	}
	return winRectToRect(r), true
}

func dwmAttributeUint32(hwnd uintptr, attr uint32) (uint32, bool) {
	if err := procDwmGetWindowAttribute.Find(); err != nil {
		return 0, false
	}
	var value uint32
	if r1, _, _ := procDwmGetWindowAttribute.Call(hwnd, uintptr(attr), uintptr(unsafe.Pointer(&value)), unsafe.Sizeof(value)); r1 != 0 {
		return 0, false
	}
	return value, true
}

// windowProcessName resolves the owning process image name. Access-denied on
// elevated/system processes degrades to an empty name, never an error.
func windowProcessName(hwnd uintptr) string {
	var pid uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return ""
	}
	handle, _, _ := procOpenProcess.Call(processQueryLimitedInfo, 0, uintptr(pid))
	if handle == 0 {
		return ""
	}
	defer windows.CloseHandle(windows.Handle(handle))
	buf := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buf))
	if r1, _, _ := procQueryFullProcessImageNameW.Call(handle, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size))); r1 == 0 {
		return ""
	}
	return filepath.Base(windows.UTF16ToString(buf[:size]))
}

// captureWindow grabs the window's on-screen rectangle. Without focus the
// capture shows whatever is visibly on top (the surveyed tools do the same
// and document it); with focus the window is restored and raised first.
func captureWindow(win Window, focus bool) (image.Image, error) {
	hwnd := uintptr(win.ID)
	var (
		frame      image.Image
		captureErr error
	)
	setThreadDpiAware(func() {
		if r1, _, _ := procIsWindowVisible.Call(hwnd); r1 == 0 {
			captureErr = fmt.Errorf("%w: window %d is no longer visible", ErrWindowNotFound, win.ID)
			return
		}
		r1Iconic, _, _ := procIsIconic.Call(hwnd)
		minimized := r1Iconic != 0
		if minimized && !focus {
			captureErr = errors.New("window is minimized; retry with focus=true to restore it")
			return
		}
		if focus {
			if minimized {
				procShowWindow.Call(hwnd, swRestore)
			}
			procSetForegroundWindow.Call(hwnd)
			time.Sleep(focusSettleDelay)
		}
		rect, ok := windowBounds(hwnd)
		if !ok || rect.Width <= 0 || rect.Height <= 0 {
			captureErr = fmt.Errorf("%w: window rect unavailable", ErrCaptureFailed)
			return
		}
		frame, captureErr = bitBltScreen(rect)
	})
	return frame, captureErr
}

func captureRegion(region Rect) (image.Image, error) {
	var (
		frame      image.Image
		captureErr error
	)
	setThreadDpiAware(func() {
		if !virtualScreenContains(region) {
			captureErr = fmt.Errorf("%w: region %v is outside the virtual screen", ErrBadRegion, region)
			return
		}
		frame, captureErr = bitBltScreen(region)
	})
	return frame, captureErr
}

func captureDisplay(display int) (image.Image, error) {
	var (
		frame      image.Image
		captureErr error
	)
	setThreadDpiAware(func() {
		monitors := enumDisplayRects()
		if len(monitors) == 0 {
			// Fall back to the raw virtual-screen metrics when monitor
			// enumeration fails (never observed, but the GDI path needs a rect).
			x, _, _ := procGetSystemMetrics.Call(smXVirtualScreen)
			y, _, _ := procGetSystemMetrics.Call(smYVirtualScreen)
			cx, _, _ := procGetSystemMetrics.Call(smCxVirtualScreen)
			cy, _, _ := procGetSystemMetrics.Call(smCyVirtualScreen)
			monitors = []Rect{{X: int(int32(x)), Y: int(int32(y)), Width: int(int32(cx)), Height: int(int32(cy))}}
		}
		if display < 0 || display >= len(monitors) {
			captureErr = fmt.Errorf("%w: display %d of %d available", ErrDisplayNotFound, display, len(monitors))
			return
		}
		frame, captureErr = bitBltScreen(monitors[display])
	})
	return frame, captureErr
}

func virtualScreenContains(r Rect) bool {
	x, _, _ := procGetSystemMetrics.Call(smXVirtualScreen)
	y, _, _ := procGetSystemMetrics.Call(smYVirtualScreen)
	cx, _, _ := procGetSystemMetrics.Call(smCxVirtualScreen)
	cy, _, _ := procGetSystemMetrics.Call(smCyVirtualScreen)
	vs := Rect{X: int(int32(x)), Y: int(int32(y)), Width: int(int32(cx)), Height: int(int32(cy))}
	return r.X >= vs.X && r.Y >= vs.Y && r.X+r.Width <= vs.X+vs.Width && r.Y+r.Height <= vs.Y+vs.Height
}

func enumDisplayRects() []Rect {
	var rects []Rect
	// MonitorEnumProc takes (HMONITOR, HDC, LPRECT, LPARAM); syscall.NewCallback
	// accepts pointer-sized Go pointer parameters, so the rect arrives typed —
	// no uintptr->pointer conversion. Arity must match exactly or the callback
	// is never invoked.
	cb := syscall.NewCallback(func(_, _ uintptr, r *winRect, _ uintptr) uintptr {
		if r == nil {
			return 0
		}
		rects = append(rects, winRectToRect(*r))
		return 1
	})
	procEnumDisplayMonitors.Call(0, 0, cb, 0)
	return rects
}

// bitBltScreen grabs one screen-space rectangle via GDI into a DIB section.
// The screen DC spans the whole virtual screen, so secondary monitors with
// negative coordinates work without special casing. A DIB section (rather
// than CreateCompatibleBitmap + GetDIBits) means the pixels land directly in
// memory we own — one copy, no size-dependent GetDIBits quirks. Alpha from
// the screen DC is undefined and the bytes come back BGRA; both are
// normalized here.
func bitBltScreen(rect Rect) (image.Image, error) {
	w, h := rect.Width, rect.Height
	if w <= 0 || h <= 0 || w > MaxRegionWidthPx || h > MaxRegionHeightPx {
		return nil, fmt.Errorf("%w: %dx%d outside capture limits", ErrBadRegion, w, h)
	}
	hdcSrc, _, _ := procGetDC.Call(0)
	if hdcSrc == 0 {
		return nil, fmt.Errorf("%w: GetDC(screen) failed", ErrCaptureFailed)
	}
	defer procReleaseDC.Call(0, hdcSrc)
	hdcMem, _, _ := procCreateCompatibleDC.Call(hdcSrc)
	if hdcMem == 0 {
		return nil, fmt.Errorf("%w: CreateCompatibleDC failed", ErrCaptureFailed)
	}
	defer procDeleteDC.Call(hdcMem)
	bmi := bitmapInfoHeader{
		Size:     40,
		Width:    int32(w),
		Height:   -int32(h), // negative = top-down rows
		Planes:   1,
		BitCount: 32,
	}
	// Received as unsafe.Pointer from the start: no uintptr->pointer
	// conversion anywhere, and the DIB memory is valid until DeleteObject.
	var bits unsafe.Pointer
	hbm, _, _ := procCreateDIBSection.Call(hdcSrc, uintptr(unsafe.Pointer(&bmi)), 0 /* DIB_RGB_COLORS */, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if hbm == 0 || bits == nil {
		return nil, fmt.Errorf("%w: CreateDIBSection(%d,%d) failed", ErrCaptureFailed, w, h)
	}
	defer procDeleteObject.Call(hbm)
	prev, _, _ := procSelectObject.Call(hdcMem, hbm)
	r1, _, _ := procBitBlt.Call(hdcMem, 0, 0, uintptr(w), uintptr(h), hdcSrc, uintptr(rect.X), uintptr(rect.Y), srccopy)
	procSelectObject.Call(hdcMem, prev)
	if r1 == 0 {
		return nil, fmt.Errorf("%w: BitBlt failed", ErrCaptureFailed)
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	src := unsafe.Slice((*byte)(bits), 4*w*h)
	for s, d := 0, 0; s < len(src); s, d = s+4, d+4 {
		img.Pix[d+0] = src[s+2]
		img.Pix[d+1] = src[s+1]
		img.Pix[d+2] = src[s+0]
		img.Pix[d+3] = 0xFF
	}
	return img, nil
}

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

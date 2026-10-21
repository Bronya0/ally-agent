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
	"context"
	"errors"
	"fmt"
	"image"
	"path/filepath"
	"runtime"
	"syscall"
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
	procPrintWindow                = user32.NewProc("PrintWindow")
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
	// PW_RENDERFULLCONTENT (Windows 8.1+): have DWM render the full window
	// content. Without it, DirectComposition-backed windows (WebView2,
	// Chrome, Electron, UWP) come back as an empty frame.
	pwRenderFullContent = 0x00000002
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
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := procSetThreadDpiAwareness.Find(); err == nil {
		prev, _, _ := procSetThreadDpiAwareness.Call(dpiAwarenessPerMonitorV2)
		if prev != 0 {
			defer procSetThreadDpiAwareness.Call(prev)
		}
	}
	fn()
}

// listWindows enumerates visible, uncloaked, titled top-level windows in
// z-order (front to back). The focused flag is exact (GetForegroundWindow).
type windowEnumeration struct {
	ctx        context.Context
	windows    []Window
	foreground uintptr
}

// Windows callbacks are process-lifetime allocations. Keep exactly one per
// callback signature; each synchronous enumeration carries its own state.
var enumWindowsCallback = syscall.NewCallback(collectWindow)

func collectWindow(hwnd uintptr, state *windowEnumeration) uintptr {
	if state == nil || state.ctx.Err() != nil {
		return 0
	}
	if r1, _, _ := procIsWindowVisible.Call(hwnd); r1 == 0 {
		return 1
	}
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
	state.windows = append(state.windows, Window{
		ID: int(hwnd), Title: SanitizeTitle(title), Process: windowProcessName(hwnd),
		X: rect.X, Y: rect.Y, Width: rect.Width, Height: rect.Height,
		Focused: hwnd == state.foreground,
	})
	return 1
}

func listWindows(ctx context.Context) ([]Window, error) {
	var (
		enumErr error
	)
	state := &windowEnumeration{ctx: ctx}
	setThreadDpiAware(func() {
		if hwnd, _, _ := procGetForegroundWindow.Call(); hwnd != 0 {
			state.foreground = hwnd
		}
		if r1, _, callErr := procEnumWindows.Call(enumWindowsCallback, uintptr(unsafe.Pointer(state))); r1 == 0 {
			enumErr = fmt.Errorf("%w: EnumWindows failed: %v", ErrCaptureFailed, callErr)
		}
	})
	runtime.KeepAlive(state)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if enumErr != nil {
		return nil, enumErr
	}
	return state.windows, nil
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

// captureWindow grabs the window's own content through PrintWindow, so a
// window covered by others still yields its own pixels — the contract the
// macOS (-l) and Linux (maim -i) paths already honour. focus restores a
// minimized window and raises it first; occlusion alone does not need it.
func captureWindow(ctx context.Context, win *Window, focus bool) (image.Image, error) {
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
			if err := waitForFocus(ctx); err != nil {
				captureErr = err
				return
			}
		}
		rect, ok := windowBounds(hwnd)
		if !ok || rect.Width <= 0 || rect.Height <= 0 {
			captureErr = fmt.Errorf("%w: window rect unavailable", ErrCaptureFailed)
			return
		}
		// Restoring/focusing may move or resize the window after enumeration.
		win.X, win.Y, win.Width, win.Height = rect.X, rect.Y, rect.Width, rect.Height
		frame, captureErr = printWindowFrame(hwnd, rect)
		if captureErr == nil {
			return
		}
		// A window may refuse to render itself (protected video, some GPU
		// surfaces). Screen pixels are an honest fallback only while the
		// window is in front; behind another window the rectangle holds the
		// occluder, and a silently wrong frame is worse than a coded failure.
		if fg, _, _ := procGetForegroundWindow.Call(); fg == hwnd {
			frame, captureErr = bitBltScreen(rect)
		}
	})
	return frame, captureErr
}

func captureRegion(ctx context.Context, region Rect) (image.Image, error) {
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

func captureDisplay(ctx context.Context, display int) (image.Image, error) {
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

type monitorEnumeration struct{ rects []Rect }

var enumMonitorsCallback = syscall.NewCallback(func(_, _ uintptr, r *winRect, state *monitorEnumeration) uintptr {
	if r == nil || state == nil {
		return 0
	}
	state.rects = append(state.rects, winRectToRect(*r))
	return 1
})

func enumDisplayRects() []Rect {
	state := &monitorEnumeration{}
	procEnumDisplayMonitors.Call(0, 0, enumMonitorsCallback, uintptr(unsafe.Pointer(state)))
	runtime.KeepAlive(state)
	return state.rects
}

// dibFrame is one top-down 32-bit DIB section selected into a fresh memory
// DC: the shared canvas for both capture paths. A DIB section (rather than
// CreateCompatibleBitmap + GetDIBits) means the pixels land directly in
// memory we own — one copy, no size-dependent GetDIBits quirks.
type dibFrame struct {
	hdcMem uintptr
	hbm    uintptr
	prev   uintptr
	bits   unsafe.Pointer
	width  int
	height int
}

func newDibFrame(w, h int) (dibFrame, error) {
	if w <= 0 || h <= 0 || w > MaxRegionWidthPx || h > MaxRegionHeightPx {
		return dibFrame{}, fmt.Errorf("%w: %dx%d outside capture limits", ErrBadRegion, w, h)
	}
	hdcSrc, _, _ := procGetDC.Call(0)
	if hdcSrc == 0 {
		return dibFrame{}, fmt.Errorf("%w: GetDC(screen) failed", ErrCaptureFailed)
	}
	defer procReleaseDC.Call(0, hdcSrc)
	hdcMem, _, _ := procCreateCompatibleDC.Call(hdcSrc)
	if hdcMem == 0 {
		return dibFrame{}, fmt.Errorf("%w: CreateCompatibleDC failed", ErrCaptureFailed)
	}
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
		procDeleteDC.Call(hdcMem)
		return dibFrame{}, fmt.Errorf("%w: CreateDIBSection(%d,%d) failed", ErrCaptureFailed, w, h)
	}
	prev, _, _ := procSelectObject.Call(hdcMem, hbm)
	return dibFrame{hdcMem: hdcMem, hbm: hbm, prev: prev, bits: bits, width: w, height: h}, nil
}

func (d dibFrame) release() {
	procSelectObject.Call(d.hdcMem, d.prev)
	procDeleteObject.Call(d.hbm)
	procDeleteDC.Call(d.hdcMem)
}

// image converts the DIB pixels into an NRGBA frame. Alpha from the screen DC
// and from PrintWindow is undefined and the bytes come back BGRA; both are
// normalized here.
func (d dibFrame) image() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, d.width, d.height))
	src := unsafe.Slice((*byte)(d.bits), 4*d.width*d.height)
	for s, dst := 0, 0; s < len(src); s, dst = s+4, dst+4 {
		img.Pix[dst+0] = src[s+2]
		img.Pix[dst+1] = src[s+1]
		img.Pix[dst+2] = src[s+0]
		img.Pix[dst+3] = 0xFF
	}
	return img
}

// bitBltScreen grabs one screen-space rectangle via GDI. The screen DC spans
// the whole virtual screen, so secondary monitors with negative coordinates
// work without special casing.
func bitBltScreen(rect Rect) (image.Image, error) {
	frame, err := newDibFrame(rect.Width, rect.Height)
	if err != nil {
		return nil, err
	}
	defer frame.release()
	hdcSrc, _, _ := procGetDC.Call(0)
	if hdcSrc == 0 {
		return nil, fmt.Errorf("%w: GetDC(screen) failed", ErrCaptureFailed)
	}
	defer procReleaseDC.Call(0, hdcSrc)
	r1, _, _ := procBitBlt.Call(frame.hdcMem, 0, 0, uintptr(rect.Width), uintptr(rect.Height), hdcSrc, uintptr(rect.X), uintptr(rect.Y), srccopy)
	if r1 == 0 {
		return nil, fmt.Errorf("%w: BitBlt failed", ErrCaptureFailed)
	}
	return frame.image(), nil
}

// printWindowFrame renders the window's own content into an offscreen DIB and
// crops it to the visible frame. The canvas is sized to the full window rect
// (what PrintWindow paints into); GetWindowRect includes the invisible resize
// borders that the DWM frame bounds exclude, so the crop lands the image on
// exactly the rectangle action=list reports. PrintWindow is synchronous — it
// waits for the target to process WM_PRINT. This runs only in the killable
// window-capture helper, never in the main Ally process.
func printWindowFrame(hwnd uintptr, rect Rect) (image.Image, error) {
	var full winRect
	if r1, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&full))); r1 == 0 {
		return nil, fmt.Errorf("%w: GetWindowRect failed", ErrCaptureFailed)
	}
	frame, err := newDibFrame(int(full.Right-full.Left), int(full.Bottom-full.Top))
	if err != nil {
		return nil, err
	}
	defer frame.release()
	if r1, _, _ := procPrintWindow.Call(hwnd, frame.hdcMem, pwRenderFullContent); r1 == 0 {
		return nil, fmt.Errorf("%w: PrintWindow refused to render window %d", ErrCaptureFailed, hwnd)
	}
	img := frame.image()
	crop := image.Rect(rect.X-int(full.Left), rect.Y-int(full.Top),
		rect.X-int(full.Left)+rect.Width, rect.Y-int(full.Top)+rect.Height)
	if crop.In(img.Bounds()) {
		return img.SubImage(crop), nil
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

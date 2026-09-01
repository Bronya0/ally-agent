// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

//go:build windows

package app

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	taskbarStateNoProgress    = 0
	taskbarStateIndeterminate = 1
	clsctxInprocServer        = 0x1
	coinitApartmentThreaded   = 0x2
	flashwTray                = 0x2
	// flashCount limits the flash to a bounded number of iterations instead
	// of FLASHW_TIMERNOFG's "flash until foregrounded" semantics, which keeps
	// a pending flash latched on the window forever — if the window is later
	// re-shown or the taskbar button is rebuilt (tray exit/restore, Explorer
	// refresh), the stale flash resumes out of nowhere.
	flashCount = 8
)

var (
	modOle32   = windows.NewLazySystemDLL("ole32.dll")
	modShell32 = windows.NewLazySystemDLL("shell32.dll")
	modUser32  = windows.NewLazySystemDLL("user32.dll")

	procCoInitializeEx   = modOle32.NewProc("CoInitializeEx")
	procCoUninitialize   = modOle32.NewProc("CoUninitialize")
	procCoCreateInstance = modOle32.NewProc("CoCreateInstance")
	procFindWindowW      = modUser32.NewProc("FindWindowW")
	procGetForegroundWnd = modUser32.NewProc("GetForegroundWindow")
	procFlashWindowEx    = modUser32.NewProc("FlashWindowEx")
)

var (
	clsidTaskbarList = windows.GUID{Data1: 0x56fdf344, Data2: 0xfd6d, Data3: 0x11d0, Data4: [8]byte{0x95, 0x8a, 0x00, 0x60, 0x97, 0xc9, 0xa0, 0x90}}
	iidTaskbarList3  = windows.GUID{Data1: 0xea1afb91, Data2: 0x9e28, Data3: 0x4b86, Data4: [8]byte{0x90, 0xe9, 0x9e, 0x9f, 0x8a, 0x5e, 0xeb, 0xfb}}
)

type flashWindowInfo struct {
	Size    uint32
	Window  uintptr
	Flags   uint32
	Count   uint32
	Timeout uint32
}

type taskbarList3 struct {
	lpVtbl *taskbarList3Vtbl
}

type taskbarList3Vtbl struct {
	QueryInterface        uintptr
	AddRef                uintptr
	Release               uintptr
	HrInit                uintptr
	AddTab                uintptr
	DeleteTab             uintptr
	ActivateTab           uintptr
	SetActiveAlt          uintptr
	MarkFullscreenWindow  uintptr
	SetProgressValue      uintptr
	SetProgressState      uintptr
	RegisterTab           uintptr
	UnregisterTab         uintptr
	SetTabOrder           uintptr
	SetTabActive          uintptr
	ThumbBarAddButtons    uintptr
	ThumbBarUpdateButtons uintptr
	ThumbBarSetImageList  uintptr
	SetOverlayIcon        uintptr
	SetThumbnailTooltip   uintptr
	SetThumbnailClip      uintptr
}

func findMainWindowHandle() uintptr {
	className, err := windows.UTF16PtrFromString(WindowsWindowClassName)
	if err == nil {
		if hwnd, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(className)), 0); hwnd != 0 {
			return hwnd
		}
	}
	title, err := windows.UTF16PtrFromString(appName)
	if err != nil {
		return 0
	}
	hwnd, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(title)))
	return hwnd
}

func setTaskbarProgressState(state uint32) {
	hwnd := findMainWindowHandle()
	if hwnd == 0 {
		return
	}

	initialized := false
	if hr, _, _ := procCoInitializeEx.Call(0, coinitApartmentThreaded); hr == 0 || hr == 1 {
		initialized = true
	}
	if initialized {
		defer procCoUninitialize.Call()
	}

	var taskbar *taskbarList3
	hr, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidTaskbarList)),
		0,
		clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidTaskbarList3)),
		uintptr(unsafe.Pointer(&taskbar)),
	)
	if hr != 0 || taskbar == nil || taskbar.lpVtbl == nil {
		return
	}
	defer syscall.SyscallN(taskbar.lpVtbl.Release, uintptr(unsafe.Pointer(taskbar)))

	if hr, _, _ := syscall.SyscallN(taskbar.lpVtbl.HrInit, uintptr(unsafe.Pointer(taskbar))); hr != 0 {
		return
	}
	syscall.SyscallN(taskbar.lpVtbl.SetProgressState, uintptr(unsafe.Pointer(taskbar)), hwnd, uintptr(state))
}

func setTaskbarRunningProgress() {
	setTaskbarProgressState(taskbarStateIndeterminate)
}

func clearTaskbarProgress() {
	setTaskbarProgressState(taskbarStateNoProgress)
}

func flashTaskbarWindowIfInactive() {
	hwnd := findMainWindowHandle()
	if hwnd == 0 {
		return
	}
	foreground, _, _ := procGetForegroundWnd.Call()
	if foreground == hwnd {
		return
	}

	info := flashWindowInfo{
		Window: hwnd,
		Flags:  flashwTray,
		Count:  flashCount,
	}
	info.Size = uint32(unsafe.Sizeof(info))
	procFlashWindowEx.Call(uintptr(unsafe.Pointer(&info)))
}

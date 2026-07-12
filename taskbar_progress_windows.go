//go:build windows

package main

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
)

var (
	modOle32   = windows.NewLazySystemDLL("ole32.dll")
	modShell32 = windows.NewLazySystemDLL("shell32.dll")
	modUser32  = windows.NewLazySystemDLL("user32.dll")

	procCoInitializeEx   = modOle32.NewProc("CoInitializeEx")
	procCoUninitialize   = modOle32.NewProc("CoUninitialize")
	procCoCreateInstance = modOle32.NewProc("CoCreateInstance")
	procFindWindowW      = modUser32.NewProc("FindWindowW")
)

var (
	clsidTaskbarList = windows.GUID{Data1: 0x56fdf344, Data2: 0xfd6d, Data3: 0x11d0, Data4: [8]byte{0x95, 0x8a, 0x00, 0x60, 0x97, 0xc9, 0xa0, 0x90}}
	iidTaskbarList3  = windows.GUID{Data1: 0xea1afb91, Data2: 0x9e28, Data3: 0x4b86, Data4: [8]byte{0x90, 0xe9, 0x9e, 0x9f, 0x8a, 0x5e, 0xeb, 0xfb}}
)

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
	className, err := windows.UTF16PtrFromString(windowsWindowClassName)
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

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
	"unsafe"

	"golang.org/x/sys/windows"
)

// 运行期间不在任务栏上放任何进度指示：TBPF_INDETERMINATE 的循环
// 跑马灯会持续整个 run，后台窗口的任务栏按钮被用户感知为
// “一直在闪烁”（2026-09 反馈）。任务栏提示只保留 run 结束时的
// 一次性 FlashWindowEx 闪烁。
const (
	flashwTray = 0x2
	// flashCount limits the flash to a bounded number of iterations instead
	// of FLASHW_TIMERNOFG's "flash until foregrounded" semantics, which keeps
	// a pending flash latched on the window forever — if the window is later
	// re-shown or the taskbar button is rebuilt (tray exit/restore, Explorer
	// refresh), the stale flash resumes out of nowhere.
	flashCount = 8
)

var (
	// modShell32 lives here next to modUser32 as the package's shared Win32
	// DLL registry; its only current consumer is host_clipboard_windows.go
	// (DragQueryFileW for CF_HDROP paste).
	modUser32  = windows.NewLazySystemDLL("user32.dll")
	modShell32 = windows.NewLazySystemDLL("shell32.dll")

	procFindWindowW      = modUser32.NewProc("FindWindowW")
	procGetForegroundWnd = modUser32.NewProc("GetForegroundWindow")
	procFlashWindowEx    = modUser32.NewProc("FlashWindowEx")
)

type flashWindowInfo struct {
	Size    uint32
	Window  uintptr
	Flags   uint32
	Count   uint32
	Timeout uint32
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

// flashTaskbarWindowIfInactive flashes the taskbar button a bounded number of
// times when the main window is not foreground — the run-end attention cue.
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

//go:build windows

package desktop

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// The notification area draws icons at the small icon metric of the taskbar's
// DPI (16 pixels at 100%, 20 at 125%, 24 at 150%, 32 at 200%).
const (
	smCxIcon   = 11
	smCxSmIcon = 49
)

var (
	user32                     = windows.NewLazySystemDLL("user32.dll")
	procGetDpiForSystem        = user32.NewProc("GetDpiForSystem")
	procFindWindowW            = user32.NewProc("FindWindowW")
	procGetDpiForWindow        = user32.NewProc("GetDpiForWindow")
	procGetSystemMetricsForDpi = user32.NewProc("GetSystemMetricsForDpi")
	procGetSystemMetrics       = user32.NewProc("GetSystemMetrics")
)

// Use the taskbar's DPI, not the main window's or the login-time system DPI.
func systemTrayIcon() ([]byte, bool, error) {
	icon, err := trayMarkPNG(trayIconSize())
	return icon, false, err
}

func trayIconSize() int {
	class, _ := windows.UTF16PtrFromString("Shell_TrayWnd")
	hwnd, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(class)), 0)
	return systemIconSize(hwnd, smCxSmIcon, 16)
}

func systemIconSize(hwnd uintptr, metric, baseSize int) int {
	dpi := 96
	if d, ok := callProc(procGetDpiForSystem); ok && d >= 48 && d <= 960 {
		dpi = int(d)
	}
	if hwnd != 0 {
		if d, ok := callProc(procGetDpiForWindow, hwnd); ok && d >= 48 && d <= 960 {
			dpi = int(d)
		}
	}
	if n, ok := callProc(procGetSystemMetricsForDpi, uintptr(metric), uintptr(dpi)); ok && n >= 8 && n <= 512 {
		return int(n)
	}
	if n, ok := callProc(procGetSystemMetrics, uintptr(metric)); ok && n >= 8 && n <= 512 {
		return int(n)
	}
	return (baseSize*dpi + 48) / 96
}

// callProc reports false where Windows does not have the function: they arrived
// in Windows 10 1607.
func callProc(p *windows.LazyProc, args ...uintptr) (uintptr, bool) {
	if err := p.Find(); err != nil {
		return 0, false
	}
	r, _, _ := p.Call(args...)
	return r, true
}

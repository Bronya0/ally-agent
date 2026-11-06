// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"sync"
	"syscall"

	"github.com/egoist/mygo"
	"golang.org/x/sys/windows"
)

// MyGo forces Win10's native menus dark/light along with the app. Opt only
// the tray popup into the system appearance; leave MyGo's Win11 drawing alone.
// Ordinal 135 is SetPreferredAppMode only since Windows 10 1903.
var trayMenuThemeProcs = sync.OnceValues(func() (set, flush uintptr) {
	version := windows.RtlGetVersion()
	if version.MajorVersion != 10 || version.BuildNumber < 18362 || version.BuildNumber >= 22000 {
		return 0, 0
	}
	uxtheme := windows.NewLazySystemDLL("uxtheme.dll")
	if err := uxtheme.Load(); err != nil {
		return 0, 0
	}
	getProc := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcAddress")
	set, _, _ = getProc.Call(uxtheme.Handle(), 135)
	flush, _, _ = getProc.Call(uxtheme.Handle(), 136)
	return set, flush
})

func popUpSystemTrayMenu(tray *mygo.Tray) {
	withSystemTrayMenuTheme(tray.PopUpMenu)
}

// The native menu's nested loop runs on the UI thread. Do not change
// mygo.Theme.Source: that would also recolor the main window and previews.
func withSystemTrayMenuTheme(show func()) {
	set, flush := trayMenuThemeProcs()
	if set == 0 || flush == 0 {
		show()
		return
	}
	const allowDark = 1 // Honor the system preference instead of forcing dark/light.
	previous, _, _ := syscall.SyscallN(set, allowDark)
	syscall.SyscallN(flush)
	defer func() {
		syscall.SyscallN(set, previous)
		syscall.SyscallN(flush)
	}()
	show()
}

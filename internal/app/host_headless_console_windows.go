// SPDX-License-Identifier: GPL-3.0-only
//go:build windows

package app

import (
	"os"

	"golang.org/x/sys/windows"
)

// Release builds use the GUI subsystem. Reuse the invoking shell's console
// without opening a window; keep valid redirected output handles unchanged.
func attachHeadlessConsole() {
	attach := windows.NewLazySystemDLL("kernel32.dll").NewProc("AttachConsole")
	_, _, _ = attach.Call(uintptr(^uint32(0))) // ATTACH_PARENT_PROCESS
	for _, stream := range []struct {
		file **os.File
		id   uint32
		name string
	}{{&os.Stdout, windows.STD_OUTPUT_HANDLE, "stdout"}, {&os.Stderr, windows.STD_ERROR_HANDLE, "stderr"}} {
		if _, err := (*stream.file).Stat(); err == nil {
			continue
		}
		if handle, err := windows.GetStdHandle(stream.id); err == nil && handle != 0 && handle != windows.InvalidHandle {
			*stream.file = os.NewFile(uintptr(handle), stream.name)
		}
	}
}

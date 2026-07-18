//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// hideCommandWindow suppresses the console window that Windows would otherwise
// allocate when a GUI process (Ally) launches a console subprocess (cmd.exe,
// npx, python, ssh, etc.). Two flags together:
//   - CREATE_NO_WINDOW (0x08000000): don't allocate a new console for the
//     child. This is the actual fix — without it, HideWindow alone still
//     flashes a console for processes that don't inherit one.
//   - HideWindow + wShowWindow=SW_HIDE: belt-and-suspenders so any console
//     that does get allocated is hidden.
//
// Use this for every exec.Cmd whose stdout/stderr we capture via pipes.
func hideCommandWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
}

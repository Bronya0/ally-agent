//go:build windows

package main

import (
	"os"
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

// newDetachedProcessAttr returns an os.ProcAttr that launches the new Ally
// process in its own process group, so it survives the parent exit. Used by
// the self-update restart path.
func newDetachedProcessAttr() *os.ProcAttr {
	return &os.ProcAttr{
		Sys: &syscall.SysProcAttr{
			CreationFlags: 0x00000200, // CREATE_NEW_PROCESS_GROUP
		},
		Files: []*os.File{nil, nil, nil},
	}
}

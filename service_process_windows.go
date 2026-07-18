//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"syscall"
)

func prepareServiceCommand(cmd *exec.Cmd) {
	// CREATE_NO_WINDOW prevents a console window from flashing on screen when
	// a GUI process (Ally) launches a console subprocess (npm/vite/python…).
	// CREATE_NEW_PROCESS_GROUP is preserved so background services can be
	// stopped via Ctrl-Break without signaling the parent Ally process.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000 | syscall.CREATE_NEW_PROCESS_GROUP, // CREATE_NO_WINDOW | CREATE_NEW_PROCESS_GROUP
	}
}

func stopProcessTree(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid: %d", pid)
	}
	cmd := exec.Command("taskkill.exe", "/PID", fmt.Sprintf("%d", pid), "/T", "/F")
	hideCommandWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("taskkill failed: %w: %s", err, string(out))
	}
	return nil
}

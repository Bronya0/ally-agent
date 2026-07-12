//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"syscall"
)

func prepareServiceCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
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

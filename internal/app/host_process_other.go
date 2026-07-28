//go:build !windows

package app

import (
	"fmt"
	"os/exec"
	"syscall"
)

func hideCommandWindow(cmd *exec.Cmd) {}

func prepareServiceCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func stopProcessTree(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid: %d", pid)
	}
	return syscall.Kill(-pid, syscall.SIGTERM)
}

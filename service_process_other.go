//go:build !windows

package main

import (
	"fmt"
	"os/exec"
	"syscall"
)

func prepareServiceCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func stopProcessTree(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid: %d", pid)
	}
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		return err
	}
	return nil
}

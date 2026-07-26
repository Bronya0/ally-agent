//go:build !windows

package grep

import "os/exec"

func hideCommandWindow(cmd *exec.Cmd) {}

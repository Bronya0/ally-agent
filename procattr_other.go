//go:build !windows

package main

import (
	"os"
	"os/exec"
)

func hideCommandWindow(cmd *exec.Cmd) {
}

// newDetachedProcessAttr returns nil on non-Windows; the self-update restart
// path is Windows-only and never reaches here.
func newDetachedProcessAttr() *os.ProcAttr {
	return &os.ProcAttr{}
}

//go:build !windows

package app

import (
	"os"
	"syscall"
	"testing"
)

func TestProcessTreeRejectsOwnProcessAndGroup(t *testing.T) {
	for _, pid := range []int{-1, 0, 1, os.Getpid(), syscall.Getpgrp()} {
		if err := stopProcessTree(pid); err == nil {
			t.Fatalf("accepted unsafe kill target %d", pid)
		}
		if err := gracefulStopProcessTree(pid); err == nil {
			t.Fatalf("accepted unsafe graceful target %d", pid)
		}
		if _, err := serviceProcessStop(pid); err == nil {
			t.Fatalf("accepted unsafe service group %d", pid)
		}
	}
}

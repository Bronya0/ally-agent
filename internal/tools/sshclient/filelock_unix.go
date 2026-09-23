// SPDX-License-Identifier: GPL-3.0-only
//go:build unix

package sshclient

import (
	"os"
	"syscall"
)

func lockKnownHostsFile(file *os.File) (func(), error) {
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return nil, err
	}
	return func() { _ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN) }, nil
}

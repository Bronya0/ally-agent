// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

//go:build darwin

package app

import (
	"os"
	"syscall"
	"time"
)

// macOS：Stat_t 用 Atimespec/Ctimespec/Birthtimespec（带纳秒的 Timespec）。
func statTimes(info os.FileInfo) statTimesResult {
	if sys, ok := info.Sys().(*syscall.Stat_t); ok {
		return statTimesResult{
			access: time.Unix(sys.Atimespec.Sec, sys.Atimespec.Nsec),
			change: time.Unix(sys.Ctimespec.Sec, sys.Ctimespec.Nsec),
			birth:  time.Unix(sys.Birthtimespec.Sec, sys.Birthtimespec.Nsec),
		}
	}
	mod := info.ModTime()
	return statTimesResult{access: mod, change: mod, birth: mod}
}

func statAlloc(info os.FileInfo) (alloc, blocks, blockSize int64) {
	if sys, ok := info.Sys().(*syscall.Stat_t); ok {
		return int64(sys.Blocks) * 512, int64(sys.Blocks), int64(sys.Blksize)
	}
	return 0, 0, 0
}

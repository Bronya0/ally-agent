// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

//go:build linux

package app

import (
	"os"
	"syscall"
	"time"
)

// Linux：Stat_t 用 Atim/Ctim（不带 spec 后缀），且无 birthtime 字段；
// 部分文件系统（如 ext4）其实有 inode birthtime，但 syscall.Stat_t 不暴露。
func statTimes(info os.FileInfo) statTimesResult {
	if sys, ok := info.Sys().(*syscall.Stat_t); ok {
		return statTimesResult{
			access: time.Unix(sys.Atim.Sec, sys.Atim.Nsec),
			change: time.Unix(sys.Ctim.Sec, sys.Ctim.Nsec),
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

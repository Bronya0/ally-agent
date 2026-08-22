// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

//go:build windows

package app

import (
	"os"
	"syscall"
	"time"
)

// Windows：FileInfo.Sys() 是 syscall.Win32FileAttributeData，可取创建/
// 访问/写入时间；无 POSIX ctime 与块分配信息，回退零值。
func statTimes(info os.FileInfo) statTimesResult {
	if sys, ok := info.Sys().(*syscall.Win32FileAttributeData);
	ok && sys != nil {
		toTime := func(ft syscall.Filetime) time.Time {
			if ft.HighDateTime == 0 && ft.LowDateTime == 0 {
				return time.Time{}
			}
			return time.Unix(0, ft.Nanoseconds())
		}
		mod := info.ModTime()
		return statTimesResult{
			access: toTime(sys.LastAccessTime),
			change: mod,
			birth:  toTime(sys.CreationTime),
		}
	}
	mod := info.ModTime()
	return statTimesResult{access: mod, change: mod, birth: mod}
}

func statAlloc(info os.FileInfo) (alloc, blocks, blockSize int64) {
	return 0, 0, 0
}

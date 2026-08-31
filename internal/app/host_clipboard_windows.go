// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

//go:build windows

package app

// Clipboard file-list reading for the explorer paste-into-workspace flow.
// Windows Explorer's Ctrl+C places a CF_HDROP block (double-null-terminated
// wide-char path list) on the clipboard; DragQueryFileW is the canonical
// reader for that format.

import (
	"fmt"
	"strings"
	"syscall"
	"time"
	"unsafe"
)
var (
	procOpenClipboard              = modUser32.NewProc("OpenClipboard")
	procCloseClipboard             = modUser32.NewProc("CloseClipboard")
	procGetClipboardData           = modUser32.NewProc("GetClipboardData")
	procIsClipboardFormatAvailable = modUser32.NewProc("IsClipboardFormatAvailable")

	procDragQueryFileW = modShell32.NewProc("DragQueryFileW")

	// CF_HDROP is a registered standard clipboard format (15).
	cfHDROP = uintptr(15)
)

// clipboardFiles returns the file/folder paths currently on the system
// clipboard. A nil slice with a nil error means the clipboard simply holds no
// file list ("nothing to paste"); a non-nil error means the clipboard could
// not be read (typically held by another process), which the user should be
// told instead of being shown a misleading "no files" notice.
func clipboardFiles() ([]string, error) {
	// Another process (Explorer, a clipboard manager) commonly holds the
	// clipboard for a few milliseconds; retry briefly before giving up.
	opened := false
	for attempt := 0; attempt < 10; attempt++ {
		ret, _, _ := procOpenClipboard.Call(0)
		if ret != 0 {
			opened = true
			break
		}
		sleepMS(10)
	}
	if !opened {
		return nil, fmt.Errorf("clipboard is busy (held by another process); try again in a moment")
	}
	defer procCloseClipboard.Call()

	available, _, _ := procIsClipboardFormatAvailable.Call(cfHDROP)
	if available == 0 {
		return nil, nil
	}
	handle, _, _ := procGetClipboardData.Call(cfHDROP)
	if handle == 0 {
		return nil, fmt.Errorf("reading the clipboard file list failed")
	}
	count, _, _ := procDragQueryFileW.Call(handle, 0xFFFFFFFF, 0, 0)
	files := make([]string, 0, count)
	for i := uintptr(0); i < count; i++ {
		size, _, _ := procDragQueryFileW.Call(handle, i, 0, 0)
		if size == 0 {
			continue
		}
		buf := make([]uint16, size+1)
		ret, _, _ := procDragQueryFileW.Call(handle, i, uintptr(unsafe.Pointer(&buf[0])), size+1)
		if ret == 0 {
			continue
		}
		if p := strings.TrimRight(syscall.UTF16ToString(buf), "\x00"); p != "" {
			files = append(files, p)
		}
	}
	return files, nil
}

func sleepMS(ms int) { time.Sleep(time.Duration(ms) * time.Millisecond) }

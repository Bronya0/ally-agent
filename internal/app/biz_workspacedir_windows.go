// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
//
//go:build windows

package app

import (
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

// folderIDDocuments 是 Windows FOLDERID_Documents 的 KNOWNFOLDERID：
// {FDD39AD0-238F-46AF-ADB4-6C85480369C7}。
var folderIDDocuments = syscall.GUID{
	Data1: 0xFDD39AD0,
	Data2: 0x238F,
	Data3: 0x46AF,
	Data4: [8]byte{0xAD, 0xB4, 0x6C, 0x85, 0x48, 0x03, 0x69, 0xC7},
}

// defaultWorkspaceDir 返回首次启动的默认工作空间：Windows 用户文档目录。
// 优先用 SHGetKnownFolderPath 取真实的 Documents 已知文件夹（尊重 OneDrive
// 重定向等），失败时回退到 ~\Documents。
func defaultWorkspaceDir() string {
	if dir, err := knownFolderPath(&folderIDDocuments); err == nil && dir != "" {
		return dir
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, "Documents")
	}
	return ""
}

func knownFolderPath(id *syscall.GUID) (string, error) {
	var p *uint16
	r, _, _ := syscall.NewLazyDLL("shell32.dll").NewProc("SHGetKnownFolderPath").Call(
		uintptr(unsafe.Pointer(id)),
		0,
		0,
		uintptr(unsafe.Pointer(&p)),
	)
	if r != 0 {
		return "", syscall.Errno(r)
	}
	if p == nil {
		return "", syscall.EINVAL
	}
	defer syscall.NewLazyDLL("ole32.dll").NewProc("CoTaskMemFree").Call(uintptr(unsafe.Pointer(p)))
	const maxLen = 1 << 20
	hdr := (*[maxLen]uint16)(unsafe.Pointer(p))
	n := 0
	for n < maxLen && hdr[n] != 0 {
		n++
	}
	return syscall.UTF16ToString(hdr[:n:n]), nil
}

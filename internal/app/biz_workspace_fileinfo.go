// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"time"
)

type WorkspaceFileInfo struct {
	Path       string `json:"path"`
	Absolute   string `json:"absolute"`
	Name       string `json:"name"`
	Extension  string `json:"extension"`
	IsDir      bool   `json:"isDir"`
	Size       int64  `json:"size"`
	AllocSize  int64  `json:"allocSize"`
	Blocks     int64  `json:"blocks"`
	BlockSize  int64  `json:"blockSize"`
	Mode       string `json:"mode"`
	ModTime    string `json:"modTime"`
	AccessTime string `json:"accessTime"`
	ChangeTime string `json:"changeTime"`
	BirthTime  string `json:"birthTime"`
	Symlink    bool   `json:"symlink"`
	LinkTarget string `json:"linkTarget"`
	MD5        string `json:"md5"`
	SHA1       string `json:"sha1"`
	SHA256     string `json:"sha256"`
	SHA512     string `json:"sha512"`
	CRC32      string `json:"crc32"`
	DirCount   int64  `json:"dirCount"`
	FileCount  int64  `json:"fileCount"`
	TotalSize  int64  `json:"totalSize"`
}

// GetWorkspaceFileInfo returns detailed metadata and content hashes for a
// workspace path, powering the explorer's "file info" dialog. Files are
// streamed through every hash in a single pass; directories are summarized
// with a bounded recursive walk.
func (a *App) GetWorkspaceFileInfo(path string) (WorkspaceFileInfo, error) {
	cfg := a.effectiveConfig(ConfigState{})
	resolved, err := resolveReadPath(cfg, path)
	if err != nil {
		return WorkspaceFileInfo{}, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return WorkspaceFileInfo{}, err
	}
	// lstat 信息（符号链接判定 + 目标）独立于 stat：stat 跟随链接
	linkTarget := ""
	if linfo, lerr := os.Lstat(resolved); lerr == nil && linfo.Mode()&os.ModeSymlink != 0 {
		if target, terr := os.Readlink(resolved); terr == nil {
			linkTarget = target
		}
	}
	times := statTimes(info)
	result := WorkspaceFileInfo{
		Path:       displayPathForConfig(cfg, resolved),
		Absolute:   filepath.Clean(resolved),
		Name:       filepath.Base(resolved),
		Extension:  filepath.Ext(resolved),
		IsDir:      info.IsDir(),
		Size:       info.Size(),
		Mode:       info.Mode().String(),
		ModTime:    info.ModTime().Format(time.RFC3339Nano),
		AccessTime: times.access.Format(time.RFC3339Nano),
		ChangeTime: times.change.Format(time.RFC3339Nano),
		BirthTime:  times.birth.Format(time.RFC3339Nano),
		Symlink:    linkTarget != "",
		LinkTarget: linkTarget,
	}
	result.AllocSize, result.Blocks, result.BlockSize = statAlloc(info)
	if !info.IsDir() {
		if err := hashWorkspaceFile(resolved, &result); err != nil {
			return WorkspaceFileInfo{}, err
		}
		return result, nil
	}
	if err := summarizeWorkspaceDir(resolved, &result); err != nil {
		return WorkspaceFileInfo{}, err
	}
	return result, nil
}

// hashWorkspaceFile streams one file through MD5/SHA1/SHA256/SHA512/CRC32 in
// a single pass. Only the rolling hash states live in memory.
func hashWorkspaceFile(path string, out *WorkspaceFileInfo) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	md5h, sha1h := md5.New(), sha1.New()
	sha256h, sha512h := sha256.New(), sha512.New()
	crc32h := crc32.NewIEEE()
	buf := make([]byte, 64*1024)
	for {
		n, rerr := file.Read(buf)
		if n > 0 {
			for _, h := range []hash.Hash{md5h, sha1h, sha256h, sha512h, crc32h} {
				h.Write(buf[:n])
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	out.MD5 = hex.EncodeToString(md5h.Sum(nil))
	out.SHA1 = hex.EncodeToString(sha1h.Sum(nil))
	out.SHA256 = hex.EncodeToString(sha256h.Sum(nil))
	out.SHA512 = hex.EncodeToString(sha512h.Sum(nil))
	out.CRC32 = fmt.Sprintf("%08x", crc32h.Sum32())
	return nil
}

// summarizeWorkspaceDir walks a directory tree and aggregates entry counts
// and total size. The walk is bounded by entry count to stay responsive on
// huge trees; unreadable subtrees are skipped, not fatal.
const workspaceFileInfoMaxEntries = 200000

func summarizeWorkspaceDir(root string, out *WorkspaceFileInfo) error {
	entries := 0
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		entries++
		if entries > workspaceFileInfoMaxEntries {
			return filepath.SkipAll
		}
		if d.IsDir() {
			out.DirCount++
			return nil
		}
		out.FileCount++
		if info, ierr := d.Info(); ierr == nil {
			out.TotalSize += info.Size()
		}
		return nil
	})
}

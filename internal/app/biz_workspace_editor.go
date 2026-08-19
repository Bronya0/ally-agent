// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const workspaceEditorMaxBytes = 2 * 1024 * 1024

type WorkspaceFileContent struct {
	Path       string `json:"path"`
	Content    string `json:"content"`
	Version    string `json:"version"`
	SHA256     string `json:"sha256"`
	Size       int64  `json:"size"`
	LineEnding string `json:"lineEnding"`
}

type SaveWorkspaceFileRequest struct {
	Path    string `json:"path"`
	Version string `json:"version"`
	Content string `json:"content"`
}

type SaveWorkspaceFileResult struct {
	Path    string `json:"path"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
}

// ReadWorkspaceFile returns an unnumbered, complete text snapshot for the
// user-facing workspace editor. It is deliberately separate from ReadFile,
// whose bounded line-number preview is designed for model context.
func (a *App) ReadWorkspaceFile(path string) (WorkspaceFileContent, error) {
	cfg := a.effectiveConfig(ConfigState{})
	resolved, err := resolveReadPath(cfg, path)
	if err != nil {
		return WorkspaceFileContent{}, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return WorkspaceFileContent{}, err
	}
	if info.IsDir() {
		return WorkspaceFileContent{}, codedToolError("E_BAD_PATH", fmt.Errorf("not a file: %s", path))
	}
	if info.Size() > workspaceEditorMaxBytes {
		return WorkspaceFileContent{}, codedToolError("E_FILE_TOO_LARGE", fmt.Errorf("file is larger than the %d MiB editor limit", workspaceEditorMaxBytes/(1024*1024)))
	}
	data, info, err := readTextFile(resolved)
	if err != nil {
		return WorkspaceFileContent{}, err
	}
	if len(data) > workspaceEditorMaxBytes {
		return WorkspaceFileContent{}, codedToolError("E_FILE_TOO_LARGE", fmt.Errorf("file is larger than the %d MiB editor limit", workspaceEditorMaxBytes/(1024*1024)))
	}
	content, ending, _ := normalizeText(data)
	sha, version := hashBytesAndVersion(data)
	return WorkspaceFileContent{
		Path:       displayPathForConfig(cfg, resolved),
		Content:    content,
		Version:    version,
		SHA256:     sha,
		Size:       info.Size(),
		LineEnding: ending,
	}, nil
}

// SaveWorkspaceFile atomically replaces one editor snapshot after validating
// its optimistic-concurrency token. The shared file mutex keeps UI writes from
// racing Agent file mutations in the same process.
func (a *App) SaveWorkspaceFile(req SaveWorkspaceFileRequest) (SaveWorkspaceFileResult, error) {
	if strings.TrimSpace(req.Path) == "" {
		return SaveWorkspaceFileResult{}, codedToolError("E_BAD_PATH", errors.New("path is required"))
	}
	if err := validateVersion(req.Version); err != nil {
		return SaveWorkspaceFileResult{}, err
	}
	if len(req.Content) > workspaceEditorMaxBytes {
		return SaveWorkspaceFileResult{}, codedToolError("E_FILE_TOO_LARGE", fmt.Errorf("content is larger than the %d MiB editor limit", workspaceEditorMaxBytes/(1024*1024)))
	}

	cfg := a.effectiveConfig(ConfigState{})
	a.fileOpsMu.Lock()
	defer a.fileOpsMu.Unlock()

	roots, err := workspaceRoots(cfg)
	if err != nil {
		return SaveWorkspaceFileResult{}, err
	}
	resolved, err := safeJoin(roots, req.Path)
	if err != nil {
		return SaveWorkspaceFileResult{}, err
	}
	before, info, err := readTextFile(resolved)
	if err != nil {
		return SaveWorkspaceFileResult{}, err
	}
	_, currentVersion := hashBytesAndVersion(before)
	if !strings.EqualFold(req.Version, currentVersion) {
		return SaveWorkspaceFileResult{}, codedToolError("E_VERSION_MISMATCH", fmt.Errorf("file %s changed outside the editor; reopen it before saving", req.Path))
	}
	_, ending, hadBOM := normalizeText(before)
	after := encodeText(req.Content, ending, hadBOM)
	if len(after) > workspaceEditorMaxBytes {
		return SaveWorkspaceFileResult{}, codedToolError("E_FILE_TOO_LARGE", fmt.Errorf("encoded content is larger than the %d MiB editor limit", workspaceEditorMaxBytes/(1024*1024)))
	}
	if err := safeWriteFile(resolved, after, info.Mode().Perm()); err != nil {
		return SaveWorkspaceFileResult{}, err
	}
	sha, version := hashBytesAndVersion(after)
	a.invalidateWorkspaceMapCache(cfg)
	return SaveWorkspaceFileResult{
		Path:    displayPathForConfig(cfg, resolved),
		Version: version,
		SHA256:  sha,
		Size:    int64(len(after)),
	}, nil
}

var imageExtensions = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp", ".bmp": "image/bmp",
	".svg": "image/svg+xml", ".ico": "image/x-icon",
}

// IsWorkspaceImage reports whether the given path has an image extension.
func (a *App) IsWorkspaceImage(path string) bool {
	return imageExtMime(path) != ""
}

func imageExtMime(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	return imageExtensions[ext]
}

// ReadWorkspaceImage reads a binary image file and returns a data URL.
func (a *App) ReadWorkspaceImage(path string) (WorkspaceImageContent, error) {
	cfg := a.effectiveConfig(ConfigState{})
	resolved, err := resolveReadPath(cfg, path)
	if err != nil {
		return WorkspaceImageContent{}, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return WorkspaceImageContent{}, err
	}
	if info.IsDir() {
		return WorkspaceImageContent{}, codedToolError("E_BAD_PATH", fmt.Errorf("not a file: %s", path))
	}
	mime := imageExtMime(path)
	if mime == "" {
		return WorkspaceImageContent{}, codedToolError("E_BAD_PATH", fmt.Errorf("not an image: %s", path))
	}
	if info.Size() > workspaceEditorMaxBytes {
		return WorkspaceImageContent{}, codedToolError("E_FILE_TOO_LARGE", fmt.Errorf("file is larger than the %d MiB editor limit", workspaceEditorMaxBytes/(1024*1024)))
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return WorkspaceImageContent{}, err
	}
	return WorkspaceImageContent{
		Path: displayPathForConfig(cfg, resolved),
		Mime: mime,
		Size: info.Size(),
		Data: "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data),
	}, nil
}

type WorkspaceImageContent struct {
	Path string `json:"path"`
	Mime string `json:"mime"`
	Size int64  `json:"size"`
	Data string `json:"data"`
}

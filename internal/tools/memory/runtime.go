// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
// Package memory holds the global-memory tool: pure parsing/formatting
// helpers (memory.go) plus the host-neutral orchestration that binds them to
// the ~/.ally_agent/memories directory, atomic file writes, version checks,
// and an in-process index cache.
//
// The orchestration layer depends only on the shared.Runtime interface (for
// the memories directory path) and on internal/tools/read pure helpers
// (file IO, hashing, version validation). App supplies a thin wrapper that
// injects *App as the Runtime.
package memory

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"ally-dev/internal/tools/pathutil"
	"ally-dev/internal/tools/read"
	"ally-dev/internal/tools/shared"
)

// Runtime is the host capability surface the memory tool needs. App
// satisfies this structurally; tests can supply a fake.
type Runtime interface {
	// MemoriesDir returns the absolute path to ~/.ally_agent/memories/,
	// creating it if necessary.
	MemoriesDir() string
}

// IndexEntry is one entry in the memory index list.
type IndexEntry struct {
	Path        string `json:"path"`
	Description string `json:"description"`
	SHA256      string `json:"sha256,omitempty"`
	Size        int64  `json:"size,omitempty"`
}

// ListResult is the result of listing all memories.
type ListResult struct {
	Dir      string       `json:"dir"`
	Memories []IndexEntry `json:"memories"`
	Count    int          `json:"count"`
}

// ReadRequest is the request payload used when the model reads a memory file
// directly through the read tool (paths under ~/.ally_agent/memories).
type ReadRequest struct {
	Path string `json:"path"`
}

// ReadResult is the result of reading a memory file.
type ReadResult struct {
	Path        string `json:"path"`
	Description string `json:"description"`
	Content     string `json:"content"`
	SHA256      string `json:"sha256"`
	Version     string `json:"version"`
	Size        int64  `json:"size"`
}

// WriteRequest is the request payload used when the model writes a memory
// file directly through create/edit (paths under ~/.ally_agent/memories).
type WriteRequest struct {
	Path        string `json:"path"`
	Description string `json:"description"`
	Content     string `json:"content"`
	Version     string `json:"version,omitempty"`
}

// WriteResult is the result of writing a memory file.
type WriteResult struct {
	Path         string `json:"path"`
	Description  string `json:"description"`
	SHA256       string `json:"sha256"`
	Version      string `json:"version"`
	Size         int64  `json:"size"`
	Created      bool   `json:"created"`
	UpdatedIndex bool   `json:"updatedIndex"`
}

// indexCache memoizes ListResult across the process so the system prompt
// builder does not rescan the memories directory on every turn. Invalidation
// is explicit: Write invalidates the cache after a successful write.
type indexCache struct {
	sync.Mutex
	result    ListResult
	dirMtime  time.Time
	populated bool
}

// IndexCache is the process-wide memory index cache. Callers outside the
// memory package (e.g. the system prompt builder) use Lookup/Store/Invalidate
// through the package-level IndexCache variable.
var IndexCache = &indexCache{}

func (c *indexCache) Lookup(rt Runtime) (ListResult, bool) {
	c.Lock()
	defer c.Unlock()
	if !c.populated {
		return ListResult{}, false
	}
	if info, err := os.Stat(rt.MemoriesDir()); err == nil {
		if info.ModTime().After(c.dirMtime) {
			return ListResult{}, false
		}
	}
	return c.result, true
}

func (c *indexCache) Store(result ListResult, rt Runtime) {
	dir := rt.MemoriesDir()
	mtime := time.Time{}
	if info, err := os.Stat(dir); err == nil {
		mtime = info.ModTime()
	}
	c.Lock()
	c.result = result
	c.dirMtime = mtime
	c.populated = true
	c.Unlock()
}

func (c *indexCache) Invalidate() {
	c.Lock()
	c.populated = false
	c.result = ListResult{}
	c.Unlock()
}

// List enumerates all .md memory files under the memories directory and
// returns their index entries sorted by path.
func List(rt Runtime) (ListResult, error) {
	dir := rt.MemoriesDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ListResult{}, err
	}
	entries := []IndexEntry{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if strings.ToLower(filepath.Ext(path)) != ".md" {
			return nil
		}
		// Reuse the DirEntry's own FileInfo instead of calling os.Stat
		// again inside read.ReadTextFile. On a directory with many memory
		// files this halves the stat syscalls.
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.Size() > read.MaxReadBytes {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		// Fast binary/UTF-8 guards. bytes.IndexByte is vectorized on
		// amd64/arm64 and beats the manual byte loop in read.hasNUL.
		if bytes.IndexByte(data, 0) >= 0 {
			return nil
		}
		if !utf8.Valid(data) {
			return nil
		}
		desc, _ := ParseMarkdown(string(data))
		if strings.TrimSpace(desc) == "" {
			return nil
		}
		entries = append(entries, IndexEntry{
			Path:        filepath.ToSlash(path),
			Description: desc,
			SHA256:      read.HashBytes(data),
			Size:        info.Size(),
		})
		return nil
	})
	if err != nil {
		return ListResult{}, err
	}
	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].Path) < strings.ToLower(entries[j].Path)
	})
	return ListResult{Dir: filepath.ToSlash(dir), Memories: entries, Count: len(entries)}, nil
}

// ResolvePath validates and resolves a memory path against the memories
// directory. Absolute paths are accepted; relative paths are joined under
// the memories directory. The result is always inside the memories directory
// and has a .md extension.
func ResolvePath(rt Runtime, p string) (string, error) {
	root := rt.MemoriesDir()
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	p = strings.TrimSpace(p)
	if p == "" {
		return "", errors.New("memory path is required")
	}
	var target string
	if filepath.IsAbs(p) {
		target = p
	} else {
		target = filepath.Join(root, filepath.Clean(p))
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	rootAbs = filepath.Clean(rootAbs)
	if !insideRoot(rootAbs, abs) || strings.ToLower(filepath.Ext(abs)) != ".md" {
		return "", fmt.Errorf("memory path must be a .md file under %s", filepath.ToSlash(rootAbs))
	}
	return abs, nil
}

// Read loads one memory file, parses frontmatter, and
// returns the description, body, hash, and version token.
func Read(rt Runtime, req ReadRequest) (ReadResult, error) {
	path, err := ResolvePath(rt, req.Path)
	if err != nil {
		return ReadResult{}, err
	}
	data, info, err := read.ReadTextFile(path)
	if err != nil {
		return ReadResult{}, err
	}
	desc, body := ParseMarkdown(string(data))
	// Hash once instead of HashBytes + HashVersion (two SHA-256 passes).
	sha256Hex, version := read.HashBytesAndVersion(data)
	return ReadResult{
		Path:        filepath.ToSlash(path),
		Description: desc,
		Content:     body,
		SHA256:      sha256Hex,
		Version:     version,
		Size:        info.Size(),
	}, nil
}

// Write creates or updates one memory file with
// optimistic-concurrency version checking. On success the index cache is
// invalidated so the next system prompt build rescans.
func Write(rt Runtime, req WriteRequest) (WriteResult, error) {
	if strings.TrimSpace(req.Description) == "" {
		return WriteResult{}, errors.New("write requires a non-empty description")
	}
	if strings.TrimSpace(req.Content) == "" {
		return WriteResult{}, errors.New("write requires non-empty content")
	}
	pathValue := req.Path
	if strings.TrimSpace(pathValue) == "" {
		pathValue = DefaultPath(req.Description)
	}
	path, err := ResolvePath(rt, pathValue)
	if err != nil {
		return WriteResult{}, err
	}
	before := []byte{}
	created := true
	if existing, _, err := read.ReadTextFile(path); err == nil {
		before = existing
		created = false
		if req.Version == "" {
			return WriteResult{}, fmt.Errorf("memory already exists: %s; pass version from a prior read", filepath.ToSlash(path))
		}
		if err := read.ValidateVersion(req.Version); err != nil {
			return WriteResult{}, err
		}
		currentVersion := read.HashVersion(existing)
		if !strings.EqualFold(req.Version, currentVersion) {
			return WriteResult{}, shared.New("E_VERSION_MISMATCH", fmt.Errorf("version %s does not match current memory version %s", req.Version, currentVersion))
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return WriteResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return WriteResult{}, err
	}
	data := []byte(FormatMarkdown(req.Description, req.Content))
	if err := read.SafeWriteFile(path, data, 0o644); err != nil {
		return WriteResult{}, err
	}
	IndexCache.Invalidate()
	// Hash once instead of HashBytes + HashVersion (two SHA-256 passes).
	sha256Hex, version := read.HashBytesAndVersion(data)
	return WriteResult{
		Path:         filepath.ToSlash(path),
		Description:  strings.TrimSpace(req.Description),
		SHA256:       sha256Hex,
		Version:      version,
		Size:         int64(len(data)),
		Created:      created,
		UpdatedIndex: !bytes.Equal(before, data),
	}, nil
}

// insideRoot delegates to pathutil so the memory package does not maintain
// its own copy of the lexical workspace-containment check.
func insideRoot(root, target string) bool {
	return pathutil.InsideRoot(root, target)
}

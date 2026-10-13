// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
// Package memory holds the global-memory index: pure Markdown frontmatter
// helpers (memory.go) plus the host-neutral listing of ~/.ally_agent/memories
// with an in-process index cache.
//
// The memory_read/memory_write tools are gone: the model reads and writes
// memory files with the read/create/edit tools, so nothing here writes files
// or checks versions. The listing needs only the Runtime interface (for the
// memories directory path); App supplies a thin wrapper that injects *App.
package memory

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"ally-dev/internal/tools/read"
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

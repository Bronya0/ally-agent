package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type memoryIndexCacheType struct {
	sync.Mutex
	result    MemoryListResult
	dirMtime  time.Time
	populated bool
}

var memoryIndexCache = memoryIndexCacheType{}

func (c *memoryIndexCacheType) lookup() (MemoryListResult, bool) {
	c.Lock()
	defer c.Unlock()
	if !c.populated {
		return MemoryListResult{}, false
	}
	if info, err := os.Stat(memoriesDir()); err == nil {
		if info.ModTime().After(c.dirMtime) {
			return MemoryListResult{}, false
		}
	}
	return c.result, true
}

func (c *memoryIndexCacheType) store(result MemoryListResult) {
	dir := memoriesDir()
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

func (c *memoryIndexCacheType) invalidate() {
	c.Lock()
	c.populated = false
	c.result = MemoryListResult{}
	c.Unlock()
}

func listMemories() (MemoryListResult, error) {
	dir := memoriesDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return MemoryListResult{}, err
	}
	entries := []MemoryIndexEntry{}
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
		data, info, err := readTextFile(path)
		if err != nil {
			return nil
		}
		desc, _ := parseMemoryMarkdown(string(data))
		if strings.TrimSpace(desc) == "" {
			return nil
		}
		entries = append(entries, MemoryIndexEntry{
			Path:        filepath.ToSlash(path),
			Description: desc,
			SHA256:      hashBytes(data),
			Size:        info.Size(),
		})
		return nil
	})
	if err != nil {
		return MemoryListResult{}, err
	}
	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].Path) < strings.ToLower(entries[j].Path)
	})
	return MemoryListResult{Dir: filepath.ToSlash(dir), Memories: entries, Count: len(entries)}, nil
}

func parseMemoryMarkdown(text string) (string, string) {
	if !strings.HasPrefix(text, "---") {
		return "", text
	}
	rest := text[3:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", text
	}
	front := rest[:end]
	body := rest[end+len("\n---"):]
	body = strings.TrimPrefix(body, "\r\n")
	body = strings.TrimPrefix(body, "\n")
	desc := ""
	for _, line := range strings.Split(front, "\n") {
		if v := parseYAMLField(strings.TrimSpace(line), "description"); v != "" {
			desc = v
			break
		}
	}
	return desc, body
}

func formatMemoryMarkdown(description, content string) string {
	description = strings.TrimSpace(description)
	description = strings.ReplaceAll(description, "\r", " ")
	description = strings.ReplaceAll(description, "\n", " ")
	content = normalizeEditString(content)
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("description: ")
	if strings.ContainsAny(description, `":#[]{}&*!|>'%@`+"\t") {
		quoted, _ := json.Marshal(description)
		b.Write(quoted)
	} else {
		b.WriteString(description)
	}
	b.WriteString("\n---\n\n")
	b.WriteString(content)
	if !strings.HasSuffix(content, "\n") {
		b.WriteString("\n")
	}
	return b.String()
}

func resolveMemoryPath(p string) (string, error) {
	root := memoriesDir()
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

func defaultMemoryPath(description string) string {
	slug := strings.ToLower(strings.TrimSpace(description))
	slug = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "memory"
	}
	if len(slug) > 64 {
		slug = strings.Trim(slug[:64], "-")
	}
	return slug + ".md"
}

func (a *App) memoryRead(req MemoryReadRequest) (MemoryReadResult, error) {
	path, err := resolveMemoryPath(req.Path)
	if err != nil {
		return MemoryReadResult{}, err
	}
	data, info, err := readTextFile(path)
	if err != nil {
		return MemoryReadResult{}, err
	}
	desc, body := parseMemoryMarkdown(string(data))
	return MemoryReadResult{
		Path:        filepath.ToSlash(path),
		Description: desc,
		Content:     body,
		SHA256:      hashBytes(data),
		Version:     hashVersion(data),
		Size:        info.Size(),
	}, nil
}

func (a *App) memoryWrite(req MemoryWriteRequest) (MemoryWriteResult, error) {
	if strings.TrimSpace(req.Description) == "" {
		return MemoryWriteResult{}, errors.New("memory_write requires a non-empty description")
	}
	if strings.TrimSpace(req.Content) == "" {
		return MemoryWriteResult{}, errors.New("memory_write requires non-empty content")
	}
	pathValue := req.Path
	if strings.TrimSpace(pathValue) == "" {
		pathValue = defaultMemoryPath(req.Description)
	}
	path, err := resolveMemoryPath(pathValue)
	if err != nil {
		return MemoryWriteResult{}, err
	}
	before := []byte{}
	created := true
	if existing, _, err := readTextFile(path); err == nil {
		before = existing
		created = false
		if req.Version == "" {
			return MemoryWriteResult{}, fmt.Errorf("memory already exists: %s; pass version from memory_read", filepath.ToSlash(path))
		}
		if err := validateVersion(req.Version); err != nil {
			return MemoryWriteResult{}, err
		}
		currentVersion := hashVersion(existing)
		if !strings.EqualFold(req.Version, currentVersion) {
			return MemoryWriteResult{}, fmt.Errorf("[E_VERSION_MISMATCH] version %s does not match current memory version %s", req.Version, currentVersion)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return MemoryWriteResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return MemoryWriteResult{}, err
	}
	data := []byte(formatMemoryMarkdown(req.Description, req.Content))
	if err := safeWriteFile(path, data, 0o644); err != nil {
		return MemoryWriteResult{}, err
	}
	memoryIndexCache.invalidate()
	return MemoryWriteResult{
		Path:         filepath.ToSlash(path),
		Description:  strings.TrimSpace(req.Description),
		SHA256:       hashBytes(data),
		Version:      hashVersion(data),
		Size:         int64(len(data)),
		Created:      created,
		UpdatedIndex: !bytes.Equal(before, data),
	}, nil
}

// agentFile is a discovered AGENTS.md (or fallback) file to merge into the system prompt.

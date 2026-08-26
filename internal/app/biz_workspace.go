// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"ally-dev/internal/tools/grep"

	gitignore "github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

// workspaceCacheHolder owns all workspace-map / path-index memoization state
// (map TTL caches, path index, in-flight rebuild dedup, global index version).
// It lives in biz_workspace.go so that every cache's fields, TTL constants,
// read/write logic, and invalidation stay in one file instead of spreading
// across app.go's struct definition.
type workspaceCacheHolder struct {
	mapMu       sync.Mutex
	mapCache    map[string]workspaceMapCacheEntry
	pathMu      sync.Mutex
	pathCache   map[string]*workspacePathIndex
	pathBuilds  map[string]chan struct{}
	pathVersion int64
}

func newWorkspaceCacheHolder() *workspaceCacheHolder {
	return &workspaceCacheHolder{
		mapCache:   map[string]workspaceMapCacheEntry{},
		pathCache:  map[string]*workspacePathIndex{},
		pathBuilds: map[string]chan struct{}{},
	}
}

func (a *App) listFilesWithConfig(cfg ConfigState, req ListFilesRequest) (ListFilesResult, error) {
	root, err := workspaceRoot(cfg)
	if err != nil {
		return ListFilesResult{}, err
	}
	start := root
	if strings.TrimSpace(req.Path) != "" {
		start, err = resolveReadablePath(cfg, req.Path)
		if err != nil {
			return ListFilesResult{}, err
		}
	}
	info, err := os.Stat(start)
	if err != nil {
		return ListFilesResult{}, err
	}
	if !info.IsDir() {
		return ListFilesResult{}, codedToolError("E_BAD_PATH", fmt.Errorf("not a directory: %s", req.Path))
	}
	inRoot := insideRoot(root, start)
	if !inRoot {
		if blocked, reason := isDangerousSearchRoot(start); blocked {
			return ListFilesResult{}, codedToolError("E_SEARCH_ROOT_BLOCKED", fmt.Errorf("%s\n\nThis listing has been blocked for safety. Specify a narrower project subdirectory or explicit file path.", reason))
		}
	}
	// includeIgnored=false skips gitignored paths (workspace .gitignore rules)
	// plus a small hardcoded fallback list (see isHeavyDir). Gitignore rules
	// only apply inside the workspace; out-of-root listings use the hardcoded
	// list alone. Mirrors how Trae's LS hides node_modules/.git automatically.
	var ignoreMatcher gitignore.Matcher
	if !req.IncludeIgnored && inRoot {
		ignoreMatcher = loadRootGitignoreRules(root)
	}

	maxDepth := req.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 3
	}
	limit := req.Limit
	if limit <= 0 || limit > 1000 {
		limit = 200
	}

	// Pre-allocate up to limit capacity. The previous zero-capacity slice
	// re-allocated ~log2(limit) times (1→2→4→...→limit), copying the entire
	// backing array each time.
	entries := make([]FileEntry, 0, limit)
	// lowerPaths[i] holds strings.ToLower(entries[i].Path) so the sort
	// comparator doesn't re-ToLower the same path on every comparison. The
	// previous code called strings.ToLower twice per compare (O(N log N)
	// total calls); now it's O(N) one-time work.
	lowerPaths := make([]string, 0, limit)
	truncated := false
	err = filepath.WalkDir(start, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if path == start {
			return nil
		}
		name := d.Name()
		// VCS internals are always pruned for model-facing listings even
		// when the model opts into hidden/ignored paths — .git noise easily
		// dominates the entry limit. The UI explorer (ModelFacing=false)
		// still shows them.
		if req.ModelFacing && isVCSDirName(name) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !req.IncludeHidden && strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !req.IncludeIgnored && d.IsDir() && isHeavyDir(name) {
			return filepath.SkipDir
		}
		if ignoreMatcher != nil {
			relToRoot, _ := filepath.Rel(root, path)
			if matchGitignoreRules(ignoreMatcher, relToRoot, d.IsDir()) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}

		rel, _ := filepath.Rel(start, path)
		depth := pathDepth(rel)
		if depth > maxDepth {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if len(entries) >= limit {
			truncated = true
			// Stop walking entirely once we've collected enough entries.
			// The previous code returned SkipDir for directories and nil
			// for files, which meant WalkDir kept traversing the rest of
			// the tree (potentially thousands of entries) just to throw
			// every one away. fs.SkipAll aborts the walk immediately.
			return fs.SkipAll
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		displayPath := grep.DisplayPathForRoot(root, path)
		entries = append(entries, FileEntry{
			Path:    displayPath,
			Name:    name,
			Dir:     d.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime().Format(time.RFC3339),
		})
		lowerPaths = append(lowerPaths, strings.ToLower(displayPath))
		return nil
	})
	if err != nil {
		return ListFilesResult{}, err
	}
	// Sort by precomputed lowerPaths. sort.Slice swaps entries in place but
	// does NOT touch lowerPaths, so we sort an index permutation and then
	// apply it to entries in a single pass — this keeps the entries↔lowerPaths
	// alignment intact and avoids per-compare ToLower.
	idx := make([]int, len(entries))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		ea, eb := entries[idx[a]], entries[idx[b]]
		if ea.Dir != eb.Dir {
			return ea.Dir
		}
		return lowerPaths[idx[a]] < lowerPaths[idx[b]]
	})
	sorted := make([]FileEntry, len(entries))
	for i, j := range idx {
		sorted[i] = entries[j]
	}
	return ListFilesResult{Entries: sorted, Count: len(sorted), Truncated: truncated}, nil
}

func (a *App) workspaceMapContext(cfg ConfigState) string {
	root, err := workspaceRoot(cfg)
	if err != nil {
		return ""
	}
	key := workspaceMapCacheKey(root)

	a.workspaceCaches.mapMu.Lock()
	cached, ok := a.workspaceCaches.mapCache[key]
	if ok && time.Since(cached.generatedAt) < workspaceMapTTL {
		content := cached.content
		a.workspaceCaches.mapMu.Unlock()
		return content
	}
	a.workspaceCaches.mapMu.Unlock()

	content := buildWorkspaceMapContext(root)

	a.workspaceCaches.mapMu.Lock()
	a.workspaceCaches.mapCache[key] = workspaceMapCacheEntry{content: content, generatedAt: time.Now()}
	a.workspaceCaches.mapMu.Unlock()
	return content
}

// workspaceMapSnapshotNote prefixes every session-frozen workspace map so the
// model knows the structure is a point-in-time view, not live state.
const workspaceMapSnapshotNote = "Snapshot frozen at this session's first request; not updated as files change during the session — use list_files for the current structure.\n\n"

// sessionWorkspaceMap returns the workspace map text for a chat session,
// frozen at the session's first request. Later runs in the same session reuse
// the exact same bytes so the request prefix (system prompt + map + history)
// stays byte-stable and provider prompt caches survive across runs —
// rebuilding the map after edits (the previous behavior) changed the prefix
// and killed the entire history cache on every follow-up run. The underlying
// per-workspace cache still refreshes normally for other consumers; only the
// prompt-side view is frozen. Requests without a session id fall back to the
// live map.
func (a *App) sessionWorkspaceMap(sessionID string, cfg ConfigState) string {
	if strings.TrimSpace(sessionID) == "" {
		return a.workspaceMapContext(cfg)
	}
	a.mu.Lock()
	if content, ok := a.sessionWorkspaceMaps[sessionID]; ok {
		a.mu.Unlock()
		return content
	}
	a.mu.Unlock()

	content := a.workspaceMapContext(cfg)
	if content == "" {
		return ""
	}
	content = workspaceMapSnapshotNote + content

	a.mu.Lock()
	if a.sessionWorkspaceMaps == nil {
		a.sessionWorkspaceMaps = map[string]string{}
	}
	a.sessionWorkspaceMaps[sessionID] = content
	a.mu.Unlock()
	return content
}

func (a *App) invalidateWorkspaceMapCache(cfg ConfigState) {
	root, err := workspaceRoot(cfg)
	if err != nil {
		return
	}
	key := workspaceMapCacheKey(root)
	a.workspaceCaches.mapMu.Lock()
	delete(a.workspaceCaches.mapCache, key)
	a.workspaceCaches.mapMu.Unlock()
}

func workspaceMapCacheKey(root string) string {
	key := filepath.Clean(root)
	if goruntime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	return key
}

type workspaceMapEntry struct {
	Path      string
	Dir       bool
	Size      int64
	MoreFiles int // >0 时渲染为 "+N more files" 折叠占位
}

type workspaceMapBuildResult struct {
	Entries        []workspaceMapEntry
	Truncated      bool
	SkippedDepth   int
	SkippedIgnored int
	SkippedHeavy   int
	SkippedLimit   int
	Source         string // "rg"（默认）或 "walkdir"（回退）
}

type workspacePathIndex struct {
	Entries       []workspacePathIndexedEntry
	GeneratedAt   time.Time
	BuildDuration time.Duration
	Version       int64
	Truncated     bool
	Source        string
}

type workspacePathIndexedEntry struct {
	Path      string
	Name      string
	LowerPath string
	LowerName string
	Dir       bool
}

type workspacePathCandidate struct {
	Entry workspacePathIndexedEntry
	Score int
	Pos   int
}

type workspacePathIndexBuilder struct {
	entries   []workspacePathIndexedEntry
	seen      map[string]struct{}
	truncated bool
}

func (a *App) searchWorkspacePaths(cfg ConfigState, req WorkspacePathSearchRequest) (WorkspacePathSearchResult, error) {
	root, err := workspaceRoot(cfg)
	if err != nil {
		return WorkspacePathSearchResult{}, err
	}
	limit := req.Limit
	if limit <= 0 {
		limit = workspacePathSearchDefaultLimit
	}
	if limit > workspacePathSearchMaxLimit {
		limit = workspacePathSearchMaxLimit
	}
	index, err := a.workspacePathIndex(root, req.Force)
	if err != nil {
		return WorkspacePathSearchResult{}, err
	}
	query := strings.TrimSpace(strings.TrimPrefix(req.Query, "@"))
	query = strings.Trim(query, "\"'")
	lowerQuery := strings.ToLower(filepath.ToSlash(query))
	if lowerQuery == "" {
		count := len(index.Entries)
		if count > limit {
			count = limit
		}
		entries := make([]WorkspacePathEntry, 0, count)
		for _, entry := range index.Entries[:count] {
			entries = append(entries, WorkspacePathEntry{Path: entry.Path, Name: entry.Name, Dir: entry.Dir})
		}
		return WorkspacePathSearchResult{
			Entries:       entries,
			Count:         len(index.Entries),
			Total:         len(index.Entries),
			Truncated:     index.Truncated || len(index.Entries) > len(entries),
			IndexVersion:  index.Version,
			IndexedAt:     index.GeneratedAt.Format(time.RFC3339),
			BuildDuration: index.BuildDuration.Milliseconds(),
			Source:        index.Source,
		}, nil
	}

	candidateCap := min(max(limit*8, 64), 512)
	candidates := make([]workspacePathCandidate, 0, min(candidateCap, len(index.Entries)))
	count := 0
	for pos, entry := range index.Entries {
		score, ok := workspacePathMatchScore(entry, lowerQuery)
		if !ok {
			continue
		}
		count++
		candidate := workspacePathCandidate{Entry: entry, Score: score, Pos: pos}
		if len(candidates) < candidateCap {
			candidates = append(candidates, candidate)
			continue
		}
		worst := 0
		for i := 1; i < len(candidates); i++ {
			if workspacePathCandidateLess(candidates[worst], candidates[i]) {
				worst = i
			}
		}
		if workspacePathCandidateLess(candidate, candidates[worst]) {
			candidates[worst] = candidate
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return workspacePathCandidateLess(candidates[i], candidates[j]) })

	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	entries := make([]WorkspacePathEntry, 0, len(candidates))
	for _, candidate := range candidates {
		entry := candidate.Entry
		entries = append(entries, WorkspacePathEntry{Path: entry.Path, Name: entry.Name, Dir: entry.Dir})
	}
	return WorkspacePathSearchResult{
		Entries:       entries,
		Count:         count,
		Total:         len(index.Entries),
		Truncated:     index.Truncated || count > len(entries),
		IndexVersion:  index.Version,
		IndexedAt:     index.GeneratedAt.Format(time.RFC3339),
		BuildDuration: index.BuildDuration.Milliseconds(),
		Source:        index.Source,
	}, nil
}

func workspacePathCandidateLess(a, b workspacePathCandidate) bool {
	if a.Score != b.Score {
		return a.Score < b.Score
	}
	if a.Entry.Dir != b.Entry.Dir {
		return a.Entry.Dir
	}
	if len(a.Entry.Path) != len(b.Entry.Path) {
		return len(a.Entry.Path) < len(b.Entry.Path)
	}
	return a.Pos < b.Pos
}

func workspacePathMatchScore(entry workspacePathIndexedEntry, query string) (int, bool) {
	if query == "" {
		if entry.Dir {
			return 10, true
		}
		return 20, true
	}
	if strings.HasPrefix(entry.LowerName, query) {
		return 0, true
	}
	if strings.HasPrefix(entry.LowerPath, query) {
		return 1, true
	}
	if strings.Contains(entry.LowerPath, "/"+query) {
		return 2, true
	}
	for _, part := range strings.Split(entry.LowerPath, "/") {
		if strings.HasPrefix(part, query) {
			return 3, true
		}
	}
	return 0, false
}

func (a *App) workspacePathIndex(root string, force bool) (*workspacePathIndex, error) {
	key := workspaceMapCacheKey(root)
	for {
		a.workspaceCaches.pathMu.Lock()
		cached := a.workspaceCaches.pathCache[key]
		if !force && cached != nil {
			if time.Since(cached.GeneratedAt) >= workspacePathIndexRefreshTTL(cached) && !isBroadWorkspacePathRoot(root) {
				if _, ok := a.workspaceCaches.pathBuilds[key]; !ok {
					waitCh := make(chan struct{})
					a.workspaceCaches.pathBuilds[key] = waitCh
					go a.rebuildWorkspacePathIndex(root, key, waitCh)
				}
			}
			a.workspaceCaches.pathMu.Unlock()
			return cached, nil
		}
		if waitCh, ok := a.workspaceCaches.pathBuilds[key]; ok {
			a.workspaceCaches.pathMu.Unlock()
			<-waitCh
			force = false
			continue
		}
		waitCh := make(chan struct{})
		a.workspaceCaches.pathBuilds[key] = waitCh
		a.workspaceCaches.pathMu.Unlock()

		index, err := a.buildWorkspacePathIndex(root)

		a.workspaceCaches.pathMu.Lock()
		a.finishWorkspacePathIndexBuildLocked(key, index, err)
		close(waitCh)
		a.workspaceCaches.pathMu.Unlock()
		return index, err
	}
}

func (a *App) rebuildWorkspacePathIndex(root, key string, waitCh chan struct{}) {
	index, err := a.buildWorkspacePathIndex(root)
	a.workspaceCaches.pathMu.Lock()
	a.finishWorkspacePathIndexBuildLocked(key, index, err)
	close(waitCh)
	a.workspaceCaches.pathMu.Unlock()
}

func (a *App) finishWorkspacePathIndexBuildLocked(key string, index *workspacePathIndex, err error) {
	delete(a.workspaceCaches.pathBuilds, key)
	if err == nil && index != nil {
		a.workspaceCaches.pathVersion++
		index.Version = a.workspaceCaches.pathVersion
		a.workspaceCaches.pathCache[key] = index
	}
}

func workspacePathIndexRefreshTTL(index *workspacePathIndex) time.Duration {
	if index != nil && index.Truncated {
		return workspacePathTruncatedRefreshTTL
	}
	return workspacePathIndexTTL
}

func isBroadWorkspacePathRoot(root string) bool {
	clean := filepath.Clean(strings.TrimSpace(root))
	if clean == "." || clean == string(filepath.Separator) || filepath.Dir(clean) == clean {
		return true
	}
	if home, err := os.UserHomeDir(); err == nil && samePath(clean, home) {
		return true
	}
	vol := filepath.VolumeName(clean)
	rest := strings.Trim(strings.TrimPrefix(clean, vol), `\/`)
	if rest == "" {
		return true
	}
	parts := strings.FieldsFunc(rest, func(r rune) bool { return r == '/' || r == '\\' })
	return goruntime.GOOS == "windows" && len(parts) <= 1
}

func (a *App) buildWorkspacePathIndex(root string) (*workspacePathIndex, error) {
	started := time.Now()
	baseCtx := a.ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	ctx, cancel := context.WithTimeout(baseCtx, workspacePathIndexBuildTimeout)
	defer cancel()
	builder := newWorkspacePathIndexBuilder()
	source, truncated, err := workspacePathFilesWithRipgrep(ctx, root, builder.addFile)
	if err != nil && ctx.Err() == nil {
		builder = newWorkspacePathIndexBuilder()
		source, truncated, err = workspacePathFilesWithWalkDir(ctx, root, builder.addFile)
	}
	if err != nil && len(builder.entries) == 0 {
		return nil, err
	}
	if err != nil {
		truncated = true
	}
	if builder.truncated {
		truncated = true
	}
	if source == "" {
		source = "partial"
	}
	entries := builder.entries
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Dir != entries[j].Dir {
			return entries[i].Dir
		}
		return entries[i].LowerPath < entries[j].LowerPath
	})
	return &workspacePathIndex{Entries: entries, GeneratedAt: time.Now(), BuildDuration: time.Since(started), Truncated: truncated, Source: source}, nil
}

func newWorkspacePathIndexBuilder() *workspacePathIndexBuilder {
	return &workspacePathIndexBuilder{
		entries: make([]workspacePathIndexedEntry, 0, 1024),
		seen:    make(map[string]struct{}, 1024),
	}
}

func (b *workspacePathIndexBuilder) addFile(rel string) bool {
	rel = strings.Trim(strings.TrimSpace(filepath.ToSlash(rel)), "/")
	if rel == "" || rel == "." {
		return true
	}
	if !b.add(rel, false) {
		return false
	}
	parts := strings.Split(rel, "/")
	if len(parts) <= 1 {
		return true
	}
	cur := ""
	for i := 0; i < len(parts)-1; i++ {
		if cur == "" {
			cur = parts[i]
		} else {
			cur += "/" + parts[i]
		}
		if !b.add(cur, true) {
			return false
		}
	}
	return true
}

func (b *workspacePathIndexBuilder) add(rel string, dir bool) bool {
	rel = strings.Trim(strings.TrimSpace(filepath.ToSlash(rel)), "/")
	if rel == "" || rel == "." {
		return true
	}
	key := rel
	if goruntime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	if _, ok := b.seen[key]; ok {
		return true
	}
	if len(b.entries) >= workspacePathIndexMaxEntries {
		b.truncated = true
		return false
	}
	b.seen[key] = struct{}{}
	name := path.Base(rel)
	b.entries = append(b.entries, workspacePathIndexedEntry{
		Path:      rel,
		Name:      name,
		LowerPath: strings.ToLower(rel),
		LowerName: strings.ToLower(name),
		Dir:       dir,
	})
	return true
}

func workspacePathFilesWithRipgrep(ctx context.Context, root string, add func(string) bool) (string, bool, error) {
	rgPath, err := grep.Find()
	if err != nil {
		return "", false, err
	}
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	args := []string{"--files", "--hidden", "-g", "!.git", "-g", "!.git/**"}
	for _, dir := range workspacePathIndexIgnoredDirs() {
		args = append(args, "-g", "!"+dir+"/**", "-g", "!**/"+dir+"/**")
	}
	cmd := exec.CommandContext(runCtx, rgPath, args...)
	cmd.Dir = root
	hideCommandWindow(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", false, err
	}
	stderr := &limitedBuffer{limit: 8 * 1024}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return "", false, err
	}
	truncated := false
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		p := strings.TrimSpace(scanner.Text())
		if p == "" {
			continue
		}
		if !add(p) {
			truncated = true
			stop()
			break
		}
	}
	scanErr := scanner.Err()
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return "rg", true, ctx.Err()
	}
	if scanErr != nil && !truncated {
		return "", truncated, scanErr
	}
	if waitErr != nil && !truncated {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return "", truncated, fmt.Errorf("rg --files failed: %s", msg)
		}
		return "", truncated, waitErr
	}
	return "rg", truncated, nil
}

func workspacePathFilesWithWalkDir(ctx context.Context, root string, add func(string) bool) (string, bool, error) {
	truncated := false
	err := filepath.WalkDir(root, func(absPath string, d fs.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if walkErr != nil || absPath == root {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if isWorkspaceMapHeavyDir(name) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, absPath)
		if err == nil && !add(rel) {
			truncated = true
			return fs.SkipAll
		}
		return nil
	})
	return "walkdir", truncated, err
}

func workspacePathIndexIgnoredDirs() []string {
	return []string{".git", "node_modules", "dist", "build", "target", ".next", ".nuxt", ".svelte-kit", "vendor", "__pycache__"}
}

// workspaceMapIgnoredDirs 是 Workspace Map 的 rg 排除目录列表：除重目录外
// 追加常见依赖/缓存目录，防止依赖库吃光 320 条配额。
func workspaceMapIgnoredDirs() []string {
	return []string{
		".git", "node_modules", "dist", "build", "target", ".next", ".nuxt", ".svelte-kit", "vendor", "__pycache__",
		".venv", "venv", ".cache", ".pytest_cache", ".mypy_cache", ".ruff_cache", ".turbo", ".parcel-cache", ".vite", "coverage",
		"site-packages", "bower_components", ".pnpm-store", "go/pkg/mod", "jars", "lib64", "packages/*/dist", "*.egg-info",
	}
}

func buildWorkspaceMapContext(root string) string {
	result := buildWorkspaceMap(root, workspaceMapDepth, workspaceMapLimit)
	if len(result.Entries) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("# Workspace Map\n\n")
	b.WriteString("Bounded hidden workspace map: file paths with byte sizes; contents never included.\n")
	b.WriteString("Each file shows its size (B/KB/MB); directories show no size. \"+N more files\" means that directory has N more entries beyond the per-directory budget.\n")
	b.WriteString("Root: " + filepath.ToSlash(root) + "\n")
	b.WriteString(fmt.Sprintf("Limits: depth=%d entries=%d truncated=%t\n", workspaceMapDepth, workspaceMapLimit, result.Truncated))
	if result.Source != "" {
		b.WriteString("Source: " + result.Source + "\n")
	}

	if stack := detectWorkspaceStack(root); len(stack) > 0 {
		b.WriteString("Detected stack: " + strings.Join(stack, ", ") + "\n")
	}
	if keyFiles := detectWorkspaceKeyFiles(root); len(keyFiles) > 0 {
		b.WriteString("Key files: " + strings.Join(keyFiles, ", ") + "\n")
	}
	// rg 路径下 ignored/heavy 由 rg 自行处理（.gitignore/.ignore/-g 黑名单/超大文件），
	// 不计数，避免误导 AI 以为没有跳过任何文件。depth/limit 是消费端计数。
	if result.Source == "walkdir" {
		if result.SkippedIgnored > 0 || result.SkippedHeavy > 0 || result.SkippedDepth > 0 || result.SkippedLimit > 0 {
			b.WriteString(fmt.Sprintf("Skipped: ignored=%d heavy=%d depth=%d limit=%d\n", result.SkippedIgnored, result.SkippedHeavy, result.SkippedDepth, result.SkippedLimit))
		}
	} else {
		if result.SkippedDepth > 0 || result.SkippedLimit > 0 {
			b.WriteString(fmt.Sprintf("Skipped: depth=%d limit=%d (ignored/heavy/excluded files are filtered by ripgrep, not counted here)\n", result.SkippedDepth, result.SkippedLimit))
		}
	}

	b.WriteString("\nTree:\n")
	b.WriteString(".\n")
	for _, entry := range result.Entries {
		depth := strings.Count(entry.Path, "/")
		name := path.Base(entry.Path)
		if entry.Dir {
			name += "/"
		}
		b.WriteString(strings.Repeat("  ", depth))
		b.WriteString("- ")
		if entry.MoreFiles > 0 {
			b.WriteString(fmt.Sprintf("+%d more files", entry.MoreFiles))
		} else {
			b.WriteString(name)
			// 文件一律显示大小（0 字节显示 "0 B"），目录不显示。
			if !entry.Dir {
				b.WriteString("  " + formatMapFileSize(entry.Size))
			}
		}
		b.WriteString("\n")
	}
	b.WriteString("\nUse read for file contents only when needed.\n")
	return b.String()
}

// formatMapFileSize 把字节数格式化成紧凑可读形式（B/KB/MB），与前端
// formatBytes 保持一致；非文件条目（目录/折叠行）不显示。
func formatMapFileSize(bytes int64) string {
	switch {
	case bytes < 1024:
		return fmt.Sprintf("%d B", bytes)
	case bytes < 1024*1024:
		return fmt.Sprintf("%.0f KB", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
	}
}

const (
	workspaceMapDirBudget = 50
	// workspaceMapScanTimeout 限制 rg 扫描时长：超时回退 WalkDir，避免
	// 网络盘/病态文件系统让首次消息无限等待（rg 进程同步阻塞 buildMessages）。
	workspaceMapScanTimeout = 10 * time.Second
)

// buildWorkspaceMap 优先用 ripgrep 生成 Workspace Map；rg 不可用或运行失败
// （启动错误/非零退出/超时）时回退 WalkDir。两条路径应用相同的过滤语义
// （.env 敏感文件、重目录、gitignore、深度）、每目录折叠预算与全局 320 条
// 硬停，保证输出结构一致。
func buildWorkspaceMap(root string, maxDepth, limit int) workspaceMapBuildResult {
	if maxDepth <= 0 {
		maxDepth = workspaceMapDepth
	}
	if limit <= 0 {
		limit = workspaceMapLimit
	}
	if result, ok := buildWorkspaceMapWithRg(root, maxDepth, limit); ok {
		return result
	}
	return buildWorkspaceMapWalkDir(root, maxDepth, limit)
}

// buildWorkspaceMapWithRg 用 `rg --files` 扫描；rg 缺失/启动失败/非零退出/超时
// 返回 ok=false 让调用方回退 WalkDir；硬停（全局 limit 满）是正常结束。
func buildWorkspaceMapWithRg(root string, maxDepth, limit int) (workspaceMapBuildResult, bool) {
	rgPath, err := grep.Find()
	if err != nil {
		return workspaceMapBuildResult{}, false
	}
	runCtx, cancel := context.WithTimeout(context.Background(), workspaceMapScanTimeout)
	defer cancel()

	// --sort path 保证确定性（rg 并行遍历默认顺序不定，决定哪些条目存活于
	// 320 截断/折叠）。.env 敏感文件不在 rg 参数层排除：rg 的 -g 包含模式
	// 会变成白名单（只输出匹配文件），所以交给消费端 isWorkspaceMapSensitiveFile
	// 过滤，与 walkdir 回退路径语义完全一致（.env/.env.* 排除，模板保留）。
	args := []string{"--files", "--hidden", "--no-require-git", "--sort", "path",
		"--max-filesize", "1M",
		"--iglob", "!.git", "--iglob", "!.git/**"}
	for _, dir := range workspaceMapIgnoredDirs() {
		args = append(args, "--iglob", "!"+dir+"/**", "--iglob", "!**/"+dir+"/**")
	}
	cmd := exec.CommandContext(runCtx, rgPath, args...)
	cmd.Dir = root
	hideCommandWindow(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return workspaceMapBuildResult{}, false
	}
	cmd.Stderr = &limitedBuffer{limit: 8 * 1024}
	if err := cmd.Start(); err != nil {
		return workspaceMapBuildResult{}, false
	}

	result := workspaceMapBuildResult{Entries: make([]workspaceMapEntry, 0, min(limit, 64)), Source: "rg"}
	budget := newWorkspaceMapDirBudgets()
	truncated := false
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		rel := strings.TrimSpace(scanner.Text())
		if rel == "" {
			continue
		}
		if !buildWorkspaceMapAdd(&result, budget, root, rel, maxDepth, limit) {
			// 全局 limit 满：标记 truncated 并硬停（result.Truncated 必须设置，
			// 否则渲染层显示 truncated=false 误导 AI）。
			result.Truncated = true
			result.SkippedLimit++
			truncated = true
			cancel() // 杀掉 rg
			break
		}
	}
	// 硬停（truncated）是正常结束；其余错误/超时统一回退 WalkDir，
	// 避免把残缺 map 缓存 30 秒。
	if !truncated && (runCtx.Err() != nil || scanner.Err() != nil || cmd.Wait() != nil) {
		return workspaceMapBuildResult{}, false
	}
	sortWorkspaceMapEntries(&result)
	return result, true
}

// workspaceMapDirBudgets 跟踪每个父目录已展开的直接子项数，用于每目录预算折叠。
type workspaceMapDirBudgets struct {
	counts map[string]int
}

func newWorkspaceMapDirBudgets() *workspaceMapDirBudgets {
	return &workspaceMapDirBudgets{counts: map[string]int{}}
}

// parentDir 返回 rel 的直接父目录路径（不含叶子名），根的直接子项父目录为 ""。
func parentDir(rel string) string {
	idx := strings.LastIndex(rel, "/")
	if idx < 0 {
		return ""
	}
	return rel[:idx]
}

// buildWorkspaceMapAdd 处理单个 rg 输出的文件路径：先插入其所有父目录节点
// （仅 maxDepth 内的目录，避免深层目录链吃光配额），文件本身超过 maxDepth
// 时跳过；达到全局 limit 时返回 false 触发硬停。
func buildWorkspaceMapAdd(result *workspaceMapBuildResult, budget *workspaceMapDirBudgets, root, rel string, maxDepth, limit int) bool {
	rel = strings.Trim(strings.TrimSpace(filepath.ToSlash(rel)), "/")
	if rel == "" || rel == "." {
		return true
	}
	// 敏感文件过滤（.env/.env.*，保留 example/sample/template）：与 walkdir
	// 回退路径共用 isWorkspaceMapSensitiveFile，保证两条路径语义一致。
	if isWorkspaceMapSensitiveFile(path.Base(rel), false) {
		result.SkippedIgnored++
		return true
	}
	// 全局硬停前置：即使后续都是折叠递增（不新增条目），也必须停。
	if len(result.Entries) >= limit {
		return false
	}

	// 插入父目录节点：只插 maxDepth 内的目录（目录 depth ≤ maxDepth 保留，
	// 更深目录不插入——其文件同样会被下面的深度过滤跳过）。
	parts := strings.Split(rel, "/")
	cur := ""
	for i := 0; i < len(parts)-1; i++ {
		if cur == "" {
			cur = parts[i]
		} else {
			cur += "/" + parts[i]
		}
		if pathDepth(cur) > maxDepth {
			break
		}
		if !workspaceMapEnsureDir(result, cur, limit) {
			return false
		}
	}

	// 文件深度过滤：超过 maxDepth 的文件不展开（目录节点已插入）
	if pathDepth(rel) > maxDepth {
		result.SkippedDepth++
		return true
	}
	return workspaceMapAddFile(result, budget, root, rel, limit)
}

// workspaceMapEnsureDir 插入目录节点（若尚未插入）。目录本身不计入子项预算，
// 只保证树结构完整；达到全局 limit 时返回 false。
func workspaceMapEnsureDir(result *workspaceMapBuildResult, rel string, limit int) bool {
	for _, e := range result.Entries {
		if e.Dir && strings.EqualFold(e.Path, rel) {
			return true
		}
	}
	if len(result.Entries) >= limit {
		return false
	}
	result.Entries = append(result.Entries, workspaceMapEntry{Path: rel, Dir: true})
	return true
}

// workspaceMapAddFile 追加一个文件条目；同一父目录下已展开的直接子项数超过
// workspaceMapDirBudget 后，后续直接子项合并为一个 "+N more files" 占位条目
// （不 stat、不新增条目，但全局 limit 仍生效）。返回 false 表示全局已满。
func workspaceMapAddFile(result *workspaceMapBuildResult, budget *workspaceMapDirBudgets, root, rel string, limit int) bool {
	parent := parentDir(rel)
	count := budget.counts[parent]
	if count >= workspaceMapDirBudget {
		// 折叠：即使不新增条目，全局配额也必须触发硬停。
		if len(result.Entries) >= limit {
			return false
		}
		// 占位条目按 MoreFiles 标记查找（真实文件即使叫 "+more" 也不会被劫持）。
		for i := len(result.Entries) - 1; i >= 0; i-- {
			e := result.Entries[i]
			if e.MoreFiles > 0 && parentDir(e.Path) == parent {
				result.Entries[i].MoreFiles++
				budget.counts[parent]++
				return true
			}
		}
		placeholder := parent + "/+more"
		if parent == "" {
			placeholder = "+more"
		}
		result.Entries = append(result.Entries, workspaceMapEntry{Path: placeholder, Dir: false, MoreFiles: 1})
		budget.counts[parent]++
		return true
	}
	budget.counts[parent]++
	if len(result.Entries) >= limit {
		return false
	}
	// 只有真正追加真实条目才 stat（折叠路径不 stat，避免大目录 10 万次 lstat）。
	size := int64(0)
	if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
		size = info.Size()
	}
	result.Entries = append(result.Entries, workspaceMapEntry{Path: rel, Dir: false, Size: size})
	return true
}

// workspaceMapSortKey 返回条目排序键。折叠占位条目（MoreFiles>0）用
// "父目录/\uffff" 作为键：\uffff 是最大码位，保证它排在同目录所有真实
// 条目之后（internal/app/+more 排在 internal/app/app.go 之后），但仍在
// 目录范围内（排在 internal/tools/... 之前）。根级占位（Path="+more"）
// 键为 "\uffff"，排在整个树末尾——根目录"还有 N 个文件"收尾是合理的。
func workspaceMapSortKey(e workspaceMapEntry) string {
	key := strings.ToLower(e.Path)
	if e.MoreFiles > 0 {
		parent := parentDir(key)
		if parent == "" {
			return "\uffff"
		}
		return parent + "/\uffff"
	}
	return key
}

func sortWorkspaceMapEntries(result *workspaceMapBuildResult) {
	sort.Slice(result.Entries, func(i, j int) bool {
		return workspaceMapSortKey(result.Entries[i]) < workspaceMapSortKey(result.Entries[j])
	})
}

func buildWorkspaceMapWalkDir(root string, maxDepth, limit int) workspaceMapBuildResult {
	if maxDepth <= 0 {
		maxDepth = workspaceMapDepth
	}
	if limit <= 0 {
		limit = workspaceMapLimit
	}
	rules := loadRootGitignoreRules(root)
	result := workspaceMapBuildResult{Entries: make([]workspaceMapEntry, 0, min(limit, 64)), Source: "walkdir"}
	budget := newWorkspaceMapDirBudgets()
	truncated := false

	_ = filepath.WalkDir(root, func(absPath string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if samePath(absPath, root) {
			return nil
		}

		name := d.Name()
		rel, err := filepath.Rel(root, absPath)
		if err != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)

		if d.IsDir() && isWorkspaceMapHeavyDir(name) {
			result.SkippedHeavy++
			return filepath.SkipDir
		}
		if isWorkspaceMapSensitiveFile(name, d.IsDir()) {
			result.SkippedIgnored++
			return nil
		}
		if matchGitignoreRules(rules, relSlash, d.IsDir()) {
			result.SkippedIgnored++
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if truncated {
			return fs.SkipAll
		}

		// 深度：目录超过 maxDepth 整棵剪枝（与 rg 路径一致：目录节点只插
		// maxDepth 内、更深文件不展开）。
		depth := pathDepth(rel)
		if depth > maxDepth {
			result.SkippedDepth++
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if len(result.Entries) >= limit {
			truncated = true
			result.Truncated = true
			result.SkippedLimit++
			return fs.SkipAll
		}

		if d.IsDir() {
			if !workspaceMapEnsureDir(&result, relSlash, limit) {
				truncated = true
				result.Truncated = true
				result.SkippedLimit++
				return fs.SkipAll
			}
			return nil
		}
		if !workspaceMapAddFile(&result, budget, root, relSlash, limit) {
			truncated = true
			result.Truncated = true
			result.SkippedLimit++
			return fs.SkipAll
		}
		return nil
	})

	sortWorkspaceMapEntries(&result)
	return result
}

func detectWorkspaceStack(root string) []string {
	type marker struct {
		label string
		paths []string
	}
	markers := []marker{
		{"Go", []string{"go.mod"}},
		{"Wails", []string{"wails.json"}},
		{"Node", []string{"package.json", "frontend/package.json"}},
		{"Vue", []string{"frontend/src/App.vue", "src/App.vue"}},
		{"Vite", []string{"vite.config.js", "vite.config.ts", "frontend/vite.config.js", "frontend/vite.config.ts"}},
		{"TypeScript", []string{"tsconfig.json", "frontend/tsconfig.json"}},
		{"Python", []string{"pyproject.toml", "requirements.txt"}},
		{"Rust", []string{"Cargo.toml"}},
		{"Docker", []string{"Dockerfile", "docker-compose.yml", "compose.yml"}},
	}

	var stack []string
	for _, marker := range markers {
		for _, rel := range marker.paths {
			if fileOrDirExists(filepath.Join(root, filepath.FromSlash(rel))) {
				stack = append(stack, marker.label)
				break
			}
		}
	}
	return stack
}

func detectWorkspaceKeyFiles(root string) []string {
	candidates := []string{
		"AGENTS.md", "CLAUDE.md", "README.md",
		"go.mod", "go.sum", "wails.json",
		"package.json", "frontend/package.json",
		"vite.config.js", "vite.config.ts", "frontend/vite.config.js", "frontend/vite.config.ts",
		"tsconfig.json", "frontend/tsconfig.json",
		"pyproject.toml", "Cargo.toml", "Dockerfile",
		".gitignore",
	}

	var files []string
	for _, rel := range candidates {
		if fileOrDirExists(filepath.Join(root, filepath.FromSlash(rel))) {
			files = append(files, rel)
		}
	}
	return files
}

func fileOrDirExists(absPath string) bool {
	_, err := os.Stat(absPath)
	return err == nil
}

// ── Workspace map / gitignore helpers ────────────────────────

func pathDepth(rel string) int {
	rel = filepath.Clean(rel)
	if rel == "." || rel == "" {
		return 0
	}
	return strings.Count(rel, string(os.PathSeparator)) + 1
}

// 名单刻意保持极小：只兜底「任何项目都不该让模型看到」的目录。
// 其余忽略语义交给工作区 .gitignore（见 listFilesWithConfig），
// 与 Trae LS 的行为一致。文件浏览器通过 includeIgnored=true 保留全部内容。
func isHeavyDir(name string) bool {
	switch strings.ToLower(name) {
	case "__pycache__", "node_modules":
		return true
	default:
		return false
	}
}

// isVCSDirName centralizes VCS-internal directory names. They are never
// useful in model-facing list_files output and are pruned unconditionally
// there (see listFilesWithConfig), while the UI explorer keeps them visible.
func isVCSDirName(name string) bool {
	switch name {
	case ".git", ".svn", ".hg":
		return true
	default:
		return false
	}
}

func isWorkspaceMapHeavyDir(name string) bool {
	if isHeavyDir(name) {
		return true
	}
	switch strings.ToLower(name) {
	case ".venv", "venv", ".cache", ".pytest_cache", ".mypy_cache", ".ruff_cache", ".turbo", ".parcel-cache", ".vite", "coverage":
		return true
	default:
		return false
	}
}

func isWorkspaceMapSensitiveFile(name string, isDir bool) bool {
	if isDir {
		return false
	}
	lower := strings.ToLower(name)
	if lower == ".env" || strings.HasPrefix(lower, ".env.") {
		return !strings.Contains(lower, "example") && !strings.Contains(lower, "sample") && !strings.Contains(lower, "template")
	}
	return false
}

func loadRootGitignoreRules(root string) gitignore.Matcher {
	data, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	patterns := make([]gitignore.Pattern, 0, len(lines))
	for _, raw := range lines {
		line := strings.TrimSuffix(raw, "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Keep escaped comment markers literal while letting the library handle
		// negation, directory rules, globs, and **.
		line = strings.ReplaceAll(line, `\#`, "#")
		patterns = append(patterns, gitignore.ParsePattern(line, nil))
	}
	if len(patterns) == 0 {
		return nil
	}
	return gitignore.NewMatcher(patterns)
}

func matchGitignoreRules(matcher gitignore.Matcher, relPath string, isDir bool) bool {
	if matcher == nil {
		return false
	}
	relPath = strings.Trim(filepath.ToSlash(relPath), "/")
	if relPath == "" {
		return false
	}
	return matcher.Match(strings.Split(relPath, "/"), isDir)
}

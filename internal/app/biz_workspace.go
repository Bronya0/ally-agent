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
	"time"

	"ally-dev/internal/tools/grep"
)

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
	if !insideRoot(root, start) {
		if blocked, reason := isDangerousSearchRoot(start); blocked {
			return ListFilesResult{}, codedToolError("E_SEARCH_ROOT_BLOCKED", fmt.Errorf("%s\n\nThis listing has been blocked for safety. Specify a narrower project subdirectory or explicit file path.", reason))
		}
	}

	maxDepth := req.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 3
	}
	limit := req.Limit
	if limit <= 0 || limit > 1000 {
		limit = 200
	}

	entries := []FileEntry{}
	truncated := false
	err = filepath.WalkDir(start, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if path == start {
			return nil
		}
		name := d.Name()
		if !req.IncludeHidden && strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !req.IncludeIgnored && d.IsDir() && isHeavyDir(name) {
			return filepath.SkipDir
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
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		entries = append(entries, FileEntry{
			Path:    grep.DisplayPathForRoot(root, path),
			Name:    name,
			Dir:     d.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime().Format(time.RFC3339),
		})
		return nil
	})
	if err != nil {
		return ListFilesResult{}, err
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Dir != entries[j].Dir {
			return entries[i].Dir
		}
		return strings.ToLower(entries[i].Path) < strings.ToLower(entries[j].Path)
	})
	return ListFilesResult{Entries: entries, Count: len(entries), Truncated: truncated}, nil
}

func (a *App) workspaceMapContext(cfg ConfigState) string {
	root, err := workspaceRoot(cfg)
	if err != nil {
		return ""
	}
	key := workspaceMapCacheKey(root)

	a.workspaceMapMu.Lock()
	cached, ok := a.workspaceMapCache[key]
	if ok && time.Since(cached.generatedAt) < workspaceMapTTL {
		content := cached.content
		a.workspaceMapMu.Unlock()
		return content
	}
	a.workspaceMapMu.Unlock()

	content := buildWorkspaceMapContext(root)

	a.workspaceMapMu.Lock()
	a.workspaceMapCache[key] = workspaceMapCacheEntry{content: content, generatedAt: time.Now()}
	a.workspaceMapMu.Unlock()
	return content
}

func (a *App) invalidateWorkspaceMapCache(cfg ConfigState) {
	root, err := workspaceRoot(cfg)
	if err != nil {
		return
	}
	key := workspaceMapCacheKey(root)
	a.workspaceMapMu.Lock()
	delete(a.workspaceMapCache, key)
	a.workspaceMapMu.Unlock()
}

func workspaceMapCacheKey(root string) string {
	key := filepath.Clean(root)
	if goruntime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	return key
}

type workspaceMapEntry struct {
	Path string
	Dir  bool
}

type workspaceMapBuildResult struct {
	Entries        []workspaceMapEntry
	Truncated      bool
	SkippedDepth   int
	SkippedIgnored int
	SkippedHeavy   int
	SkippedLimit   int
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
		a.workspacePathMu.Lock()
		cached := a.workspacePathCache[key]
		if !force && cached != nil {
			if time.Since(cached.GeneratedAt) >= workspacePathIndexRefreshTTL(cached) && !isBroadWorkspacePathRoot(root) {
				if _, ok := a.workspacePathBuilds[key]; !ok {
					waitCh := make(chan struct{})
					a.workspacePathBuilds[key] = waitCh
					go a.rebuildWorkspacePathIndex(root, key, waitCh)
				}
			}
			a.workspacePathMu.Unlock()
			return cached, nil
		}
		if waitCh, ok := a.workspacePathBuilds[key]; ok {
			a.workspacePathMu.Unlock()
			<-waitCh
			force = false
			continue
		}
		waitCh := make(chan struct{})
		a.workspacePathBuilds[key] = waitCh
		a.workspacePathMu.Unlock()

		index, err := a.buildWorkspacePathIndex(root)

		a.workspacePathMu.Lock()
		a.finishWorkspacePathIndexBuildLocked(key, index, err)
		close(waitCh)
		a.workspacePathMu.Unlock()
		return index, err
	}
}

func (a *App) rebuildWorkspacePathIndex(root, key string, waitCh chan struct{}) {
	index, err := a.buildWorkspacePathIndex(root)
	a.workspacePathMu.Lock()
	a.finishWorkspacePathIndexBuildLocked(key, index, err)
	close(waitCh)
	a.workspacePathMu.Unlock()
}

func (a *App) finishWorkspacePathIndexBuildLocked(key string, index *workspacePathIndex, err error) {
	delete(a.workspacePathBuilds, key)
	if err == nil && index != nil {
		a.workspacePathVersion++
		index.Version = a.workspacePathVersion
		a.workspacePathCache[key] = index
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

func buildWorkspaceMapContext(root string) string {
	result := buildWorkspaceMap(root, workspaceMapDepth, workspaceMapLimit)
	if len(result.Entries) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("# Workspace Map\n\n")
	b.WriteString("This is a bounded hidden workspace map. It contains paths only, not file contents.\n")
	b.WriteString("Root: " + filepath.ToSlash(root) + "\n")
	b.WriteString(fmt.Sprintf("Limits: depth=%d entries=%d truncated=%t\n", workspaceMapDepth, workspaceMapLimit, result.Truncated))

	if stack := detectWorkspaceStack(root); len(stack) > 0 {
		b.WriteString("Detected stack: " + strings.Join(stack, ", ") + "\n")
	}
	if keyFiles := detectWorkspaceKeyFiles(root); len(keyFiles) > 0 {
		b.WriteString("Key files: " + strings.Join(keyFiles, ", ") + "\n")
	}
	if result.SkippedIgnored > 0 || result.SkippedHeavy > 0 || result.SkippedDepth > 0 || result.SkippedLimit > 0 {
		b.WriteString(fmt.Sprintf("Skipped: ignored=%d heavy=%d depth=%d limit=%d\n", result.SkippedIgnored, result.SkippedHeavy, result.SkippedDepth, result.SkippedLimit))
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
		b.WriteString(name)
		b.WriteString("\n")
	}
	b.WriteString("\nUse read for file contents only when needed.\n")
	return b.String()
}

func buildWorkspaceMap(root string, maxDepth, limit int) workspaceMapBuildResult {
	if maxDepth <= 0 {
		maxDepth = workspaceMapDepth
	}
	if limit <= 0 {
		limit = workspaceMapLimit
	}
	rules := loadRootGitignoreRules(root)
	result := workspaceMapBuildResult{Entries: make([]workspaceMapEntry, 0, min(limit, 64))}

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

		depth := pathDepth(rel)
		if depth > maxDepth {
			result.SkippedDepth++
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if len(result.Entries) >= limit {
			result.Truncated = true
			result.SkippedLimit++
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		result.Entries = append(result.Entries, workspaceMapEntry{Path: relSlash, Dir: d.IsDir()})
		return nil
	})

	sort.Slice(result.Entries, func(i, j int) bool {
		return strings.ToLower(result.Entries[i].Path) < strings.ToLower(result.Entries[j].Path)
	})
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

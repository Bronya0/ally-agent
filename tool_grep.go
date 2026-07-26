package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

func (a *App) GrepFiles(req GrepRequest) (*GrepResult, error) {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return a.grepFilesWithConfig(ctx, a.effectiveConfig(ConfigState{}), req)
}

func (a *App) grepFilesWithConfig(ctx context.Context, cfg ConfigState, req GrepRequest) (*GrepResult, error) {
	root, err := workspaceRoot(cfg)
	if err != nil {
		return nil, codedToolError("E_GREP_WORKSPACE", err)
	}
	if strings.TrimSpace(req.Pattern) == "" {
		return nil, codedToolError("E_GREP_BAD_PATTERN", errors.New("grep_files requires a non-empty pattern"))
	}
	searchRoot := root
	if strings.TrimSpace(req.Path) != "" {
		searchRoot, err = resolveReadablePath(cfg, req.Path)
		if err != nil {
			return nil, codedToolError("E_GREP_PATH", err)
		}
	}
	if _, err := os.Stat(searchRoot); err != nil {
		return nil, codedToolError("E_GREP_PATH", err)
	}

	// Safety: block broad/system searches only outside the selected workspace.
	if !insideRoot(root, searchRoot) {
		if blocked, reason := isDangerousSearchRoot(searchRoot); blocked {
			return nil, codedToolError("E_SEARCH_ROOT_BLOCKED", fmt.Errorf("%s\n\nThis search has been blocked for safety. If you need to search this path, do it manually.", reason))
		}
	}

	rgPath, err := findRipgrep()
	if err != nil {
		a.emitRipgrepMissingIfNeeded()
		return nil, ripgrepMissingError()
	}

	timeoutSeconds := grepTimeoutSeconds(req)
	grepCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	result, err := grepFilesWithRipgrep(grepCtx, rgPath, root, searchRoot, req)
	if err != nil {
		return nil, normalizeGrepError(err, timeoutSeconds)
	}
	return result, nil
}

func grepFilesWithRipgrep(ctx context.Context, rgPath, root, searchRoot string, req GrepRequest) (*GrepResult, error) {
	maxDepth, maxFiles, maxMatches := grepLimits(req)

	lineCount, fileCount, err := ripgrepCount(ctx, rgPath, root, searchRoot, req, maxDepth, false)
	if err != nil {
		return nil, err
	}
	if lineCount == 0 {
		return &GrepResult{Matches: []GrepMatch{}, Count: 0, Occurrences: 0, Files: 0, Truncated: false, SamplesTruncated: false, StatsExact: true}, nil
	}
	occurrences, _, err := ripgrepCount(ctx, rgPath, root, searchRoot, req, maxDepth, true)
	if err != nil {
		return nil, err
	}
	matches, samplesTruncated, err := ripgrepSampleMatches(ctx, rgPath, root, searchRoot, req, maxDepth, maxFiles, maxMatches)
	if err != nil {
		return nil, err
	}

	return &GrepResult{
		Matches:          matches,
		Count:            lineCount,
		Occurrences:      occurrences,
		Files:            fileCount,
		Truncated:        samplesTruncated,
		SamplesTruncated: samplesTruncated,
		StatsExact:       true,
	}, nil
}

func grepTimeoutSeconds(req GrepRequest) int {
	timeout := req.TimeoutSeconds
	if timeout <= 0 {
		return defaultGrepTimeout
	}
	if timeout > maxGrepTimeout {
		return maxGrepTimeout
	}
	return timeout
}

func grepLimits(req GrepRequest) (maxDepth, maxFiles, maxMatches int) {
	maxDepth = req.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 20
	}
	if maxDepth > 100 {
		maxDepth = 100
	}
	maxFiles = req.MaxFiles
	if maxFiles <= 0 {
		maxFiles = 50
	}
	if maxFiles > 1000 {
		maxFiles = 1000
	}
	maxMatches = req.MaxMatches
	if maxMatches <= 0 {
		maxMatches = maxFiles * 10
	}
	if maxMatches > 5000 {
		maxMatches = 5000
	}
	return maxDepth, maxFiles, maxMatches
}

func ripgrepBaseArgs(req GrepRequest, maxDepth int) []string {
	args := []string{
		"--color=never",
		"--ignore-case",
		"--max-filesize", "10M",
		"--max-depth", strconv.Itoa(maxDepth),
		"--sort", "path",
	}
	if req.IncludeIgnored {
		args = append(args, "--no-ignore")
	}
	if strings.TrimSpace(req.Glob) != "" {
		args = append(args, "-g", filepath.ToSlash(req.Glob))
	}
	for _, dir := range ripgrepExcludedDirs() {
		args = append(args, "-g", "!"+dir+"/**")
		args = append(args, "-g", "!**/"+dir+"/**")
	}
	return args
}

func ripgrepCount(ctx context.Context, rgPath, root, searchRoot string, req GrepRequest, maxDepth int, countMatches bool) (total int, files int, err error) {
	args := ripgrepBaseArgs(req, maxDepth)
	if countMatches {
		args = append(args, "--count-matches")
	} else {
		args = append(args, "--count")
	}
	args = append(args, "--with-filename", "-e", req.Pattern, searchRoot)

	cmd := exec.CommandContext(ctx, rgPath, args...)
	cmd.Dir = root
	hideCommandWindow(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, 0, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return 0, 0, err
	}
	if err := cmd.Start(); err != nil {
		return 0, 0, err
	}

	errBuf := &limitedBuffer{limit: 16 * 1024}
	var errWG sync.WaitGroup
	errWG.Add(1)
	go func() {
		defer errWG.Done()
		_, _ = io.Copy(errBuf, stderr)
	}()

	parseErr := error(nil)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		n, ok := parseRipgrepCountLine(scanner.Text())
		if !ok {
			parseErr = fmt.Errorf("could not parse rg count output: %q", scanner.Text())
			break
		}
		total += n
		files++
	}
	if err := scanner.Err(); err != nil && parseErr == nil && ctx.Err() == nil {
		parseErr = err
	}

	waitErr := cmd.Wait()
	errWG.Wait()
	if parseErr != nil {
		return 0, 0, parseErr
	}
	if ctx.Err() != nil {
		return 0, 0, ctx.Err()
	}
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			if exitErr.ExitCode() == 1 {
				return 0, 0, nil
			}
			msg := strings.TrimSpace(errBuf.String())
			if msg == "" {
				msg = waitErr.Error()
			}
			return 0, 0, ripgrepFailureError(msg)
		}
		return 0, 0, waitErr
	}
	return total, files, nil
}

func parseRipgrepCountLine(line string) (int, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return 0, false
	}
	idx := strings.LastIndex(line, ":")
	if idx >= 0 {
		line = line[idx+1:]
	}
	n, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		return 0, false
	}
	return n, true
}

func ripgrepSampleMatches(ctx context.Context, rgPath, root, searchRoot string, req GrepRequest, maxDepth, maxFiles, maxMatches int) ([]GrepMatch, bool, error) {
	args := ripgrepBaseArgs(req, maxDepth)
	args = append(args,
		"--json",
		"--line-number",
		"-e", req.Pattern,
	)
	args = append(args, searchRoot)

	cmd := exec.CommandContext(ctx, rgPath, args...)
	cmd.Dir = root
	hideCommandWindow(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, false, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, false, err
	}
	if err := cmd.Start(); err != nil {
		return nil, false, err
	}

	errBuf := &limitedBuffer{limit: 16 * 1024}
	var errWG sync.WaitGroup
	errWG.Add(1)
	go func() {
		defer errWG.Done()
		_, _ = io.Copy(errBuf, stderr)
	}()

	matches := []GrepMatch{}
	sampleFiles := map[string]bool{}
	truncated := false
	parseErr := error(nil)

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		if ctx.Err() != nil {
			break
		}
		event, ok, err := parseRipgrepMatch(scanner.Bytes(), root)
		if err != nil {
			parseErr = err
			break
		}
		if !ok {
			continue
		}
		canIncludeFile := sampleFiles[event.Path] || len(sampleFiles) < maxFiles
		canIncludeMatch := len(matches) < maxMatches
		if canIncludeFile && canIncludeMatch {
			sampleFiles[event.Path] = true
			matches = append(matches, GrepMatch{
				Path:    event.Path,
				LineNum: event.LineNum,
				Content: truncateLine(event.Content, 200),
			})
			continue
		}
		truncated = true
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		break
	}
	if err := scanner.Err(); err != nil && parseErr == nil && ctx.Err() == nil && !truncated {
		parseErr = err
	}

	waitErr := cmd.Wait()
	errWG.Wait()
	if parseErr != nil {
		return nil, false, parseErr
	}
	if ctx.Err() != nil && !truncated {
		return nil, false, ctx.Err()
	}
	if waitErr != nil && !truncated {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			if exitErr.ExitCode() != 1 {
				msg := strings.TrimSpace(errBuf.String())
				if msg == "" {
					msg = waitErr.Error()
				}
				return nil, false, ripgrepFailureError(msg)
			}
		} else {
			return nil, false, waitErr
		}
	}

	return matches, truncated, nil
}

func normalizeGrepError(err error, timeoutSeconds int) error {
	if err == nil {
		return nil
	}
	if toolErrorCode(err) != "" {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return codedToolError("E_GREP_TIMEOUT", fmt.Errorf("grep_files timed out after %ds", timeoutSeconds))
	}
	if errors.Is(err, context.Canceled) {
		return codedToolError("E_GREP_CANCELLED", errors.New("grep_files was cancelled"))
	}
	return codedToolError("E_GREP_FAILED", err)
}

func ripgrepFailureError(stderr string) error {
	msg := strings.TrimSpace(stderr)
	if msg == "" {
		msg = "unknown ripgrep failure"
	}
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "error parsing glob"):
		return codedToolError("E_GREP_GLOB", fmt.Errorf("invalid grep_files glob: %s", msg))
	case strings.Contains(lower, "regex parse error"), strings.Contains(lower, "error parsing regexp"):
		return codedToolError("E_GREP_REGEX", fmt.Errorf("invalid grep_files regex pattern: %s", msg))
	default:
		return codedToolError("E_GREP_FAILED", fmt.Errorf("rg failed: %s", msg))
	}
}

type ripgrepMatchEvent struct {
	Path        string
	LineNum     int
	Content     string
	Occurrences int
}

func parseRipgrepMatch(line []byte, root string) (ripgrepMatchEvent, bool, error) {
	var event struct {
		Type string `json:"type"`
		Data struct {
			Path struct {
				Text string `json:"text"`
			} `json:"path"`
			Lines struct {
				Text string `json:"text"`
			} `json:"lines"`
			LineNumber int `json:"line_number"`
			Submatches []struct {
				Start int `json:"start"`
				End   int `json:"end"`
			} `json:"submatches"`
		} `json:"data"`
	}
	if err := json.Unmarshal(line, &event); err != nil {
		return ripgrepMatchEvent{}, false, err
	}
	if event.Type != "match" || event.Data.Path.Text == "" {
		return ripgrepMatchEvent{}, false, nil
	}
	occurrences := len(event.Data.Submatches)
	if occurrences == 0 {
		occurrences = 1
	}
	rel := displayPathForRoot(root, event.Data.Path.Text)
	return ripgrepMatchEvent{
		Path:        rel,
		LineNum:     event.Data.LineNumber,
		Content:     strings.TrimRight(event.Data.Lines.Text, "\r\n"),
		Occurrences: occurrences,
	}, true, nil
}

func normalizeRipgrepPath(root, p string) string {
	return displayPathForRoot(root, p)
}

func displayPathForRoot(root, p string) string {
	if p == "" {
		return ""
	}
	clean := filepath.Clean(p)
	if !filepath.IsAbs(clean) {
		clean = filepath.Join(root, clean)
	}
	rel, err := filepath.Rel(root, clean)
	if err != nil || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || filepath.IsAbs(rel) {
		return filepath.ToSlash(clean)
	}
	return filepath.ToSlash(rel)
}

func displayPathForConfig(cfg ConfigState, p string) string {
	root, err := workspaceRoot(cfg)
	if err != nil {
		return filepath.ToSlash(filepath.Clean(p))
	}
	return displayPathForRoot(root, p)
}

func findRipgrep() (string, error) {
	return findRipgrepForOS(goruntime.GOOS)
}

func ripgrepCandidatesForOS(goos string) []string {
	candidates := []string{}
	if p := strings.TrimSpace(os.Getenv("ALLY_RG_PATH")); p != "" {
		candidates = append(candidates, p)
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		names := []string{"rg"}
		if goos == "windows" {
			names = []string{"rg.exe", "rg"}
		}
		for _, name := range names {
			candidates = append(candidates, filepath.Join(dir, "tools", name), filepath.Join(dir, name))
		}
	}
	if goos == "darwin" {
		candidates = append(candidates,
			"/opt/homebrew/bin/rg",
			"/usr/local/bin/rg",
		)
	}
	return candidates
}

func findRipgrepForOS(goos string) (string, error) {
	candidates := ripgrepCandidatesForOS(goos)
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return exec.LookPath("rg")
}

func ripgrepExcludedDirs() []string {
	return []string{
		".git",
		"node_modules",
		"dist",
		"build",
		"target",
		".next",
		".nuxt",
		".svelte-kit",
		"vendor",
		"__pycache__",
		".venv",
		"venv",
		".cache",
		"coverage",
	}
}

func ripgrepInstallInstructions() []string {
	return []string{
		"Windows: winget install BurntSushi.ripgrep  (or: scoop install ripgrep / choco install ripgrep)",
		"macOS: brew install ripgrep",
		"Debian/Ubuntu: sudo apt install ripgrep",
		"Fedora: sudo dnf install ripgrep",
		"Arch: sudo pacman -S ripgrep",
		"openSUSE: sudo zypper install ripgrep",
		"Alpine: sudo apk add ripgrep",
		"Rust/Cargo: cargo install ripgrep",
	}
}

func ripgrepMissingError() error {
	return codedToolError("E_RIPGREP_NOT_FOUND", fmt.Errorf("grep_files requires ripgrep (`rg`), but `rg` was not found in PATH or the Ally tools directory.\n\nInstall ripgrep and restart Ally:\n%s", strings.Join(ripgrepInstallInstructions(), "\n")))
}

func (a *App) emitRipgrepMissingIfNeeded() {
	if _, err := findRipgrep(); err == nil {
		return
	}
	a.emit("dependency:missing", map[string]any{
		"tool":         "rg",
		"name":         "ripgrep",
		"message":      "grep_files requires ripgrep (`rg`), but it was not found. Install ripgrep and restart Ally.",
		"installSteps": ripgrepInstallInstructions(),
	})
}

func (a *App) emitGitBashMissingIfNeeded() {
	if goruntime.GOOS != "windows" {
		return
	}
	cfg, err := a.getConfig()
	if err != nil {
		return
	}
	if _, bashName := findWindowsBash(cfg.GitBashPath); bashName != "" {
		return
	}
	a.emit("dependency:missing", map[string]any{
		"tool":       "bash",
		"name":       "Git Bash",
		"messageKey": "app.dependency.gitBashMissing",
		"message":    "Git Bash was not found. run_command will fall back to PowerShell. Install Git for Windows, or set the Git Bash path in Settings → General → Git Bash Path.",
		"installStepKeys": []string{
			"app.dependency.gitBashDownload",
			"app.dependency.gitBashConfigure",
		},
		"installSteps": []string{
			"Download from https://git-scm.com/download/win",
			"Or set the path manually in Settings, e.g. C:\\Program Files\\Git\\bin\\bash.exe",
		},
	})
}

func matchToolGlob(pattern, relPath, base string) (bool, error) {
	pattern = filepath.ToSlash(pattern)
	if strings.Contains(pattern, "/") {
		matched, err := path.Match(pattern, relPath)
		if err != nil || matched {
			return matched, err
		}
		if strings.Contains(pattern, "**") {
			re, err := regexp.Compile("^" + globPatternToRegex(pattern) + "$")
			if err != nil {
				return false, err
			}
			return re.MatchString(relPath), nil
		}
		return false, nil
	}
	return path.Match(pattern, base)
}

func globPatternToRegex(pattern string) string {
	var b strings.Builder
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		switch ch {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				b.WriteString(".*")
				i++
			} else {
				b.WriteString(`[^/]*`)
			}
		case '?':
			b.WriteString(`[^/]`)
		default:
			b.WriteString(regexp.QuoteMeta(string(ch)))
		}
	}
	return b.String()
}

func compilePattern(pattern string) (*regexp.Regexp, error) {
	return regexp.Compile(pattern)
}

func truncateLine(line string, maxLen int) string {
	if len(line) > maxLen {
		return line[:maxLen] + "..."
	}
	return line
}

// ── Batch Read ───────────────────────────────────────────

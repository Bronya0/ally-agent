// Package grep wraps ripgrep execution for bounded search operations.
//
// Nothing here may depend on App state, ConfigState, or any *App receiver.
// Errors that need a stable tool error code are wrapped using the shared
// internal/tools/shared package so callers can extract codes uniformly.
package grep

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	toolerrors "ally-dev/internal/tools/shared"
)

// DefaultTimeout is the default grep timeout in seconds when the request
// does not specify one.
const DefaultTimeout = 30

// MaxTimeout is the upper bound on grep timeout in seconds.
const MaxTimeout = 300

const (
	// maxGrepRecordBytes prevents one unusually long rg output record from
	// forcing an unbounded allocation while parsing search results or counts.
	maxGrepRecordBytes = 2 * 1024 * 1024
	// maxGrepStdoutBytes bounds the amount of rg output consumed for samples.
	// Exact counts are collected by the separate count pass.
	maxGrepStdoutBytes = 10 * 1024 * 1024
)

// Request is the model-facing grep_files request shape.
type Request struct {
	Pattern        string `json:"pattern"`
	Path           string `json:"path,omitempty"`
	Glob           string `json:"glob,omitempty"`
	IncludeIgnored bool   `json:"includeIgnored,omitempty"`
	MaxDepth       int    `json:"maxDepth,omitempty"`
	MaxFiles       int    `json:"maxFiles,omitempty"`
	MaxMatches     int    `json:"maxMatches,omitempty"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"`
}

// Match is a single sample match returned by grep_files.
type Match struct {
	Path    string `json:"path"`
	LineNum int    `json:"lineNum"`
	Content string `json:"content"`
}

// Result is the grep_files tool result.
type Result struct {
	Matches          []Match `json:"matches"`
	Count            int     `json:"count"`
	Occurrences      int     `json:"occurrences"`
	Files            int     `json:"files"`
	Truncated        bool    `json:"truncated"`
	SamplesTruncated bool    `json:"samplesTruncated"`
	StatsExact       bool    `json:"statsExact"`
}

// Find locates the ripgrep binary. It checks the ALLY_RG_PATH env variable,
// the directory next to the running executable, common macOS homebrew paths,
// and finally PATH.
func Find() (string, error) {
	return FindForOS(runtime.GOOS)
}

// FindForOS is Find with an explicit GOOS for testability.
func FindForOS(goos string) (string, error) {
	for _, candidate := range CandidatesForOS(goos) {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return exec.LookPath("rg")
}

// CandidatesForOS returns the candidate ripgrep paths for the given OS.
func CandidatesForOS(goos string) []string {
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

// MissingError returns the coded error reported when ripgrep is not found.
func MissingError() error {
	return toolerrors.New("E_RIPGREP_NOT_FOUND", fmt.Errorf("grep_files requires ripgrep (`rg`), but `rg` was not found in PATH or the Ally tools directory.\n\nInstall ripgrep and restart Ally:\n%s", strings.Join(InstallInstructions(), "\n")))
}

// InstallInstructions returns the per-OS install instructions shown when
// ripgrep is missing.
func InstallInstructions() []string {
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

// TimeoutSeconds returns the effective timeout for req, applying the default
// and clamping to MaxTimeout.
func TimeoutSeconds(req Request) int {
	timeout := req.TimeoutSeconds
	if timeout <= 0 {
		return DefaultTimeout
	}
	if timeout > MaxTimeout {
		return MaxTimeout
	}
	return timeout
}

// Search runs ripgrep against searchRoot and returns sample matches plus
// exact counts. root is the workspace root used to compute relative display
// paths.
func Search(ctx context.Context, rgPath, root, searchRoot string, req Request) (*Result, error) {
	maxDepth, maxFiles, maxMatches := limits(req)

	lineCount, fileCount, err := count(ctx, rgPath, root, searchRoot, req, maxDepth, false)
	if err != nil {
		return nil, err
	}
	if lineCount == 0 {
		return &Result{Matches: []Match{}, Count: 0, Occurrences: 0, Files: 0, Truncated: false, SamplesTruncated: false, StatsExact: true}, nil
	}
	occurrences, _, err := count(ctx, rgPath, root, searchRoot, req, maxDepth, true)
	if err != nil {
		return nil, err
	}
	matches, samplesTruncated, err := sampleMatches(ctx, rgPath, root, searchRoot, req, maxDepth, maxFiles, maxMatches)
	if err != nil {
		return nil, err
	}

	return &Result{
		Matches:          matches,
		Count:            lineCount,
		Occurrences:      occurrences,
		Files:            fileCount,
		Truncated:        samplesTruncated,
		SamplesTruncated: samplesTruncated,
		StatsExact:       true,
	}, nil
}

// NormalizeError wraps err with a stable code if it isn't already coded.
// DeadlineExceeded → E_GREP_TIMEOUT, Canceled → E_GREP_CANCELLED,
// anything else → E_GREP_FAILED.
func NormalizeError(err error, timeoutSeconds int) error {
	if err == nil {
		return nil
	}
	if toolerrors.Code(err) != "" {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return toolerrors.New("E_GREP_TIMEOUT", fmt.Errorf("grep_files timed out after %ds", timeoutSeconds))
	}
	if errors.Is(err, context.Canceled) {
		return toolerrors.New("E_GREP_CANCELLED", errors.New("grep_files was cancelled"))
	}
	return toolerrors.New("E_GREP_FAILED", err)
}

// DisplayPathForRoot converts an absolute or relative path into a display
// path relative to root. If the path cannot be expressed relative to root,
// the cleaned absolute (slash) path is returned.
func DisplayPathForRoot(root, p string) string {
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

// GlobPatternToRegex converts a glob pattern (with * and ** and ?) into a
// regex source string suitable for regexp.Compile. Single * matches anything
// except /, ** matches anything including /.
func GlobPatternToRegex(pattern string) string {
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
			b.WriteString(regexpQuoteMeta(string(ch)))
		}
	}
	return b.String()
}

// limits returns the effective maxDepth/maxFiles/maxMatches for req, applying
// defaults and clamping to upper bounds.
func limits(req Request) (maxDepth, maxFiles, maxMatches int) {
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

func baseArgs(req Request, maxDepth int) []string {
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
	for _, dir := range excludedDirs() {
		args = append(args, "-g", "!"+dir+"/**")
		args = append(args, "-g", "!**/"+dir+"/**")
	}
	return args
}

func count(ctx context.Context, rgPath, root, searchRoot string, req Request, maxDepth int, countMatches bool) (total int, files int, err error) {
	args := baseArgs(req, maxDepth)
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
	reader := bufio.NewReaderSize(stdout, 64*1024)
	for {
		line, recordTruncated, _, readErr := readGrepRecord(reader, maxGrepRecordBytes)
		if len(line) == 0 && readErr == io.EOF {
			break
		}
		if parseErr == nil {
			switch {
			case recordTruncated:
				parseErr = fmt.Errorf("rg count output record exceeded %d bytes", maxGrepRecordBytes)
			case len(line) > 0:
				n, ok := parseCountLine(string(line))
				if !ok {
					parseErr = fmt.Errorf("could not parse rg count output: %q", string(line))
				} else {
					total += n
					files++
				}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			if parseErr == nil && ctx.Err() == nil {
				parseErr = readErr
			}
			break
		}
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
			return 0, 0, failureError(msg)
		}
		return 0, 0, waitErr
	}
	return total, files, nil
}

func parseCountLine(line string) (int, bool) {
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

func sampleMatches(ctx context.Context, rgPath, root, searchRoot string, req Request, maxDepth, maxFiles, maxMatches int) ([]Match, bool, error) {
	args := baseArgs(req, maxDepth)
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

	matches := []Match{}
	sampleFiles := map[string]bool{}
	truncated := false
	parseErr := error(nil)
	sampleLimitReached := false
	outputCapped := false
	stdoutBytes := 0

	reader := bufio.NewReaderSize(stdout, 64*1024)
	for {
		if ctx.Err() != nil {
			break
		}
		recordLimit := maxGrepRecordBytes
		if outputCapped || sampleLimitReached || parseErr != nil {
			// Continue draining the pipe without retaining records once the
			// result is already complete, capped, or known to be invalid.
			recordLimit = 0
		}
		line, recordTruncated, bytesRead, readErr := readGrepRecord(reader, recordLimit)
		if !outputCapped {
			remainingOutput := maxGrepStdoutBytes - stdoutBytes
			if bytesRead > remainingOutput {
				outputCapped = true
				truncated = true
				stdoutBytes = maxGrepStdoutBytes
			} else {
				stdoutBytes += bytesRead
			}
		}
		if recordTruncated {
			truncated = true
		}

		if parseErr == nil && !outputCapped && !sampleLimitReached && !recordTruncated && len(line) > 0 {
			event, ok, err := parseMatch(line, root)
			if err != nil {
				parseErr = err
			} else if ok {
				canIncludeFile := sampleFiles[event.Path] || len(sampleFiles) < maxFiles
				canIncludeMatch := len(matches) < maxMatches
				if canIncludeFile && canIncludeMatch {
					sampleFiles[event.Path] = true
					matches = append(matches, Match{
						Path:    event.Path,
						LineNum: event.LineNum,
						Content: truncateLine(event.Content, 200),
					})
				} else {
					sampleLimitReached = true
					truncated = true
				}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			if parseErr == nil && ctx.Err() == nil {
				parseErr = readErr
			}
			break
		}
	}

	waitErr := cmd.Wait()
	errWG.Wait()
	if parseErr != nil {
		return nil, false, parseErr
	}
	if ctx.Err() != nil {
		return nil, false, ctx.Err()
	}
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			if exitErr.ExitCode() != 1 {
				msg := strings.TrimSpace(errBuf.String())
				if msg == "" {
					msg = waitErr.Error()
				}
				return nil, false, failureError(msg)
			}
		} else {
			return nil, false, waitErr
		}
	}

	return matches, truncated, nil
}

// readGrepRecord reads one newline-delimited rg record without using
// bufio.Scanner's fixed token limit. It retains at most maxRecordBytes and
// continues reading until the record boundary so callers can safely drain the
// child process even when a source line is unusually large. A zero limit
// discards the record while still draining it.
func readGrepRecord(reader *bufio.Reader, maxRecordBytes int) (line []byte, recordTruncated bool, bytesRead int, err error) {
	for {
		chunk, readErr := reader.ReadSlice('\n')
		bytesRead += len(chunk)

		if maxRecordBytes <= 0 {
			if len(chunk) > 0 {
				recordTruncated = true
			}
		} else if !recordTruncated {
			remainingRecord := maxRecordBytes - len(line)
			switch {
			case remainingRecord <= 0:
				recordTruncated = true
			case len(chunk) > remainingRecord:
				line = append(line, chunk[:remainingRecord]...)
				recordTruncated = true
			default:
				line = append(line, chunk...)
			}
		}

		if readErr == bufio.ErrBufferFull {
			continue
		}
		if readErr == io.EOF {
			return line, recordTruncated, bytesRead, io.EOF
		}
		if readErr != nil {
			return line, recordTruncated, bytesRead, readErr
		}
		return line, recordTruncated, bytesRead, nil
	}
}

func failureError(stderr string) error {
	msg := strings.TrimSpace(stderr)
	if msg == "" {
		msg = "unknown ripgrep failure"
	}
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "error parsing glob"):
		return toolerrors.New("E_GREP_GLOB", fmt.Errorf("invalid grep_files glob: %s", msg))
	case strings.Contains(lower, "regex parse error"), strings.Contains(lower, "error parsing regexp"):
		return toolerrors.New("E_GREP_REGEX", fmt.Errorf("invalid grep_files regex pattern: %s", msg))
	default:
		return toolerrors.New("E_GREP_FAILED", fmt.Errorf("rg failed: %s", msg))
	}
}

type matchEvent struct {
	Path        string
	LineNum     int
	Content     string
	Occurrences int
}

func parseMatch(line []byte, root string) (matchEvent, bool, error) {
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
		return matchEvent{}, false, err
	}
	if event.Type != "match" || event.Data.Path.Text == "" {
		return matchEvent{}, false, nil
	}
	occurrences := len(event.Data.Submatches)
	if occurrences == 0 {
		occurrences = 1
	}
	rel := DisplayPathForRoot(root, event.Data.Path.Text)
	return matchEvent{
		Path:        rel,
		LineNum:     event.Data.LineNumber,
		Content:     strings.TrimRight(event.Data.Lines.Text, "\r\n"),
		Occurrences: occurrences,
	}, true, nil
}

func excludedDirs() []string {
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

func truncateLine(line string, maxLen int) string {
	if len(line) > maxLen {
		return line[:maxLen] + "..."
	}
	return line
}

// limitedBuffer is a mutex-protected byte buffer that silently truncates
// writes beyond limit. Used to capture bounded stderr from ripgrep.
type limitedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.limit <= 0 {
		return len(p), nil
	}
	remaining := b.limit - b.buf.Len()
	if remaining <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = b.buf.Write(p[:remaining])
		b.truncated = true
		return len(p), nil
	}
	_, _ = b.buf.Write(p)
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// regexpQuoteMeta isolates the regexp import so the package does not need to
// import regexp just for GlobPatternToRegex. The implementation mirrors
// regexp.QuoteMeta.
func regexpQuoteMeta(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\\', '.', '+', '*', '?', '(', ')', '|', '[', ']', '{', '}', '^', '$':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

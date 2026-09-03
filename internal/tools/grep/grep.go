// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
// Package grep wraps ripgrep execution for bounded search operations.
//
// Nothing here may depend on App state, ConfigState, or any *App receiver.
// Errors that need a stable tool error code are wrapped using the shared
// internal/tools/shared package so callers can extract codes uniformly.
package grep

import (
	"bufio"
	"bytes"
	"container/heap"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
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
	// maxGrepStdoutBytes bounds the amount of rg output retained for samples.
	// The stream still drains after this cap so exact summary statistics remain
	// available without a second traversal.
	maxGrepStdoutBytes = 10 * 1024 * 1024
	// maxGrepFileCountEntries caps the per-file hotspot list (fileCounts)
	// returned to the model, sorted by count descending.
	maxGrepFileCountEntries = 100
	// maxGrepThreads keeps interactive searches responsive without allowing
	// ripgrep to occupy every logical CPU on large workspaces.
	maxGrepThreads = 4

	// DefaultMaxMatches is the default match limit (100, matching Pi).
	DefaultMaxMatches = 100
	// DefaultMaxOutputBytes is the maximum byte limit for grep output (50KB, matching Pi).
	DefaultMaxOutputBytes = 50 * 1024
	// MaxGrepLineLength is the maximum characters per grep match line (500, matching Pi).
	MaxGrepLineLength = 500
)

const (
	// OutputModeLines is the default grep output: one entry per matching
	// line, grouped by file, carrying only the line number (no line text).
	// Compact and flat so broad searches stay small; the model reads the
	// specific lines via the read tool.
	OutputModeLines = "lines"
	// OutputModeCountMatches returns the exact occurrence count per file.
	OutputModeCountMatches = "count_matches"
)

// normalizedOutputMode maps the request outputMode to a supported mode.
// The empty default and the legacy files_with_matches/content aliases all
// collapse to lines: line numbers are the smallest payload that still lets
// the model jump straight to a location, so the heavier path-only and
// line-text shapes are no longer exposed.
func normalizedOutputMode(mode string) string {
	switch strings.TrimSpace(mode) {
	case OutputModeCountMatches:
		return OutputModeCountMatches
	default:
		return OutputModeLines
	}
}

// Request is the model-facing grep request shape.
type Request struct {
	Pattern string `json:"pattern"`
	// OutputMode defaults to lines, which returns one entry per matching line
	// (path + line number, no line text) grouped by file. Use count_matches
	// for exact per-file occurrence counts.
	OutputMode     string `json:"outputMode,omitempty"`
	Path           string `json:"path,omitempty"`
	Glob           string `json:"glob,omitempty"`
	IncludeIgnored bool   `json:"includeIgnored,omitempty"`
	MaxDepth       int    `json:"maxDepth,omitempty"`
	MaxFiles       int    `json:"maxFiles,omitempty"`
	MaxMatches     int    `json:"maxMatches,omitempty"`
	Timeout        int    `json:"timeout,omitempty"`
	// CaseSensitive matches case exactly. Default false: searches are
	// case-insensitive (the historic Ally default and the tool description's
	// contract).
	CaseSensitive bool `json:"caseSensitive,omitempty"`
	// Offset skips the first N result entries before collecting a page. In
	// lines mode entries are matching lines; in count_matches mode they are
	// distinct matching files. Pass the previous result's NextOffset here to
	// page through large sets.
	Offset int `json:"offset,omitempty"`

	// Pi compatibility fields:
	IgnoreCase *bool `json:"ignoreCase,omitempty"`
	Literal    bool  `json:"literal,omitempty"`
	Context    int   `json:"context,omitempty"`
	Limit      int   `json:"limit,omitempty"`
}

// LineFileMatch groups matching line numbers for one file. The path is
// emitted once; Lines lists the 1-based line numbers of matching lines in
// ascending order. It carries no line text, keeping the result flat so a
// broad search stays compact.
type LineFileMatch struct {
	Path  string `json:"path"`
	Lines []int  `json:"lines"`
}

// FileCount is one entry in the per-file count list returned as
// Result.FileCounts in count_matches mode.
type FileCount struct {
	Path  string `json:"path"`
	Count int    `json:"count"`
}

// Result is the grep tool result. Mode lines returns matching line numbers
// grouped by file (no line text). Mode count_matches returns per-file counts.
// MatchedLines/Hits/Files stay exact across all modes. NextOffset pages the
// active mode's result entries.
type Result struct {
	Output       string          `json:"output,omitempty"`
	Mode         string          `json:"mode"`
	LineHits     []LineFileMatch `json:"matches,omitempty"`
	FileCounts   []FileCount     `json:"fileCounts,omitempty"`
	MatchedLines int             `json:"matchedLines"`
	Hits         int             `json:"hits"`
	Files        int             `json:"files"`
	Truncated    bool            `json:"truncated"`
	StatsExact   bool            `json:"statsExact"`
	NextOffset   int             `json:"nextOffset,omitempty"`
	// OffsetExhausted reports that Request.Offset skipped past the end of the
	// match stream: matches is empty even though Hits > 0. Reset offset to 0
	// to page from the beginning.
	OffsetExhausted bool `json:"offsetExhausted,omitempty"`
	// Skipped describes workspace-wide search policies that may hide content.
	// Explicit path searches do not apply these broad exclusions.
	Skipped []string `json:"skipped,omitempty"`
	// Warnings carries non-fatal issues encountered during search, such as
	// individual files that rg could not open (Windows reserved names, etc.).
	// Results are still returned; only the bad files were skipped.
	Warnings []string `json:"warnings,omitempty"`
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
	return toolerrors.New("E_RIPGREP_NOT_FOUND", fmt.Errorf("grep requires ripgrep (`rg`), but `rg` was not found in PATH or the Ally tools directory.\n\nInstall ripgrep and restart Ally:\n%s", strings.Join(InstallInstructions(), "\n")))
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

// EffectiveTimeoutSeconds returns the effective timeout for req, applying the
// default and clamping to MaxTimeout.
func EffectiveTimeoutSeconds(req Request) int {
	timeout := req.Timeout
	if timeout <= 0 {
		return DefaultTimeout
	}
	if timeout > MaxTimeout {
		return MaxTimeout
	}
	return timeout
}

// SearchStats carries the exact search statistics collected from ripgrep's
// --stats output: matched_lines, matches, and files contained matches.
type SearchStats struct {
	MatchedLines     int
	Matches          int
	FilesWithMatches int
}

// Search runs ripgrep against searchRoot and returns a mode-specific result.
// The default lines mode returns matching line numbers grouped by file,
// keeping broad searches compact; count_matches returns per-file counts.
// Exact counts are collected for every mode.
//
// It runs one --json --stats pass. Once the line budget is full, the parser
// stops retaining line numbers but keeps draining the same process so the
// trailing summary and per-file end events remain exact without rescanning
// the workspace.
func Search(ctx context.Context, rgPath, root, searchRoot string, req Request) (*Result, error) {
	mode := normalizedOutputMode(req.OutputMode)
	maxDepth, maxFiles, maxMatches := limits(req)

	lineHits, fileCounts, output, nextOffset, stats, truncated, offsetExhausted, warnings, err := sampleMatches(ctx, rgPath, root, searchRoot, req, maxDepth, maxFiles, maxMatches)
	if err != nil {
		return nil, err
	}
	if stats == nil {
		return nil, errors.New("ripgrep did not emit summary statistics")
	}

	res := &Result{
		Output:          output,
		Mode:            mode,
		MatchedLines:    stats.MatchedLines,
		Hits:            stats.Matches,
		Files:           stats.FilesWithMatches,
		Truncated:       truncated,
		StatsExact:      true,
		NextOffset:      nextOffset,
		OffsetExhausted: offsetExhausted,
		Skipped:         searchSkipNotices(req),
		Warnings:        warnings,
	}
	if mode == OutputModeCountMatches {
		// count_matches returns the exact count for each file in this page.
		// Line numbers would only duplicate that information.
		res.FileCounts = fileCounts
	} else {
		// lines mode: the path→line-number groups are the payload.
		res.LineHits = lineHits
	}
	return res, nil
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
		return toolerrors.New("E_GREP_TIMEOUT", fmt.Errorf("grep timed out after %ds", timeoutSeconds))
	}
	if errors.Is(err, context.Canceled) {
		return toolerrors.New("E_GREP_CANCELLED", errors.New("grep was cancelled"))
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
	maxMatches = req.Limit
	if maxMatches <= 0 {
		maxMatches = req.MaxMatches
	}
	if maxMatches <= 0 {
		if req.MaxFiles > 0 {
			maxMatches = maxFiles * 10
		} else {
			maxMatches = DefaultMaxMatches
		}
	}
	if maxMatches > 5000 {
		maxMatches = 5000
	}
	return maxDepth, maxFiles, maxMatches
}

// excludedGlobArgs is the static -g exclusion list derived from excludedDirs,
// built once and shared across every rg invocation.
var excludedGlobArgs = func() []string {
	var args []string
	for _, dir := range excludedDirs() {
		args = append(args, "-g", "!"+dir+"/**", "-g", "!**/"+dir+"/**")
	}
	return args
}()

// windowsReservedGlobArgs excludes Windows reserved filenames that cause
// "incorrect function" (os error 1) when rg tries to open them. Applied on
// all platforms so cross-platform workspaces don't break.
var windowsReservedGlobArgs = func() []string {
	reserved := []string{"nul", "con", "prn", "aux"}
	for i := 1; i <= 9; i++ {
		reserved = append(reserved, fmt.Sprintf("com%d", i))
		reserved = append(reserved, fmt.Sprintf("lpt%d", i))
	}
	var args []string
	for _, name := range reserved {
		args = append(args, "-g", "!"+name, "-g", "!**/"+name)
	}
	return args
}()

func baseArgs(req Request, maxDepth int) []string {
	args := []string{
		"--color=never",
		"--max-depth", strconv.Itoa(maxDepth),
		"--threads", strconv.Itoa(grepThreads()),
	}
	// Workspace-wide searches stay bounded. An explicit path is an intentional
	// narrowing request, so it may inspect generated directories and files over
	// 10 MB instead of silently returning a false negative.
	if strings.TrimSpace(req.Path) == "" {
		args = append(args, "--max-filesize", "10M")
	}
	if req.Literal {
		args = append(args, "--fixed-strings")
	}
	if req.Context > 0 {
		args = append(args, "-C", strconv.Itoa(req.Context))
	}
	if req.IgnoreCase != nil {
		if *req.IgnoreCase {
			args = append(args, "--ignore-case")
		} else {
			args = append(args, "--case-sensitive")
		}
	} else if req.CaseSensitive {
		// rg's default is case-sensitive, but pin the explicit flag so the
		// behavior survives a future default flip.
		args = append(args, "--case-sensitive")
	} else {
		args = append(args, "--ignore-case")
	}
	if req.IncludeIgnored {
		args = append(args, "--no-ignore")
	}
	if strings.TrimSpace(req.Path) == "" {
		args = append(args, excludedGlobArgs...)
	}
	// Windows reserved filenames (nul, con, prn, aux, com1-9, lpt1-9) cause
	// "incorrect function" (os error 1) when rg tries to open them. Exclude
	// them via glob on all platforms so cross-platform workspaces with stray
	// reserved-name files don't break the search.
	args = append(args, windowsReservedGlobArgs...)
	if strings.TrimSpace(req.Glob) != "" {
		args = append(args, "-g", filepath.ToSlash(req.Glob))
	}
	return args
}

func grepThreads() int {
	cores := runtime.NumCPU()
	if cores < 1 {
		return 1
	}
	if cores > maxGrepThreads {
		return maxGrepThreads
	}
	return cores
}

func searchSkipNotices(req Request) []string {
	if strings.TrimSpace(req.Path) != "" {
		return []string{}
	}
	skipped := []string{"files_over_10MB"}
	if !req.IncludeIgnored {
		skipped = append(skipped, "ignored_by_ignore_files")
	}
	skipped = append(skipped, "directories:"+strings.Join(excludedDirs(), ","))
	return skipped
}

// parseEndEvent extracts the exact per-file match count from a ripgrep
// --json "end" event (emitted once per searched file). The path is
// relativized for display; ok=false for any other event type.
func parseEndEvent(line []byte, root string) (path string, count int, ok bool) {
	if !bytes.Contains(line, []byte(`"type":"end"`)) {
		return "", 0, false
	}
	var event struct {
		Type string `json:"type"`
		Data struct {
			Path struct {
				Text string `json:"text"`
			} `json:"path"`
			Stats struct {
				Matches int `json:"matches"`
			} `json:"stats"`
		} `json:"data"`
	}
	if err := json.Unmarshal(line, &event); err != nil {
		return "", 0, false
	}
	if event.Type != "end" || event.Data.Path.Text == "" {
		return "", 0, false
	}
	return DisplayPathForRoot(root, event.Data.Path.Text), event.Data.Stats.Matches, true
}

func sampleMatches(ctx context.Context, rgPath, root, searchRoot string, req Request, maxDepth, maxFiles, maxMatches int) ([]LineFileMatch, []FileCount, string, int, *SearchStats, bool, bool, []string, error) {
	mode := normalizedOutputMode(req.OutputMode)
	args := baseArgs(req, maxDepth)
	args = append(args,
		"--json",
		"--stats",
		"--line-number",
		"-e", req.Pattern,
	)
	args = append(args, searchRoot)

	cmd := exec.CommandContext(ctx, rgPath, args...)
	cmd.Dir = root
	hideCommandWindow(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, "", 0, nil, false, false, nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, "", 0, nil, false, false, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, "", 0, nil, false, false, nil, err
	}

	errBuf := &limitedBuffer{limit: 16 * 1024}
	var errWG sync.WaitGroup
	errWG.Add(1)
	go func() {
		defer errWG.Done()
		_, _ = io.Copy(errBuf, stderr)
	}()

	groups := []*sampleFile{}
	groupByPath := map[string]*sampleFile{}
	fileCounts := &fileCountHeap{}
	heap.Init(fileCounts)
	totalLines := 0
	truncated := false
	parseErr := error(nil)
	sampleLimitReached := false
	matchLimitReached := false
	outputBytesLimitReached := false
	linesTruncated := false
	matchCount := 0
	outputLines := []string{}
	outputBytes := 0
	outputCapped := false
	stdoutBytes := 0
	// The stream always drains to completion, so summary statistics and file
	// counts stay exact even after the sample budget is exhausted.
	var stats *SearchStats
	// seen counts every matching line consumed (offset-skips plus samples) so
	// NextOffset can resume exactly after the last consumed line.
	seen := 0

	reader := bufio.NewReaderSize(stdout, 64*1024)
	for {
		if ctx.Err() != nil {
			break
		}
		line, recordTruncated, bytesRead, readErr := readGrepRecord(reader, maxGrepRecordBytes)
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

		if parseErr == nil && len(line) > 0 {
			if summary := parseSummaryStats(line); summary != nil {
				stats = summary
			} else if path, n, ok := parseEndEvent(line, root); ok {
				if n > 0 {
					fileCounts.Add(FileCount{Path: path, Count: n})
					if group := groupByPath[path]; group != nil {
						group.matchCount = n
					}
				}
			} else if !recordTruncated && !outputCapped {
				ev, ok, err := parseGrepLine(line, root)
				if err != nil {
					parseErr = err
				} else if ok {
					if mode == OutputModeCountMatches {
						// count_matches resolves its page from the globally
						// sorted per-file counts collected by the end events, so
						// line grouping is skipped entirely.
						continue
					}

					rawText := strings.TrimRight(ev.Text, "\r\n")
					runes := []rune(rawText)
					lineText := rawText
					if len(runes) > MaxGrepLineLength {
						lineText = string(runes[:MaxGrepLineLength]) + "... [truncated]"
						linesTruncated = true
					}

					if ev.IsMatch {
						matchCount++
						if sampleLimitReached {
							continue
						}
						if seen < req.Offset {
							seen++
							continue
						}
						g := groupByPath[ev.Path]
						if g == nil && len(groups) < maxFiles {
							g = &sampleFile{path: ev.Path, lines: []int{}}
							groupByPath[ev.Path] = g
							groups = append(groups, g)
						}
						if g != nil && totalLines < maxMatches {
							if len(g.lines) == 0 || g.lines[len(g.lines)-1] != ev.LineNumber {
								g.lines = append(g.lines, ev.LineNumber)
								totalLines++
								seen++
							}
						} else {
							sampleLimitReached = true
							matchLimitReached = true
							truncated = true
							continue
						}
					}

					// Format output lines (matching lines + context lines)
					if !outputBytesLimitReached && (!sampleLimitReached || ev.IsContext) {
						if ev.IsMatch && seen <= req.Offset {
							// skipped by offset
						} else {
							sep := ":"
							if ev.IsContext {
								sep = "-"
							}
							formatted := fmt.Sprintf("%s%s%d%s %s", ev.Path, sep, ev.LineNumber, sep, lineText)
							lineBytes := len(formatted) + 1
							if outputBytes+lineBytes > DefaultMaxOutputBytes {
								outputBytesLimitReached = true
								truncated = true
							} else {
								outputLines = append(outputLines, formatted)
								outputBytes += lineBytes
							}
						}
					}
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
		return nil, nil, "", 0, nil, false, false, nil, parseErr
	}
	if ctx.Err() != nil {
		return nil, nil, "", 0, nil, false, false, nil, ctx.Err()
	}
	var warnings []string
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			if exitErr.ExitCode() != 1 {
				msg := strings.TrimSpace(errBuf.String())
				if msg == "" {
					msg = waitErr.Error()
				}
				// rg exited with an error (exit code 2) but may have already
				// emitted valid results to stdout before hitting a bad file
				// (e.g. Windows reserved filenames like nul/con/aux). If we
				// have stats, return the partial results with a warning
				// instead of discarding everything.
				if stats != nil {
					warnings = append(warnings, msg)
				} else {
					return nil, nil, "", 0, nil, false, false, nil, failureError(msg)
				}
			}
		} else {
			return nil, nil, "", 0, nil, false, false, nil, waitErr
		}
	}
	if stats == nil {
		return nil, nil, "", 0, nil, false, false, nil, errors.New("ripgrep did not emit summary statistics")
	}

	var finalOutput string
	if mode != OutputModeCountMatches {
		if matchCount == 0 {
			finalOutput = "No matches found"
		} else {
			finalOutput = strings.Join(outputLines, "\n")
			var notices []string
			if matchLimitReached {
				notices = append(notices, fmt.Sprintf("%d matches limit reached. Use limit=%d for more, or refine pattern", maxMatches, maxMatches*2))
			}
			if outputBytesLimitReached {
				notices = append(notices, "50.0KB limit reached")
			}
			if linesTruncated {
				notices = append(notices, "Some lines truncated to 500 chars. Use read tool to see full lines")
			}
			if len(notices) > 0 {
				finalOutput += "\n\n[" + strings.Join(notices, ". ") + "]"
			}
		}
	}

	lineHits := make([]LineFileMatch, 0, len(groups))
	for _, g := range groups {
		lineHits = append(lineHits, LineFileMatch{Path: g.path, Lines: g.lines})
	}
	// count_matches resolves its page from the globally sorted per-file counts
	// collected by the end events so the hottest files lead and pagination is
	// stable; lines mode keeps the rg traversal order for predictable offsets.
	var resultCounts []FileCount
	if mode == OutputModeCountMatches {
		sorted := fileCounts.Items()
		start := req.Offset
		if start < 0 {
			start = 0
		}
		if start > len(sorted) {
			start = len(sorted)
		}
		end := start + maxFiles
		if end > len(sorted) {
			end = len(sorted)
		}
		resultCounts = sorted[start:end]
	}

	nextOffset := 0
	offsetExhausted := false
	if mode == OutputModeCountMatches {
		total := stats.FilesWithMatches
		if req.Offset > 0 && req.Offset >= total {
			offsetExhausted = total > 0
		} else if total > req.Offset+len(resultCounts) {
			nextOffset = req.Offset + len(resultCounts)
			truncated = true
		}
	} else {
		if truncated && stats.MatchedLines > seen {
			if seen > req.Offset {
				nextOffset = seen
			} else if outputCapped && seen == req.Offset {
				// An unusually large first record can consume the output budget before
				// it is parsed. Advance once so paging cannot repeat offset zero.
				nextOffset = seen + 1
			}
		}
		offsetExhausted = req.Offset > 0 && stats.MatchedLines > 0 && req.Offset >= stats.MatchedLines && len(groups) == 0
	}
	return lineHits, resultCounts, finalOutput, nextOffset, stats, truncated, offsetExhausted, warnings, nil
}

// sampleFile accumulates matching line numbers for one file path during
// sample collection. matchCount is the exact hit count from the per-file end
// event (used by count_matches mode).
type sampleFile struct {
	path       string
	lines      []int
	matchCount int
}

// fileCountHeap keeps only the globally hottest files. The weakest entry is
// at the root, so memory and final sorting stay bounded by 100 items.
type fileCountHeap struct {
	items []FileCount
}

func (h fileCountHeap) Len() int { return len(h.items) }

func (h fileCountHeap) Less(i, j int) bool {
	if h.items[i].Count != h.items[j].Count {
		return h.items[i].Count < h.items[j].Count
	}
	return h.items[i].Path > h.items[j].Path
}

func (h fileCountHeap) Swap(i, j int) { h.items[i], h.items[j] = h.items[j], h.items[i] }

func (h *fileCountHeap) Push(value any) { h.items = append(h.items, value.(FileCount)) }

func (h *fileCountHeap) Pop() any {
	last := len(h.items) - 1
	value := h.items[last]
	h.items = h.items[:last]
	return value
}

func (h *fileCountHeap) Add(value FileCount) {
	if value.Count <= 0 {
		return
	}
	if h.Len() < maxGrepFileCountEntries {
		heap.Push(h, value)
		return
	}
	if fileCountMoreRelevant(value, h.items[0]) {
		heap.Pop(h)
		heap.Push(h, value)
	}
}

func fileCountMoreRelevant(a, b FileCount) bool {
	if a.Count != b.Count {
		return a.Count > b.Count
	}
	return a.Path < b.Path
}

func (h *fileCountHeap) Items() []FileCount {
	result := append([]FileCount(nil), h.items...)
	sort.Slice(result, func(i, j int) bool {
		return fileCountMoreRelevant(result[i], result[j])
	})
	return result
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
		return toolerrors.New("E_GREP_GLOB", fmt.Errorf("invalid grep glob: %s", msg))
	case strings.Contains(lower, "regex parse error"), strings.Contains(lower, "error parsing regexp"):
		return toolerrors.New("E_GREP_REGEX", fmt.Errorf("invalid grep regex pattern: %s\n\nripgrep uses Rust regex syntax: look-around and backreferences are not supported. Simplify the pattern (e.g. split it into alternations) or use a plain substring.", msg))
	default:
		return toolerrors.New("E_GREP_FAILED", fmt.Errorf("rg failed: %s", msg))
	}
}

// parseSummaryStats extracts the exact counts from rg's --json --stats
// trailing summary event. It returns nil for any other event type. A cheap
// byte pre-check avoids a full JSON unmarshal on every non-summary record in
// the stream (the common case).
func parseSummaryStats(line []byte) *SearchStats {
	if !bytes.Contains(line, []byte(`"type":"summary"`)) {
		return nil
	}
	var event struct {
		Type string `json:"type"`
		Data struct {
			Stats struct {
				MatchedLines      int `json:"matched_lines"`
				Matches           int `json:"matches"`
				SearchesWithMatch int `json:"searches_with_match"`
			} `json:"stats"`
		} `json:"data"`
	}
	if err := json.Unmarshal(line, &event); err != nil {
		return nil
	}
	if event.Type != "summary" {
		return nil
	}
	return &SearchStats{
		MatchedLines:     event.Data.Stats.MatchedLines,
		Matches:          event.Data.Stats.Matches,
		FilesWithMatches: event.Data.Stats.SearchesWithMatch,
	}
}

type parsedGrepLine struct {
	Path       string
	LineNumber int
	Text       string
	IsMatch    bool
	IsContext  bool
}

func parseGrepLine(line []byte, root string) (parsedGrepLine, bool, error) {
	if !bytes.Contains(line, []byte(`"type":"match"`)) && !bytes.Contains(line, []byte(`"type":"context"`)) {
		return parsedGrepLine{}, false, nil
	}
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
		} `json:"data"`
	}
	if err := json.Unmarshal(line, &event); err != nil {
		return parsedGrepLine{}, false, err
	}
	if (event.Type != "match" && event.Type != "context") || event.Data.Path.Text == "" {
		return parsedGrepLine{}, false, nil
	}
	return parsedGrepLine{
		Path:       DisplayPathForRoot(root, event.Data.Path.Text),
		LineNumber: event.Data.LineNumber,
		Text:       event.Data.Lines.Text,
		IsMatch:    event.Type == "match",
		IsContext:  event.Type == "context",
	}, true, nil
}

// parseMatch returns the display path and 1-based line number for a ripgrep
// --json "match" event. begin/end/summary/context events are handled by their
// dedicated parsers (or ignored) and never reach here.
func parseMatch(line []byte, root string) (path string, lineNum int, ok bool, err error) {
	p, ok, err := parseGrepLine(line, root)
	if err != nil || !ok || !p.IsMatch {
		return "", 0, false, err
	}
	return p.Path, p.LineNumber, true, nil
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

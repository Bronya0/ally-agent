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
	"unicode/utf8"

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
	// Exact counts come from the in-pass --stats summary event in the common
	// case; the fallback count pass covers truncated runs.
	maxGrepStdoutBytes = 10 * 1024 * 1024
	// maxMatchPreviewBytes caps each sample line content shown to the model.
	maxMatchPreviewBytes = 200
	// maxGrepFileCountEntries caps the per-file hotspot list (fileCounts)
	// returned to the model, sorted by count descending.
	maxGrepFileCountEntries = 100
	// maxContextPending bounds the per-file before-context buffer (rg -B
	// lines arrive before the first match of a file). The schema caps
	// contextBefore at 50, so this is a generous secondary guard.
	maxContextPending = 200
)

// Request is the model-facing grep request shape.
type Request struct {
	Pattern        string `json:"pattern"`
	Path           string `json:"path,omitempty"`
	Glob           string `json:"glob,omitempty"`
	IncludeIgnored bool   `json:"includeIgnored,omitempty"`
	MaxDepth       int    `json:"maxDepth,omitempty"`
	MaxFiles       int    `json:"maxFiles,omitempty"`
	MaxMatches     int    `json:"maxMatches,omitempty"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"`
	// CaseSensitive matches case exactly. Default false: searches are
	// case-insensitive (the historic Ally default and the tool description's
	// contract).
	CaseSensitive bool `json:"caseSensitive,omitempty"`
	// Offset skips the first N matching lines (entries) before collecting
	// samples, mirroring the pagination contract of kimi-code's GrepTool.
	// Pass the previous result's NextOffset here to page through large sets.
	Offset int `json:"offset,omitempty"`
	// ContextBefore/ContextAfter include that many context lines around each
	// match (rg -B/-A). Context lines are marked `context: true` in matches
	// and never count toward stats or pagination offsets.
	ContextBefore int `json:"contextBefore,omitempty"`
	ContextAfter  int `json:"contextAfter,omitempty"`
}

// Match is a single sample line within a file group. Content is capped
// at 200 bytes; ContentTruncated is set when the cap was applied so the model
// knows the line was shortened (and must re-read before using it as edit
// source text). Context marks a surrounding line requested via
// ContextBefore/ContextAfter rather than a match itself.
type Match struct {
	LineNum          int    `json:"lineNum"`
	Content          string `json:"content"`
	ContentTruncated bool   `json:"contentTruncated,omitempty"`
	Context          bool   `json:"context,omitempty"`
}

// FileMatch groups sample matches by file path so the path is emitted once.
// MatchCount is the exact number of hits in that file across the whole
// search (not just the sampled lines), so the model can spot hotspots even
// when only a few sample lines are shown.
type FileMatch struct {
	Path       string  `json:"path"`
	Matches    []Match `json:"matches"`
	MatchCount int     `json:"matchCount"`
}

// FileCount is one entry in the per-file hotspot list returned as
// Result.FileCounts.
type FileCount struct {
	Path  string `json:"path"`
	Count int    `json:"count"`
}

// Result is the grep tool result. FileHits groups sample matches by
// file path; MatchedLines/Hits/Files are exact stats across all matches.
// FileCounts lists every matched file with its exact hit count, sorted by
// count descending (top hotspots first). NextOffset is non-zero when more
// matches remain past the sample window; pass it back as Request.Offset to
// page through the rest.
type Result struct {
	FileHits            []FileMatch `json:"fileHits"`
	FileCounts          []FileCount `json:"fileCounts"`
	MatchedLines        int         `json:"matchedLines"`
	Hits                int         `json:"hits"`
	Files               int         `json:"files"`
	Truncated           bool        `json:"truncated"`
	SamplesTruncated    bool        `json:"samplesTruncated"`
	StatsExact          bool        `json:"statsExact"`
	NextOffset          int         `json:"nextOffset,omitempty"`
	FileCountsTruncated bool        `json:"fileCountsTruncated,omitempty"`
	// OffsetExhausted reports that Request.Offset skipped past the end of
	// the match stream: fileHits is empty even though Hits > 0. Reset
	// offset to 0 to page from the beginning.
	OffsetExhausted bool `json:"offsetExhausted,omitempty"`
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

// SearchStats carries the exact search statistics collected from ripgrep's
// --stats output: matched_lines, matches, and files contained matches.
type SearchStats struct {
	MatchedLines     int
	Matches          int
	FilesWithMatches int
}

// Search runs ripgrep against searchRoot and returns sample matches plus
// exact counts. root is the workspace root used to compute relative display
// paths.
//
// It runs a single --json --stats pass and uses the trailing summary event
// for the exact counts plus the per-file end events for exact per-file
// counts. Only when that pass was truncated early (sample or output caps)
// does it fall back to one additional lightweight --count --with-filename
// --stats pass, so the common case costs one ripgrep invocation instead of
// three.
func Search(ctx context.Context, rgPath, root, searchRoot string, req Request) (*Result, error) {
	maxDepth, maxFiles, maxMatches := limits(req)

	fileHits, endCounts, nextOffset, stats, truncated, offsetExhausted, err := sampleMatches(ctx, rgPath, root, searchRoot, req, maxDepth, maxFiles, maxMatches)
	if err != nil {
		return nil, err
	}

	counts := endCounts
	matchedLines, hits, files := 0, 0, 0
	if !truncated && stats != nil {
		// The sample pass read the whole output, so the summary event holds
		// exact counts — no second invocation needed.
		matchedLines, hits, files = stats.MatchedLines, stats.Matches, stats.FilesWithMatches
	} else {
		// The sample pass was cut short (or produced no summary): collect
		// exact counts and the complete per-file counts from a dedicated
		// lightweight count pass.
		var err error
		matchedLines, hits, files, counts, err = count(ctx, rgPath, root, searchRoot, req, maxDepth)
		if err != nil {
			return nil, err
		}
	}

	// Attach the exact per-file hit count to each sampled group so the model
	// can rank sampled files by real frequency instead of sample size, then
	// order the groups by that count descending so the most relevant file is
	// read first without extra calls.
	for i := range fileHits {
		if n, ok := counts[fileHits[i].Path]; ok {
			fileHits[i].MatchCount = n
		}
	}
	sort.SliceStable(fileHits, func(i, j int) bool {
		if fileHits[i].MatchCount != fileHits[j].MatchCount {
			return fileHits[i].MatchCount > fileHits[j].MatchCount
		}
		return fileHits[i].Path < fileHits[j].Path
	})

	fileCounts, fileCountsTruncated := buildFileCounts(counts)

	return &Result{
		FileHits:            fileHits,
		FileCounts:          fileCounts,
		MatchedLines:        matchedLines,
		Hits:                hits,
		Files:               files,
		Truncated:           truncated,
		SamplesTruncated:    truncated,
		StatsExact:          true,
		NextOffset:          nextOffset,
		FileCountsTruncated: fileCountsTruncated,
		OffsetExhausted:     offsetExhausted,
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
	maxMatches = req.MaxMatches
	if maxMatches <= 0 {
		maxMatches = maxFiles * 10
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

func baseArgs(req Request, maxDepth int) []string {
	args := []string{
		"--color=never",
		"--max-filesize", "10M",
		"--max-depth", strconv.Itoa(maxDepth),
	}
	if req.CaseSensitive {
		// rg's default is case-sensitive, but pin the explicit flag so the
		// behavior survives a future default flip.
		args = append(args, "--case-sensitive")
	} else {
		args = append(args, "--ignore-case")
	}
	if req.IncludeIgnored {
		args = append(args, "--no-ignore")
	}
	args = append(args, excludedGlobArgs...)
	if strings.TrimSpace(req.Glob) != "" {
		args = append(args, "-g", filepath.ToSlash(req.Glob))
	}
	return args
}

// count runs one lightweight rg pass that emits --count --with-filename --stats
// output and parses the trailing human-readable stats block for the exact
// matched-lines, match, and file counts, plus each per-file `path:N` count line
// for the hotspot list. If no stats field is recognized the pass fails loudly
// rather than silently reporting zero matches.
func count(ctx context.Context, rgPath, root, searchRoot string, req Request, maxDepth int) (matchedLines, matches, files int, fileCounts map[string]int, err error) {
	args := baseArgs(req, maxDepth)
	args = append(args, "--count", "--with-filename", "--stats", "-e", req.Pattern, searchRoot)

	cmd := exec.CommandContext(ctx, rgPath, args...)
	cmd.Dir = root
	hideCommandWindow(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, 0, 0, nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return 0, 0, 0, nil, err
	}
	if err := cmd.Start(); err != nil {
		return 0, 0, 0, nil, err
	}

	errBuf := &limitedBuffer{limit: 16 * 1024}
	var errWG sync.WaitGroup
	errWG.Add(1)
	go func() {
		defer errWG.Done()
		_, _ = io.Copy(errBuf, stderr)
	}()

	parseErr := error(nil)
	// rg --count --stats emits one `path:N` line per matching file BEFORE the
	// trailing stats block, so the block's position depends on the match
	// count. We therefore never buffer arbitrary output lines; we parse the
	// three known stats fields directly as the stream passes through, which
	// stays correct regardless of how many files match.
	foundMatched, foundMatches, foundFiles := false, false, false
	fileCounts = map[string]int{}
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
				if n, ok := parseStatsValue(string(line), " matched lines"); ok {
					matchedLines, foundMatched = n, true
				} else if n, ok := parseStatsValue(string(line), " matches"); ok {
					matches, foundMatches = n, true
				} else if n, ok := parseStatsValue(string(line), " files contained matches"); ok {
					files, foundFiles = n, true
				} else if p, n, ok := parsePerFileCount(string(line), root); ok {
					fileCounts[p] = n
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
	if parseErr == nil && (!foundMatched || !foundMatches || !foundFiles) {
		parseErr = fmt.Errorf("could not parse rg --stats output: missing matched-lines, matches, or files-contained-matches fields")
	}

	waitErr := cmd.Wait()
	errWG.Wait()
	if parseErr != nil {
		return 0, 0, 0, nil, parseErr
	}
	if ctx.Err() != nil {
		return 0, 0, 0, nil, ctx.Err()
	}
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			if exitErr.ExitCode() == 1 {
				// No matches: rg --stats still emits the stats block, so the
				// parsed zero counts are exact.
				return matchedLines, matches, files, fileCounts, nil
			}
			msg := strings.TrimSpace(errBuf.String())
			if msg == "" {
				msg = waitErr.Error()
			}
			return 0, 0, 0, nil, failureError(msg)
		}
		return 0, 0, 0, nil, waitErr
	}
	return matchedLines, matches, files, fileCounts, nil
}

// parsePerFileCount extracts a `path:N` per-file count line emitted by
// rg --count --with-filename (one line per matching file, before the stats
// block). The path is relativized for display. ok=false for stats-block
// lines (non-integer suffix) and empty lines.
func parsePerFileCount(line, root string) (path string, count int, ok bool) {
	line = strings.TrimSpace(line)
	idx := strings.LastIndex(line, ":")
	if idx <= 0 {
		return "", 0, false
	}
	n, err := strconv.Atoi(line[idx+1:])
	if err != nil {
		return "", 0, false
	}
	return DisplayPathForRoot(root, line[:idx]), n, true
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

// buildFileCounts converts the per-file count map into a descending-sorted
// hotspot list. It caps the list at maxGrepFileCountEntries and reports
// whether entries were dropped.
func buildFileCounts(counts map[string]int) ([]FileCount, bool) {
	if len(counts) == 0 {
		return []FileCount{}, false
	}
	entries := make([]FileCount, 0, len(counts))
	for p, n := range counts {
		if n > 0 {
			entries = append(entries, FileCount{Path: p, Count: n})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Count != entries[j].Count {
			return entries[i].Count > entries[j].Count
		}
		return entries[i].Path < entries[j].Path
	})
	truncated := false
	if len(entries) > maxGrepFileCountEntries {
		entries = entries[:maxGrepFileCountEntries]
		truncated = true
	}
	return entries, truncated
}

// parseStatsValue extracts the integer from a human-readable rg --stats line
// such as "123 matches". It returns ok=false for any other line.
func parseStatsValue(line, suffix string) (int, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasSuffix(line, suffix) {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(line, suffix)))
	if err != nil {
		return 0, false
	}
	return n, true
}

func sampleMatches(ctx context.Context, rgPath, root, searchRoot string, req Request, maxDepth, maxFiles, maxMatches int) ([]FileMatch, map[string]int, int, *SearchStats, bool, bool, error) {
	args := baseArgs(req, maxDepth)
	args = append(args,
		"--json",
		"--stats",
		"--line-number",
		"-e", req.Pattern,
	)
	if req.ContextBefore > 0 {
		args = append(args, "-B", strconv.Itoa(req.ContextBefore))
	}
	if req.ContextAfter > 0 {
		args = append(args, "-A", strconv.Itoa(req.ContextAfter))
	}
	args = append(args, searchRoot)

	cmd := exec.CommandContext(ctx, rgPath, args...)
	cmd.Dir = root
	hideCommandWindow(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, 0, nil, false, false, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, 0, nil, false, false, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, 0, nil, false, false, err
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
	totalMatches := 0
	truncated := false
	parseErr := error(nil)
	sampleLimitReached := false
	outputCapped := false
	stdoutBytes := 0
	// stats is populated from the trailing summary event when the stream is
	// read to completion. It stays nil when the pass was killed early.
	var stats *SearchStats
	// endCounts accumulates the exact per-file match counts from rg --json
	// "end" events, authoritative even when the sample window is offset.
	endCounts := map[string]int{}
	// seen counts every match event consumed (offset-skips plus samples) so
	// NextOffset can resume exactly after the last consumed event.
	seen := 0
	// pendingContext buffers rg context events that arrived before the first
	// sampled match of their file (rg emits -B lines before the match), so
	// before-context is not lost. Bounded per path by maxContextPending.
	pendingContext := map[string][]Match{}

	reader := bufio.NewReaderSize(stdout, 64*1024)
	// killed marks that we intentionally terminated rg early. Once set, the
	// following cmd.Wait() will observe a non-zero exit code that we must
	// tolerate (it's not a real search failure).
	killed := false
	for {
		if ctx.Err() != nil {
			break
		}
		// Early-exit: once we have enough samples or hit the output cap, kill
		// rg immediately instead of draining the rest of its stdout. On large
		// repos with a high-frequency pattern, rg can emit hundreds of
		// thousands of records after the limit — draining them all is pure
		// waste (the exact counts are collected separately when this happens).
		if (sampleLimitReached || outputCapped) && !killed {
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			killed = true
			break
		}
		recordLimit := maxGrepRecordBytes
		if parseErr != nil {
			// Continue draining the pipe without retaining records once the
			// result is known to be invalid.
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

		if parseErr == nil && !recordTruncated && !outputCapped && !sampleLimitReached && len(line) > 0 {
			if summary := parseSummaryStats(line); summary != nil {
				stats = summary
			} else if path, n, ok := parseEndEvent(line, root); ok {
				if n > 0 {
					endCounts[path] = n
				}
			} else {
				event, ok, err := parseMatch(line, root)
				if err != nil {
					parseErr = err
				} else if ok {
					if event.Context {
						// Context line (-B/-A): attach to the file's group when it
						// already has samples; otherwise buffer it until the first
						// real match arrives (rg emits -B lines before it). Never
						// counts toward stats, offsets, or sample limits. Once the
						// sample quota is exhausted, drop context lines too: they
						// belong to matches that will never be sampled. The quota
						// check is direct (totalMatches >= maxMatches) because
						// sampleLimitReached is only set when the next match
						// arrives, after this context line.
						if sampleLimitReached || totalMatches >= maxMatches {
							continue
						}
						if g := groupByPath[event.Path]; g != nil {
							content, contentTruncated := truncateLine(event.Content, maxMatchPreviewBytes)
							g.matches = append(g.matches, Match{
								LineNum:          event.LineNum,
								Content:          content,
								ContentTruncated: contentTruncated,
								Context:          true,
							})
						} else {
							pending := pendingContext[event.Path]
							if len(pending) < maxContextPending {
								content, contentTruncated := truncateLine(event.Content, maxMatchPreviewBytes)
								pending = append(pending, Match{
									LineNum:          event.LineNum,
									Content:          content,
									ContentTruncated: contentTruncated,
									Context:          true,
								})
								pendingContext[event.Path] = pending
							}
						}
						continue
					}
					if seen < req.Offset {
						// Within the skip window: count the consumed match but
						// do not sample it. Drop its buffered before-context so
						// skipped matches do not leak context lines.
						seen++
						delete(pendingContext, event.Path)
					} else {
						g := groupByPath[event.Path]
						if g == nil && len(groups) < maxFiles {
							// Initialize the slice so an empty group (sample quota
							// exhausted before its first match was sampled)
							// serializes as [] instead of null.
							g = &sampleFile{path: event.Path, matches: []Match{}}
							groupByPath[event.Path] = g
							groups = append(groups, g)
						}
						if g != nil && totalMatches < maxMatches {
							// Flush buffered before-context first so the group
							// reads in file order.
							if pending := pendingContext[event.Path]; len(pending) > 0 {
								g.matches = append(g.matches, pending...)
								delete(pendingContext, event.Path)
							}
							content, contentTruncated := truncateLine(event.Content, maxMatchPreviewBytes)
							g.matches = append(g.matches, Match{
								LineNum:          event.LineNum,
								Content:          content,
								ContentTruncated: contentTruncated,
							})
							totalMatches++
							seen++
						} else {
							sampleLimitReached = true
							truncated = true
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
		return nil, nil, 0, nil, false, false, parseErr
	}
	if ctx.Err() != nil {
		return nil, nil, 0, nil, false, false, ctx.Err()
	}
	if waitErr != nil && !killed {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			if exitErr.ExitCode() != 1 {
				msg := strings.TrimSpace(errBuf.String())
				if msg == "" {
					msg = waitErr.Error()
				}
				return nil, nil, 0, nil, false, false, failureError(msg)
			}
		} else {
			return nil, nil, 0, nil, false, false, waitErr
		}
	}

	fileHits := make([]FileMatch, 0, len(groups))
	for _, g := range groups {
		fileHits = append(fileHits, FileMatch{Path: g.path, Matches: g.matches})
	}
	// nextOffset is non-zero only when more matches remain after the sample
	// window: either the sample cap was hit, or the stream was cut short
	// before the summary (so the count pass will report the true total and
	// the model can page further with offset=nextOffset).
	nextOffset := 0
	if truncated && seen > req.Offset {
		nextOffset = seen
	}
	// OffsetExhausted: the skip window consumed every match, so no samples
	// exist at this offset even though the search found hits. seen == 0
	// (no matches at all) is not exhaustion.
	offsetExhausted := req.Offset > 0 && seen > 0 && seen <= req.Offset
	return fileHits, endCounts, nextOffset, stats, truncated, offsetExhausted, nil
}

// sampleFile accumulates matches for one file path during sample collection.
type sampleFile struct {
	path    string
	matches []Match
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

type matchEvent struct {
	Path        string
	LineNum     int
	Content     string
	Occurrences int
	// Context marks a surrounding line emitted by rg -B/-A rather than a
	// real match. Context lines never count toward stats, offsets, or
	// per-file counts.
	Context bool
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
	// ripgrep --json emits surrounding lines (from -B/-A) as dedicated
	// "context" events; real matches are "match" events with submatches.
	if event.Data.Path.Text == "" {
		return matchEvent{}, false, nil
	}
	rel := DisplayPathForRoot(root, event.Data.Path.Text)
	content := strings.TrimRight(event.Data.Lines.Text, "\r\n")
	if event.Type == "context" {
		return matchEvent{
			Path:    rel,
			LineNum: event.Data.LineNumber,
			Content: content,
			Context: true,
		}, true, nil
	}
	if event.Type != "match" {
		return matchEvent{}, false, nil
	}
	occurrences := len(event.Data.Submatches)
	if occurrences == 0 {
		occurrences = 1
	}
	return matchEvent{
		Path:        rel,
		LineNum:     event.Data.LineNumber,
		Content:     content,
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

// truncateLine caps a match preview to maxLen bytes. The cut point is walked
// back to the previous UTF-8 rune boundary so we never slice through a
// multi-byte sequence — slicing at an arbitrary byte would produce invalid
// UTF-8 that JSON-serializes to U+FFFD and renders as garbage in the UI for
// CJK/emoji content. The second return value reports whether the line was
// shortened, so callers can surface the cap to the model instead of letting
// it mistake the truncated preview for the full line.
func truncateLine(line string, maxLen int) (string, bool) {
	if len(line) <= maxLen {
		return line, false
	}
	end := maxLen
	for end > 0 && !utf8.RuneStart(line[end]) {
		end--
	}
	return line[:end] + "...", true
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

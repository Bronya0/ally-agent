// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package grep

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// requireRipgrep skips the test when rg is unavailable so the suite still runs
// on machines without ripgrep installed.
func requireRipgrep(t *testing.T) string {
	t.Helper()
	rg, err := exec.LookPath("rg")
	if err != nil {
		t.Skip("ripgrep is not installed")
	}
	return rg
}

func writeGrepTestFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSearchExactStatsSinglePass(t *testing.T) {
	rg := requireRipgrep(t)
	root := t.TempDir()
	writeGrepTestFile(t, root, "a.txt", "foo\nfoo bar\nx foo\nplain\n")
	writeGrepTestFile(t, root, "b.txt", "foo\n")

	result, err := Search(context.Background(), rg, root, root, Request{Pattern: "foo"})
	if err != nil {
		t.Fatal(err)
	}
	// a.txt: 3 matching lines / 3 matches; b.txt: 1/1.
	if result.MatchedLines != 4 || result.Hits != 4 || result.Files != 2 {
		t.Fatalf("unexpected stats: %#v", result)
	}
	if result.Truncated || result.SamplesTruncated || !result.StatsExact {
		t.Fatalf("small search must not be truncated: %#v", result)
	}
	if len(result.FileHits) != 2 {
		t.Fatalf("expected samples from both files, got %#v", result.FileHits)
	}
	totalSamples := 0
	for _, fh := range result.FileHits {
		totalSamples += len(fh.Matches)
		if fh.Path != "a.txt" && fh.Path != "b.txt" {
			t.Fatalf("unexpected sample path %q", fh.Path)
		}
	}
	if totalSamples != 4 {
		t.Fatalf("expected 4 sample matches, got %d", totalSamples)
	}
}

func TestSearchTruncatedByMatchLimitKeepsExactStats(t *testing.T) {
	rg := requireRipgrep(t)
	root := t.TempDir()
	var b strings.Builder
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, "line %d needle\n", i)
	}
	writeGrepTestFile(t, root, "big.txt", b.String())

	result, err := Search(context.Background(), rg, root, root, Request{
		Pattern:    "needle",
		MaxMatches: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Truncated || !result.SamplesTruncated {
		t.Fatalf("expected sample truncation, got %#v", result)
	}
	if result.MatchedLines != 200 || result.Hits != 200 || result.Files != 1 || !result.StatsExact {
		t.Fatalf("truncated search must still report exact stats, got %#v", result)
	}
	if len(result.FileHits) != 1 || len(result.FileHits[0].Matches) != 50 {
		t.Fatalf("expected 50 bounded sample matches, got %#v", result.FileHits)
	}
}

func TestSearchTruncatedByFileLimitKeepsExactStats(t *testing.T) {
	rg := requireRipgrep(t)
	root := t.TempDir()
	for f := 0; f < 5; f++ {
		name := fmt.Sprintf("file%d.txt", f)
		var b strings.Builder
		for i := 0; i < 10; i++ {
			fmt.Fprintf(&b, "row %d needle\n", i)
		}
		writeGrepTestFile(t, root, name, b.String())
	}

	result, err := Search(context.Background(), rg, root, root, Request{
		Pattern:  "needle",
		MaxFiles: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Truncated || !result.SamplesTruncated {
		t.Fatalf("expected file-limit truncation, got %#v", result)
	}
	if result.MatchedLines != 50 || result.Hits != 50 || result.Files != 5 || !result.StatsExact {
		t.Fatalf("file-truncated search must still report exact stats, got %#v", result)
	}
	if len(result.FileHits) != 1 || len(result.FileHits[0].Matches) != 10 {
		t.Fatalf("expected one file's full sample, got %#v", result.FileHits)
	}
}

func TestSearchNoMatch(t *testing.T) {
	rg := requireRipgrep(t)
	root := t.TempDir()
	writeGrepTestFile(t, root, "a.txt", "nothing here\n")

	result, err := Search(context.Background(), rg, root, root, Request{Pattern: "zzz-never-matches"})
	if err != nil {
		t.Fatal(err)
	}
	if result.MatchedLines != 0 || result.Hits != 0 || result.Files != 0 {
		t.Fatalf("expected zero stats, got %#v", result)
	}
	if result.Truncated || result.SamplesTruncated || len(result.FileHits) != 0 {
		t.Fatalf("expected clean empty result, got %#v", result)
	}
}

func TestSearchIncludeIgnored(t *testing.T) {
	rg := requireRipgrep(t)
	root := t.TempDir()
	// .ignore is honored everywhere (unlike .gitignore, which only applies
	// inside a git repository).
	writeGrepTestFile(t, root, ".ignore", "ignored.txt\n")
	writeGrepTestFile(t, root, "kept.txt", "needle\n")
	writeGrepTestFile(t, root, "ignored.txt", "needle\n")

	defaultResult, err := Search(context.Background(), rg, root, root, Request{Pattern: "needle"})
	if err != nil {
		t.Fatal(err)
	}
	if defaultResult.MatchedLines != 1 {
		t.Fatalf("expected ignored file to be skipped by default, got %#v", defaultResult)
	}

	includeResult, err := Search(context.Background(), rg, root, root, Request{Pattern: "needle", IncludeIgnored: true})
	if err != nil {
		t.Fatal(err)
	}
	if includeResult.MatchedLines != 2 || includeResult.Files != 2 {
		t.Fatalf("expected ignored file to be included, got %#v", includeResult)
	}
}

func TestSearchMarksTruncatedMatchContent(t *testing.T) {
	rg := requireRipgrep(t)
	root := t.TempDir()
	long := strings.Repeat("界", 300) + " needle"
	writeGrepTestFile(t, root, "long.txt", long+"\nshort needle\n")

	result, err := Search(context.Background(), rg, root, root, Request{Pattern: "needle"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.FileHits) != 1 {
		t.Fatalf("expected one file, got %#v", result.FileHits)
	}
	byLine := map[int]Match{}
	for _, m := range result.FileHits[0].Matches {
		byLine[m.LineNum] = m
	}
	longMatch, ok := byLine[1]
	if !ok {
		t.Fatalf("expected line 1 match, got %#v", byLine)
	}
	if !longMatch.ContentTruncated {
		t.Fatalf("long line must be marked contentTruncated, got %#v", longMatch)
	}
	if len(longMatch.Content) > maxMatchPreviewBytes+len("...") || !strings.HasSuffix(longMatch.Content, "...") {
		t.Fatalf("truncated content must stay bounded and end with ..., got %d bytes %q", len(longMatch.Content), longMatch.Content)
	}
	shortMatch, ok := byLine[2]
	if !ok {
		t.Fatalf("expected line 2 match, got %#v", byLine)
	}
	if shortMatch.ContentTruncated || shortMatch.Content != "short needle" {
		t.Fatalf("short line must not be marked truncated, got %#v", shortMatch)
	}
}

func TestSearchManyFilesFallbackKeepsExactStats(t *testing.T) {
	// Regression: the fallback count pass parses the trailing --stats block,
	// which comes AFTER one path:N line per matching file. Collecting the
	// first 128 lines used to evict the stats block once >124 files matched,
	// silently zeroing `files` or failing hard. The pass must stay correct
	// regardless of how many files match.
	rg := requireRipgrep(t)
	root := t.TempDir()
	const files = 130
	for i := 0; i < files; i++ {
		writeGrepTestFile(t, root, fmt.Sprintf("f%03d.txt", i), "needle\n")
	}

	result, err := Search(context.Background(), rg, root, root, Request{
		Pattern:    "needle",
		MaxFiles:   1, // force the sample pass to truncate -> count fallback
		MaxMatches: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.MatchedLines != files || result.Hits != files || result.Files != files || !result.StatsExact {
		t.Fatalf("fallback stats must stay exact for %d files, got %#v", files, result)
	}
	if !result.Truncated || !result.SamplesTruncated || len(result.FileHits) != 1 {
		t.Fatalf("expected truncated single-file samples, got %#v", result)
	}
}

func TestParseStatsValue(t *testing.T) {
	cases := []struct {
		line   string
		suffix string
		want   int
		ok     bool
	}{
		{"5 matches", " matches", 5, true},
		{"5 matched lines", " matched lines", 5, true},
		{"1 files contained matches", " files contained matches", 1, true},
		{"  12 matches", " matches", 12, true},
		{"path:5", " matches", 0, false},
		{"", " matches", 0, false},
		{"abc matches", " matches", 0, false},
	}
	for _, c := range cases {
		got, ok := parseStatsValue(c.line, c.suffix)
		if got != c.want || ok != c.ok {
			t.Fatalf("parseStatsValue(%q, %q) = (%d, %v), want (%d, %v)", c.line, c.suffix, got, ok, c.want, c.ok)
		}
	}
}

func TestParseSummaryStatsIgnoresNonSummaryEvents(t *testing.T) {
	if s := parseSummaryStats([]byte(`{"type":"begin","data":{}}`)); s != nil {
		t.Fatalf("begin event must not produce stats, got %#v", s)
	}
	if s := parseSummaryStats([]byte(`not json`)); s != nil {
		t.Fatalf("invalid json must not produce stats, got %#v", s)
	}
}

func TestSearchReportsPerFileMatchCount(t *testing.T) {
	rg := requireRipgrep(t)
	root := t.TempDir()
	writeGrepTestFile(t, root, "a.txt", "foo foo\nfoo\n")
	writeGrepTestFile(t, root, "b.txt", "foo\n")

	result, err := Search(context.Background(), rg, root, root, Request{Pattern: "foo"})
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]int{}
	for _, fh := range result.FileHits {
		byPath[fh.Path] = fh.MatchCount
	}
	if byPath["a.txt"] != 3 {
		t.Fatalf("a.txt matchCount = %d, want 3", byPath["a.txt"])
	}
	if byPath["b.txt"] != 1 {
		t.Fatalf("b.txt matchCount = %d, want 1", byPath["b.txt"])
	}
	// matchCount is exact even though samples are capped at one line per
	// file in the common case.
	for _, fh := range result.FileHits {
		if len(fh.Matches) < 1 || fh.MatchCount < len(fh.Matches) {
			t.Fatalf("matchCount must be >= sampled lines, got %#v", fh)
		}
	}
}

func TestSearchFileCountsSortedDescending(t *testing.T) {
	rg := requireRipgrep(t)
	root := t.TempDir()
	writeGrepTestFile(t, root, "few.txt", "foo\n")
	writeGrepTestFile(t, root, "many.txt", "foo foo foo\n")
	writeGrepTestFile(t, root, "mid.txt", "foo foo\n")

	result, err := Search(context.Background(), rg, root, root, Request{Pattern: "foo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.FileCounts) != 3 {
		t.Fatalf("expected 3 fileCounts entries, got %#v", result.FileCounts)
	}
	if result.FileCounts[0].Path != "many.txt" || result.FileCounts[0].Count != 3 {
		t.Fatalf("expected many.txt(3) first, got %#v", result.FileCounts[0])
	}
	if result.FileCounts[1].Path != "mid.txt" || result.FileCounts[1].Count != 2 {
		t.Fatalf("expected mid.txt(2) second, got %#v", result.FileCounts[1])
	}
	if result.FileCounts[2].Path != "few.txt" || result.FileCounts[2].Count != 1 {
		t.Fatalf("expected few.txt(1) last, got %#v", result.FileCounts[2])
	}
	if result.FileCountsTruncated {
		t.Fatalf("3 files must not be truncated, got %#v", result)
	}
}

func TestSearchOffsetPagesThroughMatches(t *testing.T) {
	rg := requireRipgrep(t)
	root := t.TempDir()
	var b strings.Builder
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&b, "line %d needle\n", i)
	}
	writeGrepTestFile(t, root, "big.txt", b.String())

	first, err := Search(context.Background(), rg, root, root, Request{
		Pattern:    "needle",
		MaxMatches: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Truncated || first.NextOffset != 5 {
		t.Fatalf("expected truncated first page with NextOffset 5, got %#v", first)
	}
	if first.MatchedLines != 20 || first.Hits != 20 || first.Files != 1 {
		t.Fatalf("first page must still report exact stats, got %#v", first)
	}
	if len(first.FileHits) != 1 || len(first.FileHits[0].Matches) != 5 {
		t.Fatalf("expected 5 sample matches on page 1, got %#v", first.FileHits)
	}
	if first.FileHits[0].MatchCount != 20 {
		t.Fatalf("matchCount must be the full-file exact count, got %d", first.FileHits[0].MatchCount)
	}

	second, err := Search(context.Background(), rg, root, root, Request{
		Pattern:    "needle",
		MaxMatches: 5,
		Offset:     first.NextOffset,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.NextOffset != 10 {
		t.Fatalf("expected NextOffset 10 on page 2, got %d", second.NextOffset)
	}
	if len(second.FileHits) != 1 || len(second.FileHits[0].Matches) != 5 {
		t.Fatalf("expected 5 sample matches on page 2, got %#v", second.FileHits)
	}
	// Page 2 must show lines 6-10 (1-based), distinct from page 1.
	lineNums := map[int]bool{}
	for _, m := range second.FileHits[0].Matches {
		lineNums[m.LineNum] = true
	}
	for want := 6; want <= 10; want++ {
		if !lineNums[want] {
			t.Fatalf("page 2 must contain line %d, got %#v", want, lineNums)
		}
	}

	last, err := Search(context.Background(), rg, root, root, Request{
		Pattern:    "needle",
		MaxMatches: 5,
		Offset:     15,
	})
	if err != nil {
		t.Fatal(err)
	}
	if last.NextOffset != 0 || last.Truncated || last.OffsetExhausted {
		t.Fatalf("page at offset 15 must reach the end without truncation or exhaustion, got %#v", last)
	}
	if len(last.FileHits) != 1 || len(last.FileHits[0].Matches) != 5 {
		t.Fatalf("expected 5 remaining sample matches, got %#v", last.FileHits)
	}
}

func TestSearchCaseSensitiveFlag(t *testing.T) {
	rg := requireRipgrep(t)
	root := t.TempDir()
	writeGrepTestFile(t, root, "a.txt", "Foo\nfoo\n")

	insensitive, err := Search(context.Background(), rg, root, root, Request{Pattern: "foo"})
	if err != nil {
		t.Fatal(err)
	}
	if insensitive.MatchedLines != 2 {
		t.Fatalf("default must be case-insensitive, got %#v", insensitive)
	}

	sensitive, err := Search(context.Background(), rg, root, root, Request{Pattern: "foo", CaseSensitive: true})
	if err != nil {
		t.Fatal(err)
	}
	if sensitive.MatchedLines != 1 || sensitive.Hits != 1 {
		t.Fatalf("caseSensitive must match only exact case, got %#v", sensitive)
	}
}

func TestParseEndEvent(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "a.txt")
	// Marshal the path so backslashes on Windows are properly JSON-escaped.
	pathJSON, err := json.Marshal(abs)
	if err != nil {
		t.Fatal(err)
	}
	line := []byte(`{"type":"end","data":{"path":{"text":` + string(pathJSON) + `},"stats":{"matches":3}}}`)
	path, count, ok := parseEndEvent(line, root)
	if !ok || path != "a.txt" || count != 3 {
		t.Fatalf("parseEndEvent = (%q, %d, %v), want (a.txt, 3, true)", path, count, ok)
	}
	if _, _, ok := parseEndEvent([]byte(`{"type":"begin","data":{}}`), root); ok {
		t.Fatalf("begin event must not parse as end event")
	}
	if _, _, ok := parseEndEvent([]byte(`not json`), root); ok {
		t.Fatalf("invalid json must not parse as end event")
	}
}

func TestParsePerFileCount(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "a.txt")
	path, count, ok := parsePerFileCount(abs+":3", root)
	if !ok || path != "a.txt" || count != 3 {
		t.Fatalf("parsePerFileCount = (%q, %d, %v), want (a.txt, 3, true)", path, count, ok)
	}
	if _, _, ok := parsePerFileCount("4 matches", root); ok {
		t.Fatalf("stats line must not parse as per-file count")
	}
	if _, _, ok := parsePerFileCount("", root); ok {
		t.Fatalf("empty line must not parse as per-file count")
	}
}

func TestSearchFileHitsSortedByMatchCount(t *testing.T) {
	rg := requireRipgrep(t)
	root := t.TempDir()
	// Deliberately create files so rg's traversal order is NOT count order:
	// a.txt (1 hit, alphabetically first) vs z.txt (3 hits).
	writeGrepTestFile(t, root, "a.txt", "foo\n")
	writeGrepTestFile(t, root, "m.txt", "foo foo\n")
	writeGrepTestFile(t, root, "z.txt", "foo foo foo\n")

	result, err := Search(context.Background(), rg, root, root, Request{Pattern: "foo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.FileHits) != 3 {
		t.Fatalf("expected 3 file groups, got %#v", result.FileHits)
	}
	// Most-relevant file (most hits) must be listed first so the model reads
	// it first without extra calls.
	if result.FileHits[0].Path != "z.txt" || result.FileHits[0].MatchCount != 3 {
		t.Fatalf("expected z.txt (3 hits) first, got %#v", result.FileHits[0])
	}
	if result.FileHits[1].Path != "m.txt" || result.FileHits[1].MatchCount != 2 {
		t.Fatalf("expected m.txt (2 hits) second, got %#v", result.FileHits[1])
	}
	if result.FileHits[2].Path != "a.txt" || result.FileHits[2].MatchCount != 1 {
		t.Fatalf("expected a.txt (1 hit) last, got %#v", result.FileHits[2])
	}
}

func TestSearchContextLines(t *testing.T) {
	rg := requireRipgrep(t)
	root := t.TempDir()
	writeGrepTestFile(t, root, "a.txt", "before-2\nbefore-1\nneedle\nafter-1\nafter-2\n")

	result, err := Search(context.Background(), rg, root, root, Request{
		Pattern:       "needle",
		ContextBefore: 1,
		ContextAfter:  1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.FileHits) != 1 {
		t.Fatalf("expected one file, got %#v", result.FileHits)
	}
	matches := result.FileHits[0].Matches
	// 1 before-context + 1 match + 1 after-context = 3 lines.
	if len(matches) != 3 {
		t.Fatalf("expected 3 sample lines (1 ctx + 1 match + 1 ctx), got %#v", matches)
	}
	if matches[0].LineNum != 2 || !matches[0].Context || matches[0].Content != "before-1" {
		t.Fatalf("expected before-context line 2, got %#v", matches[0])
	}
	if matches[1].LineNum != 3 || matches[1].Context || matches[1].Content != "needle" {
		t.Fatalf("expected match line 3 without context flag, got %#v", matches[1])
	}
	if matches[2].LineNum != 4 || !matches[2].Context || matches[2].Content != "after-1" {
		t.Fatalf("expected after-context line 4, got %#v", matches[2])
	}
	// Context lines must NOT inflate stats or per-file counts.
	if result.MatchedLines != 1 || result.Hits != 1 || result.Files != 1 {
		t.Fatalf("stats must count only real matches, got %#v", result)
	}
	if result.FileHits[0].MatchCount != 1 {
		t.Fatalf("matchCount must count only real matches, got %d", result.FileHits[0].MatchCount)
	}
}

func TestSearchContextLinesDoNotAffectPagination(t *testing.T) {
	rg := requireRipgrep(t)
	root := t.TempDir()
	writeGrepTestFile(t, root, "a.txt", "x1\nneedle\nx2\nneedle\nx3\nneedle\nx4\n")

	result, err := Search(context.Background(), rg, root, root, Request{
		Pattern:       "needle",
		MaxMatches:    2,
		ContextBefore: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 3 real matches, cap 2 -> truncated with nextOffset 2.
	if !result.Truncated || result.NextOffset != 2 {
		t.Fatalf("expected truncated page with NextOffset 2, got %#v", result)
	}
	if result.MatchedLines != 3 || result.Hits != 3 {
		t.Fatalf("stats must be exact with context lines present, got %#v", result)
	}
	// Each sampled match carries its before-context line.
	for _, fh := range result.FileHits {
		ctxLines := 0
		for _, m := range fh.Matches {
			if m.Context {
				ctxLines++
			}
		}
		if ctxLines != 2 {
			t.Fatalf("expected 2 context lines across 2 sampled matches, got %#v", fh.Matches)
		}
	}
}

func TestSearchEmptyGroupSerializesAsArray(t *testing.T) {
	// Regression: when the sample quota is hit, the file group created for
	// the next file holds no samples. Its Matches slice must serialize as
	// [] (never null) so the model cannot misread "matches: null" as a
	// broken entry.
	rg := requireRipgrep(t)
	root := t.TempDir()
	writeGrepTestFile(t, root, "a.txt", "needle\n")
	writeGrepTestFile(t, root, "b.txt", "needle\n")

	result, err := Search(context.Background(), rg, root, root, Request{
		Pattern:    "needle",
		MaxMatches: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.FileHits) != 2 {
		t.Fatalf("expected 2 file groups (one empty), got %#v", result.FileHits)
	}
	// rg traversal order is not guaranteed; assert the shape instead:
	// exactly one group sampled its match, the other is an empty group.
	emptyGroups, sampledGroups := 0, 0
	for _, fh := range result.FileHits {
		switch len(fh.Matches) {
		case 0:
			emptyGroups++
		case 1:
			sampledGroups++
		}
	}
	if emptyGroups != 1 || sampledGroups != 1 {
		t.Fatalf("expected [1 sample, empty group] across 2 files, got %#v", result.FileHits)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(`"matches":null`)) {
		t.Fatalf("empty sample group must serialize as [], got %s", raw)
	}
	if !result.Truncated || result.NextOffset != 1 {
		t.Fatalf("expected truncated page with NextOffset 1, got %#v", result)
	}
}

func TestSearchOffsetExhausted(t *testing.T) {
	rg := requireRipgrep(t)
	root := t.TempDir()
	var b strings.Builder
	for i := 0; i < 10; i++ {
		fmt.Fprintf(&b, "line %d needle\n", i)
	}
	writeGrepTestFile(t, root, "big.txt", b.String())

	result, err := Search(context.Background(), rg, root, root, Request{
		Pattern: "needle",
		Offset:  99999,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.OffsetExhausted {
		t.Fatalf("offset past the end must set OffsetExhausted, got %#v", result)
	}
	if len(result.FileHits) != 0 {
		t.Fatalf("no samples may be returned at an exhausted offset, got %#v", result.FileHits)
	}
	if result.MatchedLines != 10 || result.Hits != 10 || result.Files != 1 {
		t.Fatalf("stats must stay exact at an exhausted offset, got %#v", result)
	}
	if result.NextOffset != 0 || result.Truncated {
		t.Fatalf("exhausted offset must not claim more matches, got %#v", result)
	}

	// An in-range offset must not set the flag.
	ok, err := Search(context.Background(), rg, root, root, Request{
		Pattern:    "needle",
		Offset:     5,
		MaxMatches: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ok.OffsetExhausted {
		t.Fatalf("in-range offset must not set OffsetExhausted, got %#v", ok)
	}
}

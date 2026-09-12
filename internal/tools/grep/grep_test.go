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

// lineSet collects the line numbers of one path from a lines-mode result.
func lineSet(hits []LineFileMatch, path string) map[int]bool {
	for _, h := range hits {
		if h.Path == path {
			set := map[int]bool{}
			for _, n := range h.Lines {
				set[n] = true
			}
			return set
		}
	}
	return nil
}

func TestSearchDefaultReturnsLineNumbersOnly(t *testing.T) {
	rg := requireRipgrep(t)
	root := t.TempDir()
	writeGrepTestFile(t, root, "a.txt", "foo\n")
	writeGrepTestFile(t, root, "b.txt", "foo\nfoo\n")

	result, err := Search(context.Background(), rg, root, root, Request{Pattern: "foo"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Mode != OutputModeLines || len(result.LineHits) != 2 {
		t.Fatalf("default lines mode must group line numbers by file, got %#v", result)
	}
	if set := lineSet(result.LineHits, "a.txt"); len(set) != 1 || !set[1] {
		t.Fatalf("a.txt must report line 1, got %#v", result.LineHits)
	}
	if set := lineSet(result.LineHits, "b.txt"); len(set) != 2 || !set[1] || !set[2] {
		t.Fatalf("b.txt must report lines 1 and 2, got %#v", result.LineHits)
	}
	if result.MatchedLines != 3 || result.Hits != 3 || result.Files != 2 || !result.StatsExact {
		t.Fatalf("lines mode must preserve exact stats, got %#v", result)
	}
}

func TestSearchExactStatsSinglePass(t *testing.T) {
	rg := requireRipgrep(t)
	root := t.TempDir()
	writeGrepTestFile(t, root, "a.txt", "foo\nfoo bar\nx foo\nplain\n")
	writeGrepTestFile(t, root, "b.txt", "foo\n")

	result, err := Search(context.Background(), rg, root, root, Request{Pattern: "foo", OutputMode: OutputModeLines})
	if err != nil {
		t.Fatal(err)
	}
	// a.txt: 3 matching lines; b.txt: 1.
	if result.MatchedLines != 4 || result.Hits != 4 || result.Files != 2 {
		t.Fatalf("unexpected stats: %#v", result)
	}
	if result.Truncated || !result.StatsExact {
		t.Fatalf("small search must not be truncated: %#v", result)
	}
	if len(result.LineHits) != 2 {
		t.Fatalf("expected line groups from both files, got %#v", result.LineHits)
	}
	total := 0
	for _, h := range result.LineHits {
		total += len(h.Lines)
		if h.Path != "a.txt" && h.Path != "b.txt" {
			t.Fatalf("unexpected group path %q", h.Path)
		}
	}
	if total != 4 {
		t.Fatalf("expected 4 line entries, got %d", total)
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
		OutputMode: OutputModeLines,
		MaxMatches: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Truncated {
		t.Fatalf("expected truncation, got %#v", result)
	}
	if result.MatchedLines != 200 || result.Hits != 200 || result.Files != 1 || !result.StatsExact {
		t.Fatalf("truncated search must still report exact stats, got %#v", result)
	}
	if len(result.LineHits) != 1 || len(result.LineHits[0].Lines) != 50 {
		t.Fatalf("expected 50 bounded line numbers, got %#v", result.LineHits)
	}
}

func TestSearchTruncatedPreservesPerFileOccurrenceCounts(t *testing.T) {
	rg := requireRipgrep(t)
	root := t.TempDir()
	writeGrepTestFile(t, root, "hot.txt", "foo foo\nfoo foo\n")
	writeGrepTestFile(t, root, "cold.txt", "foo\n")

	result, err := Search(context.Background(), rg, root, root, Request{
		Pattern:    "foo",
		OutputMode: OutputModeCountMatches,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Hits != 5 || result.MatchedLines != 3 || result.Files != 2 {
		t.Fatalf("unexpected exact stats: %#v", result)
	}
	counts := map[string]int{}
	for _, item := range result.FileCounts {
		counts[item.Path] = item.Count
	}
	if counts["hot.txt"] != 4 || counts["cold.txt"] != 1 {
		t.Fatalf("per-file occurrence counts must be exact, got %#v", result.FileCounts)
	}
	// count_matches carries no line numbers.
	if len(result.LineHits) != 0 {
		t.Fatalf("count mode must not return line groups, got %#v", result.LineHits)
	}
}

func TestSearchTruncatedByLineBudgetKeepsExactStats(t *testing.T) {
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
		Pattern:    "needle",
		OutputMode: OutputModeLines,
		MaxMatches: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Truncated {
		t.Fatalf("expected budget truncation, got %#v", result)
	}
	if result.MatchedLines != 50 || result.Hits != 50 || result.Files != 5 || !result.StatsExact {
		t.Fatalf("budget-truncated search must still report exact stats, got %#v", result)
	}
	// rg streams matches file-by-file, so the budget is consumed by whole
	// files: one full 10-line group here, then the next matching file trips
	// the budget without creating a group.
	if len(result.LineHits) != 1 || len(result.LineHits[0].Lines) != 10 {
		t.Fatalf("expected one file's full line group, got %#v", result.LineHits)
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
	if result.Truncated || len(result.LineHits) != 0 {
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

	defaultResult, err := Search(context.Background(), rg, root, root, Request{Pattern: "needle", OutputMode: OutputModeLines})
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

func TestSearchReturnsLineNumbersForLongLines(t *testing.T) {
	rg := requireRipgrep(t)
	root := t.TempDir()
	long := strings.Repeat("界", 300) + " needle"
	writeGrepTestFile(t, root, "long.txt", long+"\nshort needle\n")

	result, err := Search(context.Background(), rg, root, root, Request{Pattern: "needle", OutputMode: OutputModeLines})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.LineHits) != 1 {
		t.Fatalf("expected one file, got %#v", result.LineHits)
	}
	// Line text is never returned; only the 1-based line numbers survive,
	// regardless of how long the matching line is.
	if set := lineSet(result.LineHits, "long.txt"); len(set) != 2 || !set[1] || !set[2] {
		t.Fatalf("expected line numbers 1 and 2, got %#v", result.LineHits)
	}
}

func TestSearchManyFilesFallbackKeepsExactStats(t *testing.T) {
	// Regression: the single JSON pass must keep the trailing summary exact
	// even when many files match and sampling stops after the first file.
	rg := requireRipgrep(t)
	root := t.TempDir()
	const files = 130
	for i := 0; i < files; i++ {
		writeGrepTestFile(t, root, fmt.Sprintf("f%03d.txt", i), "needle\n")
	}

	result, err := Search(context.Background(), rg, root, root, Request{
		Pattern:    "needle",
		OutputMode: OutputModeLines,
		// Force sample truncation while the stream keeps draining: the line
		// budget is tiny relative to the 130 matching files.
		MaxMatches: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.MatchedLines != files || result.Hits != files || result.Files != files || !result.StatsExact {
		t.Fatalf("fallback stats must stay exact for %d files, got %#v", files, result)
	}
	if !result.Truncated || len(result.LineHits) != 5 {
		t.Fatalf("expected truncated samples limited by the line budget, got %#v", result)
	}
	for _, fh := range result.LineHits {
		if len(fh.Lines) != 1 {
			t.Fatalf("expected one sampled line per file, got %#v", fh)
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

func TestSearchCountMatchesReturnsOnlyFileCounts(t *testing.T) {
	rg := requireRipgrep(t)
	root := t.TempDir()
	writeGrepTestFile(t, root, "a.txt", "foo foo\n")
	writeGrepTestFile(t, root, "b.txt", "foo\nfoo\nfoo\n")

	result, err := Search(context.Background(), rg, root, root, Request{
		Pattern:    "foo",
		OutputMode: OutputModeCountMatches,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Mode != OutputModeCountMatches || len(result.LineHits) != 0 {
		t.Fatalf("count mode must not return line groups, got %#v", result)
	}
	if len(result.FileCounts) != 2 || result.FileCounts[0].Path != "b.txt" || result.FileCounts[0].Count != 3 || result.FileCounts[1].Path != "a.txt" || result.FileCounts[1].Count != 2 {
		t.Fatalf("expected exact per-file counts, got %#v", result.FileCounts)
	}
	if result.MatchedLines != 4 || result.Hits != 5 || result.Files != 2 || !result.StatsExact {
		t.Fatalf("count mode must preserve exact totals, got %#v", result)
	}
}

func TestSearchFileCountsSortedDescending(t *testing.T) {
	rg := requireRipgrep(t)
	root := t.TempDir()
	writeGrepTestFile(t, root, "few.txt", "foo\n")
	writeGrepTestFile(t, root, "many.txt", "foo foo foo\n")
	writeGrepTestFile(t, root, "mid.txt", "foo foo\n")

	result, err := Search(context.Background(), rg, root, root, Request{Pattern: "foo", OutputMode: OutputModeCountMatches})
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
}

func TestSearchOffsetPagesThroughLines(t *testing.T) {
	rg := requireRipgrep(t)
	root := t.TempDir()
	var b strings.Builder
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&b, "line %d needle\n", i)
	}
	writeGrepTestFile(t, root, "big.txt", b.String())

	first, err := Search(context.Background(), rg, root, root, Request{
		Pattern:    "needle",
		OutputMode: OutputModeLines,
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
	if len(first.LineHits) != 1 || len(first.LineHits[0].Lines) != 5 {
		t.Fatalf("expected 5 line numbers on page 1, got %#v", first.LineHits)
	}
	if set := lineSet(first.LineHits, "big.txt"); !set[1] || !set[5] || set[6] {
		t.Fatalf("page 1 must contain lines 1-5, got %#v", first.LineHits)
	}

	second, err := Search(context.Background(), rg, root, root, Request{
		Pattern:    "needle",
		OutputMode: OutputModeLines,
		MaxMatches: 5,
		Offset:     first.NextOffset,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.NextOffset != 10 {
		t.Fatalf("expected NextOffset 10 on page 2, got %d", second.NextOffset)
	}
	if len(second.LineHits) != 1 || len(second.LineHits[0].Lines) != 5 {
		t.Fatalf("expected 5 line numbers on page 2, got %#v", second.LineHits)
	}
	if set := lineSet(second.LineHits, "big.txt"); !set[6] || !set[10] || set[11] {
		t.Fatalf("page 2 must contain lines 6-10, got %#v", second.LineHits)
	}

	last, err := Search(context.Background(), rg, root, root, Request{
		Pattern:    "needle",
		OutputMode: OutputModeLines,
		MaxMatches: 5,
		Offset:     15,
	})
	if err != nil {
		t.Fatal(err)
	}
	if last.NextOffset != 0 || last.Truncated || last.OffsetExhausted {
		t.Fatalf("page at offset 15 must reach the end without truncation or exhaustion, got %#v", last)
	}
	if len(last.LineHits) != 1 || len(last.LineHits[0].Lines) != 5 {
		t.Fatalf("expected 5 remaining line numbers, got %#v", last.LineHits)
	}
	if set := lineSet(last.LineHits, "big.txt"); !set[16] || !set[20] {
		t.Fatalf("last page must contain lines 16-20, got %#v", last.LineHits)
	}
}

func TestSearchCaseSensitiveFlag(t *testing.T) {
	rg := requireRipgrep(t)
	root := t.TempDir()
	writeGrepTestFile(t, root, "a.txt", "Foo\nfoo\n")

	insensitive, err := Search(context.Background(), rg, root, root, Request{Pattern: "foo", OutputMode: OutputModeLines})
	if err != nil {
		t.Fatal(err)
	}
	if insensitive.MatchedLines != 2 {
		t.Fatalf("default must be case-insensitive, got %#v", insensitive)
	}

	sensitive, err := Search(context.Background(), rg, root, root, Request{Pattern: "foo", OutputMode: OutputModeLines, CaseSensitive: true})
	if err != nil {
		t.Fatal(err)
	}
	if sensitive.MatchedLines != 1 || sensitive.Hits != 1 {
		t.Fatalf("caseSensitive must match only exact case, got %#v", sensitive)
	}
}

func TestBaseArgsBoundsRipgrepResourcesAndHonorsExplicitPath(t *testing.T) {
	workspaceArgs := strings.Join(baseArgs(Request{}, 20), " ")
	if !strings.Contains(workspaceArgs, "--threads") {
		t.Fatalf("workspace grep must cap ripgrep threads: %s", workspaceArgs)
	}
	if !strings.Contains(workspaceArgs, "--max-filesize 10M") || !strings.Contains(workspaceArgs, "!vendor/**") {
		t.Fatalf("workspace grep must retain broad-search bounds: %s", workspaceArgs)
	}

	explicitArgs := strings.Join(baseArgs(Request{Path: "vendor"}, 20), " ")
	if strings.Contains(explicitArgs, "--max-filesize") || strings.Contains(explicitArgs, "!vendor/**") {
		t.Fatalf("explicit path must bypass broad exclusions: %s", explicitArgs)
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

func TestSearchLinesGroupedByFileStable(t *testing.T) {
	rg := requireRipgrep(t)
	root := t.TempDir()
	writeGrepTestFile(t, root, "a.txt", "foo\n")
	writeGrepTestFile(t, root, "m.txt", "foo\nfoo\n")
	writeGrepTestFile(t, root, "z.txt", "foo\nfoo\nfoo\n")

	result, err := Search(context.Background(), rg, root, root, Request{Pattern: "foo", OutputMode: OutputModeLines})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.LineHits) != 3 {
		t.Fatalf("expected 3 file groups, got %#v", result.LineHits)
	}
	// Line numbers are exact per file; order is rg's traversal order.
	if set := lineSet(result.LineHits, "a.txt"); len(set) != 1 || !set[1] {
		t.Fatalf("a.txt expected line 1, got %#v", result.LineHits)
	}
	if set := lineSet(result.LineHits, "m.txt"); len(set) != 2 || !set[1] || !set[2] {
		t.Fatalf("m.txt expected lines 1 and 2, got %#v", result.LineHits)
	}
	if set := lineSet(result.LineHits, "z.txt"); len(set) != 3 || !set[1] || !set[2] || !set[3] {
		t.Fatalf("z.txt expected lines 1-3, got %#v", result.LineHits)
	}
}

func TestSearchLineBudgetLeavesNoEmptyGroups(t *testing.T) {
	// Regression: when the line budget is hit, the next matching file must not
	// create an empty trailing group ("lines": []); groups exist only with at
	// least one sampled line, so the model never sees an empty entry.
	rg := requireRipgrep(t)
	root := t.TempDir()
	writeGrepTestFile(t, root, "a.txt", "needle\n")
	writeGrepTestFile(t, root, "b.txt", "needle\n")

	result, err := Search(context.Background(), rg, root, root, Request{
		Pattern:    "needle",
		OutputMode: OutputModeLines,
		MaxMatches: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Truncated {
		t.Fatalf("expected truncation once the budget trips, got %#v", result)
	}
	if len(result.LineHits) != 1 || len(result.LineHits[0].Lines) != 1 {
		t.Fatalf("expected exactly one one-line group (no empty trailing group), got %#v", result.LineHits)
	}
	if result.MatchedLines != 2 || result.Files != 2 || !result.StatsExact {
		t.Fatalf("expected exact stats, got %#v", result)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(`"lines":null`)) {
		t.Fatalf("empty line group must serialize as [], got %s", raw)
	}
	if !result.Truncated || result.NextOffset != 1 {
		t.Fatalf("expected truncated page with NextOffset 1, got %#v", result)
	}
}

func TestSearchCountMatchesPagesByFile(t *testing.T) {
	rg := requireRipgrep(t)
	root := t.TempDir()
	// count_matches pages over the per-file count heap with a fixed page
	// width (countPageSize); 25 files need two pages: 20 then 5.
	for i := 0; i < 25; i++ {
		writeGrepTestFile(t, root, fmt.Sprintf("f%02d.txt", i), "needle\n")
	}

	first, err := Search(context.Background(), rg, root, root, Request{Pattern: "needle", OutputMode: OutputModeCountMatches})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.FileCounts) != countPageSize || first.NextOffset != countPageSize || !first.Truncated {
		t.Fatalf("expected first page of %d file-count entries, got %#v", countPageSize, first)
	}
	second, err := Search(context.Background(), rg, root, root, Request{Pattern: "needle", OutputMode: OutputModeCountMatches, Offset: first.NextOffset})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.FileCounts) != 5 || second.NextOffset != 0 || second.OffsetExhausted {
		t.Fatalf("expected the remaining five entries to close out paging, got %#v", second)
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
		Pattern:    "needle",
		OutputMode: OutputModeLines,
		Offset:     99999,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.OffsetExhausted {
		t.Fatalf("offset past the end must set OffsetExhausted, got %#v", result)
	}
	if len(result.LineHits) != 0 {
		t.Fatalf("no line groups may be returned at an exhausted offset, got %#v", result.LineHits)
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
		OutputMode: OutputModeLines,
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

func TestSearchLegacyAliasCollapsesToLines(t *testing.T) {
	rg := requireRipgrep(t)
	root := t.TempDir()
	writeGrepTestFile(t, root, "a.txt", "needle\n")
	writeGrepTestFile(t, root, "b.txt", "needle\n")

	// The legacy files_with_matches / content aliases must collapse to the
	// flat lines mode rather than re-introducing path-only or line-text shapes.
	for _, mode := range []string{"", "files_with_matches", "content"} {
		result, err := Search(context.Background(), rg, root, root, Request{Pattern: "needle", OutputMode: mode})
		if err != nil {
			t.Fatal(err)
		}
		if result.Mode != OutputModeLines {
			t.Fatalf("alias %q must collapse to lines, got mode %q", mode, result.Mode)
		}
		if len(result.LineHits) != 2 {
			t.Fatalf("alias %q must return line groups, got %#v", mode, result.LineHits)
		}
	}
}

func TestSearchCountMatchesPaginationTerminates(t *testing.T) {
	rg := requireRipgrep(t)
	root := t.TempDir()
	// More matching files than the per-file count heap keeps (cap 100): paging
	// must end at the retained entries instead of pinning nextOffset on a page
	// that can never be produced, which made a model loop on the same offset.
	for i := 0; i < 120; i++ {
		writeGrepTestFile(t, root, fmt.Sprintf("file-%03d.txt", i), "needle\n")
	}

	offset := 0
	for page := 0; page < 10; page++ {
		result, err := Search(context.Background(), rg, root, root, Request{
			Pattern:    "needle",
			OutputMode: OutputModeCountMatches,
			Offset:     offset,
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.NextOffset == 0 {
			if offset > 0 && !result.OffsetExhausted && len(result.FileCounts) == 0 {
				t.Fatalf("an empty page must either advance or report exhaustion: %#v", result)
			}
			return
		}
		if result.NextOffset <= offset {
			t.Fatalf("nextOffset must advance past the requested page: offset=%d next=%d", offset, result.NextOffset)
		}
		offset = result.NextOffset
	}
	t.Fatalf("count_matches pagination did not terminate, stuck at offset %d", offset)
}

func TestSanitizeLineText(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"trims surrounding whitespace and newline", "  \tfunc main() {}\r\n", "func main() {}"},
		{"keeps the first line of a multi-line match", "first line\nsecond line\n", "first line"},
		{"empty result stays empty", "   \n\t", ""},
	}
	for _, tc := range cases {
		if got := sanitizeLineText(tc.in); got != tc.want {
			t.Errorf("%s: sanitizeLineText(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestSanitizeLineTextCapsAtMaxChars(t *testing.T) {
	long := strings.Repeat("a", maxGrepLineTextChars+50)
	got := sanitizeLineText(long)
	want := strings.Repeat("a", maxGrepLineTextChars) + "…"
	if got != want {
		t.Fatalf("sanitizeLineText must cap at %d chars plus ellipsis, got %d chars", maxGrepLineTextChars, len([]rune(got)))
	}
	// The cap counts runes, not bytes, so CJK previews keep whole characters.
	cjk := strings.Repeat("中", maxGrepLineTextChars+1)
	if got := sanitizeLineText(cjk); len([]rune(got)) != maxGrepLineTextChars+1 {
		t.Fatalf("CJK cap must keep %d runes plus ellipsis, got %d", maxGrepLineTextChars, len([]rune(got)))
	}
}

func TestSearchLinesCarryAlignedTextPreviews(t *testing.T) {
	rg := requireRipgrep(t)
	root := t.TempDir()
	writeGrepTestFile(t, root, "a.txt", "  foo bar  \nnothing\n\tfoo baz\t\n")
	writeGrepTestFile(t, root, "b.txt", "lorem ipsum foo\n")

	result, err := Search(context.Background(), rg, root, root, Request{Pattern: "foo", OutputMode: OutputModeLines})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.LineHits) != 2 {
		t.Fatalf("expected groups from both files, got %#v", result.LineHits)
	}
	for _, hit := range result.LineHits {
		if len(hit.Texts) != len(hit.Lines) {
			t.Fatalf("texts must run parallel to lines for %s: lines=%v texts=%v", hit.Path, hit.Lines, hit.Texts)
		}
		switch hit.Path {
		case "a.txt":
			if hit.Lines[0] != 1 || hit.Texts[0] != "foo bar" {
				t.Fatalf("a.txt line 1 must carry the trimmed text, got line %d text %q", hit.Lines[0], hit.Texts[0])
			}
			if hit.Lines[1] != 3 || hit.Texts[1] != "foo baz" {
				t.Fatalf("a.txt line 3 must carry the trimmed text, got line %d text %q", hit.Lines[1], hit.Texts[1])
			}
		case "b.txt":
			if hit.Texts[0] != "lorem ipsum foo" {
				t.Fatalf("b.txt must carry the full line text, got %q", hit.Texts[0])
			}
		}
	}
}

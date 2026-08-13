// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package grep

import (
	"context"
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

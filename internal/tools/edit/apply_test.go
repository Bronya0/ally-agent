// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package edit

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	toolerrors "ally-dev/internal/tools/shared"
)

func TestApplyBatchTextChangesReturnsBoundedMultiMatchDiagnostics(t *testing.T) {
	content := strings.Join([]string{
		"func first() {",
		"\tshared()",
		"}",
		"func second() {",
		"\tshared()",
		"}",
		"func third() {",
		"\tshared()",
		"}",
		"func fourth() {",
		"\tshared()",
		"}",
	}, "\n") + "\n"

	_, _, err := ApplyBatchTextChanges(content, []TextChange{{OldText: "\tshared()", NewText: "\tupdated()"}})
	if err == nil || toolerrors.Code(err) != "E_MULTI_MATCH" {
		t.Fatalf("expected E_MULTI_MATCH, got %v", err)
	}
	details, ok := toolerrors.Details(err).(*MatchErrorDetails)
	if !ok || details == nil {
		t.Fatalf("expected structured match details, got %#v", toolerrors.Details(err))
	}
	if details.ChangeIndex != 1 || details.MatchCount != 4 || details.MatchType != "exact" {
		t.Fatalf("unexpected details: %#v", details)
	}
	if len(details.Candidates) != maxMatchDiagnosticCandidates || !details.CandidatesTruncated {
		t.Fatalf("expected bounded candidate list, got %#v", details)
	}
	for _, candidate := range details.Candidates {
		if candidate.Line == 0 || candidate.StartLine == 0 || candidate.EndLine < candidate.StartLine {
			t.Fatalf("invalid candidate range: %#v", candidate)
		}
		if candidate.Preview == "" || len(candidate.Preview) > maxMatchDiagnosticPreviewBytes || !utf8.ValidString(candidate.Preview) {
			t.Fatalf("invalid bounded preview (%d bytes): %#v", len(candidate.Preview), candidate)
		}
	}
	raw, err := json.Marshal(details)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > maxMatchDiagnosticTotalBytes {
		t.Fatalf("diagnostics exceeded total budget: %d bytes\n%s", len(raw), raw)
	}
}

func TestApplyBatchTextChangesBoundsUnicodePreviewOnVeryLongLine(t *testing.T) {
	const needle = "目标调用()"
	segment := strings.Repeat("界", 400_000)
	content := segment + needle + segment + needle + segment

	_, _, err := ApplyBatchTextChanges(content, []TextChange{{OldText: needle, NewText: "updated()"}})
	if err == nil || toolerrors.Code(err) != "E_MULTI_MATCH" {
		t.Fatalf("expected E_MULTI_MATCH, got %v", err)
	}
	details, ok := toolerrors.Details(err).(*MatchErrorDetails)
	if !ok || details == nil || len(details.Candidates) != 2 {
		t.Fatalf("expected two structured candidates, got %#v", toolerrors.Details(err))
	}
	for _, candidate := range details.Candidates {
		if candidate.Line != 1 || candidate.StartLine != 1 || candidate.EndLine != 1 {
			t.Fatalf("long single-line candidate has wrong line metadata: %#v", candidate)
		}
		if len(candidate.Preview) > maxMatchDiagnosticPreviewBytes || !utf8.ValidString(candidate.Preview) {
			t.Fatalf("preview is not bounded valid UTF-8: %d bytes", len(candidate.Preview))
		}
		if !strings.Contains(candidate.Preview, needle) {
			t.Fatalf("preview must retain the matched text: %q", candidate.Preview)
		}
		if strings.Contains(candidate.Preview, "…") || !candidate.PreviewTruncatedBefore || !candidate.PreviewTruncatedAfter {
			t.Fatalf("preview must remain exact raw text and report clipping separately: %#v", candidate)
		}
	}
	raw, err := json.Marshal(details)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > maxMatchDiagnosticTotalBytes {
		t.Fatalf("long-line diagnostics exceeded total budget: %d bytes", len(raw))
	}
}

func TestMultiMatchDiagnosticsStayBoundedAfterJSONEscaping(t *testing.T) {
	needle := "TARGET"
	adversarial := strings.Repeat("<\\\t", 20_000)
	content := adversarial + needle + adversarial + needle + adversarial + needle + adversarial

	_, _, err := ApplyBatchTextChanges(content, []TextChange{{OldText: needle, NewText: "updated"}})
	if err == nil {
		t.Fatal("expected ambiguous edit to fail")
	}
	details, ok := toolerrors.Details(err).(*MatchErrorDetails)
	if !ok || details == nil {
		t.Fatalf("expected match details, got %#v", toolerrors.Details(err))
	}
	raw, marshalErr := json.Marshal(details)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if len(raw) > maxMatchDiagnosticTotalBytes {
		t.Fatalf("escaped diagnostics exceeded total budget: %d bytes", len(raw))
	}
}

func TestApplyBatchTextChangesReplaceAllExactMatches(t *testing.T) {
	content := "foo foo\nfoo\n"
	result, replacements, err := ApplyBatchTextChanges(content, []TextChange{{
		OldText:    "foo",
		NewText:    "bar",
		ReplaceAll: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if replacements != 3 {
		t.Fatalf("replacements = %d, want 3", replacements)
	}
	if result.Content != "bar bar\nbar\n" {
		t.Fatalf("unexpected replace-all result: %q", result.Content)
	}
}

func TestIndentationInsensitiveReindentAdvancesAcrossBlankLines(t *testing.T) {
	content := "    if ready {\n\n        run()\n    }\n"
	oldText := "if ready {\n\n    run()\n}\n"
	newText := "if ready {\n\n    execute()\n}\n"

	result, replacements, err := ApplyBatchTextChanges(content, []TextChange{{OldText: oldText, NewText: newText}})
	if err != nil {
		t.Fatal(err)
	}
	if replacements != 1 || result.Content != "    if ready {\n\n        execute()\n    }\n" {
		t.Fatalf("unexpected blank-line reindent result: replacements=%d content=%q", replacements, result.Content)
	}
}

func TestIndentationInsensitiveMatchHandlesMillionLineFile(t *testing.T) {
	const fillerLines = 1_000_000
	content := strings.Repeat("x\n", fillerLines) + "    if ready {\n        run()\n    }\n"
	oldText := "if ready {\n    run()\n}\n"
	newText := "if ready {\n    execute()\n}\n"

	result, replacements, err := ApplyBatchTextChanges(content, []TextChange{{OldText: oldText, NewText: newText}})
	if err != nil {
		t.Fatal(err)
	}
	if replacements != 1 || !strings.HasSuffix(result.Content, "    if ready {\n        execute()\n    }\n") {
		t.Fatalf("unexpected indentation-aware edit result: replacements=%d suffix=%q", replacements, result.Content[len(result.Content)-48:])
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "ignoring leading indentation") {
		t.Fatalf("expected indentation fallback warning, got %#v", result.Warnings)
	}
}

// rangeAt builds a verified whole-line source for a test fixture. A single-line
// span omits end and endText by construction.
func rangeAt(content string, start, end int) *LineRange {
	lines := strings.Split(content, "\n")
	r := &LineRange{Start: start, StartText: lines[start-1]}
	if end != start {
		r.End = end
		r.EndText = lines[end-1]
	}
	return r
}

func TestApplyBatchTextChangesLineRangesUseOriginalSnapshot(t *testing.T) {
	content := "one\ntwo\nthree\nfour\nfive\nsix\n"
	result, replacements, err := ApplyBatchTextChanges(content, []TextChange{
		{LineRange: rangeAt(content, 2, 2), NewText: "TWO\ninserted"},
		{LineRange: rangeAt(content, 5, 5), NewText: "FIVE"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if replacements != 2 {
		t.Fatalf("replacements = %d, want 2", replacements)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("a plain line-range batch must not warn, got %#v", result.Warnings)
	}
	want := "one\nTWO\ninserted\nthree\nfour\nFIVE\nsix\n"
	if result.Content != want {
		t.Fatalf("line ranges must use original positions despite earlier expansion:\nwant %q\n got %q", want, result.Content)
	}
}

func TestApplyBatchTextChangesRejectsMissingOrOverlappingSources(t *testing.T) {
	const content = "one\ntwo\nthree\nfour\n"
	for name, changes := range map[string][]TextChange{
		"no source": {{NewText: "TWO"}},
		"overlap": {
			{LineRange: rangeAt(content, 2, 3), NewText: "replacement"},
			{OldText: "three\n", NewText: "THREE\n"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := ApplyBatchTextChanges(content, changes)
			if err == nil {
				t.Fatal("expected invalid or overlapping batch to fail")
			}
			want := "E_BAD_EDIT"
			if name == "overlap" {
				want = "E_OVERLAPPING_CHANGES"
			}
			if code := toolerrors.Code(err); code != want {
				t.Fatalf("%s error code = %q, want %s; err=%v", name, code, want, err)
			}
		})
	}
}

// A change must name exactly one source, and replaceAll is refused beside
// lineRange because it only modifies how oldText is matched. The runtime never
// picks a source silently: a silent pick is what lets a wrong-source edit land.
func TestApplyBatchTextChangesRejectsBothSourcesAndReplaceAllBesideLineRange(t *testing.T) {
	const content = "one\ntwo\nthree\nfour\n"
	cases := map[string]TextChange{
		"both sources":                {OldText: "two", LineRange: rangeAt(content, 2, 2), NewText: "TWO"},
		"replaceAll beside lineRange": {LineRange: rangeAt(content, 2, 2), NewText: "TWO", ReplaceAll: true},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := ApplyBatchTextChanges(content, []TextChange{change})
			if err == nil {
				t.Fatal("expected the batch to be refused")
			}
			if code := toolerrors.Code(err); code != "E_BAD_EDIT" {
				t.Fatalf("error code = %q, want E_BAD_EDIT; err=%v", code, err)
			}
			want := "replaceAll"
			if name == "both sources" {
				want = "exactly one source"
			}
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q must mention %q", err.Error(), want)
			}
		})
	}
}

// A declared span must be ordered and 1-based. An empty startText/endText is
// not a missing field: it pins a blank line, so it only matches an empty one.
func TestLineRangeRejectsBadBoundsAndPinsBlankLines(t *testing.T) {
	for name, change := range map[string]TextChange{
		"start past end": {LineRange: &LineRange{Start: 3, End: 2, StartText: "three", EndText: "two"}},
		"zero start":     {LineRange: &LineRange{Start: 0, StartText: "x"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := ApplyBatchTextChanges("one\ntwo\nthree\nfour\n", []TextChange{change})
			if code := toolerrors.Code(err); code != "E_BAD_EDIT" {
				t.Fatalf("error code = %q, want E_BAD_EDIT; err=%v", code, err)
			}
		})
	}
	for name, change := range map[string]TextChange{
		"empty startText on a used line": {LineRange: &LineRange{Start: 2}},
		"empty endText on a used line":   {LineRange: &LineRange{Start: 2, End: 3, StartText: "two"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := ApplyBatchTextChanges("one\ntwo\nthree\nfour\n", []TextChange{change})
			if code := toolerrors.Code(err); code != "E_LINE_RANGE" {
				t.Fatalf("error code = %q, want E_LINE_RANGE; err=%v", code, err)
			}
		})
	}
	// An empty startText is how a blank line is pinned, and the tail of a
	// single-line range is ignored rather than rejected.
	result, _, err := ApplyBatchTextChanges("one\n\nthree\n", []TextChange{
		{LineRange: &LineRange{Start: 2, StartText: ""}, NewText: "two"},
	})
	if err != nil {
		t.Fatalf("blank-line range must apply: %v", err)
	}
	if result.Content != "one\ntwo\nthree\n" {
		t.Fatalf("blank line not replaced: %q", result.Content)
	}
	if _, _, err := ApplyBatchTextChanges("one\ntwo\n", []TextChange{
		{LineRange: &LineRange{Start: 2, StartText: "two", EndText: "ignored"}, NewText: "TWO"},
	}); err != nil {
		t.Fatalf("endText on a single-line range must be ignored: %v", err)
	}
}

// The declared ends are checked against the snapshot before anything is written:
// a stale or miscounted line number must not replace the wrong region.
func TestLineRangeVerifiesBothEnds(t *testing.T) {
	const content = "one\ntwo\nthree\nfour\n"
	for name, change := range map[string]TextChange{
		"start mismatch": {LineRange: &LineRange{Start: 2, End: 3, StartText: "ONE", EndText: "three"}, NewText: "x"},
		"end mismatch":   {LineRange: &LineRange{Start: 2, End: 3, StartText: "two", EndText: "THREE"}, NewText: "x"},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := ApplyBatchTextChanges(content, []TextChange{change})
			if code := toolerrors.Code(err); code != "E_LINE_RANGE" {
				t.Fatalf("error code = %q, want E_LINE_RANGE; err=%v", code, err)
			}
			if !strings.Contains(err.Error(), "at line ") {
				t.Fatalf("error %q must name the line", err)
			}
		})
	}
	if _, _, err := ApplyBatchTextChanges(content, []TextChange{
		{LineRange: &LineRange{Start: 2, End: 3, StartText: "two", EndText: "three"}, NewText: "TWO"},
	}); err != nil {
		t.Fatalf("a verified range must apply: %v", err)
	}
}

func TestSingleLineRangeMatchesExplicitRange(t *testing.T) {
	for _, content := range []string{"one\ntwo\nthree\n", "one\ntwo"} {
		for _, replacement := range []string{"TWO", "", "TWO\ninserted"} {
			single := []TextChange{{LineRange: rangeAt(content, 2, 2), NewText: replacement}}
			explicit := []TextChange{{LineRange: &LineRange{Start: 2, End: 2, StartText: "two"}, NewText: replacement}}
			got, count, err := ApplyBatchTextChanges(content, single)
			want, wantCount, wantErr := ApplyBatchTextChanges(content, explicit)
			if err != nil || wantErr != nil || got.Content != want.Content || count != wantCount {
				t.Fatalf("single-line edit differs: got=%+v/%d/%v want=%+v/%d/%v", got, count, err, want, wantCount, wantErr)
			}
		}
	}
	_, _, err := ApplyBatchTextChanges("one\ntwo\n", []TextChange{{LineRange: &LineRange{Start: 3, StartText: "three"}, NewText: "THREE"}})
	if toolerrors.Code(err) != "E_RANGE_OOB" {
		t.Fatalf("single line past EOF must fail: %v", err)
	}
}

func TestLineIndexSpansEveryLine(t *testing.T) {
	// Mix of trailing-newline and no-trailing-newline contents, plus a few
	// edge sizes (0, 1, many lines). lineRangeOffsetsWithIndex is the
	// production consumer of the index: for every single-line range it must
	// return exactly that line's byte span.
	cases := []string{
		"",
		"one",
		"one\ntwo\nthree\n",
		"one\ntwo\nthree",
		strings.Repeat("line\n", 1000) + "tail",
		strings.Repeat("line\n", 1000),
	}
	for _, content := range cases {
		index := buildLineIndex(content)
		lines := contentLinesForDiff(content)
		if index.total() != len(lines) {
			t.Fatalf("content len=%d: index total %d != line count %d", len(content), index.total(), len(lines))
		}
		for i, want := range lines {
			start, end, err := lineRangeOffsetsWithIndex(index, len(content), i+1, i+1)
			if err != nil {
				t.Fatalf("content len=%d line %d: %v", len(content), i+1, err)
			}
			if got := strings.TrimSuffix(content[start:end], "\n"); got != want {
				t.Fatalf("content len=%d line %d: span = %q, want %q", len(content), i+1, got, want)
			}
		}
	}
}

// BenchmarkApplyBatchTextChangesLargeFileLineRanges measures the cost of many
// line-range edits on a large file. Before the lineIndex cache, each line-range
// lookup re-scanned from byte 0; with the cache it's one O(N) scan plus O(1)
// per lookup.
func BenchmarkApplyBatchTextChangesLargeFileLineRanges(b *testing.B) {
	const lineCount = 10000
	lines := make([]string, lineCount)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	content := strings.Join(lines, "\n") + "\n"
	changes := make([]TextChange, 0, 50)
	for i := 0; i < 50; i++ {
		start := 100*i + 10
		changes = append(changes, TextChange{LineRange: &LineRange{Start: start, StartText: fmt.Sprintf("line %d", start)}, NewText: fmt.Sprintf("// patch %d", i+1)})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := ApplyBatchTextChanges(content, changes); err != nil {
			b.Fatal(err)
		}
	}
}

func TestApplyBatchTextChangesReplaceAllBeyondDiagnosticLimit(t *testing.T) {
	// More occurrences than maxMatchDiagnosticCandidates: the replace-all path
	// streams matches without retaining a per-occurrence slice, so a large
	// match count must still replace every occurrence exactly.
	content := strings.Repeat("foo\n", 20)
	result, replacements, err := ApplyBatchTextChanges(content, []TextChange{{
		OldText:    "foo",
		NewText:    "bar",
		ReplaceAll: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if replacements != 20 {
		t.Fatalf("replacements = %d, want 20", replacements)
	}
	if result.Content != strings.Repeat("bar\n", 20) {
		t.Fatalf("unexpected replace-all content: %q", result.Content)
	}
}

func TestReplaceAllMergesIndependentStreamsAndRejectsOverlap(t *testing.T) {
	content := strings.Repeat("a-b\n", 100000)
	r, n, err := ApplyBatchTextChanges(content, []TextChange{{OldText: "a", NewText: "A", ReplaceAll: true}, {OldText: "b", NewText: "B", ReplaceAll: true}})
	if err != nil || n != 200000 || r.Content != strings.Repeat("A-B\n", 100000) {
		t.Fatalf("stream merge: replacements=%d err=%v", n, err)
	}
	_, _, err = ApplyBatchTextChanges("aa\n", []TextChange{{OldText: "a", NewText: "A", ReplaceAll: true}, {OldText: "aa", NewText: "B"}})
	if toolerrors.Code(err) != "E_OVERLAPPING_CHANGES" {
		t.Fatalf("overlap escaped: %v", err)
	}
}

func TestReplaceAllRejectsExpansionBeforeBuildingOutput(t *testing.T) {
	_, _, err := ApplyBatchTextChanges(strings.Repeat("a", 8192), []TextChange{{OldText: "a", NewText: strings.Repeat("b", 4097), ReplaceAll: true}})
	if toolerrors.Code(err) != "E_FILE_TOO_LARGE" {
		t.Fatalf("expected bounded edit output: %v", err)
	}
}

func BenchmarkReplaceAllMillionMatches(b *testing.B) {
	content := strings.Repeat("a\n", 1000000)
	changes := []TextChange{{OldText: "a", NewText: "b", ReplaceAll: true}}
	b.ReportAllocs()
	b.SetBytes(int64(len(content)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := ApplyBatchTextChanges(content, changes); err != nil {
			b.Fatal(err)
		}
	}
}

// TestApplyBatchTextChangesRefusesLineRangeOverATruncatedLine: read 只把长行的前
// MaxReadLineChars 个字符交给模型（尾部以 "..." 代替），按行号整行替换会把没显示过的
// 尾部一起丢掉，而且工具还报成功。锚定编辑（oldText）做不到这一点：抄不全就匹配不上。
func TestApplyBatchTextChangesRefusesLineRangeOverATruncatedLine(t *testing.T) {
	long := strings.Repeat("x", toolerrors.MaxReadLineChars+50)
	content := "header\n" + long + "\ntail\n"

	if _, _, err := ApplyBatchTextChanges(content, []TextChange{{LineRange: &LineRange{Start: 2, StartText: long}, NewText: "short"}}); err == nil {
		t.Fatal("a whole-line replacement over a line longer than the read preview must be refused")
	} else if code := toolerrors.Code(err); code != "E_BAD_ARGS" {
		t.Fatalf("error code = %q, want E_BAD_ARGS (%v)", code, err)
	}

	// What the model actually has is read's cut-off preview (MaxReadLineChars
	// runes ending in "..."), which can never equal the real line. The refusal
	// must name the reason and the way out instead of a mismatch it cannot fix.
	preview := long[:toolerrors.MaxReadLineChars-3] + "..."
	if _, _, err := ApplyBatchTextChanges(content, []TextChange{{LineRange: &LineRange{Start: 2, StartText: preview}, NewText: "short"}}); err == nil {
		t.Fatal("a range pinned with the truncated preview must be refused")
	} else if code := toolerrors.Code(err); code != "E_BAD_ARGS" {
		t.Fatalf("error code = %q, want E_BAD_ARGS (%v)", code, err)
	} else if !strings.Contains(err.Error(), "oldText") {
		t.Fatalf("the refusal must point at oldText, got %q", err)
	}

	// A range outside the file is refused before anything is replaced.
	if _, _, err := ApplyBatchTextChanges(content, []TextChange{{LineRange: &LineRange{Start: 9, StartText: "x"}, NewText: "x"}}); err == nil {
		t.Fatal("a lineRange past the end of the file must be refused")
	} else if code := toolerrors.Code(err); code != "E_RANGE_OOB" {
		t.Fatalf("error code = %q, want E_RANGE_OOB (%v)", code, err)
	}

	// Ordinary short lines still work, including a range that spans several of them.
	result, _, err := ApplyBatchTextChanges(content, []TextChange{{LineRange: &LineRange{Start: 1, StartText: "header"}, NewText: "head"}})
	if err != nil || !strings.HasPrefix(result.Content, "head\n") {
		t.Fatalf("a short line must still be replaceable by line range: %v", err)
	}
}

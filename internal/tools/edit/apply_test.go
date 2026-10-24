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

func TestApplyBatchTextChangesLineRangesUseOriginalSnapshot(t *testing.T) {
	content := "one\ntwo\nthree\nfour\nfive\nsix\n"
	result, replacements, err := ApplyBatchTextChanges(content, []TextChange{
		{LineRange: "2-2", NewText: "TWO\ninserted"},
		{LineRange: "5-5", NewText: "FIVE"},
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
	for name, changes := range map[string][]TextChange{
		"no source": {{NewText: "TWO"}},
		"overlap": {
			{LineRange: "2-3", NewText: "replacement"},
			{OldText: "three\n", NewText: "THREE\n"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := ApplyBatchTextChanges("one\ntwo\nthree\nfour\n", changes)
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
	cases := map[string]TextChange{
		"both sources":                {OldText: "two", LineRange: "2-2", NewText: "TWO"},
		"replaceAll beside lineRange": {LineRange: "2-2", NewText: "TWO", ReplaceAll: true},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := ApplyBatchTextChanges("one\ntwo\nthree\nfour\n", []TextChange{change})
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

func TestParseLineRangeRejectsAmbiguousWriteTargets(t *testing.T) {
	for _, value := range []string{"", "0", "-2", "2-", "3-2", "0-1", "x-y", "1-2-3", "2.5"} {
		if _, _, err := ParseLineRange(value); err == nil {
			t.Fatalf("ParseLineRange(%q) unexpectedly succeeded", value)
		}
	}
}

func TestSingleLineRangeMatchesExplicitRange(t *testing.T) {
	for _, value := range []string{"2", "12", " 12 "} {
		start, end, err := ParseLineRange(value)
		if err != nil || start < 1 || start != end {
			t.Fatalf("single line %q: %d-%d, %v", value, start, end, err)
		}
	}
	for _, content := range []string{"one\ntwo\nthree\n", "one\ntwo"} {
		for _, replacement := range []string{"TWO", "", "TWO\ninserted"} {
			got, count, err := ApplyBatchTextChanges(content, []TextChange{{LineRange: "2", NewText: replacement}})
			want, wantCount, wantErr := ApplyBatchTextChanges(content, []TextChange{{LineRange: "2-2", NewText: replacement}})
			if err != nil || wantErr != nil || got.Content != want.Content || count != wantCount {
				t.Fatalf("single-line edit differs: got=%+v/%d/%v want=%+v/%d/%v", got, count, err, want, wantCount, wantErr)
			}
		}
	}
	_, _, err := ApplyBatchTextChanges("one\ntwo\n", []TextChange{{LineRange: "3", NewText: "THREE"}})
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
		changes = append(changes, TextChange{LineRange: fmt.Sprintf("%d-%d", start, start), NewText: fmt.Sprintf("// patch %d", i+1)})
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

func TestStripReadLineNumberPrefixes(t *testing.T) {
	cases := []struct {
		input    string
		want     string
		wantBool bool
	}{
		{
			input:    "1: func hello() {\n2:     return 42\n3: }",
			want:     "func hello() {\n    return 42\n}",
			wantBool: true,
		},
		{
			input:    "10\tx = 1\n11\ty = 2",
			want:     "x = 1\ny = 2",
			wantBool: true,
		},
		{
			input:    " 5 | const a = 1\n 6 | const b = 2",
			want:     "const a = 1\nconst b = 2",
			wantBool: true,
		},
		{
			input:    "func hello() {\n    return 42\n}",
			want:     "func hello() {\n    return 42\n}",
			wantBool: false,
		},
		{
			input:    "1: func hello() {\nreturn 42\n}",
			want:     "1: func hello() {\nreturn 42\n}",
			wantBool: false,
		},
	}
	for _, tc := range cases {
		got, ok := StripReadLineNumberPrefixes(tc.input)
		if ok != tc.wantBool || got != tc.want {
			t.Errorf("StripReadLineNumberPrefixes(%q) = (%q, %v), want (%q, %v)", tc.input, got, ok, tc.want, tc.wantBool)
		}
	}
}

func TestApplyBatchTextChangesWithCopiedLineNumberPrefixes(t *testing.T) {
	content := "package main\n\nfunc calculate() int {\n\tx := 10\n\ty := 20\n\treturn x + y\n}\n"
	// Model copied line numbers from read output:
	oldText := "4: \tx := 10\n5: \ty := 20"
	newText := "4: \tx := 15\n5: \ty := 25"
	result, count, err := ApplyBatchTextChanges(content, []TextChange{{
		OldText: oldText,
		NewText: newText,
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 replacement, got %d", count)
	}
	expected := "package main\n\nfunc calculate() int {\n\tx := 15\n\ty := 25\n\treturn x + y\n}\n"
	if result.Content != expected {
		t.Fatalf("expected:\n%s\ngot:\n%s", expected, result.Content)
	}
	if len(result.Warnings) == 0 {
		t.Fatalf("expected warning about stripped line numbers")
	}
}

func TestMatchLineNumberPrefixDoesNotStripBareColonOrTimestamp(t *testing.T) {
	content := "const deadline = \"12:30\";\n"
	oldText := "const deadline = \"12:30\";"
	newText := "const deadline = \"13:30\";"
	result, count, err := ApplyBatchTextChanges(content, []TextChange{{
		OldText: oldText,
		NewText: newText,
	}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 1 || len(result.Warnings) != 0 {
		t.Fatalf("bare colon inside text must not trigger line number stripping warning, warnings: %v", result.Warnings)
	}
}

// TestApplyBatchTextChangesRefusesLineRangeOverATruncatedLine: read 只把长行的前
// MaxReadLineChars 个字符交给模型（尾部以 "..." 代替），按行号整行替换会把没显示过的
// 尾部一起丢掉，而且工具还报成功。锚定编辑（oldText）做不到这一点：抄不全就匹配不上。
func TestApplyBatchTextChangesRefusesLineRangeOverATruncatedLine(t *testing.T) {
	long := strings.Repeat("x", toolerrors.MaxReadLineChars+50)
	content := "header\n" + long + "\ntail\n"

	if _, _, err := ApplyBatchTextChanges(content, []TextChange{{LineRange: "2-2", NewText: "short"}}); err == nil {
		t.Fatal("a whole-line replacement over a line longer than the read preview must be refused")
	} else if code := toolerrors.Code(err); code != "E_BAD_ARGS" {
		t.Fatalf("error code = %q, want E_BAD_ARGS (%v)", code, err)
	}

	// A range outside the file is refused before anything is replaced.
	if _, _, err := ApplyBatchTextChanges(content, []TextChange{{LineRange: "9-9", NewText: "x"}}); err == nil {
		t.Fatal("a lineRange past the end of the file must be refused")
	} else if code := toolerrors.Code(err); code != "E_RANGE_OOB" {
		t.Fatalf("error code = %q, want E_RANGE_OOB (%v)", code, err)
	}

	// Ordinary short lines still work, including a range that spans several of them.
	result, _, err := ApplyBatchTextChanges(content, []TextChange{{LineRange: "1-1", NewText: "head"}})
	if err != nil || !strings.HasPrefix(result.Content, "head\n") {
		t.Fatalf("a short line must still be replaceable by line range: %v", err)
	}
}

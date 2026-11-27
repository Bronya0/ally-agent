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

func TestOverlappingExactOccurrencesAreAmbiguous(t *testing.T) {
	result, _, err := ApplyBatchTextChanges("aaa", []TextChange{{OldText: "aa", NewText: "x"}})
	if result != nil || toolerrors.Code(err) != "E_MULTI_MATCH" {
		t.Fatalf("overlapping locations were treated as unique: %+v %v", result, err)
	}
	result, count, err := ApplyBatchTextChanges("aaa", []TextChange{{OldText: "aa", NewText: "x", ReplaceAll: true}})
	if err != nil || count != 1 || result.Content != "xa" {
		t.Fatalf("replaceAll lost its non-overlapping contract: %+v %d %v", result, count, err)
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

func TestApplyBatchTextChangesUseOriginalSnapshot(t *testing.T) {
	content := "one\ntwo\nthree\nfour\nfive\nsix\n"
	result, replacements, err := ApplyBatchTextChanges(content, []TextChange{
		{OldText: "two", NewText: "TWO\ninserted"},
		{OldText: "five", NewText: "FIVE"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if replacements != 2 {
		t.Fatalf("replacements = %d, want 2", replacements)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("a plain text batch must not warn, got %#v", result.Warnings)
	}
	want := "one\nTWO\ninserted\nthree\nfour\nFIVE\nsix\n"
	if result.Content != want {
		t.Fatalf("sources must use the original snapshot despite earlier expansion:\nwant %q\n got %q", want, result.Content)
	}
}

func TestApplyBatchTextChangesRejectsMissingOrOverlappingSources(t *testing.T) {
	const content = "one\ntwo\nthree\nfour\n"
	for name, changes := range map[string][]TextChange{
		"no source": {{NewText: "TWO"}},
		"overlap": {
			{OldText: "two\nthree\n", NewText: "replacement"},
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

func TestApplyBatchTextChangesRejectsReplaceAllWithoutOldText(t *testing.T) {
	const content = "one\ntwo\nthree\nfour\n"
	cases := map[string]TextChange{
		"missing source": {NewText: "TWO", ReplaceAll: true},
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
			want := "oldText"
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q must mention %q", err.Error(), want)
			}
		})
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

func TestLineNumberPrefixesAreDroppedOnlyWhenTheSnippetIsUnique(t *testing.T) {
	content := "header\nkeep\ndrop\nfooter\n"
	res, _, err := ApplyBatchTextChanges(content, []TextChange{{OldText: "12: keep\n13: drop", NewText: "12: keep\n13: changed"}})
	if err != nil {
		t.Fatalf("copied line numbers should still match: %v", err)
	}
	if want := "header\nkeep\nchanged\nfooter\n"; res.Content != want {
		t.Fatalf("content: %q want %q", res.Content, want)
	}
	if len(res.Warnings) == 0 || !strings.Contains(res.Warnings[0], " at line 2") {
		t.Fatalf("dropping line numbers must report the hit line, got %#v", res.Warnings)
	}
	// 剥掉前缀后仍有两处候选时必须放弃，不能猜一处。
	if _, _, err := ApplyBatchTextChanges("b\nc\nb\n", []TextChange{{OldText: "12: b", NewText: "B"}}); toolerrors.Code(err) != "E_NO_MATCH" {
		t.Fatalf("ambiguous stripped snippet must stay unmatched, got %v", err)
	}
	// 全前缀无正文的 needle 剥完只剩空白（如 "12:\n13:" → "\n"）：不得拿它
	// 去碰文件里唯一的换行，宁可拒配让模型重抄。
	if _, _, err := ApplyBatchTextChanges("a\nb", []TextChange{{OldText: "12:\n13:", NewText: "X"}}); toolerrors.Code(err) != "E_NO_MATCH" {
		t.Fatalf("whitespace-only stripped snippet must stay unmatched, got %v", err)
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

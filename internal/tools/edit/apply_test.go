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
		{LineRange: "2-2", NewText: "TWO\ninserted", ReplaceAll: true},
		{LineRange: "5-5", NewText: "FIVE"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if replacements != 2 {
		t.Fatalf("replacements = %d, want 2; replace_all must be ignored for lineRange", replacements)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "ignored replace_all") {
		t.Fatalf("expected replace_all warning for lineRange, got %#v", result.Warnings)
	}
	want := "one\nTWO\ninserted\nthree\nfour\nFIVE\nsix\n"
	if result.Content != want {
		t.Fatalf("line ranges must use original positions despite earlier expansion:\nwant %q\n got %q", want, result.Content)
	}
}

func TestApplyBatchTextChangesRejectsMixedOrOverlappingSources(t *testing.T) {
	for name, changes := range map[string][]TextChange{
		"both sources": {{OldText: "two", LineRange: "2-2", NewText: "TWO"}},
		"no source":    {{NewText: "TWO"}},
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
			if name == "overlap" && toolerrors.Code(err) != "E_OVERLAPPING_CHANGES" {
				t.Fatalf("overlap error code = %q, want E_OVERLAPPING_CHANGES; err=%v", toolerrors.Code(err), err)
			}
			if name != "overlap" && toolerrors.Code(err) != "E_BAD_EDIT" {
				t.Fatalf("validation error code = %q, want E_BAD_EDIT; err=%v", toolerrors.Code(err), err)
			}
		})
	}
}

func TestParseLineRangeRejectsAmbiguousWriteTargets(t *testing.T) {
	for _, value := range []string{"2", "3-2", "0-1", "x-y", "1-2-3"} {
		if _, _, err := ParseLineRange(value); err == nil {
			t.Fatalf("ParseLineRange(%q) unexpectedly succeeded", value)
		}
	}
}

func TestLineIndexMatchesByteOffsetForLine(t *testing.T) {
	// Mix of trailing-newline and no-trailing-newline contents, plus a few
	// edge sizes (0, 1, many lines). For every (content, line) pair the
	// index-backed lookup must equal the legacy O(N) byteOffsetForLine.
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
		total := visibleLineCount(content)
		for line := 1; line <= total; line++ {
			want := byteOffsetForLine(content, line)
			got := index.offsetForLine(line)
			if got != want {
				t.Fatalf("content len=%d line=%d: index gave %d, byteOffsetForLine gave %d", len(content), line, got, want)
			}
		}
		if index.total() != total {
			t.Fatalf("index total %d != visibleLineCount %d", index.total(), total)
		}
	}
}

func TestLineIndexLineAtOffset(t *testing.T) {
	content := "a\nbb\nccc\ndddd\n"
	index := buildLineIndex(content)
	// starts = [0, 2, 5, 9]; line 1 = bytes [0,1], line 2 = bytes [2,3,4], etc.
	cases := []struct {
		offset int
		line   int
	}{
		{0, 1}, {1, 1}, // "a\n"
		{2, 2}, {3, 2}, {4, 2}, // "bb\n"
		{5, 3}, {6, 3}, {7, 3}, {8, 3}, // "ccc\n"
		{9, 4}, {10, 4}, {11, 4}, {12, 4}, {13, 4}, // "dddd\n"
	}
	for _, c := range cases {
		got := index.lineAtOffset(c.offset)
		if got != c.line {
			t.Fatalf("lineAtOffset(%d) = %d, want %d", c.offset, got, c.line)
		}
	}
}

func TestLineRangeOffsetsWithIndexMatchesLegacy(t *testing.T) {
	content := "one\ntwo\nthree\nfour\nfive\nsix\n"
	index := buildLineIndex(content)
	for startLine := 1; startLine <= 6; startLine++ {
		for endLine := startLine; endLine <= 6; endLine++ {
			wantStart, wantEnd, wantErr := lineRangeOffsets(content, startLine, endLine)
			gotStart, gotEnd, gotErr := lineRangeOffsetsWithIndex(index, len(content), startLine, endLine)
			if (wantErr == nil) != (gotErr == nil) {
				t.Fatalf("line %d-%d: error mismatch legacy=%v index=%v", startLine, endLine, wantErr, gotErr)
			}
			if wantErr != nil {
				continue
			}
			if gotStart != wantStart || gotEnd != wantEnd {
				t.Fatalf("line %d-%d: index [%d,%d) != legacy [%d,%d)", startLine, endLine, gotStart, gotEnd, wantStart, wantEnd)
			}
		}
	}
}

// BenchmarkApplyBatchTextChangesLargeFileLineRanges measures the cost of many
// line-range edits on a large file. Before the lineIndex cache, each
// lineRangeOffsets call re-scanned from byte 0; with the cache it's one O(N)
// scan plus O(1) per lookup.
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

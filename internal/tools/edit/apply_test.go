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

// SPDX-License-Identifier: GPL-3.0-only
package edit

import (
	"strings"
	"testing"

	toolerrors "ally-dev/internal/tools/shared"
)

func TestScopedTextChangesPreserveEverythingOutsideMatches(t *testing.T) {
	for name, tc := range map[string]struct {
		content, old, replacement, want string
		start, end, count               int
		all                             bool
	}{
		"unique in scope":         {"same\nkeep same here\nsame\n", "same", "new", "same\nkeep new here\nsame\n", 2, 2, 1, false},
		"replaceAll stops at end": {"same\nsame same\nsame\nsame\n", "same", "new", "same\nnew new\nnew\nsame\n", 2, 3, 3, true},
		"indentation fallback":    {"outside\n    call()\n    done()\noutside\n", "call()\ndone()", "changed()\ndone()", "outside\n    changed()\n    done()\noutside\n", 2, 3, 1, false},
		"Unicode fallback":        {"keep\nprefix “same” suffix\n“same”\n", `"same"`, `"new"`, "keep\nprefix \"new\" suffix\n“same”\n", 2, 2, 1, false},
		"long-line fragment":      {strings.Repeat("x", toolerrors.MaxReadLineChars+20) + "needle\n", "needle", "new", strings.Repeat("x", toolerrors.MaxReadLineChars+20) + "new\n", 1, 1, 1, false},
	} {
		t.Run(name, func(t *testing.T) {
			result, count, err := ApplyBatchTextChanges(tc.content, []TextChange{{OldText: tc.old, NewText: tc.replacement, LineRange: &LineRange{Start: tc.start, End: tc.end}, ReplaceAll: tc.all}})
			if err != nil || count != tc.count || result.Content != tc.want {
				t.Fatalf("scoped replacement: result=%+v count=%d err=%v want=%q", result, count, err, tc.want)
			}
		})
	}
}

func TestScopedTextFailuresCannotEscapeOrBecomeLineWrites(t *testing.T) {
	for name, tc := range map[string]struct {
		content, old string
		all          bool
		code         string
	}{
		"exact outside":       {"target\nkeep\n", "target", false, "E_NO_MATCH"},
		"indent outside":      {"    target\nkeep\n", "\ttarget", false, "E_NO_MATCH"},
		"Unicode outside":     {"“target”\nkeep\n", `"target"`, false, "E_NO_MATCH"},
		"crosses end":         {"before\nkeep\nafter\n", "keep\nafter", false, "E_NO_MATCH"},
		"ambiguous inside":    {"before\nkeep keep\n", "keep", false, "E_MULTI_MATCH"},
		"replaceAll is exact": {"before\n    target\n", "\ttarget", true, "E_NO_MATCH"},
	} {
		t.Run(name, func(t *testing.T) {
			result, _, err := ApplyBatchTextChanges(tc.content, []TextChange{{OldText: tc.old, NewText: "", LineRange: &LineRange{Start: 2}, ReplaceAll: tc.all}})
			if result != nil || toolerrors.Code(err) != tc.code {
				t.Fatalf("wrong failure: result=%+v err=%v want=%s", result, err, tc.code)
			}
		})
	}
}

func TestScopedSearchOverlapUsesActualReplacements(t *testing.T) {
	content := "first\na-b\nlast\n"
	result, count, err := ApplyBatchTextChanges(content, []TextChange{
		{OldText: "a", NewText: "A", LineRange: &LineRange{Start: 2}},
		{OldText: "b", NewText: "B", LineRange: &LineRange{Start: 2}},
	})
	if err != nil || count != 2 || result.Content != "first\nA-B\nlast\n" {
		t.Fatalf("overlapping search scopes: result=%+v count=%d err=%v", result, count, err)
	}
}

func TestScopedUnicodeMatchesUseActualOverlapBounds(t *testing.T) {
	content := "keep\n“first” and “second”\nkeep\n"
	for name, tc := range map[string]struct {
		secondOld, want, code string
	}{
		"disjoint Unicode": {`"second"`, "keep\nFIRST and SECOND\nkeep\n", ""},
		"disjoint exact":   {"“second”", "keep\nFIRST and SECOND\nkeep\n", ""},
		"overlapping":      {"first", "", "E_OVERLAPPING_CHANGES"},
	} {
		t.Run(name, func(t *testing.T) {
			result, count, err := ApplyBatchTextChanges(content, []TextChange{
				{OldText: `"first"`, NewText: "FIRST", LineRange: &LineRange{Start: 2}},
				{OldText: tc.secondOld, NewText: "SECOND", LineRange: &LineRange{Start: 2}},
			})
			if tc.code != "" {
				if result != nil || toolerrors.Code(err) != tc.code {
					t.Fatalf("overlapping match accepted: result=%+v err=%v", result, err)
				}
				return
			}
			if err != nil || count != 2 || result.Content != tc.want {
				t.Fatalf("disjoint matches rejected: result=%+v count=%d err=%v", result, count, err)
			}
		})
	}
}

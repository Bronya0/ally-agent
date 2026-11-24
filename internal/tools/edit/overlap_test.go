// SPDX-License-Identifier: GPL-3.0-only
package edit

import (
	"testing"

	toolerrors "ally-dev/internal/tools/shared"
)

func TestUnicodeMatchesUseActualOverlapBounds(t *testing.T) {
	content := "keep\ncafe\u0301 and re\u0301sume\u0301\nkeep\n"
	for name, tc := range map[string]struct {
		secondOld, want, code string
	}{
		"disjoint Unicode": {"résumé", "keep\nFIRST and SECOND\nkeep\n", ""},
		"disjoint exact":   {"re\u0301sume\u0301", "keep\nFIRST and SECOND\nkeep\n", ""},
		"overlapping":      {"afe\u0301", "", "E_OVERLAPPING_CHANGES"},
	} {
		t.Run(name, func(t *testing.T) {
			result, count, err := ApplyBatchTextChanges(content, []TextChange{
				{OldText: "café", NewText: "FIRST"},
				{OldText: tc.secondOld, NewText: "SECOND"},
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

func TestUnicodeEditsMayShareUnchangedAnchors(t *testing.T) {
	result, count, err := ApplyBatchTextChanges("cafe\u0301 and re\u0301sume\u0301\n", []TextChange{
		{OldText: "café and résumé", NewText: "CAFÉ and résumé"},
		{OldText: "résumé", NewText: "RÉSUMÉ"},
	})
	if err != nil || count != 2 || result.Content != "CAFÉ and RÉSUMÉ\n" {
		t.Fatalf("unchanged canonical anchors caused an overlap: %+v %d %v", result, count, err)
	}
}

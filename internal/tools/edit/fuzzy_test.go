// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package edit

import (
	"strings"
	"testing"

	toolerrors "ally-dev/internal/tools/shared"
)

func TestNormalizeForFuzzyMatch(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"smart single quotes", "\u2018hi\u2019", "'hi'"},
		{"smart double quotes", "\u201Chi\u201D", "\"hi\""},
		{"em dash", "a\u2014b", "a-b"},
		{"en dash", "a\u2013b", "a-b"},
		{"minus sign", "a\u2212b", "a-b"},
		{"NBSP", "a\u00A0b", "a b"},
		{"ideographic space", "a\u3000b", "a b"},
		{"trailing whitespace stripped", "  x  \n\ty\t\n", "  x\n\ty\n"},
		{"NFKC full-width", "\uFF46\uFF55\uFF4E\uFF43", "func"},
		{"mixed keeps newline count", "a\nb\nc", "a\nb\nc"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NormalizeForFuzzyMatch(c.in)
			if got != c.want {
				t.Fatalf("NormalizeForFuzzyMatch(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestApplyBatchTextChangesFuzzyFullWidthQuote(t *testing.T) {
	content := "func greet(name string) {\n\tfmt.Println(\"Hello, \u201cname\u201d\")\n}\n"
	oldText := `fmt.Println("Hello, "name"")`
	newText := `fmt.Println("Hello, " + name)`

	result, replacements, err := ApplyBatchTextChanges(content, []TextChange{{OldText: oldText, NewText: newText}})
	if err != nil {
		t.Fatal(err)
	}
	if replacements != 1 {
		t.Fatalf("replacements = %d, want 1", replacements)
	}
	want := "func greet(name string) {\n\tfmt.Println(\"Hello, \" + name)\n}\n"
	if result.Content != want {
		t.Fatalf("fuzzy result:\nwant %q\n got %q", want, result.Content)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "Unicode normalization") {
		t.Fatalf("expected Unicode normalization warning, got %#v", result.Warnings)
	}
}

func TestApplyBatchTextChangesFuzzySmartQuotesKeepsOtherLines(t *testing.T) {
	content := "const a = \"\u2018x\u2019\"\nconst untouched = \"\u2018y\u2019\"\nconst b = \"\u2018z\u2019\"\n"
	// oldText uses smart quotes so exact fails; the block containing the match
	// is rewritten in normalized form, but other lines keep original bytes.
	result, _, err := ApplyBatchTextChanges(content, []TextChange{{OldText: `const a = "'x'"`, NewText: "const a = \"fixed\""}})
	if err != nil {
		t.Fatal(err)
	}
	want := "const a = \"fixed\"\nconst untouched = \"\u2018y\u2019\"\nconst b = \"\u2018z\u2019\"\n"
	if result.Content != want {
		t.Fatalf("unchanged lines must keep original bytes:\nwant %q\n got %q", want, result.Content)
	}
}

func TestApplyBatchTextChangesFuzzyEmDash(t *testing.T) {
	content := "// start \u2014 end\nx := 1\n"
	result, _, err := ApplyBatchTextChanges(content, []TextChange{{OldText: "// start - end", NewText: "// middle"}})
	if err != nil {
		t.Fatal(err)
	}
	want := "// middle\nx := 1\n"
	if result.Content != want {
		t.Fatalf("em-dash fuzzy result:\nwant %q\n got %q", want, result.Content)
	}
}

func TestApplyBatchTextChangesFuzzyNoMatch(t *testing.T) {
	content := "one\ntwo\n"
	_, _, err := ApplyBatchTextChanges(content, []TextChange{{OldText: "three", NewText: "four"}})
	if err == nil || toolerrors.Code(err) != "E_NO_MATCH" {
		t.Fatalf("expected E_NO_MATCH, got %v", err)
	}
}

func TestApplyBatchTextChangesFuzzyAmbiguous(t *testing.T) {
	content := "a\u2014b\na\u2013b\n"
	// Both lines normalize to "a-b"; the fuzzy fallback must refuse to guess.
	_, _, err := ApplyBatchTextChanges(content, []TextChange{{OldText: "a-b", NewText: "x"}})
	if err == nil || toolerrors.Code(err) != "E_MULTI_MATCH" {
		t.Fatalf("expected E_MULTI_MATCH for ambiguous normalized match, got %v", err)
	}
}

func TestApplyBatchTextChangesFuzzySkippedForReplaceAll(t *testing.T) {
	content := "a\u2014b\n"
	// replace_all has an exact-match-only contract: fuzzy must not apply.
	_, _, err := ApplyBatchTextChanges(content, []TextChange{{OldText: "a-b", NewText: "x", ReplaceAll: true}})
	if err == nil || toolerrors.Code(err) != "E_NO_MATCH" {
		t.Fatalf("expected E_NO_MATCH for replace_all with non-exact oldText, got %v", err)
	}
}

func TestApplyBatchTextChangesFuzzyNFKC(t *testing.T) {
	content := "\uFF46unc()\n"
	result, _, err := ApplyBatchTextChanges(content, []TextChange{{OldText: "func()", NewText: "run()"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "run()\n" {
		t.Fatalf("NFKC fuzzy result = %q, want %q", result.Content, "run()\n")
	}
}

func TestApplyBatchTextChangesFuzzyMidLineKeepsLineTail(t *testing.T) {
	content := "value = \u2018old\u2019 && ready\n"
	result, _, err := ApplyBatchTextChanges(content, []TextChange{{OldText: `'old'`, NewText: "'new'"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "value = 'new' && ready\n" {
		t.Fatalf("mid-line fuzzy result = %q, want %q", result.Content, "value = 'new' && ready\n")
	}
}

func TestApplyBatchTextChangesFuzzyPreservesUnmatchedCharsOnTouchedLines(t *testing.T) {
	// Regression: the fuzzy fallback must rewrite only the matched region.
	// Smart quotes on the same line but outside the match, and on the middle
	// line of a cross-line match, must keep their original bytes.
	cases := []struct {
		name    string
		content string
		oldText string
		newText string
		want    string
	}{
		{
			name:    "same line outside match",
			content: "a\u2014b c \u2018d\u2019\n",
			oldText: "a-b",
			newText: "X",
			want:    "X c \u2018d\u2019\n",
		},
		{
			name:    "cross line middle line untouched",
			content: "a\u2014b\nc \u2018d\u2019\ne\n",
			oldText: "-b\nc",
			newText: "X",
			want:    "aX \u2018d\u2019\ne\n",
		},
		{
			name:    "multi-line match trailing newline",
			content: "a\u2014b\nc\n",
			oldText: "a-b\nc\n",
			newText: "X",
			want:    "X",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			result, _, err := ApplyBatchTextChanges(c.content, []TextChange{{OldText: c.oldText, NewText: c.newText}})
			if err != nil {
				t.Fatal(err)
			}
			if result.Content != c.want {
				t.Fatalf("fuzzy result:\nwant %q\n got %q", c.want, result.Content)
			}
		})
	}
}

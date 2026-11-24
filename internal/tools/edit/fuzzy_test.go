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
	"unicode/utf8"

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
		{"full-width forms are left alone", "\uFF46\uFF55\uFF4E\uFF43", "\uFF46\uFF55\uFF4E\uFF43"},
		{"mixed keeps newline count", "a\nb\nc", "a\nb\nc"},
		{"canonical combining accent", "cafe\u0301", "café"},
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

// 模糊匹配这一族全是同一个形状：给一份内容、一句 oldText、一句 newText，看落回来
// 的字节或错误码。收成一张表之后，Unicode 归一化到底动了哪几个字节、哪几个必须原样
// 留着，一眼能看全。
type fuzzyCase struct {
	name       string
	content    string
	oldText    string
	newText    string
	replaceAll bool
	// want 非空时校验替换后的内容；wantCode 非空时期望这个错误码。
	want     string
	wantCode string
	// 自动使用 Unicode 等价写法时必须返回警告。
	wantWarn bool
	wantRepl int
}

func TestApplyBatchTextChangesFuzzy(t *testing.T) {
	cases := []fuzzyCase{
		{name: "smart quote anchor cannot silently become a string delimiter",
			content: "func greet(name string) {\n\tfmt.Println(\"Hello, \u201cname\u201d\")\n}\n",
			oldText: `fmt.Println("Hello, "name"")`, newText: `fmt.Println("Hello, " + name)`,
			wantCode: "E_NO_MATCH"},
		{name: "smart quotes require exact source before changing a block",
			content: "const a = \"\u2018x\u2019\"\nconst untouched = \"\u2018y\u2019\"\nconst b = \"\u2018z\u2019\"\n",
			oldText: `const a = "'x'"`, newText: "const a = \"fixed\"",
			wantCode: "E_NO_MATCH"},
		{name: "em dash matches a hyphen",
			content: "// start \u2014 end\nx := 1\n", oldText: "// start - end", newText: "// middle",
			wantCode: "E_NO_MATCH"},
		{name: "dash with trailing whitespace requires exact source",
			content: "fo\u2014o   \nbar\n", oldText: "fo-o", newText: "FOO",
			wantCode: "E_NO_MATCH"},
		{name: "uncertain smart quote anchors require exact source",
			content: "value = \u2018old\u2019 && ready\n", oldText: `'old'`, newText: "'new'",
			wantCode: "E_NO_MATCH"},
		{name: "same line outside the match keeps bytes",
			content: "a\u2014b c \u2018d\u2019\n", oldText: "a-b", newText: "X",
			wantCode: "E_NO_MATCH"},
		{name: "cross-line match: middle line keeps bytes",
			content: "a\u2014b\nc \u2018d\u2019\ne\n", oldText: "-b\nc", newText: "X",
			wantCode: "E_NO_MATCH"},
		{name: "multi-line match with trailing newline",
			content: "a\u2014b\nc\n", oldText: "a-b\nc\n", newText: "X",
			wantCode: "E_NO_MATCH"},
		{name: "no match at all",
			content: "one\ntwo\n", oldText: "three", newText: "four",
			wantCode: "E_NO_MATCH"},
		{name: "ambiguous normalized match must not be guessed",
			content: "a\u2014b\na\u2013b\n", oldText: "a-b", newText: "x",
			wantCode: "E_MULTI_MATCH"},
		{name: "replaceAll keeps its exact-match-only contract",
			content: "a\u2014b\n", oldText: "a-b", newText: "x", replaceAll: true,
			wantCode: "E_NO_MATCH"},
		{name: "composed source and decomposed request",
			content: "prefix café suffix\n", oldText: "cafe\u0301", newText: "tea",
			want: "prefix tea suffix\n", wantRepl: 1, wantWarn: true},
		{name: "decomposed source keeps unchanged canonical anchor",
			content: "prefix cafe\u0301 suffix\n", oldText: "café", newText: "cafés",
			want: "prefix cafe\u0301s suffix\n", wantRepl: 1, wantWarn: true},
		{name: "canonical Hangul",
			content: "한글\n", oldText: "\u1112\u1161\u11AB글", newText: "한국어",
			want: "한국어\n", wantRepl: 1},
		{name: "uncertain multiline quote and space anchors require exact source",
			content: "label “keep”\nvalue old\u00a0tail\n", oldText: "label \"keep\"\nvalue old tail\n", newText: "label \"keep\"\nvalue new tail\n",
			wantCode: "E_NO_MATCH"},
		{name: "canonical ambiguity",
			content: "é\né\n", oldText: "e\u0301", newText: "x", wantCode: "E_MULTI_MATCH"},
		{name: "overlapping normalized candidates",
			content: "ééé", oldText: "e\u0301e\u0301", newText: "x", wantCode: "E_MULTI_MATCH"},
		{name: "partial combining segment is refused",
			content: "a\u0323\u0301", oldText: "ạ", newText: "x", wantCode: "E_NO_MATCH"},
		{name: "anchor inside stripped trailing whitespace is refused",
			content: "é  \nvalue = old", oldText: "e\u0301  \nvalue = old", newText: "e\u0301 \nvalue = new", wantCode: "E_NO_MATCH"},
		{name: "anchor at end of stripped trailing whitespace is refused",
			content: "é  \nvalue = old", oldText: "e\u0301  \nvalue = old", newText: "e\u0301  !\nvalue = new", wantCode: "E_NO_MATCH"},
		{name: "literal backslash is not a newline",
			content: "line1\\nline2", oldText: "line1\nline2", newText: "x", wantCode: "E_NO_MATCH"},
		{name: "HTML entities keep their meaning",
			content: "&quot;old&quot;", oldText: "\"old\"", newText: "x", wantCode: "E_NO_MATCH"},
		{name: "zero width characters are not removed",
			content: "re\u200bturn", oldText: "return", newText: "x", wantCode: "E_NO_MATCH"},
		{name: "full width identifiers are not folded",
			content: "ｆｏｏ", oldText: "foo", newText: "x", wantCode: "E_NO_MATCH"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, replacements, err := ApplyBatchTextChanges(tc.content, []TextChange{
				{OldText: tc.oldText, NewText: tc.newText, ReplaceAll: tc.replaceAll},
			})
			if tc.wantCode != "" {
				if result != nil || err == nil || toolerrors.Code(err) != tc.wantCode {
					t.Fatalf("expected %s, got %v", tc.wantCode, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if result.Content != tc.want {
				t.Fatalf("fuzzy result:\nwant %q\n got %q", tc.want, result.Content)
			}
			if tc.wantRepl != 0 && replacements != tc.wantRepl {
				t.Fatalf("replacements = %d, want %d", replacements, tc.wantRepl)
			}
			if tc.wantWarn {
				if len(result.Warnings) != 1 {
					t.Fatalf("expected a normalization warning, got %#v", result.Warnings)
				}
			}
		})
	}
}

func TestNormalizedCandidateRequiresExactRetry(t *testing.T) {
	longPrefix := strings.Repeat("前", 1000)
	for _, tc := range []struct{ content, old, actual, replacement, want string }{
		{"value = ‘old’ && “untouched”\n", "'old'", "‘old’", "‘new’", "value = ‘new’ && “untouched”\n"},
		{longPrefix + "a—b trailing\n", "a-b", "a—b", "X", longPrefix + "X trailing\n"},
		{"word\u00a0gap\n", "word gap", "word\u00a0gap", "new gap", "new gap\n"},
		{"é  \nvalue = old", "e\u0301  \nvalue = old", "é  \nvalue = old", "e\u0301 \nvalue = new", "e\u0301 \nvalue = new"},
	} {
		result, _, err := ApplyBatchTextChanges(tc.content, []TextChange{{OldText: tc.old, NewText: tc.replacement}})
		if result != nil || toolerrors.Code(err) != "E_NO_MATCH" {
			t.Fatalf("uncertain source was edited: %+v %v", result, err)
		}
		details, ok := toolerrors.Details(err).(*MatchErrorDetails)
		if !ok || details.MatchType != "normalized" || details.MatchCount != 1 || len(details.Candidates) != 1 {
			t.Fatalf("missing original candidate for retry: %+v", details)
		}
		preview := details.Candidates[0].Preview
		if !utf8.ValidString(preview) || len(preview) > maxMatchDiagnosticPreviewBytes || !strings.Contains(preview, tc.actual) {
			t.Fatalf("candidate lost the original characters or exceeded the preview bound: %q", preview)
		}
		result, count, err := ApplyBatchTextChanges(tc.content, []TextChange{{OldText: tc.actual, NewText: tc.replacement}})
		if err != nil || count != 1 || result.Content != tc.want {
			t.Fatalf("exact retry changed the wrong bytes: %+v %d %v", result, count, err)
		}
	}
}

// TestMapNormOffsetInLine pins the byte mapping: an ordinary mid-line boundary, the
// boundary at the end of the line (where the trailing-whitespace strip happens), and
// an offset the walk never reaches, which must be refused instead of guessed.
func TestMapNormOffsetInLine(t *testing.T) {
	orig := "value = \u2018old\u2019 && ready"
	if got, ok := mapNormOffsetInLine(orig, 0); !ok || got != 0 {
		t.Fatalf("normOff=0: got (%d,%v), want (0,true)", got, ok)
	}
	// The boundary right after the opening quote, which normalization turns into
	// one ASCII byte, still maps to the same original offset.
	if got, ok := mapNormOffsetInLine(orig, 8); !ok || got != 8 {
		t.Fatalf("mid-line boundary: got (%d,%v), want (8,true)", got, ok)
	}
	// The end of the normalized line ends the region at the original line's
	// content, leaving trailing whitespace outside it.
	if got, ok := mapNormOffsetInLine("ab   ", 2); !ok || got != 2 {
		t.Fatalf("end-of-content boundary: got (%d,%v), want (2,true)", got, ok)
	}
	if got, ok := mapNormOffsetInLine("ab   ", 99); ok {
		t.Fatalf("an offset past the normalized line must not map, got %d", got)
	}
}

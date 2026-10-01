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
	// wantWarn 非空时期望警告里出现这段（归一化是"替你猜了"，必须说出来）。
	wantWarn string
	wantRepl int
}

func TestApplyBatchTextChangesFuzzy(t *testing.T) {
	cases := []fuzzyCase{
		{name: "full-width quotes normalize",
			content: "func greet(name string) {\n\tfmt.Println(\"Hello, \u201cname\u201d\")\n}\n",
			oldText: `fmt.Println("Hello, "name"")`, newText: `fmt.Println("Hello, " + name)`,
			want:     "func greet(name string) {\n\tfmt.Println(\"Hello, \" + name)\n}\n",
			wantWarn: "Unicode normalization", wantRepl: 1},
		{name: "smart quotes: matched block normalizes, other lines keep bytes",
			content: "const a = \"\u2018x\u2019\"\nconst untouched = \"\u2018y\u2019\"\nconst b = \"\u2018z\u2019\"\n",
			oldText: `const a = "'x'"`, newText: "const a = \"fixed\"",
			want: "const a = \"fixed\"\nconst untouched = \"\u2018y\u2019\"\nconst b = \"\u2018z\u2019\"\n"},
		{name: "em dash matches a hyphen",
			content: "// start \u2014 end\nx := 1\n", oldText: "// start - end", newText: "// middle",
			want: "// middle\nx := 1\n"},
		{name: "nfkc: fullwidth latin",
			content: "\uFF46unc()\n", oldText: "func()", newText: "run()",
			want: "run()\n"},
		{name: "mid-line match keeps the line tail",
			content: "value = \u2018old\u2019 && ready\n", oldText: `'old'`, newText: "'new'",
			want: "value = 'new' && ready\n"},
		{name: "same line outside the match keeps bytes",
			content: "a\u2014b c \u2018d\u2019\n", oldText: "a-b", newText: "X",
			want: "X c \u2018d\u2019\n"},
		{name: "cross-line match: middle line keeps bytes",
			content: "a\u2014b\nc \u2018d\u2019\ne\n", oldText: "-b\nc", newText: "X",
			want: "aX \u2018d\u2019\ne\n"},
		{name: "multi-line match with trailing newline",
			content: "a\u2014b\nc\n", oldText: "a-b\nc\n", newText: "X",
			want: "X"},
		{name: "no match at all",
			content: "one\ntwo\n", oldText: "three", newText: "four",
			wantCode: "E_NO_MATCH"},
		{name: "ambiguous normalized match must not be guessed",
			content: "a\u2014b\na\u2013b\n", oldText: "a-b", newText: "x",
			wantCode: "E_MULTI_MATCH"},
		{name: "replaceAll keeps its exact-match-only contract",
			content: "a\u2014b\n", oldText: "a-b", newText: "x", replaceAll: true,
			wantCode: "E_NO_MATCH"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, replacements, err := ApplyBatchTextChanges(tc.content, []TextChange{
				{OldText: tc.oldText, NewText: tc.newText, ReplaceAll: tc.replaceAll},
			})
			if tc.wantCode != "" {
				if err == nil || toolerrors.Code(err) != tc.wantCode {
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
			if tc.wantWarn != "" {
				if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], tc.wantWarn) {
					t.Fatalf("expected %q warning, got %#v", tc.wantWarn, result.Warnings)
				}
			}
		})
	}
}

// TestMapNormOffsetInLine verifies the boundary mapper directly: exact
// mapping for ordinary content, end-of-line consumption including trailing
// whitespace, and refusal when NFKC expansion crosses the boundary.
func TestMapNormOffsetInLine(t *testing.T) {
	orig := "value = \u2018old\u2019 && ready"
	norm := NormalizeForFuzzyMatch(orig)
	if got, ok := mapNormOffsetInLine(orig, norm, 0); !ok || got != 0 {
		t.Fatalf("normOff=0: got (%d,%v)", got, ok)
	}
	// The boundary right after the opening quote maps into the original line.
	if _, ok := mapNormOffsetInLine(orig, norm, 8); !ok {
		t.Fatal("mid-line boundary failed to map")
	}
	// End-of-normalized-line boundary consumes the whole original line.
	if got, ok := mapNormOffsetInLine(orig, norm, len(norm)); !ok || got != len(orig) {
		t.Fatalf("end boundary: got (%d,%v), want (%d,true)", got, ok, len(orig))
	}
	// A boundary that lands mid-NFKC-expansion maps to the end of the
	// expanding rune: the match region then covers the whole rune, so bytes
	// outside the match are still preserved. Expansion cannot cross the
	// boundary in a way that corrupts the splice.
	if got, ok := mapNormOffsetInLine("\uFF21B", NormalizeForFuzzyMatch("\uFF21B"), 1); !ok || got != 3 {
		t.Fatalf("NFKC-expansion boundary: got (%d,%v), want (3,true)", got, ok)
	}
}

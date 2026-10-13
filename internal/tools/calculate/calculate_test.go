// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package calculate

import (
	"math"
	"strconv"
	"strings"
	"testing"
)

func TestEvaluateRejectsDeepNestingWithoutCrashing(t *testing.T) {
	for _, depth := range []int{300, 5000, 50000} {
		expr := strings.Repeat("sin(", depth) + "1" + strings.Repeat(")", depth)
		if _, err := Evaluate(Request{Expression: expr}); err == nil {
			t.Fatalf("nesting depth %d must be rejected", depth)
		}
	}
	parens := strings.Repeat("(", 5000) + "1" + strings.Repeat(")", 5000)
	if _, err := Evaluate(Request{Expression: parens}); err == nil {
		t.Fatal("deep parenthesis nesting must be rejected")
	}
}

func TestEvaluateBasicExpressions(t *testing.T) {
	tests := []struct {
		expr     string
		wantVal  float64
		wantText string
	}{
		{"2 + 3 * sin(pi / 2)", 5, "5"},
		{"144 + 2^3", 152, "152"},
		{"2 ** 8", 256, "256"},
		{"sqrt(25) + cbrt(27)", 8, "8"},
		{"log10(1000) + ln(e)", 4, "4"},
		{"abs(-42)", 42, "42"},
		{"round(3.7) + floor(3.7) + ceil(3.2)", 11, "11"},
		{"min(10, 20, 5)", 5, "5"},
		{"max(10, 20, 5)", 20, "20"},
		{"sum([1, 2, 3, 4, 5])", 15, "15"},
		{"10 > 5 ? 100 : 200", 100, "100"},
		{"true && false", 0, "false"},
		{"10 % 3", 1, "1"},
	}

	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			res, err := Evaluate(Request{Expression: tc.expr})
			if err != nil {
				t.Fatalf("Evaluate(%q) failed: %v", tc.expr, err)
			}
			if res.Text != tc.wantText {
				t.Errorf("Evaluate(%q) text = %q, want %q", tc.expr, res.Text, tc.wantText)
			}
			if res.Value != tc.wantVal {
				t.Errorf("Evaluate(%q) value = %v, want %v", tc.expr, res.Value, tc.wantVal)
			}
		})
	}
}

func TestEvaluateErrors(t *testing.T) {
	invalid := []string{
		"",
		"   ",
		"1 / 0",
		"invalid syntax +++",
		"unknownFunc(123)",
	}

	for _, expr := range invalid {
		t.Run("err_"+expr, func(t *testing.T) {
			if _, err := Evaluate(Request{Expression: expr}); err == nil {
				t.Fatalf("Evaluate(%q) must fail", expr)
			}
		})
	}
}

// A `^` inside a string literal is data, not an operator: the expression must be
// compiled exactly as written. Rewriting it to `**` used to corrupt strings,
// length checks and regex anchors.
func TestEvaluateKeepsCaretInsideStrings(t *testing.T) {
	tests := []struct {
		expr     string
		wantText string
	}{
		{`"^abc"`, "^abc"},
		{`len("^abc")`, "4"},
		{`"a^b" == "a**b"`, "false"},
		{`"abc" matches "^a"`, "true"},
		{`"http://x"`, "http://x"},
		{`144 + 2^3`, "152"},
	}

	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			res, err := Evaluate(Request{Expression: tc.expr})
			if err != nil {
				t.Fatalf("Evaluate(%q) failed: %v", tc.expr, err)
			}
			if res.Text != tc.wantText {
				t.Errorf("Evaluate(%q) text = %q, want %q", tc.expr, res.Text, tc.wantText)
			}
		})
	}
}

// `//` is a comment in the expression language, so the part it covers takes no
// part in the calculation. Such input must fail loudly instead, wherever the
// comment sits.
func TestEvaluateRejectsCommentsThatWouldEatTheExpression(t *testing.T) {
	rejected := []string{
		`13 // 5`,
		`1 + 1 // 2`,
		`(7//2)*100 + (7%2)`,
		`7 /* 2`,
		"7 // 2\n+ 1",
		`1 /* c */ + 2`,
	}

	for _, expr := range rejected {
		_, err := Evaluate(Request{Expression: expr})
		if err == nil {
			t.Fatalf("Evaluate(%q) must fail instead of ignoring part of the input", expr)
		}
		if !strings.Contains(err.Error(), "comment") {
			t.Errorf("Evaluate(%q) error = %v, want it to name the comment", expr, err)
		}
	}

	kept := []struct {
		expr     string
		wantText string
	}{
		{`13 / 5`, "2.6"},
		{`floor(13 / 5)`, "2"},
		{`"a//b"`, "a//b"},
		{`"http://x" + "/* y */"`, "http://x/* y */"},
		{`"中文 // 注释"`, "中文 // 注释"},
	}

	for _, tc := range kept {
		t.Run("kept_"+tc.expr, func(t *testing.T) {
			res, err := Evaluate(Request{Expression: tc.expr})
			if err != nil {
				t.Fatalf("Evaluate(%q) failed: %v", tc.expr, err)
			}
			if res.Text != tc.wantText {
				t.Errorf("Evaluate(%q) text = %q, want %q", tc.expr, res.Text, tc.wantText)
			}
		})
	}
}

// +, -, * are int64 arithmetic: exact up to MaxInt64 and wrapping silently past
// it. The tool schema states that range and the decimal escape hatch, so pin both
// here; if the engine ever stops wrapping, the wording has to change with it.
func TestEvaluateIntegerBoundaryMatchesSchemaWording(t *testing.T) {
	maxLiteral := strconv.FormatInt(math.MaxInt64, 10)

	res, err := Evaluate(Request{Expression: maxLiteral})
	if err != nil {
		t.Fatalf("Evaluate(%q) failed: %v", maxLiteral, err)
	}
	if res.Text != maxLiteral {
		t.Errorf("Evaluate(%q) text = %q, want the exact bound", maxLiteral, res.Text)
	}

	res, err = Evaluate(Request{Expression: maxLiteral + " + 1"})
	if err != nil {
		t.Fatalf("overflow is silent today, not an error: %v", err)
	}
	if res.Text != "-9223372036854775808" {
		t.Errorf("int64 overflow behavior changed: got %q, update the schema wording about the exact range", res.Text)
	}

	res, err = Evaluate(Request{Expression: maxLiteral + " + 1.0"})
	if err != nil {
		t.Fatalf("a decimal operand must evaluate in floating point: %v", err)
	}
	if res.Text != "9.223372036854776e+18" {
		t.Errorf("decimal escape hatch = %q, want 9.223372036854776e+18", res.Text)
	}
}

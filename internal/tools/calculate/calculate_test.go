// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package calculate

import (
	"strings"
	"testing"
)

func TestEvaluateRejectsDeepNestingWithoutCrashing(t *testing.T) {
	// Function calls recurse through parsePrimary → parseArguments → … just like
	// parentheses, so they need the same depth guard: an unguarded deep nesting
	// is a runtime stack overflow, which is a fatal error the tool executor's
	// panic recovery cannot catch.
	for _, depth := range []int{300, 5000, 50000} {
		expr := strings.Repeat("sin(", depth) + "1" + strings.Repeat(")", depth)
		if _, err := Evaluate(Request{Expression: expr}); err == nil {
			t.Fatalf("nesting depth %d must be rejected", depth)
		}
	}
	// The parenthesis form stays guarded as well.
	parens := strings.Repeat("(", 5000) + "1" + strings.Repeat(")", 5000)
	if _, err := Evaluate(Request{Expression: parens}); err == nil {
		t.Fatal("deep parenthesis nesting must be rejected")
	}
}

func TestEvaluateBasicExpressions(t *testing.T) {
	result, err := Evaluate(Request{Expression: "2 + 3 * sin(pi / 2)"})
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if result.Value != 5 {
		t.Fatalf("unexpected value: %v", result.Value)
	}
}

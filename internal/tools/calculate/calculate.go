// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package calculate

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/file"
	"github.com/expr-lang/expr/parser/lexer"
)

type Request struct {
	Expression string `json:"expression"`
}

type Result struct {
	Expression string  `json:"expression"`
	Value      float64 `json:"value,omitempty"`
	Text       string  `json:"text"`
}

// defaultEnv exposes standard math functions and constants to the evaluation environment.
var defaultEnv = map[string]any{
	// Constants
	"pi": math.Pi,
	"PI": math.Pi,
	"e":  math.E,
	"E":  math.E,

	// Power & roots
	"sqrt": math.Sqrt,
	"cbrt": math.Cbrt,
	"pow":  math.Pow,

	// Trigonometric
	"sin":   math.Sin,
	"cos":   math.Cos,
	"tan":   math.Tan,
	"asin":  math.Asin,
	"acos":  math.Acos,
	"atan":  math.Atan,
	"atan2": math.Atan2,

	// Exponential & logarithmic
	"log":   math.Log,
	"ln":    math.Log,
	"log10": math.Log10,
	"log2":  math.Log2,
	"exp":   math.Exp,
}

// Evaluate evaluates an expression using the expr-lang engine.
func Evaluate(req Request) (Result, error) {
	raw := strings.TrimSpace(req.Expression)
	if raw == "" {
		return Result{}, errors.New("expression is required")
	}
	if len(raw) > 4096 {
		return Result{}, errors.New("expression exceeds maximum length (4096 chars)")
	}
	if err := checkNestingDepth(raw, 100); err != nil {
		return Result{}, err
	}

	// Reject comment syntax before compiling, so that a python-style `13 // 5`
	// can never come back as a valid answer of 13.
	tokens, err := lexer.Lex(file.NewSource(raw))
	if err != nil {
		return Result{}, fmt.Errorf("invalid expression: %w", err)
	}
	if err := rejectComments(raw, tokens); err != nil {
		return Result{}, err
	}

	// The expression is compiled exactly as written: expr already reads `^` as
	// exponentiation, so a textual rewrite (e.g. `^` to `**`) would buy nothing
	// and would corrupt every string literal and regex anchor containing `^`.
	program, err := expr.Compile(raw, expr.Env(defaultEnv), expr.MaxNodes(200),
		expr.Patch(checkedArithmeticPatcher{}),
		expr.Function("__ally_arithmetic", checkedArithmetic),
		expr.Function("__ally_abs", checkedAbs))
	if err != nil {
		return Result{}, fmt.Errorf("invalid expression: %w", err)
	}

	output, err := expr.Run(program, defaultEnv)
	if err != nil {
		return Result{}, fmt.Errorf("eval error: %w", err)
	}

	val, text, err := formatOutput(output)
	if err != nil {
		return Result{}, err
	}

	return Result{
		Expression: raw,
		Value:      val,
		Text:       text,
	}, nil
}

// rejectComments refuses comment syntax anywhere in the expression. expr lexes
// `//` and `/* */` as comments and silently drops them, so a python-style
// `13 // 5` evaluates to 13, and a comment in the middle of an expression
// swallows whatever it covers. The lexer returns one token per piece of real
// input and skips whitespace and comments, so any gap between two tokens that is
// not blank is exactly such dropped input; string literals form tokens of their
// own, so a `//` inside one is never mistaken for a comment.
func rejectComments(raw string, tokens []lexer.Token) error {
	// Token spans are rune offsets, so the gaps have to be measured in runes as
	// well; slicing them as bytes lands inside multi-byte characters.
	runes := []rune(raw)
	prev := 0
	for _, tok := range tokens {
		if tok.Kind == lexer.EOF {
			continue
		}
		if err := reportIgnoredGap(runes, prev, tok.From); err != nil {
			return err
		}
		if tok.To > prev {
			prev = tok.To
		}
	}
	return reportIgnoredGap(runes, prev, len(runes))
}

// reportIgnoredGap reports the non-blank text the lexer skipped between two
// tokens, which is a comment by construction.
func reportIgnoredGap(runes []rune, from, to int) error {
	if to > len(runes) {
		to = len(runes)
	}
	if from >= to {
		return nil
	}
	ignored := strings.TrimSpace(string(runes[from:to]))
	if ignored == "" {
		return nil
	}
	if strings.HasPrefix(ignored, "//") {
		return fmt.Errorf("invalid expression: `//` starts a comment, so %q would be ignored; `//` is not integer division, write 13 / 5 or floor(13 / 5)", ignored)
	}
	return fmt.Errorf("invalid expression: %q is a comment and would be ignored, comments are not supported", ignored)
}

func formatOutput(out any) (float64, string, error) {
	switch v := out.(type) {
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, "", errors.New("expression result is not finite")
		}
		return v, strconv.FormatFloat(v, 'g', -1, 64), nil
	case float32:
		f := float64(v)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, "", errors.New("expression result is not finite")
		}
		return f, strconv.FormatFloat(f, 'g', -1, 32), nil
	case int:
		return float64(v), strconv.Itoa(v), nil
	case int64:
		return float64(v), strconv.FormatInt(v, 10), nil
	case int32:
		return float64(v), strconv.FormatInt(int64(v), 10), nil
	case uint:
		return float64(v), strconv.FormatUint(uint64(v), 10), nil
	case uint64:
		return float64(v), strconv.FormatUint(v, 10), nil
	case uint32:
		return float64(v), strconv.FormatUint(uint64(v), 10), nil
	case bool:
		if v {
			return 1, "true", nil
		}
		return 0, "false", nil
	case string:
		return 0, v, nil
	default:
		text := fmt.Sprint(v)
		return 0, text, nil
	}
}

func checkNestingDepth(s string, maxDepth int) error {
	depth := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '(' {
			depth++
			if depth > maxDepth {
				return errors.New("expression nesting too deep")
			}
		} else if s[i] == ')' {
			depth--
		}
	}
	return nil
}

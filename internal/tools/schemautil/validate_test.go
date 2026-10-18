// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package schemautil

import (
	"strings"
	"testing"
)

func objectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func stringProperty() map[string]any { return map[string]any{"type": "string"} }

func validateJSON(t *testing.T, schema map[string]any, args string) []Violation {
	t.Helper()
	return ValidateArgs(schema, []byte(args))
}

func TestValidateArgsReportsTheDeclaredKeywords(t *testing.T) {
	cases := []struct {
		name     string
		schema   map[string]any
		args     string
		wantPath string
		wantText string
	}{
		{
			name:     "missing required",
			schema:   objectSchema(map[string]any{"path": stringProperty()}, "path"),
			args:     `{}`,
			wantText: `missing required parameter "path"`,
		},
		{
			name:     "unknown key lists what is accepted",
			schema:   objectSchema(map[string]any{"path": stringProperty(), "recursive": map[string]any{"type": "boolean"}}),
			args:     `{"path":"a","renamed":true}`,
			wantText: `unsupported parameter "renamed" (supported: path, recursive)`,
		},
		{
			name:     "nested unknown key carries its path",
			schema:   objectSchema(map[string]any{"files": map[string]any{"type": "array", "items": objectSchema(map[string]any{"path": stringProperty()}, "path")}}, "files"),
			args:     `{"files":[{"path":"a"},{"path":"b","tailLine":5}]}`,
			wantPath: "files[1]",
			wantText: `unsupported parameter "tailLine"`,
		},
		{
			name:     "wrong type",
			schema:   objectSchema(map[string]any{"count": map[string]any{"type": "integer"}}),
			args:     `{"count":"three"}`,
			wantPath: "count",
			wantText: "must be integer, got string",
		},
		{
			name:     "enum",
			schema:   objectSchema(map[string]any{"action": map[string]any{"type": "string", "enum": []string{"start", "stop"}}}),
			args:     `{"action":"status"}`,
			wantPath: "action",
			wantText: `must be one of "start", "stop", got "status"`,
		},
		{
			name:     "length counts runes not bytes",
			schema:   objectSchema(map[string]any{"title": map[string]any{"type": "string", "maxLength": 3}}),
			args:     `{"title":"中文标题"}`,
			wantPath: "title",
			wantText: "must be at most 3 characters (got 4)",
		},
		{
			name:     "range",
			schema:   objectSchema(map[string]any{"timeout": map[string]any{"type": "integer", "minimum": 0, "maximum": 600}}),
			args:     `{"timeout":900}`,
			wantPath: "timeout",
			wantText: "must be <= 600 (got 900)",
		},
		{
			name:     "array bounds",
			schema:   objectSchema(map[string]any{"items": map[string]any{"type": "array", "minItems": 1, "maxItems": 2, "items": stringProperty()}}),
			args:     `{"items":["a","b","c"]}`,
			wantPath: "items",
			wantText: "must contain at most 2 items (got 3)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			violations := validateJSON(t, tc.schema, tc.args)
			if len(violations) == 0 {
				t.Fatalf("expected a violation for %s", tc.args)
			}
			report := DescribeViolations(violations)
			if !strings.Contains(report, tc.wantText) {
				t.Fatalf("report %q does not mention %q", report, tc.wantText)
			}
			if tc.wantPath != "" && !strings.Contains(report, tc.wantPath+":") {
				t.Fatalf("report %q does not carry the path %q", report, tc.wantPath)
			}
		})
	}
}

// TestValidateArgsNullMeansNotProvided pins the one tolerance rule: an explicit
// null is what the Go decode layer sees as "absent", so the gate must not turn
// it into a type error — while a required parameter spelled null is still
// missing.
func TestValidateArgsNullMeansNotProvided(t *testing.T) {
	optional := objectSchema(map[string]any{"path": stringProperty(), "timeout": map[string]any{"type": "integer"}})
	if violations := validateJSON(t, optional, `{"path":"a","timeout":null}`); len(violations) != 0 {
		t.Fatalf("null on an optional parameter must pass, got %v", violations)
	}
	required := objectSchema(map[string]any{"content": stringProperty()}, "content")
	if violations := validateJSON(t, required, `{"content":null}`); len(violations) == 0 {
		t.Fatal("null must not satisfy a required parameter")
	}
}

func TestValidateArgsStopsAtTheReportCap(t *testing.T) {
	schema := objectSchema(map[string]any{"path": stringProperty()})
	violations := validateJSON(t, schema, `{"a":1,"b":2,"c":3,"d":4,"e":5,"f":6,"g":7,"h":8,"i":9,"j":10,"k":11}`)
	if len(violations) != MaxReportedViolations {
		t.Fatalf("expected the report to be capped at %d, got %d", MaxReportedViolations, len(violations))
	}
}

func TestValidateArgsIgnoresANilSchema(t *testing.T) {
	if violations := ValidateArgs(nil, []byte(`{"anything":1}`)); len(violations) != 0 {
		t.Fatalf("a tool without a declaration has nothing to check, got %v", violations)
	}
}

// TestValidateArgsEnforcesAPattern: a declared pattern must be enforced by the
// production validator, and a pattern that does not compile must be reported
// rather than let a typo silently retire the constraint.
func TestValidateArgsEnforcesAPattern(t *testing.T) {
	schema := objectSchema(map[string]any{"version": map[string]any{"type": "string", "pattern": "^[0-9a-z]{6}$"}})
	if violations := validateJSON(t, schema, `{"version":"abc123"}`); len(violations) != 0 {
		t.Fatalf("a value matching the pattern must pass: %v", violations)
	}
	if violations := validateJSON(t, schema, `{"version":"nope"}`); len(violations) == 0 {
		t.Fatal("a value that breaks the pattern must be reported")
	}
	if _, err := compiledPattern(`([0-9`); err == nil {
		t.Fatal("a pattern that does not compile must be reported, not silently skipped")
	}
}

// TestValidateArgsIgnoresCompositeKeywords pins the tolerance rule for keywords
// our declarations never use: they are skipped rather than enforced. The
// declarations stay flat on purpose (some gateways refuse to carry oneOf/anyOf/
// allOf), so a value that satisfies none of the alternatives is not a violation
// here; the handler checks the combination itself.
func TestValidateArgsIgnoresCompositeKeywords(t *testing.T) {
	schema := objectSchema(map[string]any{"path": stringProperty()})
	schema["anyOf"] = []any{
		map[string]any{"required": []string{"never"}},
		map[string]any{"required": []string{"alsoNever"}},
	}
	if violations := validateJSON(t, schema, `{"path":"a"}`); len(violations) != 0 {
		t.Fatalf("a keyword the declarations never use must be ignored, got %v", violations)
	}
}

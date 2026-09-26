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

func TestValidateArgsEnforcesAlternativeShapes(t *testing.T) {
	oneOf := objectSchema(map[string]any{
		"path": stringProperty(),
		"edit": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"oldText":   stringProperty(),
				"lineRange": stringProperty(),
			},
			"additionalProperties": false,
			"oneOf": []any{
				map[string]any{"required": []string{"oldText"}, "not": map[string]any{"required": []string{"lineRange"}}},
				map[string]any{"required": []string{"lineRange"}, "not": map[string]any{"required": []string{"oldText"}}},
			},
		},
	}, "path")

	if violations := validateJSON(t, oneOf, `{"path":"a","edit":{"oldText":"x"}}`); len(violations) != 0 {
		t.Fatalf("a single shape must pass, got %v", violations)
	}
	overlap := validateJSON(t, oneOf, `{"path":"a","edit":{"oldText":"x","lineRange":"1-2"}}`)
	if len(overlap) == 0 || !strings.Contains(DescribeViolations(overlap), "exactly one") {
		t.Fatalf("two matching shapes must be rejected, got %v", overlap)
	}
	none := validateJSON(t, oneOf, `{"path":"a","edit":{}}`)
	if len(none) == 0 || !strings.Contains(DescribeViolations(none), "exactly one") {
		t.Fatalf("no matching shape must be rejected, got %v", none)
	}

	httpLike := objectSchema(map[string]any{"url": stringProperty(), "body": stringProperty(), "json": map[string]any{}})
	httpLike["not"] = map[string]any{"required": []string{"body", "json"}}
	exclusive := validateJSON(t, httpLike, `{"url":"u","body":"b","json":{}}`)
	if len(exclusive) == 0 || !strings.Contains(DescribeViolations(exclusive), "body") {
		t.Fatalf("a forbidden combination must name both keys, got %v", exclusive)
	}
	if violations := validateJSON(t, httpLike, `{"url":"u","body":"b"}`); len(violations) != 0 {
		t.Fatalf("either key alone is allowed, got %v", violations)
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

func TestCheckPatternsRejectsABrokenExpression(t *testing.T) {
	good := objectSchema(map[string]any{"version": map[string]any{"type": "string", "pattern": "^[0-9a-z]{6}$"}})
	if err := CheckPatterns(good); err != nil {
		t.Fatalf("a valid pattern must compile: %v", err)
	}
	broken := objectSchema(map[string]any{"version": map[string]any{"type": "string", "pattern": "([0-9"}})
	if err := CheckPatterns(broken); err == nil {
		t.Fatal("a pattern that does not compile must be reported, not silently skipped")
	}
}

// TestValidateArgsBlamesTheBranchTheCallAimedAt pins which branch explains a
// failed alternative shape: the branch that pins a discriminator to the value the
// call carries, not whichever branch happens to be one violation short. Counting
// violations alone answered {"action":"create","name":"n"} (only schedule
// missing) with `action: must be "list"`, which sends the model to the wrong
// action.
func TestValidateArgsBlamesTheBranchTheCallAimedAt(t *testing.T) {
	schema := objectSchema(map[string]any{
		"action":   map[string]any{"type": "string", "enum": []string{"create", "list", "delete"}},
		"name":     stringProperty(),
		"schedule": stringProperty(),
		"content":  stringProperty(),
		"id":       stringProperty(),
	})
	schema["oneOf"] = []any{
		map[string]any{"properties": map[string]any{"action": map[string]any{"const": "create"}}, "required": []string{"name", "schedule", "content"}},
		map[string]any{"properties": map[string]any{"action": map[string]any{"const": "list"}}},
		map[string]any{"properties": map[string]any{"action": map[string]any{"const": "delete"}}, "required": []string{"id"}},
	}

	report := DescribeViolations(validateJSON(t, schema, `{"action":"create","name":"n"}`))
	if !strings.Contains(report, `missing required parameter "schedule"`) {
		t.Fatalf("report %q must blame the create branch the call aimed at", report)
	}
	if strings.Contains(report, `must be "list"`) {
		t.Fatalf("report %q must not point at the list branch", report)
	}

	// A branch whose discriminator does not match is never the explanation, even
	// when it is the only branch with anything to report.
	report = DescribeViolations(validateJSON(t, schema, `{"action":"archive"}`))
	if !strings.Contains(report, "must be one of") || !strings.Contains(report, "exactly one allowed shape") {
		t.Fatalf("an unknown discriminator must be reported by name, got %q", report)
	}
}

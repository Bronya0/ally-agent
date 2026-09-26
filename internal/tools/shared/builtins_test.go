// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package shared

import (
	"strings"
	"testing"

	"ally-dev/internal/tools/schemautil"
)

func TestRuntimeSensitiveBuiltinSchemas(t *testing.T) {
	askParams, _ := builtinSchemaForTest(t, "ask")
	askProperties := schemaObjectForTest(t, askParams["properties"])
	questions := schemaObjectForTest(t, askProperties["questions"])
	questionItem := schemaObjectForTest(t, questions["items"])
	questionProperties := schemaObjectForTest(t, questionItem["properties"])
	options := schemaObjectForTest(t, questionProperties["options"])
	optionItem := schemaObjectForTest(t, options["items"])
	optionProperties := schemaObjectForTest(t, optionItem["properties"])
	if !schemaRequiredForTest(t, optionItem, "description") {
		t.Fatal("ask option description must be required to match runtime validation")
	}
	if schemaObjectForTest(t, optionProperties["description"])["minLength"] != 1 {
		t.Fatal("ask option description must reject empty strings")
	}

	readParams, _ := builtinSchemaForTest(t, "read")
	readProperties := schemaObjectForTest(t, readParams["properties"])
	readItems := schemaObjectForTest(t, schemaObjectForTest(t, readProperties["files"])["items"])
	if branches, ok := readItems["oneOf"].([]any); !ok || len(branches) != 3 {
		t.Fatalf("read file schema must distinguish omitted, non-negative, and negative-tail startLine cases; got %#v", readItems["oneOf"])
	}
	startLine := schemaObjectForTest(t, schemaObjectForTest(t, readItems["properties"])["startLine"])
	if !strings.Contains(startLine["description"].(string), "negative values") {
		t.Fatalf("read startLine description omits tail semantics: %v", startLine["description"])
	}

	sshParams, _ := builtinSchemaForTest(t, "ssh_cluster")
	sshBranches, ok := sshParams["oneOf"].([]any)
	if !ok || len(sshBranches) != 2 || !schemaRequiredForTest(t, schemaObjectForTest(t, sshBranches[1]), "alias") {
		t.Fatalf("ssh_cluster add schema must require alias: %#v", sshParams["oneOf"])
	}

	for _, name := range []string{"http_request", "web_fetch"} {
		params, _ := builtinSchemaForTest(t, name)
		properties := schemaObjectForTest(t, params["properties"])
		urlSchema := schemaObjectForTest(t, properties["url"])
		if got := urlSchema["pattern"]; got != `^https?://\S+$` {
			t.Fatalf("%s URL pattern = %#v, want an absolute HTTP(S) URL pattern", name, got)
		}
	}

	renderParams, _ := builtinSchemaForTest(t, "render_html")
	renderProperties := schemaObjectForTest(t, renderParams["properties"])
	if got := schemaObjectForTest(t, renderProperties["html"])["maxLength"]; got != MaxRenderHTMLCharacters {
		t.Fatalf("render_html maxLength = %#v, want %d", got, MaxRenderHTMLCharacters)
	}

	deleteParams, deleteDescription := builtinSchemaForTest(t, "remote_delete_path")
	deleteProperties := schemaObjectForTest(t, deleteParams["properties"])
	if !strings.Contains(deleteDescription, "other directories require recursive=true") ||
		!strings.Contains(schemaObjectForTest(t, deleteProperties["recursive"])["description"].(string), "Immediate child directories") {
		t.Fatal("remote_delete_path description must explain recursive deletion and top-level directory blocking")
	}

	scheduleParams, scheduleDescription := builtinSchemaForTest(t, "scheduled_task")
	scheduleProperties := schemaObjectForTest(t, scheduleParams["properties"])
	if !strings.Contains(scheduleDescription, "future RFC3339 one-shot") || !strings.Contains(scheduleDescription, "recurring automation") {
		t.Fatal("scheduled_task description must distinguish backend one-shot support from agent creation policy")
	}
	if schemaObjectForTest(t, scheduleProperties["schedule"])["minLength"] != 1 {
		t.Fatal("scheduled_task schedule must reject empty strings")
	}
}

// TestEveryBuiltinSchemaIsEnforceable guards the argument gate in executeTool:
// the gate rejects unknown keys and compiles each `pattern`, so a declaration
// that is not a strict object would let a stray key through, and a pattern with
// a typo would silently retire that constraint.
func TestEveryBuiltinSchemaIsEnforceable(t *testing.T) {
	for _, tool := range Builtins() {
		if tool.Function == nil {
			continue
		}
		name := tool.Function.Name
		params, ok := tool.Function.Parameters.(map[string]any)
		if !ok {
			t.Fatalf("%s parameters have type %T, want map[string]any", name, tool.Function.Parameters)
		}
		if params["type"] != "object" || params["additionalProperties"] != false {
			t.Fatalf("%s must be a strict object so unknown keys are rejected, got type=%#v additionalProperties=%#v", name, params["type"], params["additionalProperties"])
		}
		if err := schemautil.CheckPatterns(params); err != nil {
			t.Fatalf("%s has a pattern that does not compile: %v", name, err)
		}
	}
}

// TestServiceSchemaExposesTheStopAndReadTuning pins the two parameters the
// runtime always accepted but the schema never declared, so a model could not
// ask a database-style service for a longer graceful stop.
func TestServiceSchemaExposesTheStopAndReadTuning(t *testing.T) {
	params, _ := builtinSchemaForTest(t, "service")
	properties := schemaObjectForTest(t, params["properties"])

	grace := schemaObjectForTest(t, properties["graceSeconds"])
	if grace["maximum"] != MaxServiceStopGraceSeconds || grace["minimum"] != 0 {
		t.Fatalf("service graceSeconds bounds = %#v, want 0..%d so an omitted or 0 value means the default", grace, MaxServiceStopGraceSeconds)
	}
	tail := schemaObjectForTest(t, properties["tailBytes"])
	if tail["maximum"] != MaxServiceReadTailBytes || tail["minimum"] != 0 {
		t.Fatalf("service tailBytes bounds = %#v, want 0..%d", tail, MaxServiceReadTailBytes)
	}
}

func builtinSchemaForTest(t *testing.T, name string) (map[string]any, string) {
	t.Helper()
	for _, tool := range Builtins() {
		if tool.Function == nil || tool.Function.Name != name {
			continue
		}
		params, ok := tool.Function.Parameters.(map[string]any)
		if !ok {
			t.Fatalf("%s parameters have type %T, want map[string]any", name, tool.Function.Parameters)
		}
		return params, tool.Function.Description
	}
	t.Fatalf("built-in tool %q not found", name)
	return nil, ""
}

func schemaObjectForTest(t *testing.T, value any) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("schema object has type %T, want map[string]any", value)
	}
	return object
}

func schemaRequiredForTest(t *testing.T, object map[string]any, field string) bool {
	t.Helper()
	required, ok := object["required"].([]string)
	if !ok {
		t.Fatalf("required fields have type %T, want []string", object["required"])
	}
	for _, name := range required {
		if name == field {
			return true
		}
	}
	return false
}

// TestBuiltinGateTreatsEmptyOptionalsAsAbsent pins the argument gate to the
// judgement the runtime makes about mutual exclusion: it is decided on each
// option's effective value, not on the presence of its key.
//
//   - tailLines=0 and body="" mean "not provided" to the runtime, so pairing
//     them with the other form must not read as "both forms were requested".
//   - A blank source (oldText="", lineRange="") is "not provided" too — the
//     runtime picks the source by effective value (tools/edit/apply.go), so a
//     padded blank beside a real source is carried out, and one with no real
//     source at all is still refused, because no branch matches.
func TestBuiltinGateTreatsEmptyOptionalsAsAbsent(t *testing.T) {
	cases := []struct {
		name         string
		tool         string
		args         string
		wantReject   bool
		wantContains string
		wantAbsent   string
	}{
		{"read pads tailLines with 0", "read", `{"files":[{"path":"a.go","startLine":5,"endLine":20,"tailLines":0}]}`, false, "", ""},
		{"read mixes tail and range", "read", `{"files":[{"path":"a.go","startLine":5,"tailLines":100}]}`, true, "exactly one allowed shape", ""},
		{"edit pads oldText with an empty string", "edit", `{"path":"a.go","version":"9k3m7x","changes":[{"lineRange":"1-9","newText":"x","oldText":""}]}`, false, "", ""},
		{"edit pads lineRange with an empty string", "edit", `{"path":"a.go","version":"9k3m7x","changes":[{"oldText":"a","newText":"x","lineRange":""}]}`, false, "", ""},
		{"edit pads lineRange with blanks", "edit", `{"path":"a.go","version":"9k3m7x","changes":[{"oldText":"a","newText":"x","lineRange":"  "}]}`, false, "", ""},
		{"edit supplies both sources", "edit", `{"path":"a.go","version":"9k3m7x","changes":[{"oldText":"a","lineRange":"1-9","newText":"x"}]}`, true, "exactly one allowed shape", ""},
		{"edit pads every source blank", "edit", `{"path":"a.go","version":"9k3m7x","changes":[{"oldText":"","lineRange":"","newText":"x"}]}`, true, "exactly one allowed shape", ""},
		{"edit replaces nothing", "edit", `{"path":"a.go","version":"9k3m7x","changes":[{"lineRange":"abcdef","newText":"x"}]}`, true, "must match the pattern", ""},
		{"remote_edit pads oldText with an empty string", "remote_edit", `{"target":"t:/w","path":"a.go","version":"9k3m7x","changes":[{"lineRange":"1-9","newText":"x","oldText":""}]}`, false, "", ""},
		{"remote_edit pads lineRange with an empty string", "remote_edit", `{"target":"t:/w","path":"a.go","version":"9k3m7x","changes":[{"oldText":"a","newText":"x","lineRange":""}]}`, false, "", ""},
		{"http_request pads body with an empty string", "http_request", `{"url":"https://example.test","body":"","json":{"a":1}}`, false, "", ""},
		{"http_request supplies body and json", "http_request", `{"url":"https://example.test","body":"x","json":{"a":1}}`, true, "must not be combined with", ""},
		{"web_fetch pads format with an empty string", "web_fetch", `{"url":"https://example.test/","format":""}`, false, "", ""},
		{"grep pads outputMode with an empty string", "grep", `{"pattern":"x","outputMode":""}`, false, "", ""},
		{"remote_run_command pads shell with an empty string", "remote_run_command", `{"target":"t:/w","command":"ls","shell":""}`, false, "", ""},
		{"web_fetch rejects an unknown format", "web_fetch", `{"url":"https://example.test/","format":"markdown"}`, true, "must be one of", ""},
	}
	for _, tc := range cases {
		schema, _ := builtinSchemaForTest(t, tc.tool)
		violations := schemautil.ValidateArgs(schema, []byte(tc.args))
		if tc.wantReject && len(violations) == 0 {
			t.Fatalf("%s: the gate accepted args the declaration rejects: %s", tc.name, tc.args)
		}
		if !tc.wantReject && len(violations) != 0 {
			t.Fatalf("%s: the gate rejected a call the runtime accepts (%s): %s", tc.name, schemautil.DescribeViolations(violations), tc.args)
		}
		report := schemautil.DescribeViolations(violations)
		if tc.wantContains != "" && !strings.Contains(report, tc.wantContains) {
			t.Fatalf("%s: rejection %q does not mention %q", tc.name, report, tc.wantContains)
		}
		if tc.wantAbsent != "" && strings.Contains(report, tc.wantAbsent) {
			t.Fatalf("%s: rejection %q still blames the mutual-exclusion rule", tc.name, report)
		}
	}
}

// TestBuiltinSchemasExposeTheRuntimeParameters pins the parameters the runtime
// honors but the schema long left undeclared. Without a declaration the gate
// rejects the key outright, so a model cannot send auth headers, raise a body
// cap, or pick a remote shell at all.
func TestBuiltinSchemasExposeTheRuntimeParameters(t *testing.T) {
	for _, tc := range []struct{ tool, param string }{
		{"web_fetch", "headers"},
		{"web_fetch", "maxChars"},
		{"web_fetch", "maxBytes"},
		{"http_request", "maxBytes"},
		{"http_request", "followRedirects"},
		{"remote_run_command", "shell"},
	} {
		params, _ := builtinSchemaForTest(t, tc.tool)
		properties := schemaObjectForTest(t, params["properties"])
		if _, ok := properties[tc.param]; !ok {
			t.Fatalf("%s must declare %q: the runtime honors it, so an undeclared key is rejected by the argument gate", tc.tool, tc.param)
		}
	}
}

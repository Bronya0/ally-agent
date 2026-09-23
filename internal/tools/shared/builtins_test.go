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

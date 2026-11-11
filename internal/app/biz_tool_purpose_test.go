// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"ally-dev/internal/tools/schemautil"
	openai "github.com/sashabaranov/go-openai"
)

func TestPurposeContractIsTopLevelAndDetached(t *testing.T) {
	a := &App{}
	original := chatTools()
	before, _ := json.Marshal(original)
	tools := a.buildToolsForSession("purpose", ConfigState{})
	for _, tool := range tools {
		schema := tool.Function.Parameters.(map[string]any)
		field := schema[schemautil.PurposeFieldKey].(string)
		if !slices.Contains(schemautil.StringSlice(schema["required"]), field) {
			t.Fatalf("%s missing required purpose", tool.Function.Name)
		}
		// No presentation fields may leak into file/change/question item contracts.
		var visit func(any, bool)
		visit = func(value any, root bool) {
			switch value := value.(type) {
			case map[string]any:
				if !root && value[schemautil.PurposeFieldKey] != nil {
					t.Fatal("nested purpose injected")
				}
				if props, ok := value["properties"].(map[string]any); ok && !root && props[field] != nil {
					t.Fatalf("%s nested purpose", tool.Function.Name)
				}
				for _, child := range value {
					visit(child, false)
				}
			case []any:
				for _, child := range value {
					visit(child, false)
				}
			}
		}
		visit(schema, true)
		wire := schemautil.Map(schema)
		if wire[schemautil.PurposeFieldKey] != nil {
			t.Fatal("private metadata leaked to providers")
		}
	}
	after, _ := json.Marshal(original)
	if string(before) != string(after) {
		t.Fatal("injection mutated shared builtin schemas")
	}
	schema := tools[0].Function.Parameters.(map[string]any)
	delete(schema["properties"].(map[string]any), "purpose")
	frozen := a.buildToolsForSession("purpose", ConfigState{})[0].Function.Parameters.(map[string]any)
	if frozen["properties"].(map[string]any)["purpose"] == nil {
		t.Fatal("caller mutated frozen purpose contract")
	}
}

func TestPurposeMetadataCannotBlockExecution(t *testing.T) {
	a := &App{}
	ctx := withBuiltinToolAvailability(context.Background(), a.buildToolsForConfig(ConfigState{}))
	for _, purpose := range []any{nil, "", "  ", 42, strings.Repeat("字", 100)} {
		args := map[string]any{"expression": "1+1"}
		if purpose != nil {
			args["purpose"] = purpose
		}
		encoded, _ := json.Marshal(args)
		result := a.executeTool(ctx, ConfigState{}, "purpose", "calculate", encoded)
		if !result.OK {
			t.Fatalf("purpose=%v result=%+v", purpose, result)
		}
	}
	for _, n := range []int{1, 35, 99, 100, 1000} {
		args, _ := json.Marshal(map[string]any{"purpose": strings.Repeat("字", n), "expression": "1+1"})
		result := a.executeTool(ctx, ConfigState{}, "purpose", "calculate", args)
		if !result.OK || len(result.Warnings) != 0 {
			t.Fatalf("%d Unicode characters rejected or leaked to handler: %+v", n, result)
		}
	}
	// Stateless host calls and frozen legacy toolsets use their own contract.
	legacy := withBuiltinToolAvailability(ctx, chatTools())
	if result := a.executeTool(legacy, ConfigState{}, "", "calculate", []byte(`{"expression":"1+1"}`)); !result.OK {
		t.Fatal(result)
	}
}

func TestPurposePreservesMCPBusinessArgumentsAndDedup(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{"purpose": map[string]any{"type": "string"}, "value": map[string]any{"type": "string"}}, "required": []string{"purpose"}, "additionalProperties": false}
	tools := withToolPurposes([]openai.Tool{rawFunctionTool("mcp__s__t", "", schema)})
	ctx := withBuiltinToolAvailability(context.Background(), tools)
	if toolPurposeFields(ctx)["mcp__s__t"] != "_purpose" {
		t.Fatal("MCP business purpose overwritten")
	}
	args := `{"purpose":"server-owned","_purpose":"说明调用目的","value":"<raw>"}`
	stripped := prepareToolPurpose(ctx, "mcp__s__t", []byte(args))
	var object map[string]string
	if json.Unmarshal(stripped, &object) != nil || object["purpose"] != "server-owned" || object["value"] != "<raw>" || object["_purpose"] != "" {
		t.Fatalf("business parameters altered: %s", stripped)
	}
	calls := []openai.ToolCall{stagedCall("mcp__s__t", args), stagedCall("mcp__s__t", strings.Replace(args, "说明调用目的", "另一个说明", 1))}
	if got := detectToolBatchConflicts(ConfigState{}, calls, toolPurposeFields(ctx)); toolErrorCode(got[1]) != "E_DUPLICATE_TOOL_CALL" {
		t.Fatalf("presentation changed semantic identity: %v", got)
	}
	// Without an injected contract, server-owned fields remain untouched.
	unchanged := prepareToolPurpose(context.Background(), "mcp__s__t", []byte(args))
	if string(unchanged) != args {
		t.Fatal("legacy MCP arguments changed")
	}
	builtins := []openai.ToolCall{stagedCall("calculate", `{"expression":"1+1","purpose":"一次计算"}`), stagedCall("calculate", `{"expression":"1+1","purpose":"再次计算","stage":0}`)}
	if got := detectToolBatchConflicts(ConfigState{}, builtins); toolErrorCode(got[1]) != "E_DUPLICATE_TOOL_CALL" {
		t.Fatal("builtin purpose bypassed dedup")
	}
}

func TestPurposeAccountingAndCompositeMCPContract(t *testing.T) {
	legacy := chatTools()
	current := withToolPurposes(legacy)
	if got := estimateToolSchemaTokens(current); got != estimateModelToolSchemas(current) || got <= estimateToolSchemaTokens(legacy) {
		t.Fatalf("purpose tokens missing from cached estimate: %d", got)
	}
	if got := estimateToolSchemaTokens(current[:3]); got != estimateModelToolSchemas(current[:3]) {
		t.Fatal("filtered tools lost purpose accounting")
	}
	schema := map[string]any{"type": "object", "allOf": []any{map[string]any{"type": "object", "properties": map[string]any{"purpose": map[string]any{"type": "string"}}, "additionalProperties": false}}}
	injected := withToolPurposes([]openai.Tool{rawFunctionTool("mcp__composite", "", schema)})[0].Function.Parameters.(map[string]any)
	if injected[schemautil.PurposeFieldKey] != "_purpose" {
		t.Fatal("branch business purpose overwritten")
	}
	if violations := schemautil.ValidateArgs(injected, []byte(`{"purpose":"business","_purpose":"用户可见说明"}`)); len(violations) != 0 {
		t.Fatalf("closed branch rejected common metadata: %+v", violations)
	}
}

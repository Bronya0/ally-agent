// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"encoding/json"

	"ally-dev/internal/tools/schemautil"
	toolshared "ally-dev/internal/tools/shared"
	openai "github.com/sashabaranov/go-openai"
)

// Inject once at the model boundary. Tool DTOs and nested batch items keep their
// original contracts. MCP schemas may already have a business field named purpose.
func withToolPurposes(tools []openai.Tool) []openai.Tool {
	out := make([]openai.Tool, len(tools))
	copy(out, tools)
	for i := range out {
		if out[i].Function == nil {
			continue
		}
		fn := *out[i].Function
		out[i].Function = &fn
		schema := schemautil.Map(fn.Parameters)
		field := "purpose"
		for purposeFieldExists(schema, field) {
			field = "_" + field
		}
		property := map[string]any{
			"type":        "string",
			"description": "Brief purpose in the user's language, usually 10–30 characters, under 100. One sentence describing the intended action; summarize a batch once.",
		}
		addPurposeProperty(schema, field, property)
		schema["required"] = append(schemautil.StringSlice(schema["required"]), field)
		schema[schemautil.PurposeFieldKey] = field
		fn.Parameters = schema
	}
	return out
}

// Composite branches describe the same top-level object. Each closed branch
// must allow the metadata too; nested files/changes/items remain untouched.
func purposeFieldExists(schema map[string]any, field string) bool {
	if props, _ := schema["properties"].(map[string]any); props[field] != nil {
		return true
	}
	for _, key := range []string{"allOf", "anyOf", "oneOf"} {
		branches, _ := schema[key].([]any)
		for _, raw := range branches {
			if branch, ok := raw.(map[string]any); ok && purposeFieldExists(branch, field) {
				return true
			}
		}
	}
	return false
}

func addPurposeProperty(schema map[string]any, field string, property map[string]any) {
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
		schema["properties"] = props
	}
	props[field] = property
	for _, key := range []string{"allOf", "anyOf", "oneOf"} {
		branches, _ := schema[key].([]any)
		for _, raw := range branches {
			if branch, ok := raw.(map[string]any); ok {
				addPurposeProperty(branch, field, property)
			}
		}
	}
}

type toolPurposeContextKey struct{}

func toolPurposeFields(ctx context.Context) map[string]string {
	fields, _ := ctx.Value(toolPurposeContextKey{}).(map[string]string)
	return fields
}

// prepareToolPurpose only strips presentation metadata; it does not validate it.
// The schema intentionally declares purpose required to encourage useful UI
// labels, while execution accepts missing, malformed or long labels so display
// metadata can never block a business operation. Do not add execution validation.
// Strip only the field owned by this injected contract.
func prepareToolPurpose(ctx context.Context, name string, args []byte) []byte {
	field := toolPurposeFields(ctx)[normalizeToolName(name)]
	required := field != ""
	if !required {
		if !toolshared.IsBuiltinTool(normalizeToolName(name)) {
			return args
		}
		field = "purpose"
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(args, &object) != nil || object == nil {
		return args
	}
	_, present := object[field]
	if !present && !required {
		return args
	}
	delete(object, field)
	encoded, err := json.Marshal(object)
	if err != nil {
		return args
	}
	return encoded
}

func (a *App) toolEventMeta(ctx context.Context, name string) map[string]any {
	meta := a.mcpToolEventMeta(name)
	if field := toolPurposeFields(ctx)[normalizeToolName(name)]; field != "" {
		if meta == nil {
			meta = map[string]any{}
		}
		meta["purposeField"] = field
	}
	return meta
}

// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

// Package schemautil holds the JSON-Schema repair helpers used to build tool
// declarations for every provider wire format (OpenAI Chat / OpenAI Responses /
// Anthropic Messages).
//
// Tool schemas reach Ally from three sources that are all allowed to be sloppy:
// hand-written builtins, MCP servers (whose generators emit `$ref` + `$defs`
// and occasionally a `type` that contradicts an `enum`), and user import. The
// providers are not forgiving about any of it:
//
//   - an unresolved `$ref` is rejected outright;
//   - a property schema without `type` is rejected by strict validators
//     (Anthropic, OpenAI Responses strict mode).
//
// Everything here is a host-neutral pure function: it depends only on the JSON
// value it is given, never on App state, a provider client, or a config. The
// caller decides which wire format the result is bound into.
package schemautil

import (
	"encoding/json"
	"strings"
)

// Map normalizes an arbitrary tool-parameter value into a JSON-Schema object
// that every provider accepts: local `$ref` pointers are inlined, missing or
// contradictory property `type` fields are repaired, and a nil/undefined value
// becomes the empty object schema. The input is never mutated.
func Map(value any) map[string]any {
	if value == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	var m map[string]any
	if asMap, ok := value.(map[string]any); ok {
		m = cloneMap(asMap)
	} else if raw, err := json.Marshal(value); err == nil {
		_ = json.Unmarshal(raw, &m)
	}
	if m == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	dereffed := deref(m)
	return normalizeTypes(dereffed)
}

// StringSlice coerces a decoded JSON value into a string slice, dropping
// non-string entries. Used for the `required` keyword, which providers expect
// as a list of property names.
func StringSlice(value any) []string {
	switch v := value.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func cloneMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = cloneValue(v)
	}
	return out
}

func cloneValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		return cloneMap(val)
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			out[i] = cloneValue(item)
		}
		return out
	default:
		return val
	}
}

// deref resolves local $ref pointers (such as #/$defs/... and #/definitions/...)
// by inlining definitions directly into the schema. Anthropic, Moonshot (Kimi), and
// OpenAI Responses strict mode reject schemas with unresolved $ref pointers.
// Circular references are preserved as $ref to avoid infinite recursion.
func deref(root map[string]any) map[string]any {
	if root == nil {
		return nil
	}
	cloned := cloneMap(root)
	visited := make(map[string]bool)
	resolved := resolveNode(cloned, cloned, visited)
	resMap, ok := resolved.(map[string]any)
	if !ok {
		return cloned
	}
	if !hasUnresolvedRef(resMap, "$defs") {
		delete(resMap, "$defs")
	}
	if !hasUnresolvedRef(resMap, "definitions") {
		delete(resMap, "definitions")
	}
	return resMap
}

func resolveNode(node any, root map[string]any, visited map[string]bool) any {
	switch val := node.(type) {
	case []any:
		out := make([]any, len(val))
		for i, v := range val {
			out[i] = resolveNode(v, root, visited)
		}
		return out
	case map[string]any:
		if refStr, ok := val["$ref"].(string); ok && strings.HasPrefix(refStr, "#/") {
			if visited[refStr] {
				return val
			}
			target, found := resolveLocalPointer(root, refStr)
			if found {
				visited[refStr] = true
				targetResolved := resolveNode(target, root, visited)
				delete(visited, refStr)

				if targetMap, isMap := targetResolved.(map[string]any); isMap {
					merged := make(map[string]any, len(targetMap)+len(val))
					for k, v := range targetMap {
						merged[k] = v
					}
					for k, v := range val {
						if k == "$ref" {
							continue
						}
						merged[k] = resolveNode(v, root, visited)
					}
					return merged
				}
				return targetResolved
			}
			return val
		}
		out := make(map[string]any, len(val))
		for k, v := range val {
			out[k] = resolveNode(v, root, visited)
		}
		return out
	default:
		return node
	}
}

func resolveLocalPointer(root map[string]any, ref string) (any, bool) {
	if ref == "#" {
		return root, true
	}
	parts := strings.Split(strings.TrimPrefix(ref, "#/"), "/")
	var curr any = root
	for _, part := range parts {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		currMap, ok := curr.(map[string]any)
		if !ok {
			return nil, false
		}
		val, exists := currMap[part]
		if !exists {
			return nil, false
		}
		curr = val
	}
	return curr, true
}

func hasUnresolvedRef(node any, bucket string) bool {
	prefix := "#/" + bucket + "/"
	switch val := node.(type) {
	case []any:
		for _, v := range val {
			if hasUnresolvedRef(v, bucket) {
				return true
			}
		}
	case map[string]any:
		if ref, ok := val["$ref"].(string); ok && strings.HasPrefix(ref, prefix) {
			return true
		}
		for k, v := range val {
			if k == bucket {
				continue
			}
			if hasUnresolvedRef(v, bucket) {
				return true
			}
		}
	}
	return false
}

// normalizeTypes ensures all property nodes in a JSON Schema have an
// explicit valid 'type' field and repairs contradictory types (e.g. type: 'object'
// alongside enum: ["a", "b"] caused by MCP generator bugs). Anthropic, Moonshot
// (Kimi), and OpenAI Responses strict mode reject property schemas missing 'type'.
func normalizeTypes(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	normalizeNodeTypes(schema, false)
	return schema
}

func normalizeNodeTypes(node any, isPropertyDef bool) {
	m, ok := node.(map[string]any)
	if !ok || m == nil {
		return
	}

	if isPropertyDef {
		rawType, hasType := m["type"]
		typeStr, isStr := rawType.(string)
		if !hasType || !isStr || strings.TrimSpace(typeStr) == "" {
			inferred := inferTypeFromNode(m)
			if inferred != "" {
				m["type"] = inferred
			} else {
				m["type"] = "string"
			}
		} else {
			if enumVal, ok := m["enum"].([]any); ok && len(enumVal) > 0 {
				inferred := inferTypeFromEnumValues(enumVal)
				if inferred != "" && inferred != typeStr {
					m["type"] = inferred
					if inferred != "object" {
						delete(m, "properties")
						delete(m, "required")
					}
					if inferred != "array" {
						delete(m, "items")
					}
				}
			}
		}
	}

	if props, ok := m["properties"].(map[string]any); ok {
		for _, v := range props {
			normalizeNodeTypes(v, true)
		}
	}
	if patProps, ok := m["patternProperties"].(map[string]any); ok {
		for _, v := range patProps {
			normalizeNodeTypes(v, true)
		}
	}
	if items := m["items"]; items != nil {
		if itemMap, ok := items.(map[string]any); ok {
			normalizeNodeTypes(itemMap, false)
		} else if itemArr, ok := items.([]any); ok {
			for _, item := range itemArr {
				normalizeNodeTypes(item, false)
			}
		}
	}
	if allOf, ok := m["allOf"].([]any); ok {
		for _, sub := range allOf {
			normalizeNodeTypes(sub, isPropertyDef)
		}
	}
	if anyOf, ok := m["anyOf"].([]any); ok {
		for _, sub := range anyOf {
			normalizeNodeTypes(sub, isPropertyDef)
		}
	}
	if oneOf, ok := m["oneOf"].([]any); ok {
		for _, sub := range oneOf {
			normalizeNodeTypes(sub, isPropertyDef)
		}
	}
	if defs, ok := m["$defs"].(map[string]any); ok {
		for _, v := range defs {
			normalizeNodeTypes(v, false)
		}
	}
	if defs, ok := m["definitions"].(map[string]any); ok {
		for _, v := range defs {
			normalizeNodeTypes(v, false)
		}
	}
}

func inferTypeFromNode(m map[string]any) string {
	if enumVal, ok := m["enum"].([]any); ok && len(enumVal) > 0 {
		if t := inferTypeFromEnumValues(enumVal); t != "" {
			return t
		}
	}
	if constVal, ok := m["const"]; ok {
		if t := inferTypeFromValue(constVal); t != "" {
			return t
		}
	}
	if _, ok := m["properties"]; ok {
		return "object"
	}
	if _, ok := m["required"]; ok {
		return "object"
	}
	if _, ok := m["items"]; ok {
		return "array"
	}
	return ""
}

func inferTypeFromEnumValues(vals []any) string {
	var inferred string
	for _, v := range vals {
		t := inferTypeFromValue(v)
		if t == "" {
			continue
		}
		if inferred == "" {
			inferred = t
		} else if inferred != t {
			return "string"
		}
	}
	return inferred
}

func inferTypeFromValue(v any) string {
	switch v.(type) {
	case string:
		return "string"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, json.Number:
		return "number"
	case bool:
		return "boolean"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	default:
		return ""
	}
}

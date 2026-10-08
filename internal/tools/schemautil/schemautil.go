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
	"sort"
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
		// deref deep-copies the schema it is given, so cloning here as well
		// only copied every tool's schema twice per request.
		m = asMap
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

// topLevelComposites are the composition keywords the Anthropic Messages API
// refuses on a tool's input_schema root: a declaration that states its shape that
// way fails the whole request with 400 "tools.N.custom.input_schema: input_schema
// does not support oneOf, allOf, or anyOf at the top level", which leaves every
// tool of that session unusable. Only the top level is refused — a composite
// nested inside a property is accepted.
var topLevelComposites = []string{"oneOf", "anyOf", "allOf"}

// FlattenTopLevelComposites rewrites a schema whose root states its shape as a
// set of branches into a plain object schema and drops the branches: each
// branch's properties are merged into the root (a declaration already on the root
// wins, because a branch states a narrowing that only holds inside that branch),
// and `required` keeps what every alternative demands (oneOf/anyOf) or what any
// conjunct demands (allOf). A branch carrying neither properties nor required — a
// `not` guard, a scalar alternative — contributes nothing.
//
// Anthropic's Messages API needs this (see topLevelComposites); the Chat and
// Responses wire formats accept the composite forms as they are, so the repair
// lives at that one boundary instead of on the declarations themselves, which the
// argument gate still validates untouched. The result only ever *weakens* the
// root — it accepts a superset of what the root stated — so it can never refuse
// an argument the runtime accepts.
//
// The schema it is given is not mutated: the root, its properties and its
// required list are rebuilt, while the nested schemas are shared because they are
// only read.
func FlattenTopLevelComposites(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	hasComposite := false
	for _, key := range topLevelComposites {
		if _, ok := schema[key]; ok {
			hasComposite = true
			break
		}
	}
	if !hasComposite {
		return schema
	}
	var alternatives [][]any
	for _, key := range []string{"oneOf", "anyOf"} {
		if branches := branchList(schema[key]); branches != nil {
			alternatives = append(alternatives, branches)
		}
	}
	var conjuncts []any
	if branches := branchList(schema["allOf"]); branches != nil {
		conjuncts = branches
	}
	// No early return when both lists are empty: a composite keyword in a shape
	// branchList does not understand (a string, a number) is still refused by the
	// API, so it must fall through to the rebuild below that drops the keys.

	properties := map[string]any{}
	if declared, ok := schema["properties"].(map[string]any); ok {
		for name, property := range declared {
			properties[name] = property
		}
	}
	// The root's own list keeps its order: `required` serializes as an array, so
	// its bytes must not depend on map iteration — the tool declarations sit in
	// every request prefix, which providers cache by exact bytes.
	required := map[string]bool{}
	ordered := make([]string, 0, 8)
	for _, name := range StringSlice(schema["required"]) {
		if required[name] {
			continue
		}
		required[name] = true
		ordered = append(ordered, name)
	}
	add := func(names []string) {
		for _, name := range names {
			if required[name] {
				continue
			}
			required[name] = true
			ordered = append(ordered, name)
		}
	}

	for _, branches := range alternatives {
		var shared map[string]bool
		for index, branch := range branches {
			branchMap, _ := branch.(map[string]any)
			mergeBranchProperties(properties, branchMap)
			names := branchRequiredNames(branchMap)
			if index == 0 {
				shared = names
				continue
			}
			for name := range shared {
				if !names[name] {
					delete(shared, name)
				}
			}
		}
		add(sortedNames(shared))
	}
	for _, branch := range conjuncts {
		branchMap, _ := branch.(map[string]any)
		mergeBranchProperties(properties, branchMap)
		add(sortedNames(branchRequiredNames(branchMap)))
	}

	out := make(map[string]any, len(schema)+1)
	for key, value := range schema {
		out[key] = value
	}
	for _, key := range topLevelComposites {
		delete(out, key)
	}
	if len(properties) > 0 {
		out["properties"] = properties
	}
	if len(ordered) > 0 {
		out["required"] = ordered
	}
	return out
}

// branchList reads one composite keyword as a branch list. Generators
// occasionally emit a single object instead of a one-element array; treating it
// as no branches would let the keyword reach the Anthropic wire untouched and
// fail the whole request, so a lone branch counts as a one-branch list. Any
// other shape (a string, a number, nil) is not a branch list and is ignored —
// the keyword is still dropped from the flattened root.
func branchList(value any) []any {
	if branches, ok := value.([]any); ok {
		return branches
	}
	if branch, ok := value.(map[string]any); ok {
		return []any{branch}
	}
	return nil
}

// mergeBranchProperties adds the properties one composite branch declares to the
// flattened root. A name the root — or an earlier branch — already declares is
// kept as it is: a branch property narrows the same field inside that branch
// only, and carrying the narrowing over would apply it to the alternatives it
// does not belong to.
func mergeBranchProperties(target map[string]any, branch map[string]any) {
	declared, ok := branch["properties"].(map[string]any)
	if !ok {
		return
	}
	for name, property := range declared {
		if _, exists := target[name]; exists {
			continue
		}
		target[name] = property
	}
}

// branchRequiredNames reads one branch's `required` list as a set. A branch that
// is not an object schema (or carries no `required`) demands nothing.
func branchRequiredNames(branch map[string]any) map[string]bool {
	out := map[string]bool{}
	for _, name := range StringSlice(branch["required"]) {
		out[name] = true
	}
	return out
}

// sortedNames gives a name set a deterministic order, because the names it
// carries are appended to a serialized array (see FlattenTopLevelComposites).
func sortedNames(names map[string]bool) []string {
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
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
// It deep-copies the tree it is given, so callers may hand it their input
// directly (Map relies on this instead of cloning first).
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
		typeStr, hasUsableType := usableType(m)
		if !hasUsableType {
			if declaresOwnShape(m) {
				// The node already states its own shape (anyOf/oneOf/allOf or an
				// unresolved $ref); a scalar type beside it would intersect with
				// those alternatives.
			} else if inferred := inferTypeFromNode(m); inferred != "" {
				m["type"] = inferred
			} else {
				m["type"] = "string"
			}
		} else if enumVal, ok := m["enum"].([]any); ok && len(enumVal) > 0 {
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

	// The property-side repair above only covers direct property values. The
	// branches of oneOf/anyOf/not, array items and map schemas are walked without
	// isPropertyDef, so a node there could still carry object-only keywords while
	// never declaring itself an object — Gemini rejects the entire request over
	// one such node ("properties/required: only allowed for OBJECT type").
	stampObjectShape(m)

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
	// `not` and `additionalProperties` hold a schema for the same instance (the
	// map values, respectively) and are repaired like any other node: the
	// mutual-exclusion guards of edit's changes[] and http_request's body/json
	// live inside a `not`, and a node left unvisited there is exactly the node
	// stampObjectShape is meant to repair.
	if notSchema, ok := m["not"].(map[string]any); ok {
		normalizeNodeTypes(notSchema, false)
	}
	if additional, ok := m["additionalProperties"].(map[string]any); ok {
		normalizeNodeTypes(additional, false)
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

// objectOnlyKeywords are the JSON Schema keywords that only ever constrain an
// object instance. A node carrying one of them without a usable type is
// object-shaped in everything but name.
var objectOnlyKeywords = []string{"properties", "required", "patternProperties", "additionalProperties"}

// stampObjectShape gives a node that carries object-only keywords an explicit
// `type: "object"`. Validators that refuse those keywords on a non-OBJECT node
// (Gemini answers the whole request with "...properties[changes]...properties:
// only allowed for OBJECT type", which leaves no tool usable) otherwise reject
// the declaration over a node that never disagreed with them: the oneOf/anyOf
// branches and `not` sub-schemas of a mutual-exclusion rule only add constraints
// to an object. Nodes that already declare a type, and nodes whose shape is
// decided by an unresolved $ref, are left untouched; a type list such as
// ["object", "null"] states something of its own and must not be narrowed to a
// single type by a repair pass.
func stampObjectShape(m map[string]any) {
	switch raw := m["type"].(type) {
	case nil:
		// No type at all: exactly the hole this fills.
	case string:
		if strings.TrimSpace(raw) != "" {
			return
		}
	default:
		return
	}
	if ref, ok := m["$ref"].(string); ok && strings.TrimSpace(ref) != "" {
		return
	}
	for _, key := range objectOnlyKeywords {
		if _, ok := m[key]; ok {
			m["type"] = "object"
			return
		}
	}
}

// declaresOwnShape reports whether a node already states its own shape through a
// composite keyword (anyOf/oneOf/allOf) or an unresolved $ref. Such a node must
// keep that statement: stamping a scalar `type` beside it narrows the node to the
// intersection of the two, so `anyOf: [object, array, string]` reached the model
// as "just a string" and every non-string argument looked wrong.
func declaresOwnShape(m map[string]any) bool {
	for _, key := range []string{"anyOf", "oneOf", "allOf"} {
		if variants, ok := m[key].([]any); ok && len(variants) > 0 {
			return true
		}
	}
	ref, ok := m["$ref"].(string)
	return ok && strings.TrimSpace(ref) != ""
}

// usableType reports whether the node declares a usable type: a non-empty
// string. A missing, non-string, or blank type counts as absent.
func usableType(m map[string]any) (string, bool) {
	raw, ok := m["type"]
	if !ok {
		return "", false
	}
	str, ok := raw.(string)
	if !ok || strings.TrimSpace(str) == "" {
		return "", false
	}
	return str, true
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

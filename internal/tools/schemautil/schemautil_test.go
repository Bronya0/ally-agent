// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

package schemautil

import "testing"

func TestDerefInlinesLocalRefs(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"filter": map[string]any{
				"$ref":        "#/$defs/FilterType",
				"description": "override description",
			},
		},
		"$defs": map[string]any{
			"FilterType": map[string]any{
				"type": "string",
				"enum": []any{"all", "active"},
			},
		},
	}
	derefed := deref(schema)
	if _, hasDefs := derefed["$defs"]; hasDefs {
		t.Fatal("expected $defs to be cleaned up after full inlining")
	}
	props, ok := derefed["properties"].(map[string]any)
	if !ok {
		t.Fatal("properties missing")
	}
	filter, ok := props["filter"].(map[string]any)
	if !ok {
		t.Fatal("filter missing")
	}
	if filter["type"] != "string" {
		t.Fatalf("expected inlined type=string, got %v", filter["type"])
	}
	if filter["description"] != "override description" {
		t.Fatalf("sibling property override failed, got %v", filter["description"])
	}
}

func TestNormalizeTypesRepairsMissingAndContradictoryTypes(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"missing_type_enum_string": map[string]any{
				"enum": []any{"asc", "desc"},
			},
			"missing_type_enum_number": map[string]any{
				"enum": []any{1, 2, 3},
			},
			"contradictory_type": map[string]any{
				"type": "object",
				"enum": []any{"opt_a", "opt_b"},
				"properties": map[string]any{
					"bad": map[string]any{"type": "string"},
				},
			},
			"missing_type_object": map[string]any{
				"properties": map[string]any{
					"sub": map[string]any{"type": "string"},
				},
			},
			"missing_type_array": map[string]any{
				"items": map[string]any{"type": "string"},
			},
			"fallback_typeless": map[string]any{
				"description": "no info",
			},
		},
	}

	norm := normalizeTypes(schema)
	props := norm["properties"].(map[string]any)

	if props["missing_type_enum_string"].(map[string]any)["type"] != "string" {
		t.Fatalf("expected string type for enum strings, got %v", props["missing_type_enum_string"])
	}
	if props["missing_type_enum_number"].(map[string]any)["type"] != "number" {
		t.Fatalf("expected number type for enum numbers, got %v", props["missing_type_enum_number"])
	}
	contra := props["contradictory_type"].(map[string]any)
	if contra["type"] != "string" {
		t.Fatalf("expected contradictory object type to be repaired to string, got %v", contra["type"])
	}
	if _, hasProps := contra["properties"]; hasProps {
		t.Fatalf("expected properties to be deleted when repaired from object to string")
	}
	if props["missing_type_object"].(map[string]any)["type"] != "object" {
		t.Fatalf("expected object type inferred from properties")
	}
	if props["missing_type_array"].(map[string]any)["type"] != "array" {
		t.Fatalf("expected array type inferred from items")
	}
	if props["fallback_typeless"].(map[string]any)["type"] != "string" {
		t.Fatalf("expected fallback typeless property to be string")
	}
}

// TestMapHandlesAbsentParameters covers the nil case every adapter relies on:
// a tool declared without parameters must still serialize a valid object schema
// instead of omitting the required `parameters` key.
func TestMapHandlesAbsentParameters(t *testing.T) {
	got := Map(nil)
	if got["type"] != "object" {
		t.Fatalf("type = %v, want object", got["type"])
	}
	if _, ok := got["properties"].(map[string]any); !ok {
		t.Fatalf("properties = %#v, want an object", got["properties"])
	}

	// An explicitly empty schema is passed through as the permissive schema it
	// is — `{}` accepts anything. Providers that demand a root type are served
	// by their own converter (anthropicInputSchema sets type:"object" itself),
	// so Map must not invent one here.
	if got := Map(map[string]any{}); len(got) != 0 {
		t.Fatalf("an empty schema must pass through unchanged, got %#v", got)
	}
}

func TestMapDoesNotMutateInput(t *testing.T) {
	original := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"mode": map[string]any{"enum": []any{"a", "b"}},
		},
	}
	Map(original)
	if _, hasType := original["properties"].(map[string]any)["mode"].(map[string]any)["type"]; hasType {
		t.Fatal("Map must not repair the caller's schema in place")
	}
}

func TestStringSliceCoerces(t *testing.T) {
	if got := StringSlice([]any{"a", 1, "b"}); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("StringSlice = %v, want [a b]", got)
	}
	if got := StringSlice([]string{"x"}); len(got) != 1 || got[0] != "x" {
		t.Fatalf("StringSlice([]string) = %v", got)
	}
	if got := StringSlice(nil); got != nil {
		t.Fatalf("StringSlice(nil) = %v, want nil", got)
	}
}

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
// TestNormalizeTypesKeepsCompositeShapes pins the node that declares its own
// shape: an anyOf/oneOf property must not also receive a scalar type, because
// the two intersect. The built-in http_request.json parameter was sent to the
// model as `type: string` beside an anyOf that already allowed objects and
// arrays, which is what this guards.
func TestNormalizeTypesKeepsCompositeShapes(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"payload": map[string]any{
				"anyOf": []any{
					map[string]any{"type": "object"},
					map[string]any{"type": "array", "items": map[string]any{}},
					map[string]any{"type": "string"},
				},
			},
			"either": map[string]any{
				"oneOf": []any{map[string]any{"type": "integer"}, map[string]any{"type": "string"}},
			},
			"bounded": map[string]any{
				"allOf": []any{map[string]any{"type": "integer", "minimum": 0}},
			},
		},
	}

	norm := normalizeTypes(schema)
	props := norm["properties"].(map[string]any)
	for _, name := range []string{"payload", "either", "bounded"} {
		if _, hasType := props[name].(map[string]any)["type"]; hasType {
			t.Fatalf("%s declares its own shape and must not be given a scalar type, got %#v", name, props[name])
		}
	}
}

// TestStampObjectShapeCoversCompositionNodes pins the shape Gemini refuses:
// `properties`/`required` on a node whose type is not OBJECT rejects the whole
// tool declaration, and a mutual-exclusion rule lives exactly on such nodes —
// the oneOf branches of edit's changes[] / read's files[] and their `not`
// guards. The property-side repair never reached them, because composition
// branches and array items are walked without isPropertyDef.
func TestStampObjectShapeCoversCompositionNodes(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"changes": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":       "object",
					"required":   []string{"newText"},
					"properties": map[string]any{"newText": map[string]any{"type": "string"}},
					"oneOf": []any{
						map[string]any{
							"required":   []string{"oldText"},
							"properties": map[string]any{"oldText": map[string]any{"minLength": 1}},
							"not": map[string]any{
								"required":   []string{"lineRange"},
								"properties": map[string]any{"lineRange": map[string]any{"pattern": `^[1-9]$`}},
							},
						},
						map[string]any{"required": []string{"lineRange"}},
					},
				},
			},
		},
	}

	norm := normalizeTypes(schema)
	items := norm["properties"].(map[string]any)["changes"].(map[string]any)["items"].(map[string]any)
	if items["type"] != "object" {
		t.Fatalf("array items changed type: %#v", items["type"])
	}
	branches, ok := items["oneOf"].([]any)
	if !ok || len(branches) != 2 {
		t.Fatalf("oneOf branches = %#v, want two", items["oneOf"])
	}
	first := branches[0].(map[string]any)
	if first["type"] != "object" {
		t.Fatalf("a branch deciding on properties/required must declare type: object, got %#v", first["type"])
	}
	guard, ok := first["not"].(map[string]any)
	if !ok || guard["type"] != "object" {
		t.Fatalf("a not sub-schema carrying properties/required must declare type: object, got %#v", first["not"])
	}
	if second := branches[1].(map[string]any); second["type"] != "object" {
		t.Fatalf("a branch carrying only required must declare type: object, got %#v", second["type"])
	}
	if got := first["properties"].(map[string]any)["oldText"].(map[string]any)["type"]; got != "string" {
		t.Fatalf("branch property type = %#v, want the inferred string", got)
	}
}

// TestStampObjectShapeKeepsStatedShapes pins what the repair must not touch: a
// node that already declares a type, a node whose type is a list (a statement of
// its own — narrowing ["object","null"] to object would drop the null branch),
// and a node whose shape is only decided once a $ref is inlined.
func TestStampObjectShapeKeepsStatedShapes(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"files": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":       []any{"object", "null"},
					"required":   []string{"path"},
					"properties": map[string]any{"path": map[string]any{"type": "string"}},
				},
			},
			"referenced": map[string]any{
				"$ref":       "#/$defs/Cycle",
				"properties": map[string]any{"a": map[string]any{"type": "string"}},
			},
			"typed": map[string]any{"type": "string", "properties": map[string]any{}},
		},
	}

	norm := normalizeTypes(schema)
	props := norm["properties"].(map[string]any)
	items := props["files"].(map[string]any)["items"].(map[string]any)
	if _, ok := items["type"].([]any); !ok {
		t.Fatalf("a declared type list must survive the repair, got %#v", items["type"])
	}
	if got := props["referenced"].(map[string]any)["type"]; got != nil {
		t.Fatalf("a $ref node keeps its shape undecided, got %#v", got)
	}
	if got := props["typed"].(map[string]any)["type"]; got != "string" {
		t.Fatalf("stated type = %#v, want string", got)
	}
}

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

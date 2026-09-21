// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

package toolcall

import (
	"strings"
	"testing"
)

func TestForAnthropicSanitizes(t *testing.T) {
	dirtyID := "call:tool.run|123_456-abc"
	clean := ForAnthropic(dirtyID)
	if clean != "call_tool_run_123_456-abc" {
		t.Fatalf("sanitized ID = %q, want %q", clean, "call_tool_run_123_456-abc")
	}

	longID := strings.Repeat("a", 100)
	cleanLong := ForAnthropic(longID)
	if len(cleanLong) != 64 {
		t.Fatalf("expected 64 chars, got %d", len(cleanLong))
	}

	// Two distinct IDs sharing the same 70-char prefix must NOT collide after sanitization
	prefix := strings.Repeat("x", 70)
	cleanA := ForAnthropic(prefix + "_alpha")
	cleanB := ForAnthropic(prefix + "_bravo")
	if cleanA == cleanB {
		t.Fatalf("cleanA and cleanB must not collide: %q == %q", cleanA, cleanB)
	}
	if len(cleanA) != 64 || len(cleanB) != 64 {
		t.Fatalf("expected 64 chars for both, got %d and %d", len(cleanA), len(cleanB))
	}
}

func TestForResponsesCallSanitizes(t *testing.T) {
	// Compound call|item ID should take the first token
	if got := ForResponsesCall("call_12345|item_67890"); got != "call_12345" {
		t.Fatalf("expected call_12345, got %q", got)
	}
	// Safe chars replacement
	if got := ForResponsesCall("call:special!chars"); got != "call_special_chars" {
		t.Fatalf("expected call_special_chars, got %q", got)
	}
	// Length > 64 truncation with deterministic hash
	got := ForResponsesCall(strings.Repeat("a", 80))
	if len(got) != 64 {
		t.Fatalf("expected length 64, got %d (%q)", len(got), got)
	}
	if !strings.HasPrefix(got, strings.Repeat("a", 56)+"_") {
		t.Fatalf("expected prefix of 56 a's and underscore, got %q", got)
	}
	// Empty fallback
	if ForResponsesCall("") != "call_tool" {
		t.Fatal("expected call_tool on empty")
	}
	// A compound id whose first token is empty must not degrade to an empty id
	if ForResponsesCall("|item_1") != "call_tool" {
		t.Fatalf("expected call_tool for an empty first token, got %q", ForResponsesCall("|item_1"))
	}
}

func TestEffectiveFallsBackToTheFunctionName(t *testing.T) {
	if got := Effective("call_1", "grep"); got != "call_1" {
		t.Fatalf("Effective = %q, want the id", got)
	}
	if got := Effective("  ", "grep"); got != "call_grep" {
		t.Fatalf("Effective = %q, want call_grep", got)
	}
	if got := Effective("", ""); got != "call_unknown" {
		t.Fatalf("Effective = %q, want call_unknown", got)
	}
}

func TestIsSafeResponsesItemID(t *testing.T) {
	if !IsSafeResponsesItemID("fc_abc-123") {
		t.Fatal("a well-formed item id must be accepted")
	}
	for _, bad := range []string{"", "rs_1|rs_2", "rs 1", strings.Repeat("a", 65)} {
		if IsSafeResponsesItemID(bad) {
			t.Fatalf("%q must not be echoed back verbatim", bad)
		}
	}
}

func TestDecodeArgumentsEnsuresObject(t *testing.T) {
	// Arrays must be wrapped in map
	if _, ok := DecodeArguments(`[1, 2, 3]`)["_raw"]; !ok {
		t.Fatal("array arguments must be wrapped in map with _raw")
	}
	// Primitives must be wrapped in map
	if _, ok := DecodeArguments(`42`)["_raw"]; !ok {
		t.Fatal("number arguments must be wrapped in map with _raw")
	}
	// Objects must be preserved
	if got := DecodeArguments(`{"foo":"bar"}`); got["foo"] != "bar" {
		t.Fatalf("object arguments must be preserved, got %v", got)
	}
	// Empty string must return empty map
	if got := DecodeArguments(""); len(got) != 0 {
		t.Fatalf("empty arguments must return empty map, got %v", got)
	}
	// Malformed JSON is surfaced as _raw instead of being dropped
	if got := DecodeArguments(`{"foo":`); got["_raw"] != `{"foo":` {
		t.Fatalf("malformed arguments must be kept under _raw, got %v", got)
	}
}

func TestMergeRepeatedDelta(t *testing.T) {
	cases := []struct {
		name    string
		current string
		delta   string
		want    string
	}{
		{"first chunk", "", "http_request", "http_request"},
		{"exact re-send ignored", "http_request", "http_request", "http_request"},
		{"extended re-send replaces", "http_req", "http_request", "http_request"},
		{"progressive chunk appends", "http_", "request", "http_request"},
	}
	for _, tc := range cases {
		if got := MergeRepeatedDelta(tc.current, tc.delta); got != tc.want {
			t.Fatalf("%s: MergeRepeatedDelta(%q, %q) = %q, want %q", tc.name, tc.current, tc.delta, got, tc.want)
		}
	}
}

func TestTruncatedArgumentsMarkerRoundTrip(t *testing.T) {
	if got := RepairTruncatedArguments(`{"a":1}`); got != `{"a":1}` {
		t.Fatalf("valid arguments must pass through, got %q", got)
	}
	if got := RepairTruncatedArguments(""); got != "" {
		t.Fatalf("empty arguments must pass through, got %q", got)
	}
	repaired := RepairTruncatedArguments(`{"a":`)
	if repaired != TruncatedArgumentsMarker {
		t.Fatalf("truncated arguments = %q, want the marker", repaired)
	}
	if !IsTruncatedArguments(repaired) {
		t.Fatal("the repaired value must be recognised as truncated")
	}
	if IsTruncatedArguments(`{"a":1}`) {
		t.Fatal("valid arguments must not be recognised as truncated")
	}
}

func TestCleanArgumentsAndBOM(t *testing.T) {
	// BOM prefix stripped
	bomJSON := "\xef\xbb\xbf{\"command\":\"echo 1\"}"
	cleaned := CleanArguments(bomJSON)
	if cleaned != `{"command":"echo 1"}` {
		t.Fatalf("expected BOM stripped, got %q", cleaned)
	}
	if repaired := RepairTruncatedArguments(bomJSON); repaired != `{"command":"echo 1"}` {
		t.Fatalf("expected valid JSON after BOM stripped, got %q", repaired)
	}

	// null converted to {}
	if got := CleanArguments("null"); got != "{}" {
		t.Fatalf("CleanArguments(null) = %q, want {}", got)
	}
	if got := CleanArguments(" null \n"); got != "{}" {
		t.Fatalf("CleanArguments( null ) = %q, want {}", got)
	}
	if got := DecodeArguments("null"); len(got) != 0 {
		t.Fatalf("DecodeArguments(null) = %v, want empty map", got)
	}
	if got := DecodeArguments(bomJSON); got["command"] != "echo 1" {
		t.Fatalf("DecodeArguments with BOM failed: %v", got)
	}
}

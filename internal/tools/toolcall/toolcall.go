// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

// Package toolcall holds the provider-wire helpers for a tool call's identity
// and arguments: id normalization for the three supported protocols, argument
// decoding, and the truncation marker that keeps a half-streamed argument
// string out of the provider request and out of persisted history.
//
// These are the rules every adapter has to agree on. Before they lived here the
// same knowledge was spread across the protocol file (producer), the session
// sanitizer (repair on load) and the edit orchestration (refuse to execute), so
// a change to one copy could silently disagree with the others.
//
// Everything is a host-neutral pure function: no App state, no provider client,
// no config.
package toolcall

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
)

// ForAnthropic normalizes a tool call ID to conform to the Anthropic regex
// `^[a-zA-Z0-9_-]{1,64}$`. Non-matching characters are replaced with '_', and
// length is capped at 64 characters.
// If the sanitized ID exceeds 64 characters, it is truncated to 56 characters
// followed by an underscore and a 7-character deterministic hex hash of the full ID.
// This guarantees that tool_use and tool_result match identically while preventing
// collisions between distinct long IDs that share the same 64-char prefix.
func ForAnthropic(id string) string {
	return sanitize(id)
}

// ForResponsesCall normalizes a tool call ID to conform to the OpenAI Responses
// API schema (^[a-zA-Z0-9_-]{1,64}$). Compound IDs separated by '|' (such as
// call_id|item_id) are split to retain the primary call_id, and lengths > 64
// chars are truncated with a deterministic SHA256 suffix.
func ForResponsesCall(id string) string {
	id = strings.TrimSpace(id)
	if idx := strings.IndexByte(id, '|'); idx >= 0 {
		id = strings.TrimSpace(id[:idx])
		if id == "" {
			return "call_tool"
		}
	}
	return sanitize(id)
}

// Effective returns the id a provider request must reference for a tool call.
// A provider that streamed no id still needs one so the tool_use and the
// tool_result of the same call can be paired: the function name (itself folded
// by the caller) is the only stable handle available at that point.
func Effective(id, name string) string {
	if strings.TrimSpace(id) != "" {
		return id
	}
	if strings.TrimSpace(name) != "" {
		return "call_" + name
	}
	return "call_unknown"
}

// IsSafeResponsesItemID reports whether an OpenAI Responses item id can be
// echoed back verbatim. Item ids use the same charset as call ids; anything
// else (a gateway-specific or garbled id) is dropped rather than sent.
func IsSafeResponsesItemID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// DecodeArguments turns a streamed argument string into the object shape the
// Anthropic tool_use block requires. Non-object JSON is wrapped under `_raw`
// instead of being dropped, so a provider that streamed a valid-but-unusual
// payload still shows the model something recoverable.
func DecodeArguments(args string) map[string]any {
	args = strings.TrimPrefix(args, "\xef\xbb\xbf")
	args = strings.TrimSpace(args)
	if args == "" || args == "null" {
		return map[string]any{}
	}
	var decoded any
	if err := json.Unmarshal([]byte(args), &decoded); err == nil && decoded != nil {
		if m, ok := decoded.(map[string]any); ok {
			return m
		}
		return map[string]any{"_raw": decoded}
	}
	return map[string]any{"_raw": args}
}

// MergeRepeatedDelta merges one non-empty streamed id/name delta into its
// accumulated value. The OpenAI spec sends id and function name once in the
// first delta, but some relays re-send the full value in every delta; appending
// those verbatim produced names like "http_requesthttp_request..." that then
// failed dispatch with "unknown tool" and, once replayed, made providers that
// validate tool_calls reject every later request with 400.
// Exact re-sends are ignored, extended re-sends (delta starts with the
// accumulated value) replace it, and anything else is treated as a progressive
// chunk and appended.
func MergeRepeatedDelta(current, delta string) string {
	switch {
	case current == "":
		return delta
	case delta == current:
		return current
	case strings.HasPrefix(delta, current):
		return delta
	default:
		return current + delta
	}
}

// TruncatedArgumentsMarker replaces streamed tool-call arguments that were cut
// off before the stream completed, leaving invalid JSON. Some providers parse
// tool_calls[].function.arguments server-side and reject the whole request with
// 400 when the string is malformed, so a truncated prefix must never be
// replayed to the provider or persisted into history. The failed tool result
// already explains the truncation to the model.
const TruncatedArgumentsMarker = `{"allyTruncatedArguments":true}`

// CleanArguments strips UTF-8 BOM prefixes and normalizes "null" arguments to
// "{}". Empty strings are left untouched.
func CleanArguments(args string) string {
	args = strings.TrimPrefix(args, "\xef\xbb\xbf")
	trimmed := strings.TrimSpace(args)
	if trimmed == "null" {
		return "{}"
	}
	return args
}

// RepairTruncatedArguments returns args unchanged when they are valid JSON and
// the truncation marker otherwise. It repairs persisted history at load time;
// live streamed arguments are rewritten the same way by
// prepareToolCallsForExecution before execution.
func RepairTruncatedArguments(args string) string {
	args = CleanArguments(args)
	if args == "" || json.Valid([]byte(args)) {
		return args
	}
	return TruncatedArgumentsMarker
}

// IsTruncatedArguments reports whether args is the marker
// RepairTruncatedArguments substitutes. Such a tool call must not be executed
// and must not be kept in history — it poisons the session.
func IsTruncatedArguments(args string) bool {
	var v struct {
		AllyTruncatedArguments bool `json:"allyTruncatedArguments"`
	}
	return json.Unmarshal([]byte(args), &v) == nil && v.AllyTruncatedArguments
}

// sanitize is the shared core of the two provider id rules: keep the
// [a-zA-Z0-9_-] subset, substitute everything else, and cap the result at 64
// characters with a hash suffix that keeps distinct long ids distinct.
func sanitize(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "call_tool"
	}
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	s := b.String()
	if len(s) > 64 {
		h := fmt.Sprintf("%x", sha256.Sum256([]byte(id)))
		s = s[:56] + "_" + h[:7]
	}
	return s
}

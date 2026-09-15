// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3.0. See the LICENSE file for details.
package app

import (
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestRepairDanglingToolCalls(t *testing.T) {
	assistantWithCalls := func(ids ...string) openai.ChatCompletionMessage {
		calls := make([]openai.ToolCall, 0, len(ids))
		for _, id := range ids {
			calls = append(calls, openai.ToolCall{ID: id, Type: openai.ToolTypeFunction, Function: openai.FunctionCall{Name: "read", Arguments: "{}"}})
		}
		return openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, ToolCalls: calls}
	}
	toolMsg := func(id string) openai.ChatCompletionMessage {
		return openai.ChatCompletionMessage{Role: openai.ChatMessageRoleTool, ToolCallID: id, Content: `{"ok":true}`}
	}
	userMsg := openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: "next"}

	t.Run("valid pairing passes through unchanged", func(t *testing.T) {
		in := []openai.ChatCompletionMessage{
			userMsg,
			assistantWithCalls("a", "b"),
			toolMsg("a"), toolMsg("b"),
			{Role: openai.ChatMessageRoleAssistant, Content: "done"},
		}
		out := repairDanglingToolCalls(in)
		if len(out) != 5 || len(out[1].ToolCalls) != 2 || out[2].ToolCallID != "a" || out[3].ToolCallID != "b" {
			t.Fatalf("valid pairing was modified: %#v", out)
		}
	})

	t.Run("trailing dangling call stripped, answered call kept", func(t *testing.T) {
		// The exact state a mid-batch panic would persist: assistant declared
		// two calls, only one result arrived before the crash.
		in := []openai.ChatCompletionMessage{
			userMsg,
			assistantWithCalls("a", "b"),
			toolMsg("a"),
		}
		out := repairDanglingToolCalls(in)
		if len(out) != 3 {
			t.Fatalf("expected 3 messages, got %d: %#v", len(out), out)
		}
		if len(out[1].ToolCalls) != 1 || out[1].ToolCalls[0].ID != "a" {
			t.Fatalf("dangling call b was not stripped: %#v", out[1].ToolCalls)
		}
		if out[2].ToolCallID != "a" {
			t.Fatalf("answered result must survive: %#v", out[2])
		}
	})

	t.Run("mid-history dangling closed by user message", func(t *testing.T) {
		in := []openai.ChatCompletionMessage{
			userMsg,
			assistantWithCalls("a"),
			userMsg,
			{Role: openai.ChatMessageRoleAssistant, Content: "after"},
		}
		out := repairDanglingToolCalls(in)
		if len(out) != 3 {
			t.Fatalf("expected 3 messages, got %d: %#v", len(out), out)
		}
		if len(out[1].ToolCalls) != 0 {
			t.Fatalf("dangling call a must be stripped when the turn closes: %#v", out[1].ToolCalls)
		}
	})

	t.Run("assistant with all calls dangling and no text dropped", func(t *testing.T) {
		in := []openai.ChatCompletionMessage{
			userMsg,
			assistantWithCalls("a"),
			toolMsg("a"),
			assistantWithCalls("b"),
		}
		out := repairDanglingToolCalls(in)
		if len(out) != 3 {
			t.Fatalf("empty dangling assistant must be dropped, got %d: %#v", len(out), out)
		}
	})

	t.Run("assistant text preserved when calls dangle", func(t *testing.T) {
		in := []openai.ChatCompletionMessage{
			userMsg,
			{Role: openai.ChatMessageRoleAssistant, Content: "partial stream", ToolCalls: assistantWithCalls("a").ToolCalls},
		}
		out := repairDanglingToolCalls(in)
		if len(out) != 2 || out[1].Content != "partial stream" || len(out[1].ToolCalls) != 0 {
			t.Fatalf("assistant text must survive call stripping: %#v", out)
		}
	})

	t.Run("orphan and duplicate tool messages dropped", func(t *testing.T) {
		in := []openai.ChatCompletionMessage{
			userMsg,
			assistantWithCalls("a"),
			toolMsg("ghost"),
			toolMsg("a"),
			toolMsg("a"),
		}
		out := repairDanglingToolCalls(in)
		if len(out) != 3 {
			t.Fatalf("orphan and duplicate results must be dropped, got %d: %#v", len(out), out)
		}
		if out[2].ToolCallID != "a" {
			t.Fatalf("first result must survive: %#v", out[2])
		}
	})

	t.Run("duplicate call IDs are repaired instead of left dangling", func(t *testing.T) {
		// A relay can return two calls sharing one ID. The old ID-keyed table
		// consumed its entry on the first result, dropped the second result as a
		// duplicate and then had nothing left to strip — the unanswered call stayed
		// in the history and every later request was rejected with 400.
		in := []openai.ChatCompletionMessage{
			userMsg,
			assistantWithCalls("dup", "dup"),
			toolMsg("dup"),
			toolMsg("dup"),
			{Role: openai.ChatMessageRoleAssistant, Content: "done"},
		}
		out := repairDanglingToolCalls(in)
		if len(out) != 5 || len(out[1].ToolCalls) != 2 {
			t.Fatalf("two answered calls must both survive: %#v", out)
		}

		// Only one result for two same-ID calls: the second call has to be
		// stripped so nothing is left unanswered.
		out = repairDanglingToolCalls(in[:3])
		if len(out) != 3 {
			t.Fatalf("expected 3 messages, got %d: %#v", len(out), out)
		}
		if len(out[1].ToolCalls) != 1 || out[1].ToolCalls[0].ID != "dup" {
			t.Fatalf("the unanswered duplicate call must be stripped: %#v", out[1].ToolCalls)
		}
		if out[2].ToolCallID != "dup" {
			t.Fatalf("the answered result must survive: %#v", out[2])
		}
	})
}

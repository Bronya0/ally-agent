// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func contextLockMessages() []openai.ChatCompletionMessage {
	return []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: "frozen system prompt"},
		{Role: openai.ChatMessageRoleUser, Content: "lay out the plan"},
		{Role: openai.ChatMessageRoleAssistant, Content: "done"},
	}
}

func contextLockTools() []openai.Tool {
	return []openai.Tool{{
		Type:     openai.ToolTypeFunction,
		Function: &openai.FunctionDefinition{Name: "read"},
	}}
}

// TestContextLockReportsDriftAndAcceptsAppends 锁定这套机制的全部判定：首次只记基线、
// 追加放行、改中间一条要报出下标、改头部报 head、变短报 removed、换模型重开基线、
// 工具清单变化算头部变化，而只改顶层请求参数的配置切换一概不算。
func TestContextLockReportsDriftAndAcceptsAppends(t *testing.T) {
	app := NewApp()
	cfg := ConfigState{Model: "model-a", APIFormat: apiFormatOpenAIChat}
	tools := contextLockTools()
	messages := contextLockMessages()

	if verdict := app.noteRequestPrefix("lock-session", contextLockLaneChat, cfg, messages, tools); verdict.kind != "" {
		t.Fatalf("the first request must only record a baseline, got %#v", verdict)
	}
	appended := append(append([]openai.ChatCompletionMessage{}, messages...),
		openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: "and add tests"})
	if verdict := app.noteRequestPrefix("lock-session", contextLockLaneChat, cfg, appended, tools); verdict.kind != "" {
		t.Fatalf("appending a turn must not report drift, got %#v", verdict)
	}

	edited := append([]openai.ChatCompletionMessage{}, appended...)
	edited[1].Content = "lay out the plan, please"
	verdict := app.noteRequestPrefix("lock-session", contextLockLaneChat, cfg, edited, tools)
	if verdict.kind != "message" || verdict.index != 0 {
		t.Fatalf("editing a message must report its index, got %#v", verdict)
	}

	headEdited := append([]openai.ChatCompletionMessage{}, edited...)
	headEdited[0].Content = "frozen system prompt v2"
	if verdict := app.noteRequestPrefix("lock-session", contextLockLaneChat, cfg, headEdited, tools); verdict.kind != "head" {
		t.Fatalf("editing the system block must report head drift, got %#v", verdict)
	}

	if verdict := app.noteRequestPrefix("lock-session", contextLockLaneChat, cfg, headEdited[:2], tools); verdict.kind != "removed" || verdict.index != 1 {
		t.Fatalf("a shorter history must report removed at the first missing index, got %#v", verdict)
	}

	// 换模型 = 换命名空间：重开基线，不报漂移。
	if verdict := app.noteRequestPrefix("lock-session", contextLockLaneChat, ConfigState{Model: "model-b", APIFormat: apiFormatOpenAIChat}, headEdited[:2], tools); verdict.kind != "" {
		t.Fatalf("switching the model must reopen the baseline, got %#v", verdict)
	}

	// 工具清单变化：头部哈希变。
	if verdict := app.noteRequestPrefix("lock-session", contextLockLaneChat, cfg, headEdited[:2], nil); verdict.kind != "head" {
		t.Fatalf("a changed tool list must report head drift, got %#v", verdict)
	}

	// 只改顶层请求参数的配置切换不构成漂移：指纹只量上下文本身，这些值动不到它一个字节。
	// （回归：思考强度 low→high 曾在这里假报过一次"缓存已重建"。）
	offCfg := ConfigState{Model: "model-a", APIFormat: apiFormatOpenAIChat, ReasoningEffort: reasoningEffortOff}
	if verdict := app.noteRequestPrefix("lock-session", contextLockLaneChat, offCfg, headEdited[:2], nil); verdict.kind != "" {
		t.Fatalf("a thinking-level change must not report drift, got %#v", verdict)
	}
	maxCfg := ConfigState{Model: "model-a", APIFormat: apiFormatOpenAIChat, MaxTokens: 8192}
	if verdict := app.noteRequestPrefix("lock-session", contextLockLaneChat, maxCfg, headEdited[:2], nil); verdict.kind != "" {
		t.Fatalf("a maxTokens change must not report drift, got %#v", verdict)
	}
}

// TestContextLockScopesByLane 锁定泳道隔离：另一条泳道的第一次请求不该被主对话的
// 基线判成漂移（子代理/压缩的前缀本来就是另一份）。
func TestContextLockScopesByLane(t *testing.T) {
	app := NewApp()
	cfg := ConfigState{Model: "model-a", APIFormat: apiFormatOpenAIChat}
	messages := contextLockMessages()

	if verdict := app.noteRequestPrefix("lock-lane", contextLockLaneChat, cfg, messages, nil); verdict.kind != "" {
		t.Fatalf("baseline must start empty, got %#v", verdict)
	}
	target := app.sessionContextLockFor("lock-lane")
	if target == nil || len(target.entries) != 1 {
		t.Fatalf("expected exactly one lane entry, got %#v", target)
	}
}

// TestContextLockFramingStaysUnambiguous 锁定长度前缀：把同样的字节切成 [ab][c] 与
// [a][bc]，两种切分必须得到不同的头部哈希，否则"哪几条被改过"就分不清了。
func TestContextLockFramingStaysUnambiguous(t *testing.T) {
	_, single := fingerprintRequest([]openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: "ab"},
	}, nil)
	_, split := fingerprintRequest([]openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: "a"},
		{Role: openai.ChatMessageRoleSystem, Content: "b"},
	}, nil)
	if single.head == split.head {
		t.Fatal("different segmentations that concatenate to the same bytes must not share a head hash")
	}
}

// TestContextLockDropsBaselineOnHistoryRewrite 锁定与读缓存同生同灭：历史被改写后
// 基线必须消失，下一次请求重新记基线，而不是把合法改写报成漂移。
func TestContextLockDropsBaselineOnHistoryRewrite(t *testing.T) {
	app := NewApp()
	cfg := ConfigState{Model: "model-a", APIFormat: apiFormatOpenAIChat}
	messages := contextLockMessages()

	app.noteRequestPrefix("lock-rewrite", contextLockLaneChat, cfg, messages, nil)
	if lock := app.sessionContextLockFor("lock-rewrite"); lock == nil || len(lock.entries) == 0 {
		t.Fatal("expected a recorded baseline before the rewrite")
	}

	app.invalidateSessionReadCache("lock-rewrite")
	if lock, ok := app.contextLocks["lock-rewrite"]; ok && len(lock.entries) > 0 {
		t.Fatalf("a history rewrite must drop the prefix baseline, got %#v", lock.entries)
	}

	// 改写后的第一条请求应当只是记基线，不报漂移。
	shorter := messages[:2]
	if verdict := app.noteRequestPrefix("lock-rewrite", contextLockLaneChat, cfg, shorter, nil); verdict.kind != "" {
		t.Fatalf("the first request after a rewrite must only record, got %#v", verdict)
	}
}

// TestContextLockReportsDriftToFrontend 锁定漂移会同时报给前端：只提示、不拦请求，
// 所以事件里带全定位信息，靠前端决定怎么显示（App.vue 的 context:drift）。
func TestContextLockReportsDriftToFrontend(t *testing.T) {
	app := NewApp()
	sink := &captureEventSink{}
	app.events = sink
	cfg := ConfigState{Model: "model-a", APIFormat: apiFormatOpenAIChat}
	messages := contextLockMessages()

	app.noteRequestPrefix("lock-emit", contextLockLaneChat, cfg, messages, nil)
	if sink.count != 0 {
		t.Fatalf("recording a baseline must not emit anything, got %d events", sink.count)
	}

	edited := append([]openai.ChatCompletionMessage{}, messages...)
	edited[1].Content = "lay out the plan, please"
	app.noteRequestPrefix("lock-emit", contextLockLaneChat, cfg, edited, nil)

	if sink.count != 1 || sink.name != "context:drift" {
		t.Fatalf("expected exactly one context:drift event, got count=%d name=%q", sink.count, sink.name)
	}
	payload, ok := sink.payload.(map[string]any)
	if !ok {
		t.Fatalf("unexpected drift payload type: %#v", sink.payload)
	}
	if payload["sessionId"] != "lock-emit" || payload["kind"] != "message" || payload["index"] != 0 || payload["at"] != "user" {
		t.Fatalf("unexpected drift payload: %#v", payload)
	}
}

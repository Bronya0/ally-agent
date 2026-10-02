// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General Public
// License v3. See the LICENSE file for details.
package app

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestEmitThrottleWindowingAndForce locks the shared throttle primitive: the
// window is empty on first use, small payloads bypass it, force re-bases the
// window, and an event with no entry in eventCadenceTable is never throttled.
func TestEmitThrottleWindowingAndForce(t *testing.T) {
	cadence := emitCadence{interval: 100 * time.Millisecond, byteThreshold: 2048}
	base := time.Now()
	throttle := &emitThrottle{}

	if !throttle.allow(base, cadence, 4096) {
		t.Fatal("first emission must pass (empty window)")
	}
	if throttle.allow(base.Add(50*time.Millisecond), cadence, 4096) {
		t.Fatal("a large payload inside the window must be dropped")
	}
	if !throttle.wouldAllow(base.Add(60*time.Millisecond), cadence, 1024) {
		t.Fatal("a payload below the byte threshold must bypass the window")
	}
	if throttle.wouldAllow(base.Add(70*time.Millisecond), cadence, 4096) {
		t.Fatal("wouldAllow must not re-base the window")
	}
	if !throttle.allow(base.Add(150*time.Millisecond), cadence, 4096) {
		t.Fatal("emission after the window must pass")
	}

	throttle.force(base.Add(time.Second))
	if throttle.wouldAllow(base.Add(time.Second), cadence, 4096) {
		t.Fatal("force must re-base the window from the forced instant")
	}
	if !throttle.wouldAllow(base.Add(time.Second+200*time.Millisecond), cadence, 4096) {
		t.Fatal("emission after the forced instant + window must pass")
	}

	unregistered := cadenceForEvent("no:such:event")
	if unregistered.interval != 0 || unregistered.byteThreshold != 0 {
		t.Fatalf("an unregistered event must have a zero cadence, got %#v", unregistered)
	}
}

// TestStreamingTextEventsRegisterBothBuckets 锁定成对登记：流式文本事件要在
// streamingTextEvents 里登记，正文档与思考桶一起生成。压缩流曾经只发了正文、思考
// 增量静默无节流，这条护栏就是为了不再出现下一个漏登记的流。
func TestStreamingTextEventsRegisterBothBuckets(t *testing.T) {
	for _, event := range streamingTextEvents {
		content, ok := eventCadenceTable[event]
		if !ok {
			t.Errorf("%s 没有登记正文档：它的增量不会被节流", event)
		} else if content.interval != runStreamInterval {
			t.Errorf("%s 的正文档 = %#v，期望间隔 %s", event, content, runStreamInterval)
		}
		reasoning, ok := eventCadenceTable[reasoningBucketFor(event)]
		if !ok {
			t.Errorf("%s 没有登记思考桶：思考增量会静默地全量发出", event)
		} else if reasoning.interval != streamReasoningInterval {
			t.Errorf("%s 的思考桶 = %#v，期望间隔 %s", event, reasoning, streamReasoningInterval)
		}
	}
	// 反向：不允许孤立的思考桶（删事件时忘了删桶一样是漂移）。
	for name := range eventCadenceTable {
		if !strings.HasSuffix(name, reasoningBucketSuffix) {
			continue
		}
		if base := strings.TrimSuffix(name, reasoningBucketSuffix); eventCadenceTable[base].interval == 0 {
			t.Errorf("%s 是孤立的思考桶：它的事件 %s 已经不在表里", name, base)
		}
	}
}

// TestCarriedReasoningRestartsItsWindow 锁定窗口起点：思考计数被一次正文事件顺手
// 带走后，思考桶要从这一刻重新计时。否则它停在零值窗口上，会被每一条纯思考增量
// 提前放行（长思考时事件量又回到正文那一档）。
func TestCarriedReasoningRestartsItsWindow(t *testing.T) {
	emitted := 0
	emitter := newRunStreamDeltaEmitter("run-1", "session-1", func(string, map[string]any) { emitted++ })
	emitter.add("body", "think") // 正文窗口初始为空 → 立即发出，并带走思考计数
	if emitted != 1 {
		t.Fatalf("首个正文增量必须立即发出，got %d 条事件", emitted)
	}
	emitter.add("", "more") // 思考窗口从刚才那次发出起算 → 必须攒住
	if emitted != 1 {
		t.Fatalf("思考计数被带走后必须从那一刻重新计时，got %d 条事件", emitted)
	}
	emitter.flush()
	if emitted != 2 {
		t.Fatalf("收尾必须把攒住的思考计数发出去，got %d 条事件", emitted)
	}
}

// TestCompactDeltaEmitterCoalescesThinking 锁定压缩流的事件形状：与主对话共用
// textDeltaCoalescer（首字节立即、窗口内合并、flush 收尾），思考只上报字符数而不是
// 正文，sessionId 必须保留（前端靠它把增量路由回会话的压缩气泡）。
func TestCompactDeltaEmitterCoalescesThinking(t *testing.T) {
	type captured struct {
		name         string
		sessionID    string
		reasoningLen int
		content      string
		hasBody      bool
	}
	var got []captured
	emitter := newCompactDeltaEmitter("session-9", func(name string, payload map[string]any) {
		item := captured{name: name, sessionID: fmt.Sprint(payload["sessionId"])}
		if v, ok := payload["reasoningLen"].(int); ok {
			item.reasoningLen = v
		}
		if v, ok := payload["content"].(string); ok {
			item.content = v
		}
		_, item.hasBody = payload["reasoning"]
		got = append(got, item)
	})

	emitter.add("", "think")
	if len(got) != 1 {
		t.Fatalf("the first thinking delta must be emitted immediately, got %d: %#v", len(got), got)
	}
	emitter.add("body", "more")
	if len(got) != 2 {
		t.Fatalf("the first content delta must be emitted immediately, got %d: %#v", len(got), got)
	}
	// 两个窗口都刚用过：这一拍的增量必须攒住。
	emitter.add("tail", "deeper")
	if len(got) != 2 {
		t.Fatalf("deltas inside the windows must be coalesced, got %d: %#v", len(got), got)
	}

	emitter.flush()
	if len(got) != 3 {
		t.Fatalf("flush must emit the pending tail, got %d: %#v", len(got), got)
	}
	merged := got[2]
	if merged.name != compactDeltaEvent || merged.sessionID != "session-9" {
		t.Fatalf("unexpected event identity: %#v", merged)
	}
	if merged.reasoningLen != len("deeper") || merged.content != "tail" {
		t.Fatalf("unexpected merged payload: %#v", merged)
	}
	if merged.hasBody {
		t.Fatal("the thinking body must not be sent; only its length")
	}

	emitter.flush()
	if len(got) != 3 {
		t.Fatalf("flushing with nothing pending must emit nothing, got %d", len(got))
	}
}

// TestThinkingBucketIsWiderThanContent 锁定思考与正文分桶：思考窗口比正文宽，
// 且只带思考的增量不被正文窗口提前带走（长思考是事件量大头，这正是分桶的理由）。
func TestThinkingBucketIsWiderThanContent(t *testing.T) {
	contentCadence := cadenceForEvent(runStreamEvent)
	reasoningCadence := cadenceForEvent(reasoningBucketFor(runStreamEvent))
	if contentCadence.interval != runStreamInterval || reasoningCadence.interval != streamReasoningInterval {
		t.Fatalf("unexpected cadences: content=%#v reasoning=%#v", contentCadence, reasoningCadence)
	}
	if reasoningCadence.interval <= contentCadence.interval {
		t.Fatal("the thinking bucket must be wider than the content bucket")
	}

	emitted := 0
	emitter := newRunStreamDeltaEmitter("run-1", "session-1", func(string, map[string]any) { emitted++ })
	// 两个窗口都刚被用过：只带思考的增量应该被思考窗口拦住。
	emitter.reasoningThrottle.force(time.Now())
	emitter.contentThrottle.force(time.Now().Add(-runStreamInterval))
	emitter.addReasoning("x")
	if emitted != 0 {
		t.Fatalf("a thinking-only delta inside the thinking window must be buffered, got %d events", emitted)
	}
	// 正文窗口已过：正文增量照常发出，并带回锿着的思考计数。
	emitter.addContent("y")
	if emitted != 1 {
		t.Fatalf("a content delta past the content window must be emitted, got %d events", emitted)
	}
	// 再攒一个思考增量，收尾时必须被 flush 清空。
	emitter.addReasoning("z")
	if emitted != 1 {
		t.Fatalf("a thinking delta inside its window must stay buffered, got %d events", emitted)
	}
	emitter.flush()
	if emitted != 2 {
		t.Fatalf("flush must drain the buffered thinking count, got %d events", emitted)
	}
}

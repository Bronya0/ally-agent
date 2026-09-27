// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"hash/fnv"
	"strconv"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

type toolCallProgressEvent struct {
	Name    string
	Payload map[string]any
}

// textDeltaCoalescer 把一个流式文本响应的增量合并成周期事件，档位取自
// eventCadenceTable（见 infra_emit.go）。主对话（run:stream）与压缩总结
// （compact:delta）共用它。
//
// 正文与思考各有一个节流桶：正文要跟得上打字机效果（64ms），思考只驱动一个
// "思考中"标签与估算 token 数，却常常是长回合里事件量的大头，所以它走自己的
// 宽窗口（200ms）。两者互不拖累：只带思考的增量按思考窗口发，带正文的增量按
// 正文窗口发，任一个桶到点就发一条、把当时攒下的两部分一起带走。
//
// 窗口起点是"这个桶上一次真的被发出去"的时刻：被顺手带走的桶也从这一刻重新
// 计时（见 emitPending），否则一个总被别人带走的桶会永远停在零值窗口上。
//
// 思考只上报字符数、不上报正文：界面只显示一个"思考中"标签与估算 token 数，
// 传全文是纯 IPC 浪费。计数用 len()（UTF-8 字节数）而不是 rune 数——前端按
// "计数 / 3"估 token，对 ASCII 正好，对 CJK 也落在合理区间；换成 rune 数会让
// 中文思考的估算低到三分之一。
type textDeltaCoalescer struct {
	event             string
	contentCadence    emitCadence
	reasoningCadence  emitCadence
	contentThrottle   emitThrottle
	reasoningThrottle emitThrottle
	content           strings.Builder
	reasoning         int
	payload           func(content string, reasoningLen int) map[string]any
	emit              func(string, map[string]any)
}

// newTextDeltaCoalescer 组装一个合并器：正文取事件名那一档，思考取它的思考桶
// （见 infra_emit.go 的 reasoningBucketFor）。
func newTextDeltaCoalescer(event string, payload func(content string, reasoningLen int) map[string]any, emit func(string, map[string]any)) *textDeltaCoalescer {
	return &textDeltaCoalescer{
		event:            event,
		contentCadence:   cadenceForEvent(event),
		reasoningCadence: cadenceForEvent(reasoningBucketFor(event)),
		payload:          payload,
		emit:             emit,
	}
}

// newRunStreamDeltaEmitter 构造主对话的合并流发射器。
func newRunStreamDeltaEmitter(runID, sessionID string, emit func(string, map[string]any)) *textDeltaCoalescer {
	return newTextDeltaCoalescer(runStreamEvent, func(content string, reasoningLen int) map[string]any {
		out := map[string]any{"runId": runID, "sessionId": sessionID}
		if reasoningLen > 0 {
			out["reasoningLen"] = reasoningLen
		}
		if content != "" {
			out["content"] = content
		}
		return out
	}, emit)
}

// newCompactDeltaEmitter 构造压缩总结的流式发射器：与主对话同组档位。压缩原先
// 每个增量发一条事件、且带完整思考正文，是唯一没有节流的流式路径。
func newCompactDeltaEmitter(sessionID string, emit func(string, map[string]any)) *textDeltaCoalescer {
	return newTextDeltaCoalescer(compactDeltaEvent, func(content string, reasoningLen int) map[string]any {
		out := map[string]any{"sessionId": sessionID}
		if reasoningLen > 0 {
			out["reasoningLen"] = reasoningLen
		}
		if content != "" {
			out["content"] = content
		}
		return out
	}, emit)
}

func (e *textDeltaCoalescer) addContent(delta string) {
	e.add(delta, "")
}

func (e *textDeltaCoalescer) addReasoning(delta string) {
	e.add("", delta)
}

// add 接收一次流式增量（正文与思考可能同时到达），任一个桶到点就发一条合并事件。
func (e *textDeltaCoalescer) add(contentDelta, reasoningDelta string) {
	if e == nil || e.emit == nil || (contentDelta == "" && reasoningDelta == "") {
		return
	}
	if contentDelta != "" {
		e.content.WriteString(contentDelta)
	}
	if reasoningDelta != "" {
		e.reasoning += len(reasoningDelta)
	}
	now := time.Now()
	// 只有真正有待发内容的那一部分参与判窗：空桶不参与，否则它会把另一部分提前
	// 带走（"首个增量立即发出"靠的就是各自窗口初始为空）。
	due := false
	if e.content.Len() > 0 && e.contentThrottle.allow(now, e.contentCadence, 0) {
		due = true
	}
	if !due && e.reasoning > 0 && e.reasoningThrottle.allow(now, e.reasoningCadence, 0) {
		due = true
	}
	if due {
		e.emitPending(now)
	}
}

// flush 无视窗口收尾：流结束、出错、被取消时必须调用，否则尾巴留在窗口里。
// 没有待发内容时它什么也不做，所以可以无条件调用。
func (e *textDeltaCoalescer) flush() {
	if e == nil || e.emit == nil || (e.content.Len() == 0 && e.reasoning == 0) {
		return
	}
	e.emitPending(time.Now())
}

// emitPending 发出一条合并事件并清空缓冲，并让被这一次带走的桶从 now 重新计时：
// 一个桶的窗口起点只能是"它上一次真的被发出去"的时刻，否则只靠另一个桶触发的
// 那些事件会让它迟迟得不到重新计时（零值窗口 = 每次都放行）。
func (e *textDeltaCoalescer) emitPending(now time.Time) {
	content := e.content.String()
	reasoningLen := e.reasoning
	if content == "" && reasoningLen == 0 {
		return
	}
	e.content.Reset()
	e.reasoning = 0
	if content != "" {
		e.contentThrottle.force(now)
	}
	if reasoningLen > 0 {
		e.reasoningThrottle.force(now)
	}
	e.emit(e.event, e.payload(content, reasoningLen))
}

// modelToolCallEventGate prevents provider adapters from cloning and
// forwarding the full accumulated tool-call slice for every tiny argument
// delta. The final complete arguments are still emitted by runChat through
// forceEvents after the provider returns. 节流交给 emitThrottle + tool:update
// 档：小参数一律放行（节流它们只会让卡片看起来卡住），大参数按窗口限制。
type modelToolCallEventGate struct {
	forward  func(modelStreamEvent)
	throttle emitThrottle
}

func newModelToolCallEventGate(forward func(modelStreamEvent)) *modelToolCallEventGate {
	return &modelToolCallEventGate{forward: forward}
}

func (g *modelToolCallEventGate) emit(event modelStreamEvent) {
	if g == nil || g.forward == nil {
		return
	}
	if len(event.ToolCalls) > 0 {
		maxArgs := 0
		for _, call := range event.ToolCalls {
			if len(call.Function.Arguments) > maxArgs {
				maxArgs = len(call.Function.Arguments)
			}
		}
		if !g.throttle.allow(time.Now(), cadenceForEvent(toolUpdateEvent), maxArgs) {
			return
		}
	}
	g.forward(event)
}

type toolCallProgressTracker struct {
	started   map[int]bool
	lastState map[int]string
	// throttle 按 tool-call index 记账：同一批里每个调用的参数流各走各的窗口。
	throttle map[int]*emitThrottle
}

func newToolCallProgressTracker() *toolCallProgressTracker {
	return &toolCallProgressTracker{
		started:   map[int]bool{},
		lastState: map[int]string{},
		throttle:  map[int]*emitThrottle{},
	}
}

func (t *toolCallProgressTracker) throttleFor(idx int) *emitThrottle {
	th := t.throttle[idx]
	if th == nil {
		th = &emitThrottle{}
		t.throttle[idx] = th
	}
	return th
}

// events 上报流式过程中的增量（受节流窗口限制）；最终状态用 forceEvents。
func (t *toolCallProgressTracker) events(runID, sessionID, batchID string, toolCalls []openai.ToolCall, metaForName func(string) map[string]any) []toolCallProgressEvent {
	return t.eventsWithForce(runID, sessionID, batchID, toolCalls, metaForName, false)
}

// forceEvents emits the current state ignoring the update throttle. It is used
// for the final emit after streaming completes so the frontend always receives
// the complete arguments even if the last intermediate update was throttled.
func (t *toolCallProgressTracker) forceEvents(runID, sessionID, batchID string, toolCalls []openai.ToolCall, metaForName func(string) map[string]any) []toolCallProgressEvent {
	return t.eventsWithForce(runID, sessionID, batchID, toolCalls, metaForName, true)
}

func (t *toolCallProgressTracker) eventsWithForce(runID, sessionID, batchID string, toolCalls []openai.ToolCall, metaForName func(string) map[string]any, force bool) []toolCallProgressEvent {
	if t == nil {
		return nil
	}
	now := time.Now()
	cadence := cadenceForEvent(toolUpdateEvent)
	events := make([]toolCallProgressEvent, 0)
	for idx, call := range toolCalls {
		if call.ID == "" && call.Type == "" && call.Function.Name == "" && call.Function.Arguments == "" {
			continue
		}
		// Early throttle check for large argument payloads. Constructing the
		// state string (which includes the full accumulated arguments) is
		// O(len(args)), and comparing it is another O(len(args)). For a large
		// create payload with thousands of deltas this wastes CPU even
		// when the event is going to be throttled. Skip the state work entirely
		// when we're within the throttle window for an already-started tool.
		started := t.started[idx]
		throttle := t.throttleFor(idx)
		if started && !force && !throttle.wouldAllow(now, cadence, len(call.Function.Arguments)) {
			continue
		}
		// State key uses a FNV-1a hash of arguments + length, avoiding the
		// previous O(len(args)) string concatenation + full-string compare on
		// every delta. ID/Type/Name are short and stable, so they're inlined.
		argsHash, argsLen := toolCallArgsHash(call.Function.Arguments)
		state := call.ID + "\x00" + string(call.Type) + "\x00" + call.Function.Name + "\x00" + argsHash + "\x00" + strconv.Itoa(argsLen)
		if t.lastState[idx] == state {
			continue
		}
		// Do not emit tool:start until the function name is known. Emitting with
		// an empty name misclassifies the card as "other" / "Using" and consumes
		// the started state so later deltas never emit tool:start with the real name.
		if !started && call.Function.Name == "" {
			continue
		}
		eventName := toolUpdateEvent
		if !started {
			eventName = "tool:start"
			t.started[idx] = true
		}
		// 到这里已经通过预筛（或这是该调用的首个事件），只需记账；
		// force 路径无视节流，但同样要把窗口起点推到当前时刻。
		if force {
			throttle.force(now)
		} else {
			throttle.allow(now, cadence, len(call.Function.Arguments))
		}
		payload := map[string]any{
			"runId":         runID,
			"sessionId":     sessionID,
			"toolBatchId":   batchID,
			"toolCallIndex": idx,
			"toolCallId":    call.ID,
			"name":          call.Function.Name,
			"args":          call.Function.Arguments,
			"streaming":     true,
		}
		if metaForName != nil && call.Function.Name != "" {
			payload = mergeToolEventMeta(payload, metaForName(call.Function.Name))
		}
		events = append(events, toolCallProgressEvent{Name: eventName, Payload: payload})
		t.lastState[idx] = state
	}
	return events
}

// toolCallArgsHash returns a stable FNV-1a 64-bit hex digest and the byte
// length of args. Used as a cheap identity for the streaming arguments so the
// progress tracker avoids re-concatenating multi-KB argument strings on every
// delta just to compare them.
func toolCallArgsHash(args string) (string, int) {
	h := fnv.New64a()
	h.Write([]byte(args))
	return strconv.FormatUint(h.Sum64(), 16), len(args)
}

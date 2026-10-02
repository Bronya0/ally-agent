// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

// 输出事件这一层：流式文本合并器（textDeltaCoalescer）与高频事件的节流档位表
// （eventCadenceTable / emitThrottle）同在本文——档位的唯一来源与它的主要消费者
// 不再分家。

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
// eventCadenceTable（就在本文件）。主对话（run:stream）与压缩总结
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
// （见本文件的 reasoningBucketFor）。
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

// 事件节流收口（single source of truth）：所有高频事件（流式正文与思考、工具参数与
// 进度、命令输出、下载进度）的节流参数和节流原语都在这里，调用点只引用，不再各自
// 写 `time.Now().Sub(lastEmit) < X`。
//
// 三种语义必须分开实现，只共用参数与记账原语：
//   - 合并（coalescer）：窗口内攒住增量，到点发一条合并事件（textDeltaCoalescer，
//     见 infra_stream.go）。
//   - 采样（sampler）：定时去读"当前状态"（命令输出的尾巴），不是攒增量，所以仍是
//     ticker，只把参数放进本表。
//   - 收尾（force flush）：合并器与采样器都必须有人在结束时收尾，否则丢尾巴。
//
// 新增高频事件时在 eventCadenceTable 里显式选一档；没登记就是没有节流
// （cadenceForEvent 返回零值）——压缩流的思考增量曾经就是这样漏掉的。
// 流式文本事件有两个桶（正文 + 思考，窗口不同），登记处是 streamingTextEvents：
// 只在那里加事件名，两个桶的档位就一起生成。

// emitCadence 是一个事件的节流档位。
type emitCadence struct {
	// interval 是两次事件之间的最小间隔；0 表示不节流（每个增量都发）。
	interval time.Duration
	// byteThreshold 大于 0 时，载荷小于它的调用不受 interval 限制：小载荷本来就
	// 不贵，节流它们只会让界面显得卡顿。
	byteThreshold int
}

const (
	// runStreamInterval 是流式文本事件（正文 + 思考计数）的节流窗口。纯时间制：
	// 第一个字节立即 flush，之后按窗口合并。64ms ≈ 15 FPS，打字机效果足够顺，
	// 同时把 IPC 压到低位（Wails 事件是一次 JS 调用）。
	runStreamInterval = 64 * time.Millisecond
	// streamReasoningInterval 是思考增量自己的窗口，比正文宽：思考只驱动一个
	// "思考中"标签与一个估算 token 数，不需要正文那样的 15FPS，而长回合里它
	// 往往是事件量的大头（思考期 ≈5/s，而不是跟正文一起 15.6/s）。
	streamReasoningInterval = 200 * time.Millisecond
	// toolUpdateInterval 限制单个工具调用的参数/进度更新频率：窗口内、参数超过
	// toolUpdateThreshold 的更新被丢弃（最终状态由 forceEvents 兜底）。
	toolUpdateInterval = 200 * time.Millisecond
	// toolUpdateThreshold 是参数/输出多大才算"贵"的门槛（字节）。
	toolUpdateThreshold = 2048
	// updateProgressInterval 是自更新下载进度的事件间隔。
	updateProgressInterval = 500 * time.Millisecond
	// commandOutputSampleInterval 与 commandOutputStreamingTailBytes 是命令执行
	// 输出的采样参数：每 120ms 采一次，且只发尾部 16KB（完整输出在命令结束时随
	// 结果一次性返回）。
	commandOutputSampleInterval     = 120 * time.Millisecond
	commandOutputStreamingTailBytes = 16 * 1024
)

// 事件名与档位放在一起，避免"名字在一处、节流参数在另一处"。
const (
	// runStreamEvent 是主对话的合并流事件：正文与思考增量共用一次 IPC 发出，
	// 比拆成 run:reasoning + run:delta 少一半事件。
	runStreamEvent = "run:stream"
	// compactDeltaEvent 是压缩总结的流式增量事件。
	compactDeltaEvent = "compact:delta"
	// toolUpdateEvent 携带工具参数流或命令输出的进度。
	toolUpdateEvent = "tool:update"
	// updateProgressEvent 是应用自更新的进度事件。
	updateProgressEvent = "update:progress"
)

// 流式文本事件走一对桶：正文用事件名本身当桶名，思考桶名加后缀，两者窗口不同
// （正文 64ms / 思考 200ms），必须分开记账。
const reasoningBucketSuffix = "#reasoning"

// reasoningBucketFor 返回一个流式事件对应的思考桶名。
func reasoningBucketFor(event string) string {
	return event + reasoningBucketSuffix
}

// streamingTextEvents 是所有"正文 + 思考"的流式文本事件，也是这对档位的登记处：
// 新增流式事件时只需要加进这里，正文档与思考桶会一起生成——不会出现"登记了正文、
// 忘了思考桶"（那样思考增量会静默无节流，压缩流曾经就是这样漏的）。
var streamingTextEvents = []string{runStreamEvent, compactDeltaEvent}

// eventCadenceTable 是节流参数的唯一来源。
var eventCadenceTable = buildEventCadenceTable()

func buildEventCadenceTable() map[string]emitCadence {
	table := map[string]emitCadence{
		// 工具参数流与命令输出的进度：小载荷不节流（否则卡片半天不动），大载荷按
		// 窗口限制。
		toolUpdateEvent: {interval: toolUpdateInterval, byteThreshold: toolUpdateThreshold},
		// 下载进度：进度条不需要更密的更新。
		updateProgressEvent: {interval: updateProgressInterval},
	}
	// 流式文本：正文全量、思考只发字符数（见 textDeltaCoalescer）。
	for _, event := range streamingTextEvents {
		table[event] = emitCadence{interval: runStreamInterval}
		table[reasoningBucketFor(event)] = emitCadence{interval: streamReasoningInterval}
	}
	return table
}

// cadenceForEvent 取一个事件的档位；未登记的事件返回零值（= 不节流）。
func cadenceForEvent(name string) emitCadence {
	return eventCadenceTable[name]
}

// emitMap 把 a.emit 适配成合并器需要的 map 载荷回调（emit 的载荷类型是 any）。
func (a *App) emitMap() func(string, map[string]any) {
	return func(name string, payload map[string]any) { a.emit(name, payload) }
}

// emitThrottle 是所有合并/采样点共用的时间窗节流原语，取代散落各处的 lastEmit
// 字段与手写的时间比较。零值可用：首次调用总是放行。
type emitThrottle struct {
	last time.Time
}

// wouldAllow 只查询、不记账：调用点需要先做昂贵判断（构造流式参数的状态哈希）时
// 用它预筛，避免为注定被丢弃的事件白算一遍。
func (t *emitThrottle) wouldAllow(now time.Time, cadence emitCadence, payloadSize int) bool {
	if cadence.byteThreshold > 0 && payloadSize < cadence.byteThreshold {
		return true
	}
	return t.last.IsZero() || cadence.interval <= 0 || now.Sub(t.last) >= cadence.interval
}

// allow 判断并记账，返回这次事件是否放行。
func (t *emitThrottle) allow(now time.Time, cadence emitCadence, payloadSize int) bool {
	if !t.wouldAllow(now, cadence, payloadSize) {
		return false
	}
	t.last = now
	return true
}

// force 记账一次"无视节流的发送"，让随后的窗口从这一刻重新计时。
func (t *emitThrottle) force(now time.Time) {
	t.last = now
}

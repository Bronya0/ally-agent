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

type runStreamDeltaEmitter struct {
	runID           string
	sessionID       string
	contentBuffer   strings.Builder
	reasoningBuffer strings.Builder
	lastEmit        time.Time
	emit            func(string, map[string]any)
}

const (
	// runStreamDeltaThrottle bounds how often a merged run:stream event is
	// emitted during streaming. Pure time-based: the first byte always flushes
	// immediately, afterwards deltas accumulate until the throttle window
	// elapses. 64ms ≈ 15 FPS, smooth enough for the typewriter indicator
	// while keeping IPC rate low (Wails events are synchronous JS calls).
	runStreamDeltaThrottle = 64 * time.Millisecond
	// runStreamEvent is the merged streaming event. Both reasoning and content
	// deltas are flushed in a single IPC via shared payload fields, halving the
	// event count compared to emitting run:reasoning and run:delta separately.
	runStreamEvent = "run:stream"
)

func newRunStreamDeltaEmitter(runID, sessionID string, emit func(string, map[string]any)) *runStreamDeltaEmitter {
	return &runStreamDeltaEmitter{runID: runID, sessionID: sessionID, emit: emit}
}

func (e *runStreamDeltaEmitter) addContent(delta string) {
	if e == nil || delta == "" {
		return
	}
	e.contentBuffer.WriteString(delta)
	if e.shouldFlush(e.contentBuffer.Len()) {
		e.flush()
	}
}

func (e *runStreamDeltaEmitter) addReasoning(delta string) {
	if e == nil || delta == "" {
		return
	}
	e.reasoningBuffer.WriteString(delta)
	if e.shouldFlush(e.reasoningBuffer.Len()) {
		e.flush()
	}
}

func (e *runStreamDeltaEmitter) shouldFlush(bufferLen int) bool {
	if e == nil || bufferLen == 0 {
		return false
	}
	// First byte always flushes so the user sees the response start immediately.
	// Afterwards, pure time-based throttling — no byte threshold, since the
	// merged event is cheap (reasoning is just a length int) and 64ms gives a
	// smooth enough typewriter effect without flooding Wails IPC.
	if e.lastEmit.IsZero() {
		return true
	}
	return time.Since(e.lastEmit) >= runStreamDeltaThrottle
}

// flush emits a single run:stream event carrying whichever of reasoning/content
// has pending bytes. Merging here cuts IPC count in half for the common
// thinking+answer streaming case.
//
// Reasoning is sent as a character count only (reasoningLen), not the full
// text: the frontend no longer displays or stores the reasoning body, so
// transmitting 512+ bytes of thinking text per tick is pure IPC waste. Content
// is still sent in full because it is rendered live.
func (e *runStreamDeltaEmitter) flush() {
	if e == nil || e.emit == nil {
		return
	}
	reasoning := e.reasoningBuffer.String()
	content := e.contentBuffer.String()
	if reasoning == "" && content == "" {
		return
	}
	e.reasoningBuffer.Reset()
	e.contentBuffer.Reset()
	e.lastEmit = time.Now()
	payload := map[string]any{"runId": e.runID, "sessionId": e.sessionID}
	if reasoning != "" {
		payload["reasoningLen"] = len(reasoning)
	}
	if content != "" {
		payload["content"] = content
	}
	e.emit(runStreamEvent, payload)
}

// modelToolCallEventGate prevents provider adapters from cloning and
// forwarding the full accumulated tool-call slice for every tiny argument
// delta. The final complete arguments are still emitted by runChat through
// forceEvents after the provider returns.
type modelToolCallEventGate struct {
	forward  func(modelStreamEvent)
	started  bool
	lastEmit time.Time
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
		now := time.Now()
		if g.started && maxArgs > toolUpdateThreshold && now.Sub(g.lastEmit) < toolUpdateThrottle {
			return
		}
		g.started = true
		g.lastEmit = now
	}
	g.forward(event)
}

type toolCallProgressTracker struct {
	started   map[int]bool
	lastState map[int]string
	lastEmit  map[int]time.Time
	// argsRedact is applied to streamed tool-call arguments before emission
	// so secrets (e.g. ssh_credential passwords) never reach the frontend.
	argsRedact func(string) string
}

// toolUpdateThrottle bounds how often a tool:update event is emitted for a
// single tool call while streaming large argument payloads. Without this,
// streaming a large create payload produces one event per delta, each
// carrying the full accumulated arguments, which is O(N^2) in data transfer
// and floods the frontend webview (causing frozen UI and multi-GB memory
// growth). The final state is always emitted via forceEvents after the stream
// completes, so throttling only drops intermediate previews.
const (
	toolUpdateThrottle  = 200 * time.Millisecond
	toolUpdateThreshold = 2048 // only throttle when args exceed this size
)

func newToolCallProgressTracker() *toolCallProgressTracker {
	return &toolCallProgressTracker{
		started:   map[int]bool{},
		lastState: map[int]string{},
		lastEmit:  map[int]time.Time{},
	}
}

// withArgsRedact sets the argument redaction hook and returns the tracker.
func (t *toolCallProgressTracker) withArgsRedact(fn func(string) string) *toolCallProgressTracker {
	t.argsRedact = fn
	return t
}

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
		if started && !force && len(call.Function.Arguments) > toolUpdateThreshold {
			if last, ok := t.lastEmit[idx]; ok && now.Sub(last) < toolUpdateThrottle {
				continue
			}
		}
		// State key uses a FNV-1a hash of arguments + length, avoiding the
		// previous O(len(args)) string concatenation + full-string compare on
		// every delta. ID/Type/Name are short and stable, so they're inlined.
		argsHash, argsLen := toolCallArgsHash(call.Function.Arguments)
		state := call.ID + "\x00" + string(call.Type) + "\x00" + call.Function.Name + "\x00" + argsHash + "\x00" + strconv.Itoa(argsLen)
		if t.lastState[idx] == state {
			continue
		}
		eventName := "tool:update"
		if !started {
			eventName = "tool:start"
			t.started[idx] = true
		}
		t.lastEmit[idx] = now
		payload := map[string]any{
			"runId":         runID,
			"sessionId":     sessionID,
			"toolBatchId":   batchID,
			"toolCallIndex": idx,
			"toolCallId":    call.ID,
			"name":          call.Function.Name,
			"args":          redactToolCallArgs(call.Function.Arguments, t.argsRedact),
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

// redactToolCallArgs applies the tracker's redaction hook (if any) to streamed
// tool-call arguments before they are emitted to the frontend.
func redactToolCallArgs(args string, fn func(string) string) string {
	if fn == nil || args == "" {
		return args
	}
	return fn(args)
}

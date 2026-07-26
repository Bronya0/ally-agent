package app

import (
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
	runStreamDeltaThrottle  = 32 * time.Millisecond
	runStreamDeltaThreshold = 512
)

func newRunStreamDeltaEmitter(runID, sessionID string, emit func(string, map[string]any)) *runStreamDeltaEmitter {
	return &runStreamDeltaEmitter{runID: runID, sessionID: sessionID, emit: emit}
}

func (e *runStreamDeltaEmitter) addContent(delta string) {
	e.add("run:delta", &e.contentBuffer, delta)
}

func (e *runStreamDeltaEmitter) addReasoning(delta string) {
	e.add("run:reasoning", &e.reasoningBuffer, delta)
}

func (e *runStreamDeltaEmitter) add(name string, buffer *strings.Builder, delta string) {
	if e == nil || delta == "" {
		return
	}
	buffer.WriteString(delta)
	if e.shouldFlush(buffer.Len()) {
		e.flushBuffer(name, buffer)
	}
}

func (e *runStreamDeltaEmitter) shouldFlush(bufferLen int) bool {
	if e == nil || bufferLen == 0 {
		return false
	}
	if e.lastEmit.IsZero() || bufferLen >= runStreamDeltaThreshold {
		return true
	}
	return time.Since(e.lastEmit) >= runStreamDeltaThrottle
}

func (e *runStreamDeltaEmitter) flush() {
	if e == nil {
		return
	}
	e.flushBuffer("run:reasoning", &e.reasoningBuffer)
	e.flushBuffer("run:delta", &e.contentBuffer)
}

func (e *runStreamDeltaEmitter) flushBuffer(name string, buffer *strings.Builder) {
	if e == nil || buffer == nil || buffer.Len() == 0 || e.emit == nil {
		return
	}
	content := buffer.String()
	buffer.Reset()
	e.lastEmit = time.Now()
	e.emit(name, map[string]any{"runId": e.runID, "sessionId": e.sessionID, "content": content})
}

type toolCallProgressTracker struct {
	started   map[int]bool
	lastState map[int]string
	lastEmit  map[int]time.Time
}

// toolUpdateThrottle bounds how often a tool:update event is emitted for a
// single tool call while streaming large argument payloads. Without this,
// streaming a large create_file payload produces one event per delta, each
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
		// create_file payload with thousands of deltas this wastes CPU even
		// when the event is going to be throttled. Skip the state work entirely
		// when we're within the throttle window for an already-started tool.
		started := t.started[idx]
		if started && !force && len(call.Function.Arguments) > toolUpdateThreshold {
			if last, ok := t.lastEmit[idx]; ok && now.Sub(last) < toolUpdateThrottle {
				continue
			}
		}
		state := call.ID + "\x00" + string(call.Type) + "\x00" + call.Function.Name + "\x00" + call.Function.Arguments
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

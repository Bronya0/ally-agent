// SPDX-License-Identifier: GPL-3.0-only
package app

// Opt-in, content-free Responses diagnostics. No request/response bodies, error
// strings, session paths, account identifiers or credentials cross this boundary.
import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net/http/httptrace"
	"os"
	"sync"
	"time"
)

type perfContextKey struct{}
type perfReasonKey struct{}
type perfRequestKey struct{}
type perfAttemptKey struct{}

type perfRun struct {
	mu                                      sync.Mutex
	file                                    *os.File
	id                                      string
	start                                   time.Time
	requests, tools, retries, failures      int
	logicalRequests                         int
	triggers                                map[string]int
	input, cached, output, reasoning, total int
	usageReports                            int
	modelMS, toolMS                         int64
	retryWaitMS                             int64
	lastTool                                time.Time
	activeTools                             int
	seenTools                               map[[32]byte]bool
	wirePrefixes                            map[[32]byte]contextFingerprint
}

func beginPerfRun(ctx context.Context, id string) (context.Context, func()) {
	if ctx.Value(perfContextKey{}) != nil {
		return ctx, func() {}
	}
	path := os.Getenv("ALLY_PERF_DIAGNOSTICS")
	if path == "" {
		return ctx, func() {}
	}
	if id == "" {
		id = perfStandaloneID()
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		log.Print("Ally performance diagnostics: cannot open output file")
		return ctx, func() {}
	}
	p := &perfRun{file: f, id: id, start: time.Now(), seenTools: make(map[[32]byte]bool), triggers: make(map[string]int)}
	return context.WithValue(ctx, perfContextKey{}, p), func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.writeLocked(map[string]any{"event": "run_summary", "started_at": p.start.UTC(), "duration_ms": time.Since(p.start).Milliseconds(), "responses_attempts": p.requests, "model_calls": p.logicalRequests, "attempts_by_trigger": p.triggers, "tools": p.tools, "retries": p.retries, "retry_wait_ms": p.retryWaitMS, "failed_attempts": p.failures, "usage_reports": p.usageReports, "input_tokens": p.input, "cached_tokens": p.cached, "output_tokens": p.output, "reasoning_tokens": p.reasoning, "total_tokens": p.total, "model_attempt_ms_sum": p.modelMS, "tool_ms_sum": p.toolMS})
		_ = f.Close()
	}
}

func perfReason(ctx context.Context, reason string) context.Context {
	return context.WithValue(ctx, perfReasonKey{}, reason)
}

func (p *perfRun) writeLocked(row map[string]any) {
	row["run_id"] = p.id
	row["time"] = time.Now().UTC()
	// Diagnostics are best effort; an output failure must never fail inference.
	_ = json.NewEncoder(p.file).Encode(row)
}

type perfModelRequest struct{ attempts, id int }

func perfModelContext(ctx context.Context) context.Context {
	p, _ := ctx.Value(perfContextKey{}).(*perfRun)
	if p == nil {
		return ctx
	}
	p.mu.Lock()
	p.logicalRequests++
	id := p.logicalRequests
	p.mu.Unlock()
	return context.WithValue(ctx, perfRequestKey{}, &perfModelRequest{id: id})
}

type perfAttempt struct {
	run                                    *perfRun
	row                                    map[string]any
	start, firstSSE, firstOutput, terminal time.Time
	usage                                  *modelUsage
}

func beginPerfAttempt(ctx context.Context, cfg ConfigState, model string) *perfAttempt {
	p, _ := ctx.Value(perfContextKey{}).(*perfRun)
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests++
	index := 1
	requestID := 0
	if request, ok := ctx.Value(perfRequestKey{}).(*perfModelRequest); ok {
		request.attempts++
		index = request.attempts
		requestID = request.id
	}
	reason, _ := ctx.Value(perfReasonKey{}).(string)
	if reason == "" {
		reason = "model_call"
	}
	if index > 1 || reason == "turn_retry" {
		p.retries++
	}
	p.triggers[reason]++
	row := map[string]any{"event": "responses_attempt", "attempt_id": p.requests, "model_request_id": requestID, "model": model, "effort_selected": cfg.ReasoningEffort, "effort_wire": reasoningWireForAdapter(cfg, apiFormatOpenAIResponses, model, cfg.ReasoningEffort).Effort, "trigger": reason, "attempt": index, "retry": index > 1 || reason == "turn_retry"}
	if !p.lastTool.IsZero() {
		row["last_tool_to_request_ms"] = time.Since(p.lastTool).Milliseconds()
	}
	return &perfAttempt{run: p, row: row, start: time.Now()}
}

// Compare the final Responses payload, after SDK serialization and OAuth
// rewriting. The orchestration fingerprint cannot see changes at this boundary.
// Baselines are bounded, run-local and lane-specific; neither content nor hashes
// are written to the diagnostic file. An unchanged client prefix is evidence
// about the client only, not a guarantee about the service's rendered context.
func (d *perfAttempt) request(cfg ConfigState, body map[string]any) {
	if d == nil {
		return
	}
	input, ok := body["input"].([]any)
	if !ok {
		return
	}
	head := make(map[string]any, len(body))
	for key, value := range body {
		if key != "input" && key != "stream" && key != "max_output_tokens" {
			head[key] = value
		}
	}
	rawHead, err := json.Marshal(head)
	if err != nil {
		return
	}
	headHash := fmt.Sprintf("%x", sha256.Sum256(rawHead))
	model, _ := body["model"].(string)
	lane := sha256.Sum256([]byte(reasoningReplayKey(cfg, model)))
	p := d.run
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.wirePrefixes == nil {
		p.wirePrefixes = make(map[[32]byte]contextFingerprint)
	}
	base, seen := p.wirePrefixes[lane]
	if !seen && len(p.wirePrefixes) >= maxReasoningStashEntries {
		d.row["wire_prefix"] = "lane_limit"
		return
	}
	tail := sha256.New()
	overlap := min(base.count, len(input))
	overlapHash := fmt.Sprintf("%x", tail.Sum(nil))
	reasoning, encrypted, breakpoints := 0, 0, 0
	for i, item := range input {
		raw, err := json.Marshal(item)
		if err != nil {
			return
		}
		writeFramedHash(tail, raw)
		if i+1 == overlap {
			overlapHash = fmt.Sprintf("%x", tail.Sum(nil))
		}
		if item, ok := item.(map[string]any); ok {
			if item["type"] == "reasoning" {
				reasoning++
				if value, _ := item["encrypted_content"].(string); value != "" {
					encrypted++
				}
			}
			if content, ok := item["content"].([]any); ok {
				for _, raw := range content {
					if block, ok := raw.(map[string]any); ok && block["prompt_cache_breakpoint"] != nil {
						breakpoints++
					}
				}
			}
		}
	}
	state := "first_request"
	if seen {
		state = "unchanged_prefix"
		if verdict := compareContextFingerprint(base, headHash, overlapHash, len(input)); verdict.kind != "" {
			state = verdict.kind + "_changed"
		}
	}
	p.wirePrefixes[lane] = contextFingerprint{head: headHash, tail: fmt.Sprintf("%x", tail.Sum(nil)), count: len(input)}
	d.row["wire_prefix"] = state
	d.row["wire_input_items"] = len(input)
	d.row["wire_reasoning_items"] = reasoning
	d.row["wire_encrypted_reasoning_items"] = encrypted
	d.row["wire_cache_key_present"] = body["prompt_cache_key"] != nil
	d.row["wire_explicit_breakpoints"] = breakpoints
	if options, ok := body["prompt_cache_options"].(map[string]any); ok {
		if mode := options["mode"]; mode == "implicit" || mode == "explicit" {
			d.row["wire_cache_mode"] = mode
		}
	}
}

func (d *perfAttempt) event(kind string, raw []byte) {
	if d == nil {
		return
	}
	d.run.mu.Lock()
	defer d.run.mu.Unlock()
	now := time.Now()
	if d.firstSSE.IsZero() {
		d.firstSSE = now
	}
	if d.firstOutput.IsZero() && (kind == "response.output_text.delta" || kind == "response.function_call_arguments.delta" || kind == "response.function_call_arguments.done") {
		var delta struct {
			Delta     string `json:"delta"`
			Arguments string `json:"arguments"`
		}
		if json.Unmarshal(raw, &delta) == nil && (delta.Delta != "" || delta.Arguments != "") {
			d.firstOutput = now
		}
	}
	if kind == "response.output_item.added" || kind == "response.output_item.done" {
		var item struct {
			Item struct {
				Type string `json:"type"`
			} `json:"item"`
		}
		if json.Unmarshal(raw, &item) == nil && item.Item.Type == "function_call" && d.firstOutput.IsZero() {
			d.firstOutput = now
		}
	}
	if kind == "response.completed" || kind == "response.incomplete" || kind == "response.failed" {
		d.terminal = now
		d.row["terminal_event"] = kind
		d.usage = modelUsageFromResponsesEvent(raw)
		var response struct {
			Response struct {
				Reasoning struct{ Context string } `json:"reasoning"`
			} `json:"response"`
		}
		if json.Unmarshal(raw, &response) == nil {
			switch mode := response.Response.Reasoning.Context; mode {
			case "auto", "all_turns", "current_turn":
				d.row["reasoning_context_effective"] = mode
			}
		}
	}
}

func (d *perfAttempt) finish(err error) {
	if d == nil {
		return
	}
	p := d.run
	p.mu.Lock()
	defer p.mu.Unlock()
	d.row["started_at"] = d.start.UTC()
	d.row["finished_at"] = time.Now().UTC()
	d.row["duration_ms"] = time.Since(d.start).Milliseconds()
	for _, stamp := range []struct {
		name  string
		value time.Time
	}{{"first_sse_at", d.firstSSE}, {"first_output_at", d.firstOutput}, {"terminal_at", d.terminal}} {
		if !stamp.value.IsZero() {
			d.row[stamp.name] = stamp.value.UTC()
		}
	}
	if !d.terminal.IsZero() {
		d.row["terminal_to_return_ms"] = time.Since(d.terminal).Milliseconds()
	}
	p.modelMS += time.Since(d.start).Milliseconds()
	if err != nil {
		p.failures++
		d.row["error_kind"] = perfErrorKind(err)
		d.row["retryable"] = shouldRetryLLMError(err)
	}
	d.row["usage_reported"] = d.usage != nil
	if u := d.usage; u != nil {
		d.row["input_tokens"], d.row["cached_tokens"], d.row["output_tokens"], d.row["reasoning_tokens"], d.row["total_tokens"] = u.PromptTokens, u.CacheHitTokens, u.CompletionTokens, u.ReasoningTokens, u.TotalTokens
		d.row["cache_write_tokens"] = u.CacheWriteTokens
		d.row["cache_usage_reported"] = u.CacheHitTokens+u.CacheMissTokens > 0
		p.input += u.PromptTokens
		p.cached += u.CacheHitTokens
		p.output += u.CompletionTokens
		p.reasoning += u.ReasoningTokens
		p.total += u.TotalTokens
		p.usageReports++
	}
	p.writeLocked(d.row)
}

func startPerfTool(ctx context.Context, name string, args []byte) func() {
	p, _ := ctx.Value(perfContextKey{}).(*perfRun)
	if p == nil {
		return func() {}
	}
	// Hash only in memory to identify exact repeats; never persist a digest that
	// could be probed for private arguments. Repeats may be intentional polling.
	key := sha256.Sum256([]byte(name + "\x00" + string(args)))
	start := time.Now()
	p.mu.Lock()
	repeated := p.seenTools[key]
	p.seenTools[key] = true
	p.activeTools++
	p.tools++
	index := p.tools
	if p.activeTools > 1 {
		p.writeLocked(map[string]any{"event": "tool_overlap", "active_tools": p.activeTools})
	}
	p.mu.Unlock()
	return func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.activeTools--
		p.lastTool = time.Now()
		ms := time.Since(start).Milliseconds()
		p.toolMS += ms
		p.writeLocked(map[string]any{"event": "tool", "tool_index": index, "tool_type": name, "started_at": start.UTC(), "duration_ms": ms, "repeated_arguments": repeated})
	}
}

func perfStandaloneID() string { return fmt.Sprintf("standalone-%d", time.Now().UnixNano()) }

func notePerfRetry(ctx context.Context, err error, wait time.Duration) {
	p, _ := ctx.Value(perfContextKey{}).(*perfRun)
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.retryWaitMS += wait.Milliseconds()
	p.writeLocked(map[string]any{"event": "retry_scheduled", "error_kind": perfErrorKind(err), "wait_ms": wait.Milliseconds()})
}

func perfErrorKind(err error) string {
	if err == nil {
		return ""
	}
	return map[llmErrorKind]string{llmErrorKindUnknown: "unknown", llmErrorKindRateLimited: "rate_limited", llmErrorKindAuth: "auth", llmErrorKindBilling: "billing", llmErrorKindContextTooLong: "context_too_long", llmErrorKindModelNotFound: "model_not_found", llmErrorKindDeterministic400: "invalid_request"}[classifyLLMError(err)]
}

// net/http invokes trace callbacks on transport goroutines. Keep them out of
// the attempt row until the attempt finishes, using the run mutex throughout.
func (d *perfAttempt) trace() *httptrace.ClientTrace {
	stamp := func(key string, value any) { d.run.mu.Lock(); d.row[key] = value; d.run.mu.Unlock() }
	return &httptrace.ClientTrace{
		GotConn:              func(info httptrace.GotConnInfo) { stamp("connection_reused", info.Reused) },
		WroteRequest:         func(httptrace.WroteRequestInfo) { stamp("request_written_at", time.Now().UTC()) },
		GotFirstResponseByte: func() { stamp("first_response_byte_at", time.Now().UTC()) },
	}
}

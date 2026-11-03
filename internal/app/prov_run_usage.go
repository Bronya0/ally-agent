// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"sync"
	"time"
)

type modelRunUsageKey struct{}
type modelRunUsage struct {
	mu                                                  sync.Mutex
	input, output, reasoning, hit, miss, calls, unknown int
	modelDuration                                       time.Duration
}

func withModelRunUsage(ctx context.Context) context.Context {
	if ctx.Value(modelRunUsageKey{}) != nil {
		return ctx
	}
	return context.WithValue(ctx, modelRunUsageKey{}, &modelRunUsage{})
}

// Count known usage at the attempt boundary, including terminal failures and
// empty responses subsequently retried. Compression and synchronous delegates
// inherit this context, so their spend belongs to the parent run exactly once.
// This is the sole source for the UI's "this run" totals, not only successful
// top-level chat rounds; known usage from failed attempts is included as well.
func noteRunModelUsage(ctx context.Context, usage *modelUsage, result *modelStreamResult) {
	total, _ := ctx.Value(modelRunUsageKey{}).(*modelRunUsage)
	if total == nil {
		return
	}
	fallback := 0
	if result != nil && (usage == nil || usage.CompletionTokens <= 0) {
		fallback = estimateCompletionTokens(result.Content, result.Reasoning, result.ToolCalls)
	}
	total.mu.Lock()
	defer total.mu.Unlock()
	total.calls++
	total.output += fallback
	if usage == nil {
		total.unknown++
		return
	}
	total.input += usage.PromptTokens
	total.output += usage.CompletionTokens
	total.reasoning += usage.ReasoningTokens
	total.hit += usage.CacheHitTokens
	total.miss += usage.CacheMissTokens
}

// noteRunModelDuration keeps provider request time separate from end-to-end
// run time, which also includes tools and user waits. The UI uses it for the
// approximate output-token rate.
func noteRunModelDuration(ctx context.Context, duration time.Duration) {
	total, _ := ctx.Value(modelRunUsageKey{}).(*modelRunUsage)
	if total == nil || duration <= 0 {
		return
	}
	total.mu.Lock()
	total.modelDuration += duration
	total.mu.Unlock()
}

func publishRunModelUsage(ctx context.Context, payload map[string]any) {
	total, _ := ctx.Value(modelRunUsageKey{}).(*modelRunUsage)
	if total == nil {
		return
	}
	total.mu.Lock()
	defer total.mu.Unlock()
	payload["inputTokens"] = total.input
	payload["outputTokens"] = total.output
	payload["reasoningTokens"] = total.reasoning
	payload["cacheHit"] = total.hit
	payload["cacheMiss"] = total.miss
	payload["modelRequests"] = total.calls
	payload["usageUnknownAttempts"] = total.unknown
	payload["modelDurationMs"] = total.modelDuration.Milliseconds()
}

// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"fmt"

	openai "github.com/sashabaranov/go-openai"
)

// This state belongs to one delegate, not to its parent session. The original
// task and budget are pinned verbatim, as are the delegate's own instructions
// and tool grants; only its accumulated work history is replaced.
type subagentCompaction struct {
	initial      []openai.ChatCompletionMessage
	taskStart    int
	prefixTokens int
	breakdown    liveBreakdownAccumulator
	anchor       contextAnchor
	failures     int
	recovered    bool // one overflow recovery per logical turn, including retries
}

func newSubagentCompaction(initial []openai.ChatCompletionMessage, taskStart int, tools []openai.Tool) *subagentCompaction {
	prefix := estimateToolSchemaTokens(tools)
	for _, m := range initial[:leadingSystemCount(initial)] {
		prefix += estimateMessageBodyTokens(m)
	}
	return &subagentCompaction{initial: initial, taskStart: taskStart, prefixTokens: prefix}
}

func (c *subagentCompaction) tokens(messages []openai.ChatCompletionMessage) int {
	b := c.breakdown.update(messages)
	b.SystemPrompt = c.prefixTokens // includes the fixed tool schema cost
	applyContextAnchor(&b, messages, c.anchor)
	return b.Total
}

// Called before a work or report turn, not before every transient retry.
func (c *subagentCompaction) prepareTurn(a *App, ctx context.Context, cfg ConfigState, run *SubagentRun, messages *[]openai.ChatCompletionMessage, resumeNotice string) {
	c.recovered = false
	*messages = a.ensureMcpCatalog(ctx, cfg, *messages)
	if used := c.tokens(*messages); used > compactThresholdLimit(cfg) {
		// No new work to reduce yet (e.g. the fixed prefix itself is large).
		for _, m := range (*messages)[len(c.initial):] {
			if m.Role == openai.ChatMessageRoleAssistant {
				// A threshold failure leaves the original history usable. The
				// shared breaker bounds repeated attempts; overflow is separate.
				_ = c.compact(a, ctx, cfg, run, messages, resumeNotice, compactReasonThreshold, used)
				break
			}
		}
	}
}

func (c *subagentCompaction) streamTurn(a *App, ctx context.Context, cfg ConfigState, run *SubagentRun, messages *[]openai.ChatCompletionMessage, tools []openai.Tool, resumeNotice string) (*modelStreamResult, bool, error) {
	for {
		emitted := false
		resp, err := a.streamSubagentModelResponse(ctx, cfg, cfg.Model, messages, tools, func(event modelStreamEvent) {
			if event.ContentDelta != "" || event.ReasoningDelta != "" || len(event.ToolCalls) > 0 || event.Image != nil {
				emitted = true
			}
		})
		if !shouldRecoverContextOverflow(err, emitted, c.recovered) {
			if err == nil && resp != nil && resp.Usage != nil && resp.Usage.PromptTokens > 0 && modelResponseStopError(cfg, resp) == nil {
				c.anchor = contextAnchor{covered: conversationMessageCount(*messages), tokens: resp.Usage.PromptTokens}
			}
			return resp, emitted, err
		}
		c.recovered = true
		if compactErr := c.compact(a, ctx, cfg, run, messages, resumeNotice, compactReasonOverflow, c.tokens(*messages)); compactErr != nil {
			return nil, false, fmt.Errorf("%w; context overflow compaction failed: %v", err, compactErr)
		}
		ctx = perfReason(ctx, "overflow_recovery")
	}
}

func (c *subagentCompaction) compact(a *App, ctx context.Context, cfg ConfigState, run *SubagentRun, messages *[]openai.ChatCompletionMessage, resumeNotice, reason string, tokensBefore int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	history, _, err := prepareRunCompaction(cfg, reason, c.failures < maxConsecutiveCompactFailures, (*messages)[c.taskStart:], nil)
	if err != nil {
		return err
	}
	event := map[string]any{"id": run.ID, "sessionId": run.SessionID, "reason": reason, "tokensBefore": tokensBefore}
	a.emit("sub:compact", event)
	summary, usage, err := a.summarizeCompactHistory(ctx, cfg, "", history, nil)
	if err != nil {
		c.failures++
		a.emit("sub:compacted", map[string]any{"id": run.ID, "sessionId": run.SessionID, "reason": reason, "ok": false})
		return err
	}

	c.failures = 0
	c.anchor = contextAnchor{}
	a.reasoningStash.clearScope(reasoningReplayScope(cfg))
	next := append(cloneChatMessages(c.initial), openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: summary})
	if resumeNotice != "" {
		next = append(next, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: resumeNotice})
	}
	*messages = a.ensureMcpCatalog(ctx, cfg, next)
	c.breakdown.reset(nil)
	a.recordSubagentUsage(run, cfg.Model, cfg.Workspace, &modelStreamResult{Content: summary, Usage: usage})
	a.subRunsMu.Lock()
	run.ContextTokens = c.tokens(*messages)
	payload := subagentEventPayload(run, map[string]any{"id": run.ID, "sessionId": run.SessionID, "reason": reason, "ok": true})
	a.subRunsMu.Unlock()
	a.emit("sub:compacted", payload)
	return nil
}

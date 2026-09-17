// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3.0. See the LICENSE file for details.
package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// defaultCompactThreshold is the auto-compaction trigger as a fraction of
// the context window (0.6 = 60%). Treated as "use default" when the
// stored value is zero, so legacy config.json without the field migrates
// to the default transparently. Keep it in sync with the frontend default
// (frontend/src/utils/config.mjs): a request that omits the field must
// land on the same trigger the Settings UI displays.

// clampCompactThreshold normalizes the auto-compaction threshold. Zero (or
// out-of-range values) fall back to the default so legacy config.json and
// hand-edited junk both land on a sane value; otherwise the value is clamped
// to [0.2, 0.95] so the loop neither thrashes nor waits until the model
// hard-errors.
func clampCompactThreshold(v float64) float64 {
	if v <= 0 {
		return defaultCompactThreshold
	}
	if v < 0.2 {
		return 0.2
	}
	if v > 0.95 {
		return 0.95
	}
	return v
}

const (
	// defaultCompactTimeoutSeconds covers the common compaction summary call:
	// large history in, structured summary out, through a normally fast
	// provider.
	defaultCompactTimeoutSeconds = 180
	// maxCompactTimeoutSeconds lets slow long-context providers take up to an
	// hour instead of hard-failing with "context deadline exceeded".
	maxCompactTimeoutSeconds = 3600
)

// clampCompactTimeoutSeconds normalizes the compaction timeout. Zero (or
// out-of-range values) fall back to the default so legacy config.json and
// hand-edited junk both land on a sane value; otherwise the value is clamped
// to [30, 3600] seconds.
func clampCompactTimeoutSeconds(v int) int {
	if v <= 0 {
		return defaultCompactTimeoutSeconds
	}
	if v < 30 {
		return 30
	}
	if v > maxCompactTimeoutSeconds {
		return maxCompactTimeoutSeconds
	}
	return v
}

// compactSessionRunning reports whether a compaction LLM call is in flight
// for the given session.
func (a *App) compactSessionRunning(sessionID string) bool {
	a.mu.Lock()
	_, running := a.compactingSessions[sessionID]
	a.mu.Unlock()
	return running
}

// CompactSession compacts the conversation history for a session. Stage 1
// stubs stale tool-result bodies (free); the LLM summary (stage 2) only runs
// when usage is still over the threshold afterwards.
func (a *App) CompactSession(sessionID, instruction string) (map[string]any, error) {
	parent := a.ctx
	if parent == nil {
		parent = context.Background()
	}
	return a.compactSession(parent, sessionID, instruction)
}

// CancelCompaction aborts an in-flight manual compaction for the session so
// ESC does not have to wait out the compaction timeout.
func (a *App) CancelCompaction(sessionID string) error {
	a.mu.Lock()
	cancel := a.compactingCancels[sessionID]
	a.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	return nil
}

func (a *App) compactSession(parent context.Context, sessionID, instruction string) (map[string]any, error) {
	if err := a.ensureInitialized(); err != nil {
		return nil, err
	}
	cfg, err := a.getConfig()
	if err != nil {
		return nil, err
	}
	// Replay the session's frozen model fields (StartChat overlay): without
	// this, manual compaction would summarize with the persisted default
	// model's base URL / key pool and silently drop the Tab's custom headers
	// whenever the Tab model differs from the persisted default.
	cfg = a.sessionModelConfigFor(sessionID).apply(cfg)
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("model is required")
	}
	if len(resolveKeyPool(cfg)) == 0 {
		return nil, errors.New("API key is required")
	}
	// A live chat run may rewrite history concurrently (auto-compaction,
	// sanitize repair, new turns); compacting on top of it would interleave
	// saveHistory calls and race the run's own message list.
	a.mu.Lock()
	if a.activeRunForSession(sessionID) != "" {
		a.mu.Unlock()
		return nil, errSessionRunning
	}
	// Compaction rewrites the whole session history, so two concurrent
	// compactions on the same session would interleave saveHistory calls and
	// pay for the LLM request twice. Guard at the session level so other
	// sessions compact independently.
	if _, running := a.compactingSessions[sessionID]; running {
		a.mu.Unlock()
		return nil, errors.New("session is already compacting")
	}
	// The summary LLM call runs on a cancellable child of the app context so
	// CancelCompaction (ESC) can abort it mid-flight.
	ctx, cancel := context.WithCancel(parent)
	// Lazy-init guard: a nil map write here panics while a.mu is held, and
	// the panic escapes before the cleanup defer is registered — the mutex
	// stays locked forever and every later call (ESC cancel, shutdown, any
	// binding) deadlocks the whole app. App instances built outside NewApp
	// (tests) must not take the process down with them.
	if a.compactingSessions == nil {
		a.compactingSessions = map[string]struct{}{}
	}
	if a.compactingCancels == nil {
		a.compactingCancels = map[string]context.CancelFunc{}
	}
	a.compactingSessions[sessionID] = struct{}{}
	a.compactingCancels[sessionID] = cancel
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.compactingSessions, sessionID)
		delete(a.compactingCancels, sessionID)
		a.mu.Unlock()
		cancel()
		a.emit("compact:done", map[string]any{"sessionId": sessionID})
	}()

	// loadSessionHistoryCopy 持 a.mu 读取并做磁盘懒加载回退（与 buildMessages
	// 的历史来源一致）：直接裸读 a.histories 既缺少锁（并发 saveHistory 写同一
	// map 是不可恢复的 fatal panic），也会在进程重启后、历史尚未被任何会话
	// 路径懒加载时拿到 nil，误报 "no messages to compact"。
	history := a.loadSessionHistoryCopy(sessionID)
	if len(history) == 0 {
		return nil, errors.New("no messages to compact")
	}

	tokensBefore := a.getContextBreakdown(sessionID, "").Total
	if tokensBefore <= 0 {
		tokensBefore = estimateTokensFromMessages(history)
	}

	// 手动点击压缩是用户的明确意图：无条件执行总结压缩，将切分点之前的较早历史总结为 Summary。
	// （自动压缩才会受 threshold 阈值限制）
	return a.compactHistory(ctx, cfg, sessionID, instruction, history, tokensBefore)
}

const (
	// compactReasonThreshold is the auto-compaction trigger: context usage is
	// above the configured threshold.
	compactReasonThreshold = "threshold"
	// compactReasonOverflow is the recovery path for a provider that rejected the
	// request as context-too-long: the request is retried once after a forced
	// compaction (pi's overflow recovery).
	compactReasonOverflow = "overflow"
)

// errHistoryTooShortToCompact marks the "nothing worth summarizing" case. It is
// not a failure the user can act on, so callers report it differently from a
// real compaction failure.
var errHistoryTooShortToCompact = errors.New("history is too short to compact")

// compactRunHistory summarizes history and rebuilds the request message list
// from the compacted result: system context, then the compacted history, then
// the current user turn. The compaction call itself carries the trailing user
// message (that is what it summarizes), so re-appending it here is what keeps it
// in the request — carrying it inside the summarized history would duplicate it.
//
// reason labels the compact:start / run:compacted event pair so the UI can tell
// a threshold compaction from the context-overflow recovery.
func (a *App) compactRunHistory(ctx context.Context, cfg ConfigState, sessionID, reason string, req ChatRequest, history []openai.ChatCompletionMessage, tokensBefore int) ([]openai.ChatCompletionMessage, map[string]any, error) {
	h := sanitizeHistoryMessages(history)
	if len(h) <= 2 {
		return nil, nil, errHistoryTooShortToCompact
	}
	a.emit("run:compact", map[string]any{"sessionId": sessionID, "tokensBefore": tokensBefore, "reason": reason})
	result, err := a.compactHistory(ctx, cfg, sessionID, "", h, tokensBefore)
	if err != nil {
		return nil, nil, err
	}
	a.mu.Lock()
	compacted := sanitizeHistoryMessages(a.histories[sessionID])
	a.mu.Unlock()
	messages := a.buildSystemContextMessages(sessionID, cfg, a.listCachedSkills())
	messages = append(messages, compacted...)
	if strings.TrimSpace(req.Message) != "" || len(req.Attachments) > 0 {
		messages = appendUserMessageWithAttachments(messages, req.Message, req.Attachments)
	}
	payload := map[string]any{
		"sessionId":    sessionID,
		"tokensBefore": intFromAny(result["tokensBefore"]),
		"tokensAfter":  intFromAny(result["tokensAfter"]),
		"reason":       reason,
	}
	if s, _ := result["summary"].(string); s != "" {
		payload["summary"] = s
	}
	return messages, payload, nil
}

// compactThresholdLimit returns the absolute token count at which
// auto-compaction triggers for the given config (context window × threshold).
func compactThresholdLimit(cfg ConfigState) int {
	maxCtx := cfg.ContextWindow
	if maxCtx <= 0 {
		maxCtx = 1000000
	}
	return int(float64(maxCtx) * clampCompactThreshold(cfg.CompactThreshold))
}

// compactHistory summarizes the given history with the model and replaces it
// with the summary. tokensBefore is the total context tokens before compaction.
func (a *App) compactHistory(ctx context.Context, cfg ConfigState, sessionID, instruction string, history []openai.ChatCompletionMessage, tokensBefore int) (map[string]any, error) {
	// The compaction LLM call runs inside this timeout. It is configurable
	// (Settings → General) because long-context summary requests through slow
	// providers can legitimately take minutes; the default covers the common
	// case without hanging forever. Cancellation through the parent context
	// (e.g. app shutdown) still works on top of the deadline.
	timeout := time.Duration(clampCompactTimeoutSeconds(cfg.CompactTimeoutSeconds)) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if len(history) == 0 {
		return nil, errors.New("no messages to compact")
	}

	// Summarize all existing history cleanly into a single summary.
	messagesToSummarize := history
	// Build compaction prompt. Structured sections maximize information density
	// and give the model concrete anchors to recover from after compaction.
	compactPrompt := `The conversation context is getting long and is being compacted. Provide a high-density, structured handoff summary so work can continue seamlessly after clearing history.

CRITICAL LANGUAGE RULE:
Always write the summary in the primary language used by the user in the conversation (e.g. Chinese if the user spoke Chinese, English if the user spoke English, etc.). The section headings below may be translated into the user's language or kept as equivalent clear headings.

REQUIREMENTS DRIFT RULES:
- If the messages above already contain a previous compaction summary (it appears as the first message: a structured Markdown summary), carry its "User Intent & Requirements" and "Constraints & Preferences" sections forward UNCHANGED. Only modify an entry when the user explicitly changed or refined it in the newer messages; then write the current effective version and note the change briefly (e.g. "changed by user: ...").
- Never shorten, merge, or drop existing requirements or constraints for the sake of brevity, and never resurrect a requirement the user has already superseded.
- State requirements as the CURRENT EFFECTIVE version: your synthesized understanding after all clarifications and corrections. Do not quote the user verbatim - users often phrase things loosely; the refined understanding is the requirement.
- When statements conflict, the user's latest instruction wins.

Use the following structure with Markdown headings:

## User Intent & Requirements / 用户需求与目标
The user's core intent, ongoing tasks, and explicit requirements, stated as the current effective version (see the drift rules above).

## Constraints & Preferences / 约束与偏好
One-off directives that are easy to lose and costly to forget: files or areas the user said NOT to touch, mandated approaches or tools, output style, workflow preferences. These must survive every compaction unchanged unless the user changed them.

## Findings & Analysis / 探索与分析结果
Key findings, root causes, architectural patterns, or logic flow discovered during investigation.

## What Has Been Done / 已完成工作
Bullet list of concrete actions taken: files edited, created, or deleted (with notes on changes), commands executed and their outcomes, verified items.

## Key Files & Locations / 关键文件与位置
Bullet list of key file paths referenced or touched, explicitly describing each file's specific role, responsibility, and what was modified:
- [path]: 具体用途与职责（该文件在项目中负责什么），以及本次对话中涉及的改动内容与关键位置


## Next Steps / 下一步工作
Exact, prioritized next steps to take immediately.

Rules:
- Strictly write in the user's language.
- Do not call any tools. Output plain text Markdown directly.
- Keep file paths, command strings, function names, and identifiers exact.
- Factual and concise. Do not invent details; state "unknown" if not certain.
- This summary replaces prior conversation and must stand alone completely.`

	if instruction != "" {
		compactPrompt += "\n\nAdditional instruction: " + instruction
	}

	// Build messages for the compaction call
	compactionMessages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: "You are an expert engineering assistant generating a structured context compaction summary. Output plain Markdown text directly. Do not call any tools. Always respond in the user's primary language."},
	}
	compactionMessages = append(compactionMessages, messagesToSummarize...)
	compactionMessages = append(compactionMessages, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: compactPrompt,
	})

	compactionMaxTokens := cfg.MaxTokens
	if compactionMaxTokens <= 0 {
		compactionMaxTokens = defaultMaxTokensForAPIFormat(cfg.APIFormat)
	}
	a.emit("compact:start", map[string]any{
		"sessionId":    sessionID,
		"tokensBefore": tokensBefore,
		"messages":     len(history),
		"timeoutMs":    int(timeout.Milliseconds()),
	})
	// The summary call runs on the user's own thinking configuration, untouched:
	// forcing a level here ("low" or a stop-thinking one) would silently diverge
	// from what Settings shows, and a stop-thinking field breaks models that
	// require thinking.
	summary, usage, err := a.completeModelTextWithUsage(ctx, cfg, cfg.Model, compactionMessages, compactionMaxTokens)
	if err != nil {
		return nil, fmt.Errorf("compaction failed: %w", err)
	}

	if strings.TrimSpace(summary) == "" {
		return nil, errors.New("compaction returned empty summary")
	}

	// Account the compaction LLM call in the workspace/token statistics so the
	// tokens spent summarizing are visible in the footer and the stats modal.
	fallbackInput := 0
	fallbackOutput := 0
	if usage == nil || usage.PromptTokens <= 0 {
		fallbackInput = estimateRequestTokens(compactionMessages, nil)
	}
	if usage == nil || usage.CompletionTokens <= 0 {
		fallbackOutput = estimateCompletionTokens(summary, "", nil)
	}
	a.recordWorkspaceTokenUsage(cfg.Workspace, usage, fallbackInput, fallbackOutput)
	fullSummary := summary

	// Replace history cleanly with just the compacted summary as a clean start.
	newHistory := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: fullSummary},
	}

	// The conversation was replaced by the summary: the provider measurement
	// covered turns that no longer exist.
	a.clearContextAnchor(sessionID)
	// The per-turn reasoning ledger followed those turns into history: every
	// entry is now unmatched dead weight, and clearing it here is the ledger's
	// single bounding path (appendTurn never trims, to keep the request
	// prefix — and with it the provider prompt cache — byte-stable).
	a.reasoningStash.clearSession(sessionID)
	a.saveHistory(sessionID, newHistory)

	tokensAfter := a.getContextBreakdown(sessionID, "").Total
	if tokensAfter <= 0 {
		tokensAfter = estimateTokensFromMessages(newHistory)
	}

	return map[string]any{
		"summary":      summary,
		"tokensBefore": tokensBefore,
		"tokensAfter":  tokensAfter,
	}, nil
}

// intFromAny converts a JSON-decoded (float64) or native (int) numeric value
// to int. Compaction results carry native ints for direct calls and float64
// once they cross the Wails JSON boundary.
func intFromAny(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

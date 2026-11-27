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

const (
	autoCompactMethodSummary = "summary"
	autoCompactMethodRecent  = "recent"

	// DefaultAutoCompactMethod is where an unset config lands, so the settings
	// form marks the method the app actually uses.
	DefaultAutoCompactMethod = autoCompactMethodRecent
)

func normalizeAutoCompactMethod(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), autoCompactMethodSummary) {
		return autoCompactMethodSummary
	}
	return autoCompactMethodRecent
}

const (
	CompactThresholdModeRatio     = "ratio"
	CompactThresholdModeTokens    = "tokens"
	DefaultCompactThresholdTokens = 300_000
	MaxCompactThresholdTokens     = 10_000_000
)

func normalizeCompactThresholdMode(mode string) string {
	if strings.EqualFold(strings.TrimSpace(mode), CompactThresholdModeTokens) {
		return CompactThresholdModeTokens
	}
	return CompactThresholdModeRatio
}

func clampCompactThresholdTokens(tokens int) int {
	if tokens <= 0 {
		return DefaultCompactThresholdTokens
	}
	return min(tokens, MaxCompactThresholdTokens)
}

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

// autoCompactAllowed reports whether the threshold tier may run another summary
// request for this session. A successful compaction rearms it; the overflow tier
// never consults it.
func (a *App) autoCompactAllowed(sessionID string) bool {
	return a.compactFailureCount(sessionID) < maxConsecutiveCompactFailures
}

// compactFailureCount / noteCompactFailure / clearCompactFailures are the only
// readers and writers of a.compactFailures. NewApp initializes it, which is what
// makes the write path safe while a.mu is held.
func (a *App) compactFailureCount(sessionID string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.compactFailures[sessionID]
}

func (a *App) noteCompactFailure(sessionID string) int {
	a.mu.Lock()
	a.compactFailures[sessionID]++
	failures := a.compactFailures[sessionID]
	a.mu.Unlock()
	return failures
}

func (a *App) clearCompactFailures(sessionID string) {
	a.mu.Lock()
	delete(a.compactFailures, sessionID)
	a.mu.Unlock()
}

// CompactSession uses the same full-history LLM summary as automatic compaction.
// overlay carries the caller's model selection (the GUI passes the active Tab's
// model, exactly the ConfigState StartChat receives); it may be empty for
// callers without Tab context, such as the local HTTP API.
func (a *App) CompactSession(sessionID, instruction string, overlay ConfigState) (map[string]any, error) {
	parent := a.ctx
	if parent == nil {
		parent = context.Background()
	}
	return a.compactSession(parent, sessionID, instruction, overlay)
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

// compactSessionConfig decides which model the summary call runs on.
//
// Precedence:
//  1. the caller's overlay: for the GUI this is the active Tab's model, the same
//     ConfigState StartChat sends — the only source that is right for a session
//     restored from disk, which has no frozen record and no messages yet.
//  2. the session's frozen StartChat record, for callers that send no overlay
//     (the local HTTP API has no Tab context) and for sessions that already ran
//     a turn in this process.
//  3. the config's last-used model (lastUsedModel expanded by effectiveConfig),
//     for a session that never ran in this process.
func (a *App) compactSessionConfig(sessionID string, overlay ConfigState) (ConfigState, error) {
	if strings.TrimSpace(overlay.Model) != "" {
		// mergeConfig is the single request-overlay choke point: an overlay field
		// the caller left empty keeps the effective (last-used) value.
		return a.effectiveConfig(overlay), nil
	}
	if err := a.ensureInitialized(); err != nil {
		return ConfigState{}, err
	}
	return a.sessionModelConfigFor(sessionID).apply(a.effectiveConfigSafe()), nil
}

func (a *App) compactSession(parent context.Context, sessionID, instruction string, overlay ConfigState) (map[string]any, error) {
	return a.compactSessionWithMode(parent, sessionID, instruction, overlay, false)
}

// FastCompactSession is the manual local-only entry and never escalates to an
// LLM summary. The threshold path reuses this strategy when configured; either
// rewrite invalidates the provider's cached prefix from the first changed item.
func (a *App) FastCompactSession(sessionID string) (map[string]any, error) {
	parent := a.ctx
	if parent == nil {
		parent = context.Background()
	}
	return a.compactSessionWithMode(parent, sessionID, "", ConfigState{}, true)
}

func (a *App) compactSessionWithMode(parent context.Context, sessionID, instruction string, overlay ConfigState, fast bool) (map[string]any, error) {
	if err := a.ensureInitialized(); err != nil {
		return nil, err
	}
	var cfg ConfigState
	if !fast {
		var err error
		cfg, err = a.compactSessionConfig(sessionID, overlay)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(cfg.Model) == "" {
			return nil, errors.New("model is required")
		}
		if !hasModelCredential(cfg) {
			return nil, missingModelCredentialError()
		}
	}
	// A live chat run may rewrite history concurrently (auto-compaction,
	// sanitize repair, new turns); compacting on top of it would interleave
	// saveHistory calls and race the run's own message list.
	a.mu.Lock()
	if a.stopping {
		a.mu.Unlock()
		return nil, errAppStopping
	}
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
		return nil, errSessionCompacting
	}
	// The summary LLM call runs on a cancellable child of the app context so
	// CancelCompaction (ESC) can abort it mid-flight.
	ctx, cancel := context.WithCancel(parent)
	a.compactingSessions[sessionID] = struct{}{}
	a.compactingCancels[sessionID] = cancel
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.compactingSessions, sessionID)
		delete(a.compactingCancels, sessionID)
		a.mu.Unlock()
		cancel()
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

	if fast {
		return a.fastCompactHistory(ctx, sessionID, history, tokensBefore)
	}
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

// maxConsecutiveCompactFailures is the threshold tier's circuit breaker; it
// matches ZCode's MAX_CONSECUTIVE_AUTOCOMPACT_FAILURES. The threshold decision
// runs at the top of every agent step, so once a summary request fails for a
// reason that does not fix itself (provider rejection, truncated summary,
// timeout) an unbounded trigger pays for a fresh long-context summary request on
// every remaining step of the same run. Overflow recovery is deliberately
// exempt: it runs after the provider already rejected the request, so it is the
// last resort and must stay available.
const maxConsecutiveCompactFailures = 3

// errAutoCompactCircuitOpen is the threshold tier's "we already tried and failed
// maxConsecutiveCompactFailures times" outcome. It is a skip, not an event: every
// failure that opened the breaker was already reported to the UI once.
var errAutoCompactCircuitOpen = errors.New("auto compaction paused after repeated failures")

// isCompactSkip classifies the threshold tier's non-events: a history too short
// to summarize is a healthy no-op, and an open breaker is the deliberate stop
// above. Both must stay out of the UI error path.
func isCompactSkip(err error) bool {
	return errors.Is(err, errHistoryTooShortToCompact) || errors.Is(err, errAutoCompactCircuitOpen)
}

// compactRunHistory selects the threshold strategy and rebuilds the request.
// A full summary replaces the current user turn, so only that strategy needs
// to reappend it. Local compaction preserves the current task and replaces only
// earlier protocol turns with user-role reference records.
//
// reason labels the compact:start / run:compacted event pair so the UI can tell
// a threshold compaction from the context-overflow recovery.
func (a *App) compactRunHistory(ctx context.Context, cfg ConfigState, sessionID, reason string, req ChatRequest, history []openai.ChatCompletionMessage, tokensBefore int) ([]openai.ChatCompletionMessage, map[string]any, error) {
	h, candidate, err := prepareRunCompaction(cfg, reason, a.autoCompactAllowed(sessionID), history, func(h []openai.ChatCompletionMessage) (*fastCompactCandidate, error) {
		return a.prepareFastCompactHistory(ctx, sessionID, h, tokensBefore)
	})
	if err != nil {
		return nil, nil, err
	}
	if candidate != nil {
		result := a.commitFastCompactHistory(sessionID, h, candidate)
		a.clearCompactFailures(sessionID)
		compacted := a.loadSessionHistoryCopy(sessionID)
		messages := a.buildSystemContextMessages(sessionID, cfg, a.listCachedSkillsForConfig(cfg))
		messages = append(messages, compacted...)
		result["sessionId"] = sessionID
		result["reason"] = reason
		return messages, result, nil
	}
	a.emit("run:compact", map[string]any{"sessionId": sessionID, "tokensBefore": tokensBefore, "reason": reason})
	result, err := a.compactHistory(ctx, cfg, sessionID, "", h, tokensBefore)
	if err != nil {
		// 失败在唯一一处记账（阈值档与溢出档共用这条路径）：计数到上限说明这套历史
		// 压不动，继续尝试只会重复烧钱。
		if failures := a.noteCompactFailure(sessionID); failures == maxConsecutiveCompactFailures {
			a.logAppError("auto compaction paused after repeated failures", "session", sessionID, "failures", failures)
		}
		return nil, nil, err
	}
	// 成功即复位：历史已被这次压缩改写，之前“压不动”的结论不再成立。
	a.clearCompactFailures(sessionID)
	a.mu.Lock()
	compacted := sanitizeHistoryMessages(a.histories[sessionID])
	a.mu.Unlock()
	messages := a.buildSystemContextMessages(sessionID, cfg, a.listCachedSkillsForConfig(cfg))
	messages = append(messages, compacted...)
	if strings.TrimSpace(req.Message) != "" || len(req.Attachments) > 0 {
		messages = appendUserMessageWithAttachments(messages, req.Message, req.Attachments)
	}
	a.beginHistoryTask(sessionID, messages)
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

// prepareRunCompaction selects a rewrite without changing history. Fast
// compaction protects the entire current task; a sub-agent only has one task,
// so it has no older turns to offer the fast projector and falls back to a summary.
func prepareRunCompaction(cfg ConfigState, reason string, summaryAllowed bool, history []openai.ChatCompletionMessage, fast func([]openai.ChatCompletionMessage) (*fastCompactCandidate, error)) ([]openai.ChatCompletionMessage, *fastCompactCandidate, error) {
	preferFast := reason == compactReasonThreshold && normalizeAutoCompactMethod(cfg.AutoCompactMethod) == autoCompactMethodRecent
	if reason == compactReasonThreshold && !summaryAllowed && (!preferFast || fast == nil) {
		return nil, nil, errAutoCompactCircuitOpen
	}
	h := sanitizeHistoryMessages(history)
	if preferFast && fast != nil {
		candidate, err := fast(h)
		if err != nil {
			return nil, nil, err
		}
		result := candidate.result
		if candidate.messageChange && fastCompactLeavesLessThanHalf(intFromAny(result["tokensBefore"]), intFromAny(result["tokensAfter"])) {
			return h, candidate, nil
		}
	}
	if reason == compactReasonThreshold && !summaryAllowed {
		return nil, nil, errAutoCompactCircuitOpen
	}
	// Unlike summary-only mode, fast fallback can rescue one or two very long messages.
	if len(h) == 0 || (len(h) <= 2 && !preferFast) {
		return nil, nil, errHistoryTooShortToCompact
	}
	return h, nil, nil
}

func shouldRecoverContextOverflow(err error, emitted, recovered bool) bool {
	return !emitted && !recovered && classifyLLMError(err) == llmErrorKindContextTooLong
}

func compactionTimeout(cfg ConfigState) time.Duration {
	return time.Duration(clampCompactTimeoutSeconds(cfg.CompactTimeoutSeconds)) * time.Second
}

// compactThresholdLimit returns the absolute token count at which
// auto-compaction triggers, using only the selected mode's threshold.
func compactThresholdLimit(cfg ConfigState) int {
	if normalizeCompactThresholdMode(cfg.CompactThresholdMode) == CompactThresholdModeTokens {
		return clampCompactThresholdTokens(cfg.CompactThresholdTokens)
	}
	maxCtx := cfg.ContextWindow
	if maxCtx <= 0 {
		maxCtx = 1000000
	}
	return int(float64(maxCtx) * clampCompactThreshold(cfg.CompactThreshold))
}

// compactHistory summarizes the given history with the model and replaces it
// with the summary. tokensBefore is the total context tokens before compaction.
func (a *App) compactHistory(ctx context.Context, cfg ConfigState, sessionID, instruction string, history []openai.ChatCompletionMessage, tokensBefore int) (map[string]any, error) {
	// Exactly one completion event per compaction, emitted from the single place
	// both the manual and the automatic path run through: the frontend closes its
	// live summary bubble on it, and ok separates a cancelled or failed partial
	// summary from a finished one.
	done := false
	defer func() {
		a.emit("compact:done", map[string]any{"sessionId": sessionID, "ok": done})
	}()

	if len(history) == 0 {
		return nil, errors.New("no messages to compact")
	}

	a.emit("compact:start", map[string]any{
		"sessionId": sessionID, "tokensBefore": tokensBefore, "messages": len(history),
		"timeoutMs": int(compactionTimeout(cfg).Milliseconds()),
	})
	deltaEmitter := newCompactDeltaEmitter(sessionID, a.emitMap())
	fullSummary, usage, err := a.summarizeCompactHistory(ctx, cfg, instruction, history, deltaEmitter.add)
	// Remaining coalesced deltas must reach the UI before compact:done.
	deltaEmitter.flush()
	if err != nil {
		return nil, err
	}

	// Account the compaction LLM call in the workspace/token statistics so the
	// tokens spent summarizing are visible in the footer and the stats modal.
	fallbackOutput := 0
	if usage == nil || usage.CompletionTokens <= 0 {
		fallbackOutput = estimateCompletionTokens(fullSummary, "", nil)
	}
	// The summary call is ordinary spend by the same workspace, so it goes through
	// the same accounting entry point as the chat loop and sub-agents: its cost is
	// visible in the footer total and in the token dashboard, which is where a user
	// looks for what a compaction actually cost.
	a.recordLLMUsage(cfg.Model, cfg.Workspace, usage, fallbackOutput)

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
	// The prefix snapshots (system prompt, workspace map, tool schemas) are the last
	// thing frozen per session, and this is the one moment rebuilding them costs
	// nothing: the history rewrite above already broke the provider cache. Dropping
	// them here is what makes AGENTS.md / CODEGRAPH.md / LESSONS.md / USER.md edits
	// made during the conversation visible to its next request.
	a.refreshSessionPromptPrefix(sessionID)
	a.saveHistory(sessionID, newHistory)
	// The summary replaced the previous turns, so the next request must
	// establish a fresh prefix baseline.
	a.invalidateSessionContextLock(sessionID)

	tokensAfter := a.getContextBreakdown(sessionID, "").Total
	if tokensAfter <= 0 {
		tokensAfter = estimateTokensFromMessages(newHistory)
	}

	done = true
	// The reported summary is the rewritten history itself (pinned plan snapshot
	// included), not the raw model text: the UI replaces the transcript with this
	// string, so the two must not drift apart.
	return map[string]any{
		"tier":         "summary",
		"summary":      fullSummary,
		"tokensBefore": tokensBefore,
		"tokensAfter":  tokensAfter,
	}, nil
}

// summarizeCompactHistory is shared by main sessions and sub-agents. Only its
// caller decides where to commit the summary and which replay state to reset.
func (a *App) summarizeCompactHistory(ctx context.Context, cfg ConfigState, instruction string, history []openai.ChatCompletionMessage, onDelta func(string, string)) (string, *modelUsage, error) {
	ctx, cancel := context.WithTimeout(ctx, compactionTimeout(cfg))
	defer cancel()
	// This request has its own system message, not the original automatic prefix.
	ctx = context.WithValue(ctx, automaticContextPrefixKey{}, 0)

	// Build a continuation-focused handoff: preserve effective requirements and
	// actionable state without carrying forward the investigation transcript.
	compactPrompt := `The conversation context is getting long and is being compacted. Write a concise, structured handoff that lets the next assistant continue the work from its current state after the conversation history is replaced.

LANGUAGE AND OUTPUT:
- Write in the user's primary language, including the headings. Use the seven sections below, in order, with short factual bullets. If a section has no relevant information, say "None" in the user's language; do not invent content to fill it.
- Output only the Markdown summary. Do not call tools, answer the user's task, or continue the work.

CONTINUITY AND REQUIREMENTS:
- Use any previous compaction summary as the baseline and update it with the newer messages, even if it used different headings. Preserve every still-effective goal, requirement, and user constraint across repeated compactions; do not silently weaken or drop them for brevity.
- Express the current effective requirements after all clarifications. The user's latest explicit correction supersedes an older conflicting instruction; a follow-up question or status request does not by itself cancel unfinished work. Do not revive superseded requirements or recount the history of their changes unless it affects the next action.
- Keep exact wording where it affects scope, prohibitions, acceptance criteria, or permission. Distinguish user requirements from assistant proposals, confirmed facts from hypotheses, and planned actions from executed or verified actions.

## Goal / 目标
The active objective, expected deliverable, and explicit acceptance criteria. Include other unfinished user requests that remain in scope; make clear which task is currently active.

## User Constraints / 用户约束
Still-effective scope limits, prohibited actions or files, required approaches or tools, workflow and output preferences, and authorization boundaries. Preserve pending approvals and unanswered questions; do not turn a proposal or silence into user consent.

## Key Decisions / 关键决策
Decisions already made and the brief reason each matters for continuation. Include confirmed findings or root causes only when they determine the approach. Label tentative choices as tentative. Mention rejected approaches only when needed to prevent repeating a consequential mistake.

## Completed Work / 已完成事项
Concrete results that still matter: implemented changes, produced artifacts, and validation actually performed with its outcome. Attach necessary file paths or identifiers to the relevant item. Clearly distinguish implemented, tested, committed, and deployed states; never claim an unrun check passed.
Identify this task's changed files or artifacts and any partial, unverified, uncommitted, or pending-cleanup state that matters for continuation. Distinguish known pre-existing user changes that must not be overwritten; mark ownership as unknown when not established.

## Current State / 当前进度
Where work stopped: the active step, partial or unverified changes, unresolved questions, blockers, and any running operation or result still awaited. Preserve exact task, session, or process identifiers only when needed to resume. If the objective is complete, say so rather than inventing more work.
Preserve essential user-provided data, examples, reproduction conditions, decision-driving facts, and their known sources. For large material that can be reread, keep precise file, symbol, or artifact references instead of copying it; retain critical information that cannot be recovered, except secrets or credentials.
For running operations or uncertain outcomes, include the last observed state, how to check status, and the completion condition when known. Note side effects that may already have occurred; if safe retry has not been established, say so and require checking the outcome before repeating the operation.

## Next Steps / 待办
Prioritized remaining actions. When interrupted mid-task, make the first item the concrete next action, with the necessary path, command, identifier, or decision to resolve. Include required verification and pending user input. Do not use vague instructions such as "continue as planned".
If a plan tool was used, its latest available result is appended automatically after this summary. Do not copy the full checklist here: retain the immediate next action and essential details, corrections, or remaining work the snapshot does not capture. If no plan is available, this section must carry the actionable remaining plan itself. Do not treat a stale plan entry as overriding newer user instructions or observed results.

## Critical Errors / 关键错误
Only failures that still block progress or are important to avoid repeating. For each, preserve the trigger, the essential error text or code, the confirmed cause (or explicitly unknown), attempted remedies and their outcomes, and whether it remains unresolved. Omit incidental failures that were resolved and no longer affect continuation.

INFORMATION BUDGET:
- Keep only information needed to continue correctly. Remove repeated explanations, chronological narration, raw tool output, large code excerpts, exhaustive command logs, and standalone file inventories. Put necessary paths, symbols, commands, and findings in the relevant section once.
- Compress background and completed work before sacrificing effective requirements, user constraints, the current stopping point, or actionable next steps. Prefer a short summary, but do not impose an arbitrary length limit that loses critical state.
- Keep paths, commands, function names, error codes, and identifiers exact when included. Do not include secret values or credentials.
- Do not infer success, root causes, permissions, or missing details. Mark consequential uncertainty explicitly.
- The summary and any automatically appended plan snapshot replace prior conversation; they must provide enough context to resume without restarting completed exploration.`

	if instruction != "" {
		compactPrompt += "\n\nAdditional instruction: " + instruction
	}

	// Build messages for the compaction call
	compactionMessages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: "You are an expert engineering assistant generating a structured context compaction summary. Output plain Markdown text directly. Do not call any tools. Always respond in the user's primary language."},
	}
	compactionMessages = append(compactionMessages, history...)
	compactionMessages = append(compactionMessages, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: compactPrompt,
	})

	compactionMaxTokens := cfg.MaxTokens
	if compactionMaxTokens <= 0 {
		compactionMaxTokens = defaultMaxTokensForAPIFormat(cfg.APIFormat)
	}
	summary, usage, err := a.streamModelTextWithUsage(perfReason(ctx, "compaction"), cfg, cfg.Model, compactionMessages, compactionMaxTokens, onDelta)
	if err != nil {
		// The model and endpoint belong in the error: the transport error only
		// names the URL, which is exactly what the user cannot map back to a
		// model when the request goes to an endpoint they never selected.
		return "", nil, fmt.Errorf("compaction failed with model %s at %s: %w", cfg.Model, cfg.BaseURL, err)
	}

	if strings.TrimSpace(summary) == "" {
		return "", nil, errors.New("compaction returned empty summary")
	}

	// The plan tool's steps live in their tool result, so a full-history rewrite
	// would replace the agent's structured task state with a prose sentence. Pin
	// the latest one verbatim after the summary: it is written once, here, so it
	// stays byte-stable in every later request (re-injecting a plan snapshot per
	// turn is what would break the request prefix and the cache behind it).
	fullSummary := summary
	if plan := latestPlanSnapshot(history); plan != "" {
		fullSummary = strings.TrimRight(fullSummary, "\n") + "\n\n" + planSnapshotTagOpen + "\n" + plan + "\n" + planSnapshotTagClose
	}

	return fullSummary, usage, nil
}

// planToolName is the built-in tool whose result carries the agent's plan.
const planToolName = "plan"

const (
	planSnapshotTagOpen  = "<ally-plan-snapshot>"
	planSnapshotTagClose = "</ally-plan-snapshot>"
)

// latestPlanSnapshot returns the rendered result of the most recent plan tool
// call in the history being summarized, or "" when the conversation never used
// it. The plan is only addressable through the tool result that produced
// it, so the call ids have to be collected from the assistant turns first.
func latestPlanSnapshot(messages []openai.ChatCompletionMessage) string {
	planCallIDs := make(map[string]struct{})
	for _, m := range messages {
		if m.Role != openai.ChatMessageRoleAssistant {
			continue
		}
		for _, tc := range m.ToolCalls {
			if strings.EqualFold(strings.TrimSpace(tc.Function.Name), planToolName) {
				planCallIDs[tc.ID] = struct{}{}
			}
		}
	}
	if len(planCallIDs) == 0 {
		return ""
	}
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m.Role != openai.ChatMessageRoleTool {
			continue
		}
		if _, ok := planCallIDs[m.ToolCallID]; !ok {
			continue
		}
		content := strings.TrimSpace(m.Content)
		// Placeholders carry no plan state; keep looking further back.
		if content == "" || content == toolResultPlaceholder {
			continue
		}
		return content
	}
	return ""
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

// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// GetTodos returns the current todo list for a session.
func (a *App) GetTodos(sessionID string) []TodoEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	list := a.todos[sessionID]
	if list == nil {
		return []TodoEntry{}
	}
	return cloneTodos(list)
}

// ClearTodos clears the current todo list for a session.
func (a *App) ClearTodos(sessionID string) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return
	}
	a.mu.Lock()
	delete(a.todos, sid)
	a.todoRevisions[sid]++
	revision := a.todoRevisions[sid]
	a.mu.Unlock()
	a.emitTodoUpdate(sid, []TodoEntry{}, revision)
}

// emitTodoUpdate sends the current todo list to the frontend.
func (a *App) emitTodoUpdate(sid string, todos []TodoEntry, revision int64) {
	a.emit("plan:update", map[string]any{
		"sessionId": sid,
		"todos":     cloneTodos(todos),
		"revision":  revision,
	})
}

// ContextBreakdown breaks down estimated token usage by category.
type ContextBreakdownPart struct {
	Label  string `json:"label"`
	Tokens int    `json:"tokens"`
}

// ContextBreakdown breaks down the token usage of a session's next request by
// category.
//
// Total is the authoritative request size: it equals Estimated (the sum of the
// categories below) until the provider reports a real prompt usage for the
// session. From then on the reported size replaces the estimate for everything
// it covered — the request prefix as well as the messages it counted — and
// Total becomes MeasuredTokens + TrailingTokens. The category fields stay
// estimates in both cases, so they may not add up to Total once a measurement
// applies.
type ContextBreakdown struct {
	Total             int                    `json:"total"`
	Estimated         int                    `json:"estimated"`
	SystemPrompt      int                    `json:"systemPrompt"`
	SystemPromptParts []ContextBreakdownPart `json:"systemPromptParts,omitempty"`
	ToolSchemas       int                    `json:"toolSchemas"`
	UserMessages      int                    `json:"userMessages"`
	AssistantMsgs     int                    `json:"assistantMsgs"`
	ToolResults       int                    `json:"toolResults"`
	Reasoning         int                    `json:"reasoning"`
	// MeasuredTokens is the provider-reported size (prompt + completion) of the
	// session's most recent request, covering the request prefix and the first
	// MeasuredMessages conversation messages. Zero means no measurement is
	// available yet and Total is a pure text estimate.
	MeasuredTokens int `json:"measuredTokens,omitempty"`
	// MeasuredMessages is the number of conversation messages that measurement
	// covers. It counts persisted messages only: the system prompt and workspace
	// map are re-prepared for every request and never stored, so counting them
	// would make the measurement depend on which list it is resolved against.
	MeasuredMessages int `json:"measuredMessages,omitempty"`
	// TrailingTokens estimates the conversation messages added after the
	// measured request.
	TrailingTokens int `json:"trailingTokens,omitempty"`
}

// WorkspaceTokenUsage is the cumulative input/output token usage for a workspace.
type WorkspaceTokenUsage struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
}

// GetContextBreakdown returns detailed token usage breakdown for a session.
// workspaceHint is an optional override from the caller (the frontend passes
// the active Tab's workspace). It wins over the in-memory run record and the
// session index: KB/temp sessions have neither before their first run, and
// the footer polls breakdowns right at tab-switch time — before any request
// has been made. An empty hint keeps the session-based resolution.
func (a *App) GetContextBreakdown(sessionID, workspaceHint string) ContextBreakdown {
	return a.getContextBreakdown(sessionID, workspaceHint)
}

// GetWorkspaceTokenUsage returns cumulative usage for the current app run.
func (a *App) GetWorkspaceTokenUsage(workspace string) WorkspaceTokenUsage {
	key := workspaceUsageKey(workspace)
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.workspaceTokenUsage[key]
}

// GetSessionContextTokens returns the estimated token count for a session's full payload.
func (a *App) GetSessionContextTokens(sessionID string) int {
	return a.getContextBreakdown(sessionID, "").Total
}

// ResetWorkspaceTokenUsage resets cumulative token usage for a workspace.
func (a *App) ResetWorkspaceTokenUsage(workspace string) {
	key := workspaceUsageKey(workspace)
	a.mu.Lock()
	delete(a.workspaceTokenUsage, key)
	delete(a.lastEstimatedTokens, key)
	a.mu.Unlock()
	a.emit("tokens:reset", map[string]any{"workspace": workspace})
}

func (a *App) recordWorkspaceTokenUsage(workspace string, usage *modelUsage, fallbackInput, fallbackOutput int) {
	input := 0
	output := fallbackOutput
	if usage != nil {
		if usage.PromptTokens > 0 {
			input = usage.PromptTokens
		}
		if usage.CompletionTokens > 0 {
			output = usage.CompletionTokens
		}
	}
	// If the provider did not return real prompt usage, do not add the full
	// estimated request size: it includes the entire retained context and makes
	// the footer cumulative input counter jump by thousands on tiny prompts.
	_ = fallbackInput
	if input <= 0 && output <= 0 {
		return
	}

	key := workspaceUsageKey(workspace)
	a.mu.Lock()

	total := a.workspaceTokenUsage[key]
	total.InputTokens += input
	total.OutputTokens += output
	a.workspaceTokenUsage[key] = total
	a.mu.Unlock()

	a.emit("tokens:update", map[string]any{
		"workspace":    workspace,
		"inputTokens":  total.InputTokens,
		"outputTokens": total.OutputTokens,
	})
}

func workspaceUsageKey(workspace string) string {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return "__default__"
	}
	if abs, err := filepath.Abs(workspace); err == nil {
		workspace = abs
	}
	workspace = filepath.Clean(workspace)
	if goruntime.GOOS == "windows" {
		workspace = strings.ToLower(workspace)
	}
	return workspace
}

func estimateTokensFromText(text string) int {
	if text == "" {
		return 0
	}
	asciiCount := 0
	nonAsciiCount := 0
	for _, r := range text {
		if r <= 127 {
			asciiCount++
		} else {
			nonAsciiCount++
		}
	}
	// In real-world LLM tokenizers (o200k, cl100k, Claude, Qwen/DeepSeek):
	// - Code/JSON/indents/punctuation: ~3.0 - 3.2 ASCII chars per token (down from 4).
	// - Chinese / CJK characters: typically 1.3 - 1.6 tokens per character (up from 1.0).
	return int(math.Ceil(float64(asciiCount)/3.2)) + int(math.Ceil(float64(nonAsciiCount)*1.4))
}

func estimateRequestTokens(messages []openai.ChatCompletionMessage, tools []openai.Tool) int {
	total := 0
	for _, m := range messages {
		total += estimateTokensFromText(m.Role)
		total += estimateMessageBodyTokens(m)
		total += estimateTokensFromText(m.Name)
		total += estimateTokensFromText(m.ToolCallID)
		if m.ReasoningContent != "" {
			total += estimateTokensFromText(m.ReasoningContent)
		}
		for _, tc := range m.ToolCalls {
			total += estimateTokensFromText(tc.ID)
			total += estimateTokensFromText(string(tc.Type))
			total += estimateTokensFromText(tc.Function.Name)
			total += estimateTokensFromText(tc.Function.Arguments)
		}
	}
	total += estimateToolSchemaTokens(tools)
	return total
}

// builtinToolSchemaTokens caches the token estimate of the built-in tool
// list (chatTools()), which is static across the process lifetime. MCP tools
// change over time and must be re-marshaled per call; their contribution is
// added separately. Combined with chatToolsCache, this avoids re-marshaling
// the large static schema (typically 5-15KB JSON) on every getContextBreakdown
// call — the context popover refresh is user-visible and was hitting this
// path multiple times per second during a run.
var builtinToolSchemaTokens = sync.OnceValue(func() int {
	tools := chatTools()
	if len(tools) == 0 {
		return 0
	}
	data, _ := json.Marshal(tools)
	return estimateTokensFromText(string(data))
})

// mcpToolSchemaTokens re-marshals the MCP tool schemas portion of `tools`.
// `tools` is expected to be a slice returned by buildToolsWithMcp; only the
// entries whose Function.Name starts with `mcp__` are counted, so the cached
// built-in portion can be added by the caller without double counting.
func mcpToolSchemaTokens(tools []openai.Tool) int {
	if len(tools) == 0 {
		return 0
	}
	// Fast path: if no MCP tools, skip Marshal entirely.
	hasMcp := false
	for _, t := range tools {
		if t.Function != nil && strings.HasPrefix(t.Function.Name, "mcp__") {
			hasMcp = true
			break
		}
	}
	if !hasMcp {
		return 0
	}
	// Slow path: marshal only the MCP entries. Avoids re-marshaling the
	// large built-in schema that is already accounted for by the cache.
	mcpOnly := make([]openai.Tool, 0, 8)
	for _, t := range tools {
		if t.Function != nil && strings.HasPrefix(t.Function.Name, "mcp__") {
			mcpOnly = append(mcpOnly, t)
		}
	}
	if len(mcpOnly) == 0 {
		return 0
	}
	data, _ := json.Marshal(mcpOnly)
	return estimateTokensFromText(string(data))
}

func estimateToolSchemaTokens(tools []openai.Tool) int {
	if len(tools) == 0 {
		return 0
	}
	// Split into builtin (non-mcp__) and mcp portions. The builtin portion
	// uses the cache only when it is the full chatTools() set — a filtered
	// builtin slice must be re-marshaled to avoid over-counting the cached
	// full-set tokens.
	cachedBuiltinCount := len(chatTools())
	builtinCount := 0
	hasMcp := false
	for _, t := range tools {
		if t.Function != nil && strings.HasPrefix(t.Function.Name, "mcp__") {
			hasMcp = true
			continue
		}
		builtinCount++
	}
	var total int
	if builtinCount == cachedBuiltinCount {
		// Full builtin set — use the cached estimate.
		total = builtinToolSchemaTokens()
	} else if builtinCount > 0 {
		// Filtered builtin subset — marshal directly.
		builtinOnly := make([]openai.Tool, 0, builtinCount)
		for _, t := range tools {
			if t.Function != nil && !strings.HasPrefix(t.Function.Name, "mcp__") {
				builtinOnly = append(builtinOnly, t)
			}
		}
		data, _ := json.Marshal(builtinOnly)
		total = estimateTokensFromText(string(data))
	}
	if hasMcp {
		total += mcpToolSchemaTokens(tools)
	}
	return total
}

// finalizeContextBreakdownTotal recomputes the category sum and the
// authoritative total. A provider measurement wins over the text estimate:
// MeasuredTokens already covers the request prefix (system prompt, workspace
// map, plan snapshot) and the messages it counted, so only the messages added
// after that request are estimated on top (pi: estimateContextTokens,
// packages/ai/src/utils/estimate.ts).
func finalizeContextBreakdownTotal(result *ContextBreakdown) {
	result.Estimated = result.SystemPrompt + result.ToolSchemas + result.UserMessages + result.AssistantMsgs + result.ToolResults + result.Reasoning
	result.Total = result.Estimated
	if result.MeasuredTokens > 0 {
		result.Total = result.MeasuredTokens + result.TrailingTokens
	}
}

func estimateMessageBodyTokens(m openai.ChatCompletionMessage) int {
	total := estimateTokensFromText(m.Content)
	for _, part := range m.MultiContent {
		switch part.Type {
		case openai.ChatMessagePartTypeText:
			total += estimateTokensFromText(part.Text)
		case openai.ChatMessagePartTypeImageURL:
			// Image parts are charged by pixel-derived provider pricing, not by
			// base64 bytes: a 1568x1568 screenshot is roughly (1568*1568)/750 ≈ 3200
			// tokens and a typical 1024x768 view lands near 1000-1300, so a fixed
			// ~2000-token constant (validated in kimicode as MEDIA_TOKEN_ESTIMATE)
			// is within ~2x of reality. The old 256 value undercounted 6-8x, which
			// skewed the footer context percentage and delayed auto-compaction in
			// image-heavy sessions. This is a hot path (called per message per
			// step), so decoding images for exact dimensions is not an option.
			total += 2000
		default:
			total += estimateTokensFromText(part.Text)
		}
	}
	return total
}

func estimateCompletionTokens(content, reasoning string, toolCalls []openai.ToolCall) int {
	total := estimateTokensFromText(content) + estimateTokensFromText(reasoning)
	for _, tc := range toolCalls {
		total += estimateTokensFromText(tc.ID)
		total += estimateTokensFromText(string(tc.Type))
		total += estimateTokensFromText(tc.Function.Name)
		total += estimateTokensFromText(tc.Function.Arguments)
	}
	return total
}

// sessionWorkspaceOverride is the per-session workspace resolution used to
// align the footer breakdown with the config a real request would use.
type sessionWorkspaceOverride struct {
	workspace  string
	extraRoots []string
}

// sessionWorkspaceOverridesCache memoizes the session index → workspace
// resolution. getContextBreakdown is polled by the footer (debounced 120ms
// during runs); re-reading and re-unmarshaling index.json on every call is
// avoidable work. TTL is short so an updated session workspace shows up
// quickly after SaveSessionIndex.
var sessionWorkspaceOverridesCache = struct {
	sync.Mutex
	generatedAt time.Time
	overrides   map[string]sessionWorkspaceOverride
}{}

const sessionWorkspaceOverridesTTL = 2 * time.Second

// sessionContextConfig resolves the config a request for this session would
// actually use. The footer breakdown used to sample a.config (the active
// Tab's workspace), so a session running in a background Tab — or a KB/temp
// Tab whose workspace never claims config.workspace — was counted against
// the wrong workspace's AGENTS.md / CODEGRAPH / lessons / workspace map.
// Resolution order mirrors StartChat's effectiveConfig:
//  1. the caller's workspace hint (UI knows the Tab's exact workspace, even
//     before the session's first run),
//  2. the in-memory record written by StartChat (what this session's runs
//     actually used),
//  3. the session index entry's persisted workspace,
//  4. a.config as fallback for sessions recorded before Workspace persisted.
func (a *App) sessionContextConfig(sessionID string, workspaceHint string) ConfigState {
	sessionID = strings.TrimSpace(sessionID)
	a.mu.Lock()
	cfg := a.config
	if sessionID != "" && a.sessionWorkspaces != nil {
		if ws, ok := a.sessionWorkspaces[sessionID]; ok && strings.TrimSpace(ws) != "" {
			cfg.Workspace = ws
		}
	}
	a.mu.Unlock()

	// The UI hint wins over both the in-memory run record and the session
	// index: the frontend knows the Tab's exact workspace even before the
	// session's first run (KB/temp sessions have no index entry yet).
	if hint := strings.TrimSpace(workspaceHint); hint != "" {
		if override, ok := a.lookupSessionWorkspaceOverride(sessionID); ok {
			cfg.ExtraRoots = cloneStringSlice(override.extraRoots)
		}
		cfg.Workspace = hint
		return cfg
	}
	return a.sessionContextFromOverride(sessionID, cfg)
}

// sessionContextFromOverride applies the session-index workspace override on
// top of the given base config when one is recorded.
func (a *App) sessionContextFromOverride(sessionID string, cfg ConfigState) ConfigState {
	if sessionID == "" || a.sessionsDir == "" {
		return cfg
	}

	override, ok := a.lookupSessionWorkspaceOverride(sessionID)
	if !ok || strings.TrimSpace(override.workspace) == "" {
		return cfg
	}
	cfg.Workspace = override.workspace
	cfg.ExtraRoots = cloneStringSlice(override.extraRoots)
	return cfg
}

func (a *App) lookupSessionWorkspaceOverride(sessionID string) (sessionWorkspaceOverride, bool) {
	sessionWorkspaceOverridesCache.Lock()
	if time.Since(sessionWorkspaceOverridesCache.generatedAt) < sessionWorkspaceOverridesTTL {
		override, ok := sessionWorkspaceOverridesCache.overrides[sessionID]
		sessionWorkspaceOverridesCache.Unlock()
		return override, ok
	}
	sessionWorkspaceOverridesCache.Unlock()

	a.sessionMu.Lock()
	entries, err := a.readSessionIndexLocked()
	a.sessionMu.Unlock()
	if err != nil {
		return sessionWorkspaceOverride{}, false
	}
	overrides := make(map[string]sessionWorkspaceOverride, len(entries))
	for _, entry := range entries {
		overrides[entry.ID] = sessionWorkspaceOverride{
			workspace:  strings.TrimSpace(entry.Workspace),
			extraRoots: cloneStringSlice(entry.ExtraRoots),
		}
	}

	sessionWorkspaceOverridesCache.Lock()
	sessionWorkspaceOverridesCache.generatedAt = time.Now()
	sessionWorkspaceOverridesCache.overrides = overrides
	sessionWorkspaceOverridesCache.Unlock()

	override, ok := overrides[sessionID]
	return override, ok
}

// peekSessionWorkspaceMap returns the bytes the request prefix would carry
// for the workspace map without the freezing side effect of
// sessionWorkspaceMap: an already-frozen session returns its frozen bytes; a
// session that has not run yet is counted from the live map (plus the
// snapshot note) so the footer estimate matches what the eventual request
// will freeze. Read-only by design — footer polling must not pin the
// session's map bytes ahead of the first real request.
func (a *App) peekSessionWorkspaceMap(sessionID string, cfg ConfigState) string {
	if strings.TrimSpace(sessionID) == "" {
		return a.workspaceMapContext(cfg)
	}
	a.mu.Lock()
	frozen, ok := a.sessionWorkspaceMaps[sessionID]
	a.mu.Unlock()
	if ok {
		return frozen
	}
	content := a.workspaceMapContext(cfg)
	if content == "" {
		return ""
	}
	return workspaceMapSnapshotNote + content
}

// sessionPrefixBreakdown returns the request prefix this session will carry —
// the system prompt parts, the session-frozen workspace map and the current plan
// snapshot — as a token total plus the same sections broken out for the footer
// popover.
//
// It is the single source of the prefix figure: the footer displays it and the
// run loop adds it to the auto-compaction trigger, because the live message
// breakdown only covers the conversation. Both therefore agree on what the
// request carries before the first provider measurement arrives.
func (a *App) sessionPrefixBreakdown(sessionID string, cfg ConfigState, allSkills []SkillDefinition) (int, []ContextBreakdownPart) {
	parts := []ContextBreakdownPart{}
	total := 0
	appendPart := func(label, content string) {
		tokens := estimateTokensFromText(content)
		if tokens <= 0 {
			return
		}
		total += tokens
		parts = append(parts, ContextBreakdownPart{Label: label, Tokens: tokens})
	}
	// System prompt: the session-frozen variant once frozen, the live prompt
	// before that (peek semantics — footer polling must not pin the session's
	// prompt bytes ahead of the first real request). Part-level granularity is
	// preserved so the footer popover keeps its per-section breakdown
	// (core prompt, skills, memories, AGENTS.md, ...).
	for _, part := range a.systemPromptPartsForBreakdown(sessionID, cfg, allSkills) {
		appendPart(part.label, part.content)
	}
	// Workspace map: the session-frozen variant, not the live map — the request
	// prefix carries sessionWorkspaceMap(sessionID, cfg) plus its snapshot note,
	// so both consumers must count those exact bytes. A session that has not
	// frozen its map yet is counted from the live map without freezing it — the
	// actual request will freeze its own copy when it runs.
	appendPart(workspaceMapPartLabel, a.peekSessionWorkspaceMap(sessionID, cfg))
	// Plan snapshot: appendPlanForUserTurn injects it before the latest user
	// message on the first request of a run. It is request-only and transient,
	// but occupies real context budget while the plan is unfinished.
	appendPart(planSnapshotPartLabel, formatPlanSnapshot(a.GetTodos(sessionID)))
	return total, parts
}

// getContextBreakdown computes token estimates from the real session state.
// If liveBreakdown is available, it returns a merged view (live messages + current system/tools).
// workspaceHint (optional, from the UI) overrides the session-based workspace
// resolution; see GetContextBreakdown.
func (a *App) getContextBreakdown(sessionID string, workspaceHint string) ContextBreakdown {
	cfg := a.sessionContextConfig(sessionID, workspaceHint)

	// Request prefix and tool schemas: counted from the same bytes the next
	// request carries — the session-frozen system prompt, the session's
	// workspace map, the current plan snapshot and the session-frozen tool set.
	// sessionPrefixBreakdown is shared with the run loop, so the footer and the
	// auto-compaction trigger agree on the prefix figure.
	result := ContextBreakdown{}
	result.SystemPrompt, result.SystemPromptParts = a.sessionPrefixBreakdown(sessionID, cfg, a.listCachedSkills())
	result.ToolSchemas = estimateToolSchemaTokens(a.sessionToolsetForBreakdown(sessionID, cfg))

	// Check if live breakdown is available (covers tool calls + tool results not in a.histories)
	a.mu.Lock()
	live, hasLive := a.liveBreakdown[sessionID]
	a.mu.Unlock()
	if hasLive {
		// Use live message counts but keep current system/tool schemas (they may change).
		result.UserMessages = live.UserMessages
		result.AssistantMsgs = live.AssistantMsgs
		result.ToolResults = live.ToolResults
		result.Reasoning = live.Reasoning
		// The provider measurement comes from the live run: it owns the message
		// list the anchor was resolved against, so the footer must report the
		// same total the run loop uses instead of re-deriving a text estimate.
		result.MeasuredTokens = live.MeasuredTokens
		result.MeasuredMessages = live.MeasuredMessages
		result.TrailingTokens = live.TrailingTokens
		finalizeContextBreakdownTotal(&result)
		return result
	}

	// Fall back to history-based counting (missing tool calls + tool results).
	// loadSessionHistoryCopy triggers lazy disk load when the session is not yet
	// cached in this process (e.g. after switching sessions from localStorage),
	// so context stats are accurate on session restore without waiting for StartChat.
	hist := a.loadSessionHistoryCopy(sessionID)
	for _, m := range hist {
		addBreakdownMessage(&result, m)
	}
	// No live run: resolve the session's measurement against the stored
	// conversation, so a footer poll between runs still reports the
	// provider-measured size.
	a.finalizeSessionBreakdown(sessionID, &result, hist)
	return result
}

// liveBreakdownAccumulator exploits the append-only shape of the runChat
// message list. It scans only messages added since the previous step; callers
// reset it after context compaction, where the slice is replaced wholesale.
type liveBreakdownAccumulator struct {
	nextMessage int
	breakdown   ContextBreakdown
}

func newLiveBreakdownAccumulator(messages []openai.ChatCompletionMessage) *liveBreakdownAccumulator {
	acc := &liveBreakdownAccumulator{}
	acc.update(messages)
	return acc
}

func (acc *liveBreakdownAccumulator) reset(messages []openai.ChatCompletionMessage) {
	if acc == nil {
		return
	}
	acc.nextMessage = 0
	acc.breakdown = ContextBreakdown{}
	acc.update(messages)
}

func (acc *liveBreakdownAccumulator) update(messages []openai.ChatCompletionMessage) ContextBreakdown {
	if acc == nil {
		return computeLiveBreakdown(messages)
	}
	if acc.nextMessage > len(messages) {
		acc.nextMessage = 0
		acc.breakdown = ContextBreakdown{}
	}
	for _, message := range messages[acc.nextMessage:] {
		addBreakdownMessage(&acc.breakdown, message)
	}
	acc.nextMessage = len(messages)
	finalizeContextBreakdownTotal(&acc.breakdown)
	return acc.breakdown
}

// messageTokens estimates one message's wire cost: its body (text or
// multi-content parts) plus the tool-call fields — the call ID and Type are part
// of the payload too and were previously omitted. Reasoning text is charged
// separately by reasoningTokens so the breakdown can report it as its own row.
func messageTokens(message openai.ChatCompletionMessage) int {
	total := estimateMessageBodyTokens(message)
	for _, call := range message.ToolCalls {
		total += estimateTokensFromText(call.ID)
		total += estimateTokensFromText(string(call.Type))
		total += estimateTokensFromText(call.Function.Name)
		total += estimateTokensFromText(call.Function.Arguments)
	}
	return total
}

func reasoningTokens(message openai.ChatCompletionMessage) int {
	return estimateTokensFromText(message.ReasoningContent)
}

// addBreakdownMessage counts one message into the breakdown. The live
// accumulator and the saved-history fallback both go through it, so the footer
// reports the same numbers whether a run is active or not.
func addBreakdownMessage(result *ContextBreakdown, message openai.ChatCompletionMessage) {
	if result == nil {
		return
	}
	tokens := messageTokens(message)
	switch message.Role {
	case openai.ChatMessageRoleUser:
		result.UserMessages += tokens
	case openai.ChatMessageRoleAssistant:
		result.AssistantMsgs += tokens
	case openai.ChatMessageRoleTool:
		result.ToolResults += tokens
	}
	result.Reasoning += reasoningTokens(message)
}

// computeLiveBreakdown builds a ContextBreakdown from the actual live messages that will be sent to the API.
// This includes tool call arguments (assistant msgs with ToolCalls) and tool result messages,
// which are filtered out by saveHistory and thus missing from a.histories.
func computeLiveBreakdown(msgs []openai.ChatCompletionMessage) ContextBreakdown {
	result := ContextBreakdown{}
	for _, message := range msgs {
		addBreakdownMessage(&result, message)
	}
	finalizeContextBreakdownTotal(&result)
	return result
}

// Breakdown section labels. They are shared with the frontend popover, which
// maps them to localized text (ComposerInfoBar's contextPartLabel), so both
// sides name the same sections.
const (
	workspaceMapPartLabel = "工作区文件结构"
	planSnapshotPartLabel = "计划快照"
)

// contextAnchor is the provider-reported size of a session's most recent
// request plus the number of conversation messages that request carried. pi
// reads the same pair off the assistant message holding the usage
// (packages/ai/src/utils/estimate.ts, getLastAssistantUsageInfo); Ally's history
// messages are plain wire structs without a usage field, so the pair lives in
// App state next to the other per-session run records.
//
// covered counts conversation messages only: the system prompt and workspace map
// are re-prepared for every request and never persisted, so counting them would
// tie the anchor to whichever list (run messages vs. stored history) it is
// resolved against.
type contextAnchor struct {
	covered int
	tokens  int
}

// conversationMessageCount counts the messages a session persists and replays:
// everything except the request-only system prefix (system prompt + workspace
// map), which is re-prepared for every request and never stored in history.
func conversationMessageCount(messages []openai.ChatCompletionMessage) int {
	count := 0
	for _, message := range messages {
		if message.Role == openai.ChatMessageRoleSystem {
			continue
		}
		count++
	}
	return count
}

// resolveContextAnchor returns the provider measurement to use in place of the
// text estimate for a message list, plus the estimate for the messages added
// after the request that produced it.
//
// ok is false when no measurement applies: none was recorded yet, or the anchor
// no longer covers a prefix of the list. Compaction and history truncation
// rewrite the conversation, and a list shorter than the recorded coverage means
// the anchor cannot describe it any more. Falling back to the pure text estimate
// is the safe direction there: the estimate never under-counts the request.
func resolveContextAnchor(messages []openai.ChatCompletionMessage, anchor contextAnchor) (measured contextAnchor, trailing int, ok bool) {
	if anchor.tokens <= 0 || conversationMessageCount(messages) < anchor.covered {
		return contextAnchor{}, 0, false
	}
	seen := 0
	for _, message := range messages {
		if message.Role == openai.ChatMessageRoleSystem {
			continue
		}
		if seen >= anchor.covered {
			trailing += messageTokens(message) + reasoningTokens(message)
		}
		seen++
	}
	return anchor, trailing, true
}

// applyContextAnchor stores the provider measurement on the breakdown when it
// still covers a prefix of messages, then recomputes the totals.
func applyContextAnchor(breakdown *ContextBreakdown, messages []openai.ChatCompletionMessage, anchor contextAnchor) {
	if measured, trailing, ok := resolveContextAnchor(messages, anchor); ok {
		breakdown.MeasuredTokens = measured.tokens
		breakdown.MeasuredMessages = measured.covered
		breakdown.TrailingTokens = trailing
	}
	finalizeContextBreakdownTotal(breakdown)
}

// recordContextAnchor stores the provider-reported size of the request that
// produced usage. messages is the run's message list including the assistant
// reply that usage describes, so its conversation count is exactly what the
// measurement covers.
func (a *App) recordContextAnchor(sessionID string, messages []openai.ChatCompletionMessage, usage *modelUsage) {
	if sessionID == "" || usage == nil || usage.PromptTokens <= 0 {
		return
	}
	tokens := usage.PromptTokens + usage.CompletionTokens
	if tokens <= 0 {
		return
	}
	a.mu.Lock()
	// Lazy-init guard: writing an uninitialized map while holding the mutex
	// panics and leaves it locked forever, so App values built outside NewApp
	// must not be able to take the process down with them.
	if a.contextAnchors == nil {
		a.contextAnchors = map[string]contextAnchor{}
	}
	a.contextAnchors[sessionID] = contextAnchor{covered: conversationMessageCount(messages), tokens: tokens}
	a.mu.Unlock()
}

// contextAnchorFor returns the recorded measurement for a session; the zero
// value means "none", which makes resolveContextAnchor fall back to the text
// estimate.
func (a *App) contextAnchorFor(sessionID string) contextAnchor {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.contextAnchors[sessionID]
}

// clearContextAnchor drops a session's measurement when its conversation is
// rewritten (compaction, truncation): the recorded coverage describes a message
// list that no longer exists.
func (a *App) clearContextAnchor(sessionID string) {
	if sessionID == "" {
		return
	}
	a.mu.Lock()
	delete(a.contextAnchors, sessionID)
	a.mu.Unlock()
}

// finalizeSessionBreakdown resolves the session's provider measurement against
// the message list the breakdown was built from and recomputes the totals. Every
// writer of a live breakdown goes through it (or through applyContextAnchor while
// it already holds a.mu), so the footer, the run loop and the auto-compaction
// trigger all report the same total. Callers must not hold a.mu.
func (a *App) finalizeSessionBreakdown(sessionID string, breakdown *ContextBreakdown, messages []openai.ChatCompletionMessage) {
	if breakdown == nil {
		return
	}
	applyContextAnchor(breakdown, messages, a.contextAnchorFor(sessionID))
}

// handleTodoList implements the plan tool.
func (a *App) handleTodoList(sessionID string, req TodoListRequest) (any, error) {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return nil, errors.New("no active session")
	}

	inProgress := 0
	for _, todo := range req.Todos {
		switch todo.Status {
		case "pending", "in_progress", "done":
		default:
			return nil, fmt.Errorf("invalid todo status %q: must be pending, in_progress, or done", todo.Status)
		}
		if strings.TrimSpace(todo.Title) == "" {
			return nil, errors.New("todo title is required")
		}
		if todo.Status == "in_progress" {
			inProgress++
		}
	}
	if inProgress > 1 {
		return nil, fmt.Errorf("at most one todo may be in_progress at a time (got %d): mark the current item done or pending before starting another", inProgress)
	}

	a.mu.Lock()
	// Query mode: return current list
	if req.Todos == nil {
		list := a.todos[sid]
		if list == nil {
			list = []TodoEntry{}
		}
		list = cloneTodos(list)
		revision := a.todoRevisions[sid]
		a.mu.Unlock()
		return map[string]any{
			"todos":    list,
			"revision": revision,
			"message":  "Current todo list.",
		}, nil
	}

	// Replace mode. Any non-empty list with actionable work starts at its
	// first pending item, so a newly created list immediately has a visible
	// current step. This also repairs an update that finished the old step
	// without selecting the next one.
	updated := cloneTodos(req.Todos)
	if inProgress == 0 {
		for i := range updated {
			if updated[i].Status == "pending" {
				updated[i].Status = "in_progress"
				break
			}
		}
	}
	a.todos[sid] = updated
	a.todoRevisions[sid]++
	revision := a.todoRevisions[sid]
	a.mu.Unlock()

	a.emitTodoUpdate(sid, updated, revision)
	message := "Todo list updated."
	if len(updated) == 0 {
		message = "Todo list cleared."
	}
	return map[string]any{
		"todos":    updated,
		"revision": revision,
		"message":  message,
	}, nil
}

// formatPlanSnapshot renders the current plan as a compact checklist.
func formatPlanSnapshot(list []TodoEntry) string {
	var b strings.Builder
	for _, t := range list {
		switch t.Status {
		case "done":
			b.WriteString("- [x] ")
		case "in_progress":
			b.WriteString("- [~] ")
		default:
			b.WriteString("- [ ] ")
		}
		b.WriteString(strings.TrimSpace(t.Title))
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// appendPlanForUserTurn adds the current in-memory plan once before the latest
// user message. The returned slice is a request-only copy: callers must keep
// the original messages for history persistence, so the plan is not saved or
// repeated in the next turn.
func (a *App) appendPlanForUserTurn(sessionID string, messages []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	list := a.GetTodos(sessionID)
	if len(list) == 0 {
		return messages
	}

	insertAt := len(messages)
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == openai.ChatMessageRoleUser {
			insertAt = i
			break
		}
	}
	planMessage := openai.ChatCompletionMessage{
		Role: openai.ChatMessageRoleUser,
		Content: "当前会话存在未完成的计划，仅作进度参考，不是新的用户要求；\n" +
			"用户新消息的优先级高于计划：先回应用户新消息，再根据用户意图判断是否继续、调整或放弃计划：\n" +
			formatPlanSnapshot(list),
	}
	out := make([]openai.ChatCompletionMessage, 0, len(messages)+1)
	out = append(out, messages[:insertAt]...)
	out = append(out, planMessage)
	out = append(out, messages[insertAt:]...)
	return out
}

func cloneTodos(list []TodoEntry) []TodoEntry {
	if len(list) == 0 {
		return []TodoEntry{}
	}
	out := make([]TodoEntry, len(list))
	copy(out, list)
	return out
}

// ── Request message assembly ─────────────────────────────────

// buildMessages assembles the request message list and applies the model-facing
// input downgrades. It is the single choke point every provider adapter goes
// through, so a model limitation only has to be implemented once.
func (a *App) buildMessages(req ChatRequest, cfg ConfigState, allSkills []SkillDefinition) []openai.ChatCompletionMessage {
	return downgradeUnsupportedImages(a.assembleMessages(req, cfg, allSkills), cfg)
}

func (a *App) assembleMessages(req ChatRequest, cfg ConfigState, allSkills []SkillDefinition) []openai.ChatCompletionMessage {
	messages := a.buildSystemContextMessages(req.SessionID, cfg, allSkills)

	if len(req.Messages) > 0 {
		history := a.loadSessionHistoryCopy(req.SessionID)
		if len(history) > 0 {
			messages = append(messages, history...)
			messages = appendFrontendHistoryDelta(messages, history, req.Messages)
		} else {
			for _, m := range req.Messages {
				role := strings.TrimSpace(m.Role)
				if role != openai.ChatMessageRoleUser && role != openai.ChatMessageRoleAssistant {
					continue
				}
				if strings.TrimSpace(m.Content) == "" && len(m.Attachments) == 0 {
					continue
				}
				if role == openai.ChatMessageRoleUser && len(m.Attachments) > 0 {
					messages = appendUserMessageWithAttachments(messages, m.Content, m.Attachments)
				} else {
					messages = append(messages, openai.ChatCompletionMessage{Role: role, Content: m.Content})
				}
			}
		}
		return messages
	}

	if req.SessionID != "" {
		messages = append(messages, a.loadSessionHistoryCopy(req.SessionID)...)
	}
	if strings.TrimSpace(req.Message) != "" || len(req.Attachments) > 0 {
		messages = appendUserMessageWithAttachments(messages, req.Message, req.Attachments)
	}
	return messages
}

// nonVisionImagePlaceholder is what an image becomes when the model cannot take
// image input. pi uses the same wording (transform-messages.ts:12-13): telling
// the model that an image was omitted beats dropping it silently, because the
// model can then say it cannot see the image instead of guessing at it.
//
// pi also carries a tool-result variant ("tool image omitted"); Ally has no
// second case to cover: images only ever ride on user messages
// (readImageInjectionMessage builds a user turn).
const nonVisionImagePlaceholder = "(image omitted: model does not support images)"

// downgradeUnsupportedImages replaces image parts with a text placeholder when
// the model is known to reject image input. VisionCapable == nil means "unknown"
// and keeps the images untouched: guessing from the model id would be wrong on
// exactly the relay/custom endpoints where it matters, so the flag comes from the
// model catalog (pi gates this on model.input, transform-messages.ts:35-63).
//
// The downgrade runs at the request boundary rather than at injection time, so
// switching back to a vision model restores the images — the in-memory history
// still holds them.
func downgradeUnsupportedImages(messages []openai.ChatCompletionMessage, cfg ConfigState) []openai.ChatCompletionMessage {
	if cfg.VisionCapable == nil || *cfg.VisionCapable || len(messages) == 0 {
		return messages
	}
	var out []openai.ChatCompletionMessage
	for i := range messages {
		if !hasImagePart(messages[i].MultiContent) {
			continue
		}
		if out == nil {
			// Copy on first hit: the common case (a vision model, or no images at
			// all) must not pay for a full history clone.
			out = cloneChatMessages(messages)
		}
		out[i].MultiContent = replaceImagePartsWithPlaceholder(out[i].MultiContent)
	}
	if out == nil {
		return messages
	}
	return out
}

func hasImagePart(parts []openai.ChatMessagePart) bool {
	for _, part := range parts {
		if part.Type == openai.ChatMessagePartTypeImageURL {
			return true
		}
	}
	return false
}

// replaceImagePartsWithPlaceholder keeps the text parts in order and represents
// the omitted images with one placeholder part, so the message keeps a valid
// non-empty shape for a text-only model.
func replaceImagePartsWithPlaceholder(parts []openai.ChatMessagePart) []openai.ChatMessagePart {
	out := make([]openai.ChatMessagePart, 0, len(parts)+1)
	omitted := 0
	for _, part := range parts {
		if part.Type == openai.ChatMessagePartTypeImageURL {
			omitted++
			continue
		}
		out = append(out, part)
	}
	if omitted == 0 {
		return parts
	}
	label := nonVisionImagePlaceholder
	if omitted > 1 {
		label = fmt.Sprintf("%s x%d", nonVisionImagePlaceholder, omitted)
	}
	return append(out, openai.ChatMessagePart{Type: openai.ChatMessagePartTypeText, Text: label})
}

// cancelledTurnMarker returns the user-role control message recorded when the
// user interrupts a run (ESC / stop). It is persisted into the saved history so
// the next request can distinguish a user-cancelled turn from provider errors;
// the XML tag marks it as machine-generated status rather than a user utterance.
// It also declares priority: the user's next message continues the conversation
// and outranks any in-flight plan, mirroring kimicode's interruption reminder.
func cancelledTurnMarker() openai.ChatCompletionMessage {
	return openai.ChatCompletionMessage{
		Role: openai.ChatMessageRoleUser,
		Content: "<ally-cancelled>\n" +
			"上一轮已被用户手动中断，此前的部分输出可能不完整\n" +
			"用户接下来的消息延续本次会话，其意图优先级最高：先回应用户的新消息，\n" +
			"仅当用户明确要求继续时才恢复被中断的任务或计划\n" +
			"</ally-cancelled>",
	}
}

func (a *App) buildSystemContextMessages(sessionID string, cfg ConfigState, allSkills []SkillDefinition) []openai.ChatCompletionMessage {
	messages := []openai.ChatCompletionMessage{}
	if systemPrompt := a.sessionSystemPrompt(sessionID, cfg, allSkills); systemPrompt != "" {
		messages = append(messages, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleSystem, Content: systemPrompt})
	}
	messages = a.appendWorkspaceMapMessage(messages, sessionID, cfg)
	return messages
}

// sessionSystemPrompt returns the system prompt bytes for a chat session,
// frozen at the session's first request. Later runs in the same session reuse
// the exact same bytes so the request prefix (system prompt + workspace map +
// history) stays byte-stable and provider prompt caches survive across runs.
// The prompt embeds live disk sources (memory index, project lessons,
// AGENTS.md, CODEGRAPH.md) that the agent itself may write during the
// conversation; rebuilding per run would invalidate the whole prefix cache on
// every such write, so changes only take effect in new sessions. Requests
// without a session id fall back to the live prompt (stateless callers).
func (a *App) sessionSystemPrompt(sessionID string, cfg ConfigState, allSkills []SkillDefinition) string {
	return joinSystemPromptParts(a.sessionSystemPromptParts(sessionID, cfg, allSkills))
}

// sessionSystemPromptParts freezes the system prompt parts per session (the
// request path joins them into the single system message). See
// sessionSystemPrompts on App for the rationale.
func (a *App) sessionSystemPromptParts(sessionID string, cfg ConfigState, allSkills []SkillDefinition) []systemPromptPart {
	if strings.TrimSpace(sessionID) == "" {
		return buildSystemPromptParts(allSkills, cfg.Workspace, cfg.ExtraRoots, cfg.CustomPrompt, cfg.GitBashPath, cfg.KBRoot)
	}
	a.mu.Lock()
	if frozen, ok := a.sessionSystemPrompts[sessionID]; ok {
		a.mu.Unlock()
		return frozen
	}
	a.mu.Unlock()

	parts := buildSystemPromptParts(allSkills, cfg.Workspace, cfg.ExtraRoots, cfg.CustomPrompt, cfg.GitBashPath, cfg.KBRoot)
	if len(parts) == 0 {
		return nil
	}

	a.mu.Lock()
	if a.sessionSystemPrompts == nil {
		a.sessionSystemPrompts = map[string][]systemPromptPart{}
	}
	a.sessionSystemPrompts[sessionID] = parts
	a.mu.Unlock()
	return parts
}

// systemPromptPartsForBreakdown returns the parts the footer context breakdown
// counts: the session-frozen parts once frozen, the live parts before that
// (peek semantics — footer polling must not pin the session's prompt bytes
// ahead of the first real request).
func (a *App) systemPromptPartsForBreakdown(sessionID string, cfg ConfigState, skills []SkillDefinition) []systemPromptPart {
	if strings.TrimSpace(sessionID) != "" {
		a.mu.Lock()
		frozen, ok := a.sessionSystemPrompts[sessionID]
		a.mu.Unlock()
		if ok {
			return frozen
		}
	}
	return buildSystemPromptParts(skills, cfg.Workspace, cfg.ExtraRoots, cfg.CustomPrompt, cfg.GitBashPath, cfg.KBRoot)
}

func (a *App) loadSessionHistoryCopy(sessionID string) []openai.ChatCompletionMessage {
	if sessionID == "" {
		return nil
	}
	a.mu.Lock()
	h := a.histories[sessionID]
	if h == nil {
		h = a.loadHistoryLocked(sessionID)
	}
	hCopy := cloneChatMessages(sanitizeHistoryMessages(h))
	a.mu.Unlock()
	return hCopy
}

func appendFrontendHistoryDelta(messages []openai.ChatCompletionMessage, backend []openai.ChatCompletionMessage, frontend []ChatMessageInput) []openai.ChatCompletionMessage {
	backendKeys := make([]string, 0, len(backend))
	for _, m := range backend {
		// MultiContent messages (attachment images kept in memory history)
		// compare by their text projection so frontend delta alignment sees
		// them the same way as the frontend's plain-content messages.
		content := m.Content
		if len(m.MultiContent) > 0 {
			content = textFromMultiContent(m.MultiContent)
		}
		if key := comparableMessageKey(m.Role, content); key != "" {
			backendKeys = append(backendKeys, key)
		}
	}

	type frontendMessage struct {
		key string
		msg ChatMessageInput
	}
	front := make([]frontendMessage, 0, len(frontend))
	for _, m := range frontend {
		role := strings.TrimSpace(m.Role)
		if role != openai.ChatMessageRoleUser && role != openai.ChatMessageRoleAssistant {
			continue
		}
		if strings.TrimSpace(m.Content) == "" && len(m.Attachments) == 0 {
			continue
		}
		front = append(front, frontendMessage{key: comparableMessageKey(role, m.Content), msg: m})
	}

	// Match the longest suffix of backend-visible history against any
	// contiguous frontend range. This handles restart recovery where IndexedDB
	// contains an older prefix but the backend retained only its budgeted tail.
	lastMatchedFrontend := -1
	maxOverlap := len(backendKeys)
	if len(front) < maxOverlap {
		maxOverlap = len(front)
	}
	for overlap := maxOverlap; overlap > 0 && lastMatchedFrontend < 0; overlap-- {
		backendStart := len(backendKeys) - overlap
		for frontStart := len(front) - overlap; frontStart >= 0; frontStart-- {
			matched := true
			for offset := 0; offset < overlap; offset++ {
				if front[frontStart+offset].key == "" || front[frontStart+offset].key != backendKeys[backendStart+offset] {
					matched = false
					break
				}
			}
			if matched {
				lastMatchedFrontend = frontStart + overlap - 1
				break
			}
		}
	}

	appendFrom := lastMatchedFrontend + 1
	if lastMatchedFrontend < 0 && len(backendKeys) > 0 && len(front) > 0 {
		// A backend compaction summary intentionally has no textual overlap with
		// the still-expanded UI snapshot. In that case the backend remains the
		// source of truth and only the request-tail message is new.
		appendFrom = len(front) - 1
	}
	for _, item := range front[appendFrom:] {
		role := strings.TrimSpace(item.msg.Role)
		if role == openai.ChatMessageRoleUser && len(item.msg.Attachments) > 0 {
			messages = appendUserMessageWithAttachments(messages, item.msg.Content, item.msg.Attachments)
		} else {
			messages = append(messages, openai.ChatCompletionMessage{Role: role, Content: item.msg.Content})
		}
	}
	return messages
}

func comparableMessageKey(role, content string) string {
	role = strings.TrimSpace(role)
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	if role != openai.ChatMessageRoleUser && role != openai.ChatMessageRoleAssistant {
		return ""
	}
	return role + "\x00" + content
}

func cloneChatMessages(messages []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	if len(messages) == 0 {
		return nil
	}
	out := make([]openai.ChatCompletionMessage, len(messages))
	copy(out, messages)
	for i := range out {
		out[i].ToolCalls = append([]openai.ToolCall(nil), messages[i].ToolCalls...)
		out[i].MultiContent = append([]openai.ChatMessagePart(nil), messages[i].MultiContent...)
	}
	return out
}

func (a *App) appendWorkspaceMapMessage(messages []openai.ChatCompletionMessage, sessionID string, cfg ConfigState) []openai.ChatCompletionMessage {
	if content := a.sessionWorkspaceMap(sessionID, cfg); content != "" {
		messages = append(messages, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleSystem, Content: content})
	}
	return messages
}

func appendUserMessageWithAttachments(messages []openai.ChatCompletionMessage, text string, attachments []AttachmentInput) []openai.ChatCompletionMessage {
	content := buildAttachmentTextContext(text, attachments)
	parts := []openai.ChatMessagePart{}
	if strings.TrimSpace(content) != "" {
		parts = append(parts, openai.ChatMessagePart{Type: openai.ChatMessagePartTypeText, Text: content})
	}
	for _, att := range attachments {
		if !isImageAttachment(att) || !validImageDataURL(att.DataURL) {
			continue
		}
		if len(att.DataURL) > maxAttachmentDataURL {
			continue
		}
		parts = append(parts, openai.ChatMessagePart{
			Type: openai.ChatMessagePartTypeImageURL,
			ImageURL: &openai.ChatMessageImageURL{
				URL:    att.DataURL,
				Detail: openai.ImageURLDetailAuto,
			},
		})
	}
	if len(parts) > 1 {
		return append(messages, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, MultiContent: parts})
	}
	return append(messages, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: content})
}

func buildAttachmentTextContext(text string, attachments []AttachmentInput) string {
	base := strings.TrimSpace(text)
	if len(attachments) == 0 {
		return base
	}
	var b strings.Builder
	if base != "" {
		b.WriteString(base)
		b.WriteString("\n\n")
	}
	b.WriteString("Attached files:\n")
	for i, att := range attachments {
		name := strings.TrimSpace(att.Name)
		if name == "" {
			name = "unnamed"
		}
		kind := strings.TrimSpace(att.Kind)
		if kind == "" {
			kind = "file"
		}
		mimeType := strings.TrimSpace(att.Type)
		if mimeType == "" {
			mimeType = kind
		}
		state := "metadata only"
		if isImageAttachment(att) && validImageDataURL(att.DataURL) && len(att.DataURL) <= maxAttachmentDataURL {
			state = "sent as image input"
		} else if strings.TrimSpace(att.Text) != "" {
			state = "sent as text"
		}
		if att.Truncated {
			state += ", truncated"
		}
		fmt.Fprintf(&b, "%d. %s (%s, %d bytes): %s", i+1, name, mimeType, att.Size, state)
		if att.Error != "" {
			fmt.Fprintf(&b, " (%s)", att.Error)
		}
		// 附件的绝对路径（如果有）：模型可据此直接 read 文件
		if fp := strings.TrimSpace(att.FilePath); fp != "" {
			fmt.Fprintf(&b, " — path: %s", fp)
		}
		b.WriteString("\n")
	}
	for _, att := range attachments {
		if strings.TrimSpace(att.Text) == "" {
			continue
		}
		text := att.Text
		truncated := att.Truncated
		if len(text) > maxAttachmentText {
			text = text[:maxAttachmentText]
			truncated = true
		}
		b.WriteString("\n<attached_file name=\"")
		b.WriteString(escapeAttribute(att.Name))
		b.WriteString("\" mime=\"")
		b.WriteString(escapeAttribute(att.Type))
		b.WriteString("\">\n")
		b.WriteString(text)
		if truncated {
			b.WriteString("\n[attachment text truncated]\n")
		}
		b.WriteString("\n</attached_file>\n")
	}
	return b.String()
}

func isImageAttachment(att AttachmentInput) bool {
	kind := strings.ToLower(strings.TrimSpace(att.Kind))
	mimeType := strings.ToLower(strings.TrimSpace(att.Type))
	return kind == "image" || strings.HasPrefix(mimeType, "image/")
}

func validImageDataURL(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(lower, "data:image/png;base64,") ||
		strings.HasPrefix(lower, "data:image/jpeg;base64,") ||
		strings.HasPrefix(lower, "data:image/jpg;base64,") ||
		strings.HasPrefix(lower, "data:image/webp;base64,") ||
		strings.HasPrefix(lower, "data:image/gif;base64,")
}

func escapeAttribute(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "\"", "&quot;", "<", "&lt;", ">", "&gt;")
	return replacer.Replace(value)
}

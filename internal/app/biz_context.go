// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"crypto/sha256"
	"encoding/hex"
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

// contextStaticCacheHolder owns the ContextBreakdown memoization plus its
// invalidation version counter. It lives in biz_context.go so the fields,
// TTL, key derivation, read/write, and versioned invalidation are in one file.
type contextStaticCacheHolder struct {
	mu          sync.Mutex
	cache       map[string]contextStaticCacheEntry
	cacheVersion uint64
}

func newContextStaticCacheHolder() *contextStaticCacheHolder {
	return &contextStaticCacheHolder{cache: map[string]contextStaticCacheEntry{}}
}

const contextStaticCacheTTL = 30 * time.Second

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

type ContextBreakdown struct {
	Total             int                    `json:"total"`
	SystemPrompt      int                    `json:"systemPrompt"`
	SystemPromptParts []ContextBreakdownPart `json:"systemPromptParts,omitempty"`
	ToolSchemas       int                    `json:"toolSchemas"`
	UserMessages      int                    `json:"userMessages"`
	AssistantMsgs     int                    `json:"assistantMsgs"`
	ToolResults       int                    `json:"toolResults"`
	Reasoning         int                    `json:"reasoning"`
}

// WorkspaceTokenUsage is the cumulative input/output token usage for a workspace.
type WorkspaceTokenUsage struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
}

// GetContextBreakdown returns detailed token usage breakdown for a session.
func (a *App) GetContextBreakdown(sessionID string) ContextBreakdown {
	return a.getContextBreakdown(sessionID)
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
	return a.getContextBreakdown(sessionID).Total
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
	return int(math.Ceil(float64(asciiCount)/4)) + nonAsciiCount
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

func finalizeContextBreakdownTotal(result *ContextBreakdown) {
	result.Total = result.SystemPrompt + result.ToolSchemas + result.UserMessages + result.AssistantMsgs + result.ToolResults + result.Reasoning
}

func estimateMessageBodyTokens(m openai.ChatCompletionMessage) int {
	total := estimateTokensFromText(m.Content)
	for _, part := range m.MultiContent {
		switch part.Type {
		case openai.ChatMessagePartTypeText:
			total += estimateTokensFromText(part.Text)
		case openai.ChatMessagePartTypeImageURL:
			total += 256
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

// getContextBreakdown computes token estimates from the real session state.
// If liveBreakdown is available, it returns a merged view (live messages + current system/tools).
func (a *App) getContextBreakdown(sessionID string) ContextBreakdown {
	a.mu.Lock()
	cfg := a.config
	a.mu.Unlock()

	result := a.contextStaticBreakdown(cfg, a.listCachedSkills())

	// Workspace map (appended as a separate system message in buildMessages)
	if wm := a.workspaceMapContext(cfg); wm != "" {
		tokens := estimateTokensFromText(wm)
		result.SystemPrompt += tokens
		result.SystemPromptParts = append(result.SystemPromptParts, ContextBreakdownPart{
			Label:  "工作区文件结构",
			Tokens: tokens,
		})
	}

	// Check if live breakdown is available (covers tool calls + tool results not in a.histories)
	a.mu.Lock()
	live, hasLive := a.liveBreakdown[sessionID]
	a.mu.Unlock()
	if hasLive {
		// Use live message counts but keep current system/tool schemas (they may change)
		result.UserMessages = live.UserMessages
		result.AssistantMsgs = live.AssistantMsgs
		result.ToolResults = live.ToolResults
		result.Reasoning = live.Reasoning
	} else {
		// Fall back to history-based counting (missing tool calls + tool results)
		// loadSessionHistoryCopy triggers lazy disk load when the session is not yet
		// cached in this process (e.g. after switching sessions from localStorage),
		// so context stats are accurate on session restore without waiting for StartChat.
		hist := a.loadSessionHistoryCopy(sessionID)
		for _, m := range hist {
			tokens := estimateMessageBodyTokens(m)
			switch m.Role {
			case "user":
				result.UserMessages += tokens
			case "assistant":
				result.AssistantMsgs += tokens
			case "tool":
				result.ToolResults += tokens
			}
			for _, tc := range m.ToolCalls {
				result.AssistantMsgs += estimateTokensFromText(tc.Function.Name) + estimateTokensFromText(tc.Function.Arguments)
			}
			if m.ReasoningContent != "" {
				result.Reasoning += estimateTokensFromText(m.ReasoningContent)
			}
		}
	}

	// ToolSchemas already comes from contextStaticBreakdown (which caches it
	// and is invalidated by invalidateContextStaticCache on MCP/skill changes).
	// Do NOT recompute here — that would defeat the cache on every popover refresh.
	finalizeContextBreakdownTotal(&result)
	return result
}

func (a *App) contextStaticBreakdown(cfg ConfigState, skills []SkillDefinition) ContextBreakdown {
	version := a.contextStaticCaches.version()
	key := contextStaticCacheKey(cfg, skills, version)
	a.contextStaticCaches.mu.Lock()
	if cached, ok := a.contextStaticCaches.cache[key]; ok && time.Since(cached.generatedAt) < contextStaticCacheTTL {
		result := cloneContextBreakdown(cached.breakdown)
		a.contextStaticCaches.mu.Unlock()
		return result
	}
	a.contextStaticCaches.mu.Unlock()

	result := ContextBreakdown{}
	for _, part := range buildSystemPromptParts(skills, cfg.Workspace, cfg.ExtraRoots, cfg.CustomPrompt, cfg.GitBashPath) {
		tokens := estimateTokensFromText(part.content)
		if tokens <= 0 {
			continue
		}
		result.SystemPrompt += tokens
		result.SystemPromptParts = append(result.SystemPromptParts, ContextBreakdownPart{
			Label:  part.label,
			Tokens: tokens,
		})
	}
	result.ToolSchemas = estimateToolSchemaTokens(a.buildToolsForConfig(cfg))

	a.contextStaticCaches.mu.Lock()
	if len(a.contextStaticCaches.cache) >= 64 {
		a.contextStaticCaches.cache = map[string]contextStaticCacheEntry{}
	}
	a.contextStaticCaches.cache[key] = contextStaticCacheEntry{
		breakdown:   cloneContextBreakdown(result),
		generatedAt: time.Now(),
	}
	a.contextStaticCaches.mu.Unlock()
	return result
}

func cloneContextBreakdown(result ContextBreakdown) ContextBreakdown {
	result.SystemPromptParts = append([]ContextBreakdownPart(nil), result.SystemPromptParts...)
	return result
}

func contextStaticCacheKey(cfg ConfigState, skills []SkillDefinition, version uint64) string {
	h := sha256.New()
	write := func(value string) {
		_, _ = h.Write([]byte(value))
		_, _ = h.Write([]byte{0})
	}
	write(cfg.Workspace)
	for _, root := range cfg.ExtraRoots {
		write(root)
	}
	write(cfg.CustomPrompt)
	write(cfg.GitBashPath)
	write(fmt.Sprintf("%d", version))
	for _, skill := range skills {
		write(skill.Name)
		write(skill.Description)
		write(skill.Source)
		write(skill.Path)
		write(skill.WhenToUse)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (h *contextStaticCacheHolder) version() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cacheVersion
}

func (a *App) invalidateContextStaticCache() {
	a.contextStaticCaches.mu.Lock()
	a.contextStaticCaches.cacheVersion++
	a.contextStaticCaches.cache = map[string]contextStaticCacheEntry{}
	a.contextStaticCaches.mu.Unlock()
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
		addLiveBreakdownMessage(&acc.breakdown, message)
	}
	acc.nextMessage = len(messages)
	finalizeContextBreakdownTotal(&acc.breakdown)
	return acc.breakdown
}

func addLiveBreakdownMessage(result *ContextBreakdown, message openai.ChatCompletionMessage) {
	if result == nil {
		return
	}
	tokens := estimateMessageBodyTokens(message)
	switch message.Role {
	case "user":
		result.UserMessages += tokens
	case "assistant":
		result.AssistantMsgs += tokens
	case "tool":
		result.ToolResults += tokens
	}
	for _, tc := range message.ToolCalls {
		result.AssistantMsgs += estimateTokensFromText(tc.Function.Name) + estimateTokensFromText(tc.Function.Arguments)
	}
	if message.ReasoningContent != "" {
		result.Reasoning += estimateTokensFromText(message.ReasoningContent)
	}
}

// computeLiveBreakdown builds a ContextBreakdown from the actual live messages that will be sent to the API.
// This includes tool call arguments (assistant msgs with ToolCalls) and tool result messages,
// which are filtered out by saveHistory and thus missing from a.histories.
func computeLiveBreakdown(msgs []openai.ChatCompletionMessage) ContextBreakdown {
	result := ContextBreakdown{}
	for _, message := range msgs {
		addLiveBreakdownMessage(&result, message)
	}
	finalizeContextBreakdownTotal(&result)
	return result
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

func cloneTodos(list []TodoEntry) []TodoEntry {
	if len(list) == 0 {
		return []TodoEntry{}
	}
	out := make([]TodoEntry, len(list))
	copy(out, list)
	return out
}

// ── Request message assembly ─────────────────────────────────

func (a *App) buildMessages(req ChatRequest, cfg ConfigState, allSkills []SkillDefinition) []openai.ChatCompletionMessage {
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

// cancelledTurnMarker returns the user-role control message recorded when the
// user interrupts a run (ESC / stop). It is persisted into the saved history so
// the next request can distinguish a user-cancelled turn from provider errors;
// the XML tag marks it as machine-generated status rather than a user utterance.
func cancelledTurnMarker() openai.ChatCompletionMessage {
	return openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: "<ally-cancelled>\n上一条提问已被用户取消\n</ally-cancelled>",
	}
}

func (a *App) buildSystemContextMessages(sessionID string, cfg ConfigState, allSkills []SkillDefinition) []openai.ChatCompletionMessage {
	messages := []openai.ChatCompletionMessage{}
	systemPrompt := defaultSystemPrompt(allSkills, cfg.Workspace, cfg.ExtraRoots, cfg.CustomPrompt, cfg.GitBashPath)
	if systemPrompt != "" {
		messages = append(messages, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleSystem, Content: systemPrompt})
	}
	messages = a.appendWorkspaceMapMessage(messages, sessionID, cfg)
	return messages
}

// contextBudgetThresholdPct is the remaining-budget percentage below which the
// context-budget item is injected. Above it the model sees no budget message at
// all: with a ~1M-token window the numbers carry no decision information and
// models occasionally echo the note back as noise. Only when the window is
// actually getting tight does the hint matter (prefer grep over read, avoid
// re-reading).
const contextBudgetThresholdPct = 30

// appendContextBudgetMessage returns a new slice with a context-budget item
// appended to the request tail, or the input slice unchanged when remaining
// budget is above contextBudgetThresholdPct. It deliberately allocates a fresh
// slice so the caller's `messages` is never mutated; the budget item must not
// be persisted into saved history (it would bloat storage and disrupt reusable
// prefixes).
//
// Placing the budget at the tail follows the same strategy as other
// dynamic, low-priority content: it goes last. The explicit GPT-5.6
// Responses cache boundary, when active, stays before this tail.
func appendContextBudgetMessage(messages []openai.ChatCompletionMessage, usedTokens, maxCtx int) []openai.ChatCompletionMessage {
	if maxCtx <= 0 {
		maxCtx = 1000000
	}
	if usedTokens < 0 {
		usedTokens = 0
	}
	remaining := maxCtx - usedTokens
	if remaining < 0 {
		remaining = 0
	}
	usedPct := 0
	if maxCtx > 0 {
		usedPct = usedTokens * 100 / maxCtx
	}
	remainingPct := 100 - usedPct
	if remainingPct >= contextBudgetThresholdPct {
		return messages
	}
	var b strings.Builder
	b.WriteString("<ally-context-budget>\n")
	fmt.Fprintf(&b, "Window: %d tokens\n", maxCtx)
	fmt.Fprintf(&b, "Used: %d tokens (%d%%)\n", usedTokens, usedPct)
	fmt.Fprintf(&b, "Remaining: %d tokens (%d%%)\n", remaining, remainingPct)
	b.WriteString("Note: large tool results (read, command output) consume budget quickly. ")
	b.WriteString("When remaining is low, prefer grep/list_files over read, and avoid re-reading files already seen this turn.")
	b.WriteString("\n</ally-context-budget>")
	out := make([]openai.ChatCompletionMessage, len(messages)+1)
	copy(out, messages)
	out[len(messages)] = openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: b.String()}
	return out
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
		if key := comparableMessageKey(m.Role, m.Content); key != "" {
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

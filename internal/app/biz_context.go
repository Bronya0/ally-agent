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
	a.emit("todo:update", map[string]any{
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
	// uses the cache only when it is the full chatTools() set — grill mode
	// filters out side-effectful builtin tools, so the filtered builtin slice
	// must be re-marshaled to avoid over-counting the cached full-set tokens.
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
		// Filtered builtin subset (e.g. grill mode) — marshal directly.
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

	result := ContextBreakdown{}
	for _, part := range buildSystemPromptParts(a.listCachedSkills(), cfg.Workspace, cfg.ExtraRoots, cfg.CustomPrompt, cfg.GitBashPath) {
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

	// Workspace map (appended as a separate system message in buildMessages)
	if wm := a.workspaceMapContext(cfg); wm != "" {
		tokens := estimateTokensFromText(wm)
		result.SystemPrompt += tokens
		result.SystemPromptParts = append(result.SystemPromptParts, ContextBreakdownPart{
			Label:  "工作区文件结构",
			Tokens: tokens,
		})
	}

	// Active goal context (also injected as a system message)
	if g := a.getActiveGoal(sessionID); g != nil {
		var goalCtx string
		budgetInfo := ""
		if g.TurnBudget > 0 {
			budgetInfo = fmt.Sprintf(" (turns %d/%d)", g.TurnsUsed, g.TurnBudget)
		}
		goalCtx = fmt.Sprintf("Active goal: %s | Status: %s | Turns: %d%s", g.Objective, g.Status, g.TurnsUsed, budgetInfo)
		if g.CompletionCriterion != "" {
			goalCtx += " | Completion: " + g.CompletionCriterion
		}
		tokens := estimateTokensFromText(goalCtx)
		result.SystemPrompt += tokens
		result.SystemPromptParts = append(result.SystemPromptParts, ContextBreakdownPart{
			Label:  "目标上下文",
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

	result.ToolSchemas = estimateToolSchemaTokens(a.buildToolsForConfig(cfg))
	finalizeContextBreakdownTotal(&result)
	return result
}

// liveBreakdownAccumulator exploits the append-only shape of the runChat
// message list. It scans only messages added since the previous step; callers
// reset it after goal rebuilds or context compaction, where the slice is
// replaced wholesale.
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

// handleTodoList implements the todo_write tool.
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

	// Replace mode
	updated := cloneTodos(req.Todos)
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

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	goruntime "runtime"

	openai "github.com/sashabaranov/go-openai"
)

// ── Sub-agent management (frontend bindings) ──

// GetSubagents returns all sub-agent runs, both running and finished.
func (a *App) GetSubagents() []*SubagentRun {
	a.subRunsMu.Lock()
	defer a.subRunsMu.Unlock()
	result := make([]*SubagentRun, 0, len(a.subRuns))
	for _, r := range a.subRuns {
		result = append(result, cloneSubagentRun(r))
	}
	return result
}

func cloneSubagentRun(r *SubagentRun) *SubagentRun {
	if r == nil {
		return nil
	}
	c := *r
	c.cancel = nil
	c.FilesRead = append([]string(nil), r.FilesRead...)
	c.FilesEdited = append([]string(nil), r.FilesEdited...)
	c.ToolCalls = append([]SubToolEvent(nil), r.ToolCalls...)
	return &c
}

// StopSubagent cancels a running sub-agent.
func (a *App) StopSubagent(subID string) error {
	a.subRunsMu.Lock()
	run := a.subRuns[subID]
	if run == nil {
		a.subRunsMu.Unlock()
		return fmt.Errorf("sub-agent not found: %s", subID)
	}
	if run.Status != "running" {
		a.subRunsMu.Unlock()
		return fmt.Errorf("sub-agent is not running: %s", subID)
	}
	cancel := run.cancel
	a.subRunsMu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// ── Sub-agent execution loop ─────────────────────────────────

func (a *App) executeDelegate(ctx context.Context, cfg ConfigState, sessionID string, req AgentDelegateRequest, cancel context.CancelFunc) (*AgentDelegateResult, error) {
	if strings.TrimSpace(req.Task) == "" {
		return nil, errors.New("task is required")
	}
	model := cfg.Model
	if req.Model != "" {
		model = req.Model
	}
	subID := newID()
	// Keep concurrent sub-agents on independent cache routes. The key is
	// process-local and is consumed by the Responses adapter for every
	// compatible endpoint.
	cfg.responsesPromptCacheKey = openAIResponsesPromptCacheKey("subagent:" + subID)
	desc := req.Description
	if desc == "" {
		desc = req.Task
		if len(desc) > 40 {
			desc = desc[:37] + "..."
		}
	}

	run := &SubagentRun{
		ID:          subID,
		SessionID:   sessionID,
		Description: desc,
		Profile:     "coder",
		Status:      "running",
		StartTime:   time.Now().UnixMilli(),
		cancel:      cancel,
	}
	a.subRunsMu.Lock()
	a.subRuns[subID] = run
	a.subRunsMu.Unlock()
	defer a.finishSubagentRecord(subID)
	spawnPayload := map[string]any{"id": subID, "sessionId": sessionID, "description": desc, "profile": "coder", "startTime": run.StartTime}
	if meta, ok := ctx.Value(toolExecutionMetaContextKey{}).(toolExecutionMeta); ok {
		spawnPayload["runId"] = meta.runID
		spawnPayload["toolBatchId"] = meta.toolBatchID
		spawnPayload["toolCallIndex"] = meta.toolCallIndex
		spawnPayload["toolCallId"] = meta.toolCallID
	}
	a.emit("sub:spawn", spawnPayload)

	// Build messages for the sub-agent
	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: subagentSystemPrompt()},
	}
	if !req.CleanContext {
		env := a.buildSubagentEnv(cfg)
		messages = append(messages, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: env})
		if instructionContext := a.buildSubagentInstructionContext(cfg); instructionContext != "" {
			messages = append(messages, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: instructionContext})
		}
	}
	messages = append(messages, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: "## Task\n" + req.Task})

	// Run sub-agent loop synchronously (the caller is already in a goroutine for parallel)
	tools := req.tools
	if len(tools) == 0 {
		tools = a.subagentTools(cfg)
	}

	var filesRead []string
	var filesEdited []string
	seenFiles := map[string]bool{}
	step := 0
	for req.maxSteps <= 0 || step < req.maxSteps {
		select {
		case <-ctx.Done():
			a.subRunsMu.Lock()
			run.Status = "failed"
			run.Error = "cancelled"
			run.Steps = step
			a.subRunsMu.Unlock()
			a.emit("sub:error", map[string]any{"id": subID, "sessionId": sessionID, "error": "cancelled", "durationMs": time.Now().UnixMilli() - run.StartTime})
			return &AgentDelegateResult{AgentID: subID, Description: desc, Status: "failed", Steps: step, Error: "cancelled"}, ctx.Err()
		default:
		}

		// Retry transient LLM errors (429/5xx/network) at the sub-agent loop
		// level. Streaming-adapter retries only cover connection setup and
		// (for Anthropic) pre-first-event errors; this outer wrapper also
		// retries mid-stream and stop errors so a flaky provider doesn't
		// abort the whole sub-agent task.
		// 多 key 时内层 streamModelResponse 已用 maxMultiKeyAttempts 预算统一
		// 承担重试与故障切换,这里归零避免两层预算重叠(外层再套一层只会
		// 放大尝试次数;冷却会使后续轮次立即 break)。
		maxRetries := effectiveLLMRetries(cfg)
		if len(resolveKeyPool(cfg)) > 1 {
			maxRetries = 0
		}
		var modelResp *modelStreamResult
		var err error
		for attempt := 0; ; attempt++ {
			modelResp, err = a.streamModelResponse(ctx, cfg, model, messages, tools, nil)
			if err == nil {
				err = modelResponseStopError(cfg, modelResp)
			}
			if err == nil {
				break
			}
			if attempt < maxRetries && shouldRetryLLMError(err) {
				wait := llmRetryDelay(attempt + 1)
				select {
				case <-time.After(wait):
				case <-ctx.Done():
					a.subRunsMu.Lock()
					run.Status = "failed"
					run.Error = "cancelled"
					run.Steps = step
					a.subRunsMu.Unlock()
					a.emit("sub:error", map[string]any{"id": subID, "sessionId": sessionID, "error": "cancelled", "durationMs": time.Now().UnixMilli() - run.StartTime})
					return &AgentDelegateResult{AgentID: subID, Description: desc, Status: "failed", Steps: step, Error: "cancelled", Model: model}, ctx.Err()
				}
				continue
			}
			a.subRunsMu.Lock()
			run.Status = "failed"
			run.Error = err.Error()
			run.Steps = step
			a.subRunsMu.Unlock()
			a.emit("sub:error", map[string]any{"id": subID, "sessionId": sessionID, "error": err.Error(), "durationMs": time.Now().UnixMilli() - run.StartTime})
			return &AgentDelegateResult{AgentID: subID, Description: desc, Status: "failed", Steps: step, Error: err.Error(), Model: model}, err
		}

		// Accumulate token usage from the sub-agent's model response
		if modelResp.Usage != nil {
			a.subRunsMu.Lock()
			run.InputTokens += modelResp.Usage.PromptTokens
			run.OutputTokens += modelResp.Usage.CompletionTokens
			run.TotalTokens = run.InputTokens + run.OutputTokens
			a.subRunsMu.Unlock()
		}
		fallbackInput := 0
		fallbackOutput := 0
		if modelResp.Usage == nil || modelResp.Usage.PromptTokens <= 0 {
			fallbackInput = estimateRequestTokens(messages, tools)
		}
		if modelResp.Usage == nil || modelResp.Usage.CompletionTokens <= 0 {
			fallbackOutput = estimateCompletionTokens(modelResp.Content, modelResp.Reasoning, modelResp.ToolCalls)
		}
		a.recordTokenStats(
			cfg.ProviderName,
			model,
			cfg.Workspace,
			sessionID,
			"subagent",
			modelResp.Usage,
			fallbackInput,
			fallbackOutput,
		)

		assistantMessage := openai.ChatCompletionMessage{
			Role:      openai.ChatMessageRoleAssistant,
			Content:   modelResp.Content,
			ToolCalls: modelResp.ToolCalls,
		}

		if len(assistantMessage.ToolCalls) == 0 {
			// Sub-agent finished — extract summary
			summary := strings.TrimSpace(assistantMessage.Content)
			step++
			a.subRunsMu.Lock()
			run.Status = "completed"
			run.Summary = summary
			run.Steps = step
			run.FilesRead = filesRead
			run.FilesEdited = filesEdited
			a.subRunsMu.Unlock()
			a.emit("sub:done", map[string]any{
				"id": subID, "sessionId": sessionID, "status": "completed", "steps": step,
				"summary": summary, "filesRead": filesRead, "filesEdited": filesEdited, "durationMs": time.Now().UnixMilli() - run.StartTime,
				"inputTokens": run.InputTokens, "outputTokens": run.OutputTokens, "totalTokens": run.TotalTokens,
			})
			return &AgentDelegateResult{
				AgentID: subID, Description: desc, Status: "completed",
				Steps: step, Summary: summary,
				FilesRead: filesRead, FilesEdited: filesEdited, Model: model,
			}, nil
		}

		// Execute tool calls — parallel for non-file tools, ordered for file mutations
		toolIDs := make([]string, len(assistantMessage.ToolCalls))
		for i := range assistantMessage.ToolCalls {
			if assistantMessage.ToolCalls[i].ID == "" {
				assistantMessage.ToolCalls[i].ID = fmt.Sprintf("subcall_%s_%d", subID, i)
			}
			toolIDs[i] = assistantMessage.ToolCalls[i].ID
		}
		messages = append(messages, assistantMessage)
		toolConflicts := detectToolBatchConflicts(cfg, assistantMessage.ToolCalls)

		// Register all tool calls as running and emit start events
		for i, call := range assistantMessage.ToolCalls {
			name := call.Function.Name
			args := call.Function.Arguments
			cid := toolIDs[i]
			a.subRunsMu.Lock()
			run.ToolCalls = append(run.ToolCalls, SubToolEvent{ToolCallID: cid, Name: name, Args: truncateRunes(args, 4096), Status: "running"})
			if len(run.ToolCalls) > maxSubagentToolCalls {
				run.ToolCalls = append([]SubToolEvent(nil), run.ToolCalls[len(run.ToolCalls)-maxSubagentToolCalls:]...)
			}
			a.subRunsMu.Unlock()
			a.emit("sub:tool:start", map[string]any{"id": subID, "sessionId": sessionID, "toolCallId": cid, "name": name, "args": args})
		}

		// Parallel execution: non-file tools run concurrently, file mutations run afterward in order
		type subToolOutcome struct {
			index     int
			callID    string
			name      string
			args      string
			result    toolResult
			modelJSON string
			duration  int64
		}
		totalCalls := len(assistantMessage.ToolCalls)
		subToolSem := make(chan struct{}, 4)
		subOutcomes := make([]subToolOutcome, totalCalls)

		// emitSubOutcome updates the sub-run tool-call status and emits the
		// result/error event for a single outcome. Called immediately after each
		// tool finishes (from within the worker goroutine for parallel calls, or
		// serially for ordered file mutations) so the UI reflects completion as
		// soon as each tool finishes, instead of waiting for the slowest tool in
		// the batch. run.ToolCalls is guarded by subRunsMu and a.emit is
		// concurrency-safe, so this is safe to call from multiple goroutines.
		emitSubOutcome := func(idx int) {
			o := subOutcomes[idx]
			if o.result.OK {
				summary := toolResultSummary(o.name, &o.result)
				a.subRunsMu.Lock()
				for ti := range run.ToolCalls {
					if run.ToolCalls[ti].ToolCallID == o.callID {
						run.ToolCalls[ti].Status = "success"
						run.ToolCalls[ti].Summary = truncateRunes(summary, 2048)
						run.ToolCalls[ti].DurationMS = o.duration
						break
					}
				}
				a.subRunsMu.Unlock()
				a.emit("sub:tool:result", map[string]any{"id": subID, "sessionId": sessionID, "toolCallId": o.callID, "name": o.name, "summary": summary, "durationMs": o.duration})
			} else {
				a.subRunsMu.Lock()
				for ti := range run.ToolCalls {
					if run.ToolCalls[ti].ToolCallID == o.callID {
						run.ToolCalls[ti].Status = "error"
						run.ToolCalls[ti].Summary = truncateRunes(o.result.Error, 2048)
						run.ToolCalls[ti].DurationMS = o.duration
						break
					}
				}
				a.subRunsMu.Unlock()
				a.emit("sub:tool:error", map[string]any{"id": subID, "sessionId": sessionID, "toolCallId": o.callID, "name": o.name, "error": o.result.Error, "errorCode": o.result.ErrorCode, "durationMs": o.duration})
			}
		}

		executeSubCall := func(idx int, c openai.ToolCall) {
			started := time.Now()
			r := a.executeTool(ctx, cfg, sessionID, c.Function.Name, []byte(c.Function.Arguments))
			duration := time.Since(started).Milliseconds()
			rj, _ := json.Marshal(r)
			fullJSON := string(rj)
			subOutcomes[idx] = subToolOutcome{
				index: idx, callID: toolIDs[idx], name: c.Function.Name,
				args: c.Function.Arguments, result: r,
				modelJSON: compactToolResultForModel(c.Function.Name, r, fullJSON), duration: duration,
			}
			emitSubOutcome(idx)
		}
		setSubConflictOutcome := func(idx int, c openai.ToolCall, conflictErr error) {
			r := toolErrorResult(conflictErr)
			rj, _ := json.Marshal(r)
			fullJSON := string(rj)
			subOutcomes[idx] = subToolOutcome{
				index: idx, callID: toolIDs[idx], name: c.Function.Name,
				args: c.Function.Arguments, result: r,
				modelJSON: fullJSON,
			}
			emitSubOutcome(idx)
		}

		var subWg sync.WaitGroup
		for i, call := range assistantMessage.ToolCalls {
			if conflictErr, conflict := toolConflicts[i]; conflict {
				setSubConflictOutcome(i, call, conflictErr)
				continue
			}
			if isOrderedFileMutationTool(call.Function.Name) {
				continue
			}
			subWg.Add(1)
			go func(idx int, c openai.ToolCall) {
				defer subWg.Done()
				subToolSem <- struct{}{}
				defer func() { <-subToolSem }()
				executeSubCall(idx, c)
			}(i, call)
		}
		subWg.Wait()
		for i, call := range assistantMessage.ToolCalls {
			if _, conflict := toolConflicts[i]; conflict || !isOrderedFileMutationTool(call.Function.Name) {
				continue
			}
			executeSubCall(i, call)
		}

		// Process outcomes in order. File tracking and the model-facing tool
		// messages must stay ordered (filesRead/filesEdited are order-sensitive
		// shared slices); the UI-facing status/emit already happened above in
		// emitSubOutcome as each tool completed.
		for _, o := range subOutcomes {
			trackFileFromToolResult(o.name, o.args, &o.result, &filesRead, &filesEdited, seenFiles)
			messages = append(messages, openai.ChatCompletionMessage{
				Role: openai.ChatMessageRoleTool, ToolCallID: o.callID, Content: o.modelJSON,
			})
		}
		step++
		a.subRunsMu.Lock()
		run.Steps = step
		a.subRunsMu.Unlock()
		a.emit("sub:step", map[string]any{"id": subID, "sessionId": sessionID, "step": step, "inputTokens": run.InputTokens, "outputTokens": run.OutputTokens, "totalTokens": run.TotalTokens})
	}

	a.subRunsMu.Lock()
	run.Status = "timed_out"
	run.Steps = step
	run.FilesRead = filesRead
	run.FilesEdited = filesEdited
	a.subRunsMu.Unlock()
	a.emit("sub:done", map[string]any{
		"id": subID, "sessionId": sessionID, "status": "timed_out", "steps": step,
		"filesRead": filesRead, "filesEdited": filesEdited, "durationMs": time.Now().UnixMilli() - run.StartTime,
		"inputTokens": run.InputTokens, "outputTokens": run.OutputTokens, "totalTokens": run.TotalTokens,
	})
	return &AgentDelegateResult{
		AgentID: subID, Description: desc, Status: "timed_out",
		Steps: step, FilesRead: filesRead, FilesEdited: filesEdited, Model: model,
		Error: fmt.Sprintf("reached scheduled-task step limit (%d)", req.maxSteps),
	}, nil
}

func (a *App) finishSubagentRecord(subID string) {
	a.subRunsMu.Lock()
	defer a.subRunsMu.Unlock()
	if run := a.subRuns[subID]; run != nil {
		run.cancel = nil
		run.Summary = truncateRunes(run.Summary, 32*1024)
		run.Error = truncateRunes(run.Error, 8*1024)
	}
	type finishedRun struct {
		id      string
		started int64
	}
	finished := make([]finishedRun, 0, len(a.subRuns))
	for id, run := range a.subRuns {
		if run != nil && run.Status != "running" {
			finished = append(finished, finishedRun{id: id, started: run.StartTime})
		}
	}
	if len(finished) <= maxFinishedSubagents {
		return
	}
	sort.Slice(finished, func(i, j int) bool { return finished[i].started < finished[j].started })
	for _, item := range finished[:len(finished)-maxFinishedSubagents] {
		delete(a.subRuns, item.id)
	}
}

func (a *App) acquireSubagentSlot(ctx context.Context) error {
	select {
	case a.subSem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *App) releaseSubagentSlot() {
	<-a.subSem
}

// subagentSystemPrompt returns the system prompt for sub-agents.
func subagentSystemPrompt() string {
	osName := goruntime.GOOS
	arch := goruntime.GOARCH
	platformNote := "Platform: "
	switch osName {
	case "windows":
		platformNote += "Windows"
	case "linux":
		platformNote += "Linux"
	case "darwin":
		platformNote += "macOS"
	default:
		platformNote += osName
	}
	platformNote += " (" + arch + ")"
	if pythonLine := buildPythonRuntimeLine(); pythonLine != "" {
		platformNote += ". " + pythonLine
	}

	return "You are an Ally sub-agent. Complete the delegated task using available tools, then return a concise summary.\n\n" +
		"# Tool Use\n\n" +
		"Prefer dedicated tools over shell commands: `grep_files` for search, `read` for file content, `edit`/`create_file`/`delete_path` for file changes, `list_files` for directory listings.\n\n" +
		sharedBatchStrategy() +
		"# Editing Files\n\n" +
		sharedEditRules() + "\n" +
		"# Coding Guidelines\n\n" +
		sharedCodingGuidelines() + "\n" +
		"# Safety\n\n" +
		sharedSafetyBoundaries() +
		"- Do NOT ask the user questions — the user cannot see you.\n" +
		"- Do NOT call `subagent` — nested delegation is not supported.\n" +
		"- MCP tools are available when connected. Use them when they materially help the delegated task, and treat their results like any other tool output.\n" +
		"- Do NOT write global memories. The parent agent owns durable memory decisions.\n" +
		"- Use network tools only when the delegated task explicitly requires external information.\n" +
		"- When creating intermediate artifacts (scripts, drafts, test fixtures) that are not final deliverables, place them under a `.tmp/` directory within the workspace.\n\n" +
		"# Output\n\n" +
		"- Be concise. The parent agent only sees your final summary.\n" +
		"- When done, write a summary of what you did, which files you changed, and any verification results.\n" +
		"- Use `wait` only for a concrete short delay after asynchronous work has started. It must be the only tool call in that response.\n" +
		"- For remote work, every remote tool call must include an explicit target such as host:/absolute/workspace.\n" +
		"- " + platformNote + ". Use command syntax appropriate for this platform."
}

// buildSubagentEnv builds zero-cost workspace context for the sub-agent.
func (a *App) buildSubagentEnv(cfg ConfigState) string {
	var b strings.Builder
	b.WriteString("Workspace: " + cfg.Workspace + "\n")

	entries, err := a.listFilesWithConfig(cfg, ListFilesRequest{MaxDepth: 2, Limit: 60})
	if err == nil && len(entries.Entries) > 0 {
		b.WriteString("\n## Project files\n")
		for _, e := range entries.Entries {
			tag := ""
			if e.Dir {
				tag = "/"
			}
			b.WriteString(e.Path + tag + "\n")
		}
	}
	return b.String()
}

func (a *App) buildSubagentInstructionContext(cfg ConfigState) string {
	var b strings.Builder
	if cfg.Workspace != "" {
		if md := loadAgentsMd(cfg.Workspace); strings.TrimSpace(md) != "" {
			b.WriteString("## Lower-priority project instructions\n")
			b.WriteString("Follow these project instructions when they do not conflict with the sub-agent system rules or the delegated task.\n\n")
			b.WriteString(md)
			b.WriteString("\n")
		}
		if cg := buildCodeGraphPromptPart(cfg.Workspace); cg != "" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString("## Lower-priority project code graph\n")
			b.WriteString("Use this as a reference map only. Verify current files before relying on it, and follow the sub-agent system rules and delegated task first.\n")
			b.WriteString(cg)
			b.WriteString("\n")
		}
	}
	if custom := strings.TrimSpace(cfg.CustomPrompt); custom != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("## Lower-priority custom instructions\n")
		b.WriteString("Follow these custom instructions when they do not conflict with the sub-agent system rules or the delegated task.\n\n")
		b.WriteString(custom)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// subagentTools returns tools available to sub-agents, excluding parent-owned
// session state and tools that require interaction with the visible user.
func (a *App) subagentTools(cfg ConfigState) []openai.Tool {
	all := a.buildToolsForConfig(cfg)
	filtered := make([]openai.Tool, 0, len(all)-1)
	blocked := map[string]bool{
		"subagent":       true,
		"agent_delegate": true,
		"create_goal":    true,
		"update_goal":    true,
		"get_goal":       true,
		"todo_write":     true,
		"skill":          true,
		"memory_write":   true,
		"scheduled_task": true,
		"ask":            true,
	}
	for _, t := range all {
		if t.Function != nil {
			if blocked[t.Function.Name] {
				continue
			}
		}
		filtered = append(filtered, t)
	}
	return filtered
}

// trackFileFromToolResult extracts files read/edited from tool calls for the sub-agent summary.
func trackFileFromToolResult(name string, args string, result *toolResult, filesRead *[]string, filesEdited *[]string, seen map[string]bool) {
	addPath := func(list *[]string, p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		*list = append(*list, p)
	}
	switch name {
	case "read", "read_file", "batch_read":
		// extract path from args
		var req struct {
			Path  string   `json:"path"`
			Paths []string `json:"paths"`
			Files []struct {
				Path string `json:"path"`
			} `json:"files"`
		}
		if json.Unmarshal([]byte(args), &req) == nil {
			if req.Path != "" {
				addPath(filesRead, req.Path)
			}
			for _, p := range req.Paths {
				addPath(filesRead, p)
			}
			for _, file := range req.Files {
				addPath(filesRead, file.Path)
			}
		}
	case "edit":
		var req ModelEditToolRequest
		if json.Unmarshal([]byte(args), &req) == nil {
			for _, file := range req.Files {
				addPath(filesEdited, file.Path)
			}
		}
	case "create_file", "replace_exact", "replace_lines":
		var req struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(args), &req) == nil && req.Path != "" {
			addPath(filesEdited, req.Path)
		}
	}
}

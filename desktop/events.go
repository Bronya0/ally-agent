// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	backend "ally-dev/internal/app"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// dispatchEvent is called from the backend run loop. Payload maps are
// serialized before queueing because the backend may reuse them after Emit
// returns.
func (a *desktop) dispatchEvent(name string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	if a.win == nil {
		return
	}
	a.win.Update(func() {
		var p map[string]any
		_ = json.Unmarshal(data, &p)
		a.applyEvent(name, p)
	})
}

func (a *desktop) applyEvent(name string, p map[string]any) {
	if a.closing && name != "service:update" {
		return
	}
	switch name {
	case "service:update":
		a.applyServiceUpdate(p)
		return
	case "mcp:status":
		a.applyMCPStatus(p)
	case "config:warning":
		if msg := str(p, "message"); msg != "" {
			a.status = msg
		}
	case "plan:update":
		a.applyPlanUpdate(p)
	case "tokens:update":
		a.applyTokenUsage(p)
	case "ssh:clusters-changed":
		a.refreshSSH()
	case "git:changed":
		if t := a.current(); t != nil && strings.EqualFold(str(p, "workspace"), t.Workspace) {
			a.refreshGit(t)
		}
	case "scheduled:update":
		a.applyScheduledUpdate(p)
	case "update:ready", "update:error", "update:applied", "update:cancelled":
		a.applyUpdateEvent(name, p)
		return
	}

	if strings.HasPrefix(name, "sub:") {
		a.applySubagentEvent(name, p)
		return
	}

	var t *conversation
	for _, tab := range a.tabs {
		if tab.ID == str(p, "sessionId") {
			t = tab
			break
		}
	}
	if t == nil {
		if name == "compact:start" || name == "compact:done" || name == "run:compact" || name == "run:compacted" {
			// A background session has no tab here, so its compaction can only be
			// reported in the window status line.
			a.status = "正在压缩上下文…"
			if name == "compact:done" || name == "run:compacted" {
				a.status = ""
			}
		}
		return
	}
	ui := t.uiState()
	// A retry notice is about the attempt that just failed; it stays up only while
	// the run waits for the next one. Every other event that reaches this tab
	// means the run already moved past that failure, so the notice is dropped
	// here, once, instead of in a list of "progress" events: such a list has to be
	// kept in sync by hand, and whatever is missing (ask:ready, a compaction…)
	// leaves "重试中 1/6" on screen until the whole run ends. The one exception
	// below both re-arms it and is the only writer of this field.
	if name != "run:retry" {
		ui.Retry = nil
	}

	switch name {
	case "run:start":
		t.RunID = str(p, "runId")
		t.Busy = true
		t.Status = "正在连接模型…"
		if t.CancelRequested && t.RunID != "" {
			id := t.RunID
			t.CancelRequested = false
			a.job(func() error { return a.core.CancelRun(id) })
			t.Status = "正在取消…"
		}
		ui.ThinkingBytes = 0
		ui.Follow = true
		ui.CompactNote = ""
	case "run:attachment":
		a.recordAttachmentReference(t, p)
	case "run:image":
		a.recordGeneratedImage(t, p)
	case "run:stream", "run:delta":
		if n := int(num(p, "reasoningLen")); n > 0 {
			t.uiState().ThinkingBytes += n
		}
		content := str(p, "content")
		if content != "" {
			n := len(t.Messages)
			// A summary that replaced the transcript is not this run's answer: the
			// next delta opens a fresh row instead of growing the summary.
			if n == 0 || str(t.Messages[n-1], "role") != "assistant" || str(t.Messages[n-1], "toolCallId") != "" || boolOf(t.Messages[n-1], transcriptSummaryFlag) {
				t.Messages = append(t.Messages, map[string]any{"role": "assistant", "content": ""})
			}
			m := t.Messages[len(t.Messages)-1]
			m["content"] = str(m, "content") + content
			t.Status = "正在回复…"
		} else {
			t.Status = "正在思考…"
		}
		// Reasoning only changes the footer, not transcript geometry.
		if content != "" && ui.Follow {
			t.uiState().FollowPending = true
		}
	case "tool:start", "tool:update", "tool:result", "tool:error":
		a.applyToolEvent(t, name, p)
	case "ask:ready":
		t.AskID = str(p, "askId")
		b, _ := json.Marshal(p["questions"])
		_ = json.Unmarshal(b, &t.Questions)
		t.Answers = make([]string, len(t.Questions))
		t.Selected = make([]string, len(t.Questions))
		ui.AskIndex = 0
		ui.AskSelected = map[string][]string{}
		ui.AskCustom = map[string]bool{}
		ui.AskCustomTxt = map[string]string{}
		ui.AskSubmitted = false
		t.Status = "等待你的回答"
	case "ask:closed":
		t.AskID = ""
		t.Questions = nil
		ui.AskSubmitted = false
	case "run:retry":
		ui.Retry = &retryInfo{
			Attempt:     int(num(p, "attempt")),
			MaxAttempts: int(num(p, "maxAttempts")),
			Error:       str(p, "error"),
			KeyIndex:    int(num(p, "keyIndex")),
			TotalKeys:   int(num(p, "totalKeys")),
		}
		if ui.Retry.Error == "" {
			ui.Retry.Error = str(p, "reason")
		}
		t.Status = fmt.Sprintf("请求重试中（%d/%d）…", ui.Retry.Attempt, ui.Retry.MaxAttempts)
	case "run:mcp-wait":
		var names []string
		for _, item := range list(p, "servers") {
			if m, ok := item.(map[string]any); ok {
				if n := str(m, "name"); n != "" {
					names = append(names, n)
				}
			} else if s, ok := item.(string); ok {
				names = append(names, s)
			}
		}
		if len(names) > 0 {
			t.Status = "正在等待 MCP：" + strings.Join(names, "、")
		} else {
			t.Status = "正在等待 MCP…"
		}
	case "run:compact":
		// The automatic path announces its summary request before running it: this
		// is a start, not a result. The result arrives as run:compacted.
		ui.CompactBusy = true
		ui.CompactThinking = 0
		ui.CompactNote = compactStartNote(p)
		t.Status = ui.CompactNote
	case "compact:start":
		ui.CompactBusy = true
		ui.CompactThinking = 0
		ui.CompactNote = compactStartNote(p)
		t.Status = ui.CompactNote
		// A previous compaction that never closed must not leave its half-written
		// summary in front of the new one.
		dropCompactPreview(t)
	case "compact:delta":
		// The summary streams like a normal answer, so the user watches it being
		// written instead of a frozen timer.
		a.compactDelta(t, str(p, "content"), int(num(p, "reasoningLen")))
	case "compact:done":
		ui.CompactBusy = false
		ui.CompactThinking = 0
		ui.CompactNote = ""
		if ok, _ := p["ok"].(bool); !ok {
			// A failed or cancelled compaction must not leave its half-written
			// summary in the transcript as if it had been applied.
			dropCompactPreview(t)
		}
	case "run:compacted":
		ui.CompactBusy = false
		ui.CompactThinking = 0
		ui.CompactNote = ""
		if msg := str(p, "error"); msg != "" {
			if t == a.current() {
				a.notify("上下文压缩失败：" + msg)
			}
			a.refreshSessionFooter(t)
			return
		}
		// The backend already replaced or trimmed the history: mirror that here,
		// otherwise the transcript keeps showing a conversation the next request
		// is no longer built from.
		a.applyCompactionResult(t, p)
		a.persist(t)
		a.refreshSessionFooter(t)
		if t == a.current() {
			a.notify(compactToastText(int(num(p, "tokensBefore")), int(num(p, "tokensAfter"))))
		}
	case "run:inject":
		t.Status = "已注入新消息，继续执行…"
	case "run:done", "run:error":
		ui.PlanCollapsed = true
		for _, call := range t.Messages {
			if str(call, "role") == "tool_call" && toolState(call) == "running" {
				call["status"] = "success"
				call["toolState"] = "tool:result"
				if name == "run:error" {
					call["status"] = "error"
					call["error"] = str(p, "error")
				}
			}
		}
		t.Busy = false
		t.RunID = ""
		t.CancelRequested = false
		t.AskID = ""
		ui.CompactBusy = false
		if name == "run:done" {
			t.Status = "任务完成"
		} else {
			msg := str(p, "error")
			if msg == "" {
				msg = "运行失败"
			}
			t.Status = msg
			a.status = msg
			if !hasErrorTail(t.Messages, msg) {
				t.Messages = append(t.Messages, map[string]any{"role": "assistant", "content": msg, "error": true})
			}
		}
		idx := lastContentIndex(t.Messages)
		if idx >= 0 {
			stat := &roundStat{
				DurationMS:   int64(num(p, "durationMs")),
				InputTokens:  int(num(p, "inputTokens")),
				OutputTokens: int(num(p, "outputTokens")),
				CacheHit:     int(num(p, "cacheHit")),
				CacheMiss:    int(num(p, "cacheMiss")),
				Done:         true,
			}
			ui.Round[idx] = stat
			if m := t.Messages[idx]; name == "run:done" {
				m["roundDoneAt"] = time.Now().UnixMilli()
				m["roundStats"] = map[string]any{"durationMs": stat.DurationMS, "inputTokens": stat.InputTokens, "outputTokens": stat.OutputTokens, "cacheHit": stat.CacheHit, "cacheMiss": stat.CacheMiss}
			}
		}
		a.persist(t)
		a.refreshSessionFooter(t)
		if t == a.current() {
			a.refreshFiles()
			a.refreshGit(t)
		}
	}
}

func hasErrorTail(messages []map[string]any, msg string) bool {
	if len(messages) == 0 {
		return false
	}
	last := messages[len(messages)-1]
	return str(last, "role") == "assistant" && str(last, "content") == msg && last["error"] != nil
}

func lastContentIndex(messages []map[string]any) int {
	for i := len(messages) - 1; i >= 0; i-- {
		role := str(messages[i], "role")
		if role == "assistant" || role == "user" {
			return i
		}
	}
	return -1
}

func (a *desktop) applyToolEvent(t *conversation, name string, p map[string]any) {
	id := str(p, "toolCallId")
	var m map[string]any
	for i := len(t.Messages) - 1; i >= 0; i-- {
		if id != "" && str(t.Messages[i], "toolCallId") == id {
			m = t.Messages[i]
			break
		}
	}
	if m == nil {
		m = map[string]any{"role": "tool_call", "toolCallId": id}
		t.Messages = append(t.Messages, m)
	}
	for _, k := range []string{"name", "args", "result", "error", "errorCode", "durationMs", "mcpServer", "mcpTool"} {
		if v, ok := p[k]; ok {
			m[k] = v
		}
	}
	if name == "tool:update" && (str(m, "status") == "success" || str(m, "status") == "error") {
		return
	}
	m["toolState"] = name
	if name == "tool:result" {
		if started, ok := m["startedAt"].(time.Time); ok && num(m, "durationMs") == 0 {
			m["durationMs"] = time.Since(started).Milliseconds()
		}
		m["status"] = "success"
		m["body"] = p["result"]
	}
	if name == "tool:error" {
		m["status"] = "error"
		m["body"] = p["error"]
	}
	if name == "tool:start" {
		m["startedAt"] = time.Now()
		m["status"] = "running"
	}
	if name == "tool:update" && m["status"] != "success" && m["status"] != "error" {
		m["status"] = "running"
	}
	if name == "tool:result" {
		a.attachToolImage(t, m)
		if toolKind(str(m, "name")) == "render_html" {
			a.loadHTMLPreview(t, m)
		}
	}
	toolName := str(p, "name")
	if toolName != "" {
		t.Status = "正在使用 " + toolName
	}
	if t.uiState().Follow {
		t.uiState().FollowPending = true
	}
	if name == "tool:result" || name == "tool:error" {
		// A tool result is the biggest single jump in what the next request will
		// carry: refresh the context meter here, not at the end of the run.
		a.refreshSessionFooter(t)
	}
}

func (a *desktop) applyPlanUpdate(p map[string]any) {
	sid := str(p, "sessionId")
	if sid == "" {
		return
	}
	raw, _ := json.Marshal(p["plan"])
	var steps []backend.PlanStep
	if json.Unmarshal(raw, &steps) != nil {
		return
	}
	if a.plans == nil {
		a.plans = map[string][]backend.PlanStep{}
	}
	if a.planRev == nil {
		a.planRev = map[string]int64{}
	}
	a.plans[sid] = steps
	complete := len(steps) > 0
	for _, step := range steps {
		if step.Status != "done" && step.Status != "completed" {
			complete = false
		}
	}
	for _, t := range a.tabs {
		if t.ID == sid {
			t.uiState().PlanCollapsed = complete
		}
	}
	a.planRev[sid] = int64(num(p, "revision"))
}

func (a *desktop) applyTokenUsage(p map[string]any) {
	if a.current() == nil {
		return
	}
	workspace := str(p, "workspace")
	if !sameWorkspace(workspace, a.current().Workspace) {
		return
	}
	a.current().uiState().Usage = backend.WorkspaceTokenUsage{
		InputTokens:  int(num(p, "inputTokens")),
		OutputTokens: int(num(p, "outputTokens")),
	}
}

func sameWorkspace(a, b string) bool {
	if a == "" || b == "" {
		return a == b
	}
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func (a *desktop) applyServiceUpdate(p map[string]any) {
	b, _ := json.Marshal(p["service"])
	var service backend.ServiceInfo
	if json.Unmarshal(b, &service) != nil || service.ID == "" {
		return
	}
	for i := range a.services {
		if a.services[i].ID == service.ID {
			a.services[i] = service
			if a.tasksF.LogService == service.ID && a.tasksF.LogOpen {
				a.tasksF.LogBody = service.OutputTail
				a.tasksF.LogMeta = serviceMeta(service)
			}
			return
		}
	}
	a.services = append([]backend.ServiceInfo{service}, a.services...)
}

func (a *desktop) applyMCPStatus(p map[string]any) {
	b, _ := json.Marshal(p["servers"])
	var servers []map[string]any
	_ = json.Unmarshal(b, &servers)
	a.mcpServers = servers
	a.mcpSummary = mcpSummary(servers)
	go func() {
		tools := a.core.ListTools()
		a.win.Update(func() {
			if !a.closing {
				a.toolInventory = tools
				a.toolCount = enabledToolCount(tools)
			}
		})
	}()
}

func (a *desktop) applyScheduledUpdate(p map[string]any) {
	if id := str(p, "deleted"); id != "" {
		for i := range a.tasks {
			if a.tasks[i].ID == id {
				a.tasks = append(a.tasks[:i], a.tasks[i+1:]...)
				return
			}
		}
		return
	}
	raw, ok := p["task"]
	if !ok {
		return
	}
	b, _ := json.Marshal(raw)
	var task backend.ScheduledTask
	if json.Unmarshal(b, &task) != nil || task.ID == "" {
		return
	}
	for i := range a.tasks {
		if a.tasks[i].ID == task.ID {
			a.tasks[i] = task
			return
		}
	}
	a.tasks = append(a.tasks, task)
}

func (a *desktop) applySubagentEvent(name string, p map[string]any) {
	sid := str(p, "sessionId")
	var t *conversation
	for _, tab := range a.tabs {
		if tab.ID == sid {
			t = tab
			break
		}
	}
	if t == nil {
		return
	}
	id := str(p, "id")
	if id == "" {
		return
	}
	// A sub-agent row is a message so it keeps its chronological place among
	// the parent's tool cards; the event stream keeps that row up to date.
	var row map[string]any
	for i := len(t.Messages) - 1; i >= 0; i-- {
		if str(t.Messages[i], "role") == "subagent" && str(t.Messages[i], "subId") == id {
			row = t.Messages[i]
			break
		}
	}
	if row == nil {
		row = map[string]any{"role": "subagent", "subId": id, "parentToolCallId": str(p, "toolCallId")}
		t.Messages = append(t.Messages, row)
		// Nudge the reader only when a new sub-agent row appears.
		if t.uiState().Follow {
			t.uiState().FollowPending = true
		}
	}
	for _, k := range []string{"description", "profile", "status", "maxSteps", "startTime", "stopReason", "summary", "error", "step"} {
		if v, ok := p[k]; ok {
			row[k] = v
		}
	}
	for _, k := range []string{"inputTokens", "outputTokens", "totalTokens", "contextTokens", "cacheHitTokens", "cacheMissTokens", "durationMs"} {
		if v, ok := p[k]; ok {
			row[k] = v
		}
	}
	if role := str(p, "role"); role != "" {
		row["subagentRole"] = role
	}
	if step := num(p, "step"); step > 0 {
		row["steps"] = step
	}
	switch name {
	case "sub:spawn":
		row["status"] = "running"
	case "sub:done":
		row["status"] = "success"
		if stop := str(p, "stopReason"); strings.Contains(strings.ToLower(stop), "limit") {
			row["status"] = "partial"
		}
	case "sub:error":
		row["status"] = "error"
	case "sub:tool:start", "sub:tool:result", "sub:tool:error":
		tools, _ := row["toolCalls"].([]any)
		var entry map[string]any
		for _, raw := range tools {
			if item, ok := raw.(map[string]any); ok && str(item, "toolCallId") == str(p, "toolCallId") {
				entry = item
				break
			}
		}
		if entry == nil {
			entry = map[string]any{"toolCallId": str(p, "toolCallId")}
			tools = append(tools, entry)
		}
		for _, field := range []string{"name", "args", "summary", "error", "errorCode", "durationMs"} {
			if v, ok := p[field]; ok {
				entry[field] = v
			}
		}
		entry["state"] = name
		row["toolCalls"] = tools
	}
	if (name == "sub:done" || name == "sub:error") && a.core != nil && a.win != nil {
		// The backend keeps the full report; pull it once so the expanded card
		// shows the same text the Vue version did.
		go func() {
			runs := a.core.GetSubagents()
			a.win.Update(func() {
				for _, run := range runs {
					if run == nil || run.SessionID != sid {
						continue
					}
					for _, msg := range t.Messages {
						if str(msg, "role") == "subagent" && str(msg, "subId") == run.ID {
							msg["steps"] = run.Steps
							msg["summary"] = run.Summary
							msg["error"] = run.Error
							msg["stopReason"] = run.StopReason
							msg["inputTokens"] = run.InputTokens
							msg["outputTokens"] = run.OutputTokens
							msg["contextTokens"] = run.ContextTokens
							msg["cacheHitTokens"] = run.CacheHitTokens
							msg["cacheMissTokens"] = run.CacheMissTokens
							msg["status"] = strings.ToLower(run.Status)
						}
					}
				}
			})
		}()
	}
}

func (a *desktop) refreshSessionFooter(t *conversation) {
	if t == nil || a.core == nil || a.win == nil {
		// No backend to ask: headless fixtures and the window's first frames.
		return
	}
	ui := t.uiState()
	if ui.FooterBusy {
		// A compaction lands while the previous poll is still in flight, and that
		// poll answers for the history it started on. Dropping the request here is
		// what leaves the footer frozen at the pre-compaction number until some
		// later run happens to refresh it, so keep it and rerun once it finishes.
		ui.FooterPending = true
		return
	}
	ui.FooterBusy = true
	sid, workspace := t.ID, t.Workspace
	go func() {
		breakdown := a.core.GetContextBreakdown(sid, workspace)
		usage := a.core.GetWorkspaceTokenUsage(workspace)
		a.win.Update(func() {
			ui.FooterBusy = false
			ui.FooterLoaded = true
			ui.Breakdown = breakdown
			ui.Usage = usage
			if breakdown.Total > 0 {
				t.ContextTokens = breakdown.Total
			}
			if ui.FooterPending {
				ui.FooterPending = false
				a.refreshSessionFooter(t)
			}
		})
	}()
}

func (a *desktop) refreshGit(t *conversation) {
	if t == nil || t.Workspace == "" || a.core == nil || a.win == nil {
		return
	}
	workspace := t.Workspace
	ui := t.uiState()
	go func() {
		status := a.core.GetGitStatus(workspace)
		a.win.Update(func() {
			if !sameWorkspace(t.Workspace, workspace) {
				return
			}
			ui.Git = status
			ui.GitLoaded = true
		})
	}()
}

func (a *desktop) applyUpdateEvent(name string, p map[string]any) {
	switch name {
	case "update:ready":
		a.upd.State = "ready"
		a.upd.Tag = str(p, "version")
		if a.upd.Tag == "" {
			a.upd.Tag = str(p, "tag")
		}
		a.upd.Progress = 100
		a.upd.Open = true
	case "update:error":
		a.upd.State = "error"
		a.upd.Error = str(p, "error")
		a.upd.Open = true
	case "update:applied":
		a.upd.State = "restarting"
	case "update:cancelled":
		a.upd.State = "idle"
		a.upd.Busy = false
	}
}

func (a *desktop) refreshSSH(manual ...bool) {
	go func() {
		servers := a.core.ListAllSSHServers()
		a.win.Update(func() {
			a.sshServers = servers
			if len(manual) > 0 && manual[0] {
				a.notifySuccess("SSH 列表已刷新")
			}
		})
	}()
}

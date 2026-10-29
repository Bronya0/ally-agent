// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"encoding/json"
	"github.com/egoist/mygo/ui"
	"strings"
)

// Drop shell payloads before serializing/queueing a UI update, not just after
// ingestion. Leave the runtime's original event untouched for other hosts.
func toolEventForDisplay(name string, payload any) any {
	if !strings.HasPrefix(name, "tool:") {
		return payload
	}
	message, ok := payload.(map[string]any)
	if !ok || toolKind(str(message, "name")) != "command" {
		return payload
	}
	copy := make(map[string]any, len(message))
	for key, value := range message {
		copy[key] = value
	}
	retainLocalCommandSummary(copy)
	return copy
}

type commandDetail struct {
	Loading bool
	Result  map[string]any
	Error   string
}

// A transcript owns only header metadata, never a second copy of shell output.
func retainLocalCommandSummary(m map[string]any) {
	name := str(m, "name")
	if name == "" {
		name = str(m, "toolName")
	}
	if toolKind(name) != "command" {
		return
	}
	result := toolResult(m)
	summary := map[string]any{}
	for _, key := range []string{"ok", "exitCode", "timedOut", "promotedToService", "cancelled", "durationMs", "elapsedMs", "errorCode"} {
		if value, ok := result[key]; ok {
			summary[key] = value
		}
	}
	m["result"] = summary
	delete(m, "body")
	if text := str(m, "error"); text != "" {
		m["error"] = short(text, 300)
	}
}

func commandContextResult(content string) map[string]any {
	var result map[string]any
	if json.Unmarshal([]byte(content), &result) == nil && result != nil {
		unwrapped := unwrapToolResult(result)
		if commandOutput(unwrapped) == "" && str(unwrapped, "error") != "" {
			unwrapped["_text"] = str(unwrapped, "error")
		}
		return unwrapped
	}
	return map[string]any{"_text": content}
}

func (a *desktop) loadCommandDetail(t *conversation, key string) {
	st := t.uiState()
	if st.CommandDetails == nil {
		st.CommandDetails = map[string]*commandDetail{}
	}
	if st.CommandDetails[key] != nil {
		return
	}
	detail := &commandDetail{Loading: true}
	st.CommandDetails[key] = detail
	if a.core == nil || a.win == nil {
		detail.Loading = false
		detail.Error = "当前上下文暂不可用"
		return
	}
	sessionID := t.ID
	go func() {
		content, err := a.core.GetCommandContextResult(sessionID, key)
		a.win.Update(func() {
			publishCommandDetail(t, sessionID, key, detail, content, err)
		})
	}()
}

func (a *desktop) commandDetailView(c *ui.Context, t *conversation, index int, key string, args, m map[string]any) {
	detail := t.uiState().CommandDetails[key]
	if detail == nil || detail.Loading {
		ui.Row(c).Gap(gapField).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c).Size(12, 12)
			ui.Text(c, "正在读取上下文结果…").FontSize(fsSmall).TextColor(a.secondaryInk())
		})
		return
	}
	if detail.Error != "" {
		ui.Text(c, detail.Error).FontSize(fsSmall).TextColor(a.secondaryInk())
		flat(c, "重试").OnClick(func() { delete(t.uiState().CommandDetails, key); a.loadCommandDetail(t, key) })
		return
	}
	a.toolBody(c, t, index, "command", args, detail.Result, m)
}

func publishCommandDetail(t *conversation, sessionID, key string, detail *commandDetail, content string, err error) bool {
	if t.ID != sessionID || !t.uiState().ToolOpen[key] || t.uiState().CommandDetails[key] != detail {
		return false
	}
	detail.Loading = false
	if err != nil {
		detail.Error = err.Error()
		return true
	}
	detail.Result = commandContextResult(content)
	return true
}

func releaseCommandDetails(t *conversation) {
	st := t.uiState()
	for key := range st.CommandDetails {
		delete(st.ToolOpen, key)
	}
	st.CommandDetails = nil
}

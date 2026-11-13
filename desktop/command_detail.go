// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/locales"
	"encoding/json"
	"github.com/egoist/mygo/ui"
	"strings"
)

// Project shell/read payloads before serializing/queueing a UI update, not just
// after ingestion. Leave the runtime's original event untouched for other hosts.
func toolEventForDisplay(name string, payload any) any {
	if !strings.HasPrefix(name, "tool:") {
		return payload
	}
	message, ok := payload.(map[string]any)
	if !ok || (!isMCPDisplayTool(message) && toolKind(str(message, "name")) != "command" && !isReadDisplayTool(message)) {
		return payload
	}
	copy := make(map[string]any, len(message))
	for key, value := range message {
		copy[key] = value
	}
	if !retainMCPDisplaySummary(copy) && !isMCPDisplayTool(copy) {
		retainLocalCommandSummary(copy)
		retainReadDisplaySummary(copy)
	}
	return copy
}

type commandDetail struct {
	Index   int
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
	retainCommandSummary(m)
}

// toolOutcomeSummary retains outcome metadata without keeping output bodies.
// Command cards and local-compaction excerpts use the same projection.
func toolOutcomeSummary(result map[string]any) map[string]any {
	summary := map[string]any{}
	for _, key := range []string{"ok", "exitCode", "timedOut", "promotedToService", "cancelled", "durationMs", "elapsedMs", "errorCode"} {
		switch value := result[key].(type) {
		case string, bool, int, int64, float32, float64:
			summary[key] = value
		}
	}
	if text := str(result, "error"); text != "" {
		summary["error"] = short(text, 300)
	}
	return summary
}

func retainCommandSummary(m map[string]any) {
	m["result"] = toolOutcomeSummary(toolResult(m))
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

func (a *desktop) loadCommandDetail(t *conversation, index int, key string) {
	st := t.uiState()
	if st.CommandDetails == nil {
		st.CommandDetails = map[string]*commandDetail{}
	}
	if st.CommandDetails[key] != nil {
		return
	}
	detail := &commandDetail{Index: index, Loading: true}
	st.CommandDetails[key] = detail
	if a.core == nil || a.win == nil {
		detail.Loading = false
		detail.Error = locales.Text("当前上下文暂不可用")
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
			ui.Text(c, locales.Text("正在读取上下文结果…")).FontSize(fsSmall).TextColor(a.secondaryInk())
		})
		return
	}
	if detail.Error != "" {
		ui.Text(c, detail.Error).FontSize(fsSmall).TextColor(a.secondaryInk())
		flat(c, locales.Text("重试")).OnClick(func() { delete(t.uiState().CommandDetails, key); a.loadCommandDetail(t, index, key) })
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

// A command can be opened again from model context. Once its history page is
// gone, release its duplicate output and close the child disclosure together,
// so returning to the page cannot leave a spinner waiting for a dropped cache.
func releaseCommandDetailsOutsideWindow(t *conversation, start, end int) {
	st := t.uiState()
	for key, detail := range st.CommandDetails {
		if detail == nil || detail.Index < start || detail.Index >= end {
			delete(st.CommandDetails, key)
			delete(st.ToolOpen, key)
		}
	}
}

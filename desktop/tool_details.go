// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/desktop/uikit"
	"fmt"
	"github.com/egoist/mygo/ui"
)

// attachToolImage runs on event/session ingestion, never during rendering.
func (a *desktop) attachToolImage(t *conversation, m map[string]any) {
	name := toolKind(str(m, "name"))
	if name != "image_generate" && name != "image_edit" && name != "screenshot" {
		return
	}
	data := toolResult(m)
	ref, url := str(data, "imageRef"), str(data, "dataUrl")
	if ref == "" && url == "" {
		return
	}
	id := str(m, "toolCallId") + "-image"
	if len(messageAttachmentList(m)) == 0 {
		m["attachments"] = []any{map[string]any{"id": id, "name": map[bool]string{true: "截图", false: "生成的图片"}[name == "screenshot"], "kind": "image", "generated": name != "screenshot", "imageRef": ref, "dataUrl": url, "type": str(data, "mime")}}
	}
	if existing := messageAttachmentList(m); len(existing) > 0 {
		id = str(existing[0], "id")
	}
	if a.core != nil && a.win != nil {
		a.loadAttachmentImage(t, id, ref, url)
	}
}

func toolDeleteTree(c *ui.Context, args, result map[string]any, ink ...ui.Color) {
	rows := list(result, "paths")
	if len(rows) == 0 {
		rows = list(result, "results")
	}
	if len(rows) == 0 {
		rows = list(args, "paths")
	}
	if len(rows) == 0 {
		path := str(result, "deleted")
		if path == "" {
			path = str(result, "path")
		}
		if path == "" {
			path = str(args, "path")
		}
		if path != "" {
			rows = []any{path}
		}
	}
	for n, raw := range rows {
		path := fmt.Sprint(raw)
		suffix := " · ok"
		color := uikit.SecondaryInk(c, ink)
		if row, ok := raw.(map[string]any); ok {
			path = str(row, "path")
			if path == "" {
				path = str(row, "deleted")
			}
			if boolOf(row, "absent") {
				suffix = " · ok (absent)"
			}
			if v, ok := row["ok"]; ok && v == false {
				suffix = " · failed: " + str(row, "errorCode") + " " + str(row, "error")
				color = c.Theme().Danger
			}
		}
		prefix := "├─ "
		if n == len(rows)-1 {
			prefix = "└─ "
		}
		ui.Text(c, prefix+path+suffix).Font(monoFamily).FontSize(fsTool - 2).TextColor(color.Mix(c.Theme().Text, 0.3)).Selectable()
	}
	if count := int(num(result, "failedCount")); count > 0 {
		notice(c, "error", fmt.Sprintf("%d 个路径删除失败", count), ink...)
	}
}

func toolAskSummary(c *ui.Context, key string, args, result map[string]any) {
	questions := list(result, "questions")
	if len(questions) == 0 {
		questions = list(args, "questions")
	}
	for n, raw := range questions {
		if q, ok := raw.(map[string]any); ok {
			ui.Column(c.Key(fmt.Sprintf("%s-question-%d", key, n))).Padding(10).Gap(5).Border(1, c.Theme().Border).Radius(radiusCard).Children(func() {
				ui.Text(c, str(q, "question")).FontSize(fsSmall).Bold()
				for _, raw := range list(result, "answers") {
					if answer, ok := raw.(map[string]any); ok && str(answer, "questionId") == str(q, "id") {
						for _, raw := range list(answer, "selections") {
							if selection, ok := raw.(map[string]any); ok {
								ui.Row(c).Gap(6).Children(func() {
									ui.Text(c, "✓ "+str(selection, "label")).FontSize(fsSmall).TextColor(c.Theme().Success)
									if boolOf(selection, "recommended") {
										accentChip(c, "推荐")
									}
								})
							}
						}
						selected := list(answer, "selectedOptionIds")
						for _, raw := range list(q, "options") {
							if option, ok := raw.(map[string]any); ok {
								for _, id := range selected {
									if fmt.Sprint(id) == str(option, "id") {
										ui.Text(c, "✓ "+str(option, "label")).FontSize(fsSmall).TextColor(c.Theme().Success)
									}
								}
							}
						}
						if text := str(answer, "customText"); text != "" {
							ui.Text(c, text).FontSize(fsSmall).Selectable()
						}
					}
				}
			})
		}
	}
}

func toolDuration(m, result map[string]any) int64 {
	ms := int64(num(m, "durationMs"))
	if ms == 0 {
		ms = int64(num(result, "durationMs"))
		if ms == 0 {
			ms = int64(num(result, "elapsedMs"))
		}
	}
	return ms
}
func resourceStatus(status string) string {
	if text, ok := map[string]string{"running": "运行中", "stopped": "已停止", "success": "已完成", "error": "失败", "pending_approval": "等待授权", "idle": "空闲"}[status]; ok {
		return text
	}
	return status
}

func toolActionVerb(name, state string, args map[string]any, fallback string) string {
	verbs := map[string][2]string{"command": {"Running", "Ran"}, "remote_run_command": {"Remote Running", "Remote Ran"}, "edit": {"Editing", "Edited"}, "remote_edit": {"Remote Editing", "Remote Edited"}, "create": {"Creating", "Created"}, "remote_create_file": {"Remote Creating", "Remote Created"}, "delete": {"Deleting", "Deleted"}, "remote_delete_path": {"Remote Deleting", "Remote Deleted"}, "http_request": {"Requesting", "Requested"}, "web_fetch": {"Fetching", "Fetched"}, "render_html": {"Rendering", "Rendered"}, "screenshot": {"Screenshot", "Screenshot"}, "skill": {"Loading skill", "Loaded skill"}, "plan": {"Plan", "Plan"}, "ask": {"Asking", "Asked"}, "wait": {"Waiting", "Waited"}, "calculate": {"Calculating", "Calculated"}, "subagent": {"Subagent", "Subagent"}, "scheduled_task": {"Scheduling", "Scheduled"}, "service": {"Service", "Service"}, "ssh_cluster": {"SSH cluster", "SSH cluster"}, "remote_transfer": {"Transferring", "Transferred"}, "image_generate": {"Generating image", "Generated image"}, "image_edit": {"Editing image", "Edited image"}}
	pair, ok := verbs[name]
	if !ok {
		// Fall back to the tool's own verb. Answering "Called MCP" here labelled
		// every non-MCP tool that has no entry in this map (read, grep, …) as an MCP
		// call; real MCP tools get that wording from toolCard's own MCP branch.
		return fallback
	}
	if state == "error" {
		return pair[1] + " failed"
	}
	if state == "running" || state == "pending" {
		return pair[0]
	}
	return pair[1]
}

// suggestionLabelMix lifts the suggestion label from muted ink toward body text.
// Pure muted measured 4.4:1 on the light surface — below AA, so 12px labels went
// blurry — while body ink would shout next to an answer.
const suggestionLabelMix float32 = 0.35

// suggestionButton is a bare suggestion under an answer: no edge, no fill, only
// the label, which takes the accent while the pointer or the focus is on it. A
// chip outline competed with the tool card edges around it and read as a control
// rather than a shortcut. It stays built from ButtonBase rather than flat():
// flat() paints its own edge and a full-strength label, so restyling it from the
// outside lost both.
func suggestionButton(c *ui.Context, text string, ink ...ui.Color) ui.Element {
	button := ui.ButtonBase(c).Height(26).Padding(3, 8).Radius(radiusSmall).
		Center().Cursor(ui.CursorPointer).Label(text)
	color := uikit.SecondaryInk(c, ink).Mix(c.Theme().Text, suggestionLabelMix)
	if button.Hovered() || button.Focused() {
		color = c.Theme().Accent
	}
	button.Background(ui.Transparent).Border(0, ui.Transparent)
	button.Children(func() {
		ui.Text(c, text).FontSize(fsTiny).TextColor(fadeColor(button, "suggestion-label", color)).SingleLine()
	})
	return button
}

func subagentParent(t *conversation, child map[string]any) string {
	id := str(child, "parentToolCallId")
	for _, m := range t.Messages {
		if toolKind(str(m, "name")) != "subagent" {
			continue
		}
		if id != "" && str(m, "toolCallId") == id {
			return id
		}
		if str(toolResult(m), "agentId") == str(child, "subId") {
			return str(m, "toolCallId")
		}
	}
	return ""
}

func (a *desktop) scheduledTaskSummary(args, result map[string]any) string {
	action := str(args, "action")
	name := str(args, "name")
	if name == "" {
		name = str(obj(result, "task"), "name")
	}
	if name == "" {
		name = str(result, "deletedName")
	}
	id := str(args, "id")
	if id == "" {
		id = str(result, "deleted")
	}
	if name == "" {
		for _, task := range a.tasks {
			if task.ID == id {
				name = task.Name
				break
			}
		}
	}
	if name == "" {
		name = id
	}
	if name == "" {
		return action
	}
	return action + " · " + name
}

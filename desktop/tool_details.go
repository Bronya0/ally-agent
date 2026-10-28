// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
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

func toolDeleteTree(c *ui.Context, args, result map[string]any) {
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
		color := c.Theme().TextMuted
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
				color = colDanger()
			}
		}
		prefix := "├─ "
		if n == len(rows)-1 {
			prefix = "└─ "
		}
		ui.Text(c, prefix+path+suffix).Font(monoFamily).FontSize(fsTool - 2).TextColor(color.Mix(c.Theme().Text, 0.3)).Selectable()
	}
	if count := int(num(result, "failedCount")); count > 0 {
		notice(c, "error", fmt.Sprintf("%d 个路径删除失败", count))
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
									ui.Text(c, "✓ "+str(selection, "label")).FontSize(fsSmall).TextColor(colSuccess())
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
										ui.Text(c, "✓ "+str(option, "label")).FontSize(fsSmall).TextColor(colSuccess())
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
		if state == "running" || state == "pending" {
			return "Calling MCP"
		}
		return "Called MCP"
	}
	if state == "error" {
		return pair[1] + " failed"
	}
	if state == "running" || state == "pending" {
		return pair[0]
	}
	return pair[1]
}

func suggestionButton(c *ui.Context, text string) ui.Element {
	color := c.Theme().Text.Mix(c.Theme().TextMuted, 0.3)
	button := flat(c, text).Height(26).Padding(3, 8).Radius(radiusSmall).FontSize(fsTiny)
	border := c.Theme().Border
	fill := c.Theme().Surface
	labelColor := color
	if button.Hovered() || button.Focused() {
		border = c.Theme().Accent
		fill = c.Theme().SurfaceHover
		labelColor = c.Theme().Accent
	}
	button.Border(1, fadeColor(button, "border", border)).
		Background(fadeColor(button, "fill", fill)).
		TextColor(fadeColor(button, "text", labelColor))
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

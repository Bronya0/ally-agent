// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/desktop/uikit"
	backend "ally-dev/internal/app"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"
)

const subagentBlackboardSemantics = "收：已进入模型上下文的唯一消息；发：发布成功，不表示对方已接收。"

var subagentBlackboardRetention = fmt.Sprintf("序号仅在本轮有效；仅保留最近 %d 条，计数为累计值；正文为预览。", backend.MaxSubagentBlackboardEvents)

func subagentBlackboardEntry(raw map[string]any) map[string]any {
	direction := str(raw, "direction")
	seq := int64(num(raw, "seq"))
	if (direction != "sent" && direction != "received") || seq <= 0 {
		return nil
	}
	entry := map[string]any{}
	for _, key := range [...]string{"direction", "seq", "from", "to", "type", "replyTo", "body", "at", "truncated"} {
		if value, ok := raw[key]; ok {
			entry[key] = value
		}
	}
	label := "收到"
	if direction == "sent" {
		label = "发出"
	}
	kind := str(raw, "type")
	switch kind {
	case "finding":
		kind = "发现"
	case "question":
		kind = "提问"
	case "answer":
		kind = "答复"
	case "decision":
		kind = "决策"
	case "blocker":
		kind = "阻碍"
	case "handoff":
		kind = "交接"
	}
	heading := label + " · #" + strconv.FormatInt(seq, 10)
	if kind != "" {
		heading += " · " + kind
	}
	if reply := int64(num(raw, "replyTo")); reply > 0 {
		heading += " · 回复 #" + strconv.FormatInt(reply, 10)
	}
	to := str(raw, "to")
	if to == "*" {
		to = "全体"
	}
	entry["_blackboardKey"] = direction + "-" + strconv.FormatInt(seq, 10)
	entry["_blackboardHeading"] = heading
	entry["_blackboardRoute"] = str(raw, "from") + " → " + to
	if at := int64(num(raw, "at")); at > 0 {
		entry["_blackboardTime"] = time.UnixMilli(at).Format("15:04:05")
	}
	return entry
}

// Normalize only at event/snapshot/history ingress, never while drawing a card.
func normalizeSubagentBlackboard(row map[string]any) {
	if str(row, "role") != "subagent" {
		return
	}
	entries := make([]any, 0, backend.MaxSubagentBlackboardEvents)
	seen := make(map[string]bool)
	for _, raw := range list(row, "blackboardEvents") {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		entry := subagentBlackboardEntry(m)
		if entry == nil || seen[str(entry, "_blackboardKey")] {
			continue
		}
		seen[str(entry, "_blackboardKey")] = true
		entries = append(entries, entry)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i].(map[string]any), entries[j].(map[string]any)
		if num(a, "at") != num(b, "at") {
			return num(a, "at") < num(b, "at")
		}
		return num(a, "seq") < num(b, "seq")
	})
	if len(entries) > backend.MaxSubagentBlackboardEvents {
		entries = append([]any(nil), entries[len(entries)-backend.MaxSubagentBlackboardEvents:]...)
	}
	if len(entries) > 0 {
		row["blackboardEvents"] = entries
	} else {
		delete(row, "blackboardEvents")
	}
	updateSubagentBlackboardLabel(row)
}

func updateSubagentBlackboardLabel(row map[string]any) {
	sent, received := max(0, int(num(row, "blackboardSent"))), max(0, int(num(row, "blackboardReceived")))
	row["blackboardSent"], row["blackboardReceived"] = sent, received
	if entries, _ := row["blackboardEvents"].([]any); sent+received > 0 || len(entries) > 0 {
		row["_blackboardLabel"] = fmt.Sprintf("黑板 · 收 %d / 发 %d", received, sent)
	} else {
		delete(row, "_blackboardLabel")
	}
}

func applySubagentBlackboard(row, payload map[string]any) {
	for _, key := range [...]string{"blackboardSent", "blackboardReceived"} {
		row[key] = max(int(num(row, key)), int(num(payload, key)))
	}
	if name := str(payload, "name"); name != "" {
		row["subagentName"] = name
	}
	if raw, ok := payload["blackboardEvent"].(map[string]any); ok {
		entries, _ := row["blackboardEvents"].([]any)
		row["blackboardEvents"] = append(entries, raw)
		normalizeSubagentBlackboard(row)
	} else {
		updateSubagentBlackboardLabel(row)
	}
}

func mergeSubagentBlackboardRun(row map[string]any, run *backend.SubagentRun) {
	if run == nil || str(row, "role") != "subagent" || str(row, "subId") != run.ID {
		return
	}
	row["blackboardSent"] = max(int(num(row, "blackboardSent")), run.BlackboardSent)
	row["blackboardReceived"] = max(int(num(row, "blackboardReceived")), run.BlackboardReceived)
	entries, _ := row["blackboardEvents"].([]any)
	for _, event := range run.BlackboardEvents {
		entries = append(entries, map[string]any{
			"direction": event.Direction, "seq": event.Seq, "from": event.From, "to": event.To,
			"type": event.Type, "replyTo": event.ReplyTo, "body": event.Body, "at": event.At, "truncated": event.Truncated,
		})
	}
	row["blackboardEvents"] = entries
	normalizeSubagentBlackboard(row)
}

func (a *desktop) hasSubagentConversation(t *conversation, sid string) bool {
	if a.closing || t == nil || t.ID != sid {
		return false
	}
	for _, tab := range a.tabs {
		if tab == t {
			return true
		}
	}
	return false
}

func toggleSubagentDetail(t *conversation, id string, blackboard bool) {
	key, other := "sub-"+id, "sub-blackboard-"+id
	if blackboard {
		key, other = other, key
	}
	state := t.uiState().ToolOpen
	state[key], state[other] = !state[key], false
}

func (a *desktop) subagentBlackboardButton(c *ui.Context, t *conversation, row map[string]any) {
	label := str(row, "_blackboardLabel")
	if label == "" {
		return
	}
	id := str(row, "subId")
	open := t.uiState().ToolOpen["sub-blackboard-"+id]
	button, _ := buttonParts(c, "sub-blackboard-button-"+id, "", btnSecondary, true, "")
	button.Label("子代理黑板 " + id).OnClick(func() { toggleSubagentDetail(t, id, true) }).Children(func() {
		ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
			ui.Text(c, label).FontSize(uikit.ButtonFontSmall).TextColor(a.secondaryInk())
			caret := "chevron"
			if open {
				caret = "chevrondown"
			}
			iconGlyph(c, caret, 10).Shrink(0)
		})
	})
	uikit.Tooltip(c, button, subagentBlackboardSemantics)
}

func (a *desktop) subagentBlackboardDetails(c *ui.Context, t *conversation, row map[string]any) {
	if str(row, "_blackboardLabel") == "" {
		return
	}
	id := str(row, "subId")
	open := t.uiState().ToolOpen["sub-blackboard-"+id]
	uikit.Reveal(c.Key("sub-blackboard-reveal-"+id), open, func() {
		if !open {
			return
		}
		ui.Column(c).FillWidth().MinWidth(0).Gap(gapInline).Children(func() {
			ui.Text(c, subagentBlackboardSemantics).FontSize(fsTiny).TextColor(a.secondaryInk())
			ui.Text(c, subagentBlackboardRetention).FontSize(fsTiny).TextColor(a.secondaryInk())
			ui.Scroll(c.Key("sub-blackboard-body-" + id)).FillWidth().MinWidth(0).MaxHeight(280).Shrink(0).Clip().Label("子代理黑板记录 " + id).Children(func() {
				ui.Column(c).FillWidth().MinWidth(0).Gap(gapInline).Children(func() {
					entries, _ := row["blackboardEvents"].([]any)
					for _, raw := range entries {
						entry, ok := raw.(map[string]any)
						if !ok {
							continue
						}
						ui.Column(c.Key(str(entry, "_blackboardKey"))).FillWidth().MinWidth(0).Padding(8, 10).Gap(4).Background(c.Theme().Background).Radius(radiusSmall).Children(func() {
							ui.Row(c).FillWidth().Gap(gapInline).Wrap().Children(func() {
								ui.Text(c, str(entry, "_blackboardHeading")).FontSize(fsSmall).TextColor(a.secondaryInk())
								ui.Spacer(c)
								ui.Text(c, str(entry, "_blackboardTime")).Font(monoFamily).FontSize(fsTiny).TextColor(a.secondaryInk())
							})
							ui.Text(c, str(entry, "_blackboardRoute")).FontSize(fsTiny).TextColor(a.secondaryInk())
							ui.Text(c, str(entry, "body")).FontSize(fsSmall).LineHeight(1.5).Selectable()
							if boolOf(entry, "truncated") {
								ui.Text(c, "正文已截断，仅显示预览").FontSize(fsTiny).TextColor(a.secondaryInk())
							}
						})
					}
				})
			})
		})
	})
}

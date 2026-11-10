// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/desktop/uikit"
	"ally-dev/locales"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// toolDescriptor is the single table that classifies tool cards: the verb
// shown for each state, whether the body expands, and where the preview
// comes from. Cards never re-derive these per site.
type toolDescriptor struct {
	runningVerb string
	doneVerb    string
	failedVerb  string
	expandable  bool
	showTiming  bool
}

// failedVerb exists because a card in error state must never claim
// completion: "Edited" on a failed edit misleads the reader. Every state has
// its own word here, so no failed verb is ever built by suffixing a done verb
// (that produced "Remote Ran failed"). Cards read English; the folded
// read/grep/list row writes its own Chinese counts and never reads this table.
var toolTable = map[string]toolDescriptor{
	"read":               {"Reading", "Read", "Read failed", false, false},
	"remote_read":        {"Remote Reading", "Remote Read", "Remote read failed", false, false},
	"edit":               {"Editing", "Edited", "Edit failed", false, false},
	"remote_edit":        {"Remote Editing", "Remote Edited", "Remote edit failed", false, false},
	"create":             {"Creating", "Created", "Create failed", false, false},
	"remote_create_file": {"Remote Creating", "Remote Created", "Remote create failed", false, false},
	"delete":             {"Deleting", "Deleted", "Delete failed", true, false},
	"remote_delete_path": {"Remote Deleting", "Remote Deleted", "Remote delete failed", true, false},
	"command":            {"Running", "Ran", "Command failed", true, true},
	"remote_run_command": {"Remote Running", "Remote Ran", "Remote command failed", true, true},
	"grep":               {"Grepping", "Grep", "Grep failed", false, false},
	"list":               {"Listing", "Listed", "List failed", true, false},
	"glob":               {"Matching", "Matched", "Match failed", true, false},
	"http_request":       {"Requesting", "Requested", "Request failed", true, true},
	"web_fetch":          {"Fetching", "Fetched", "Fetch failed", true, true},
	"calculate":          {"Calculating", "Calculated", "Calculation failed", false, false},
	"plan":               {"Updating plan", "Plan", "Plan failed", false, false},
	"skill":              {"Loading skill", "Loaded skill", "Skill failed", false, false},
	"ask":                {"Asking", "Asked", "Ask failed", true, false},
	"subagent":           {"Delegating", "Subagent", "Delegation failed", true, true},
	"ssh_cluster":        {"Checking SSH", "SSH cluster", "SSH check failed", true, false},
	"service":            {"Managing service", "Service", "Service failed", true, false},
	"scheduled_task":     {"Scheduling", "Scheduled", "Scheduling failed", true, true},
	"remote_transfer":    {"Remote Transferring", "Remote Transferred", "Remote transfer failed", true, true},
	"screenshot":         {"Taking screenshot", "Screenshot", "Screenshot failed", true, false},
	"render_html":        {"Rendering", "Rendered", "Render failed", true, true},
	"suggest":            {"Suggesting", "Suggested", "Suggestion failed", true, false},
	"wait":               {"Waiting", "Waited", "Wait failed", false, true},
	"image_generate":     {"Generating image", "Generated image", "Image generation failed", true, true},
	"image_edit":         {"Editing image", "Edited image", "Image edit failed", true, true},
}

func toolKind(name string) string {
	switch name {
	case "list_files":
		return "list"
	case "Glob":
		return "glob"
	case "Bash":
		return "command"
	case "Skill":
		return "skill"
	case "replace_exact", "replace_lines":
		return "edit"
	case "agent_delegate":
		return "subagent"
	case "start_service", "stop_service", "list_services":
		return "service"
	}
	return name
}

func describeTool(name string) toolDescriptor {
	name = toolKind(name)
	if d, ok := toolTable[name]; ok {
		return d
	}
	if strings.HasPrefix(name, "replace_") {
		return toolTable["edit"]
	}
	if strings.HasPrefix(name, "mcp__") {
		return toolDescriptor{"Calling MCP", "Called MCP", "MCP call failed", true, true}
	}
	return toolDescriptor{"Calling", "Called", "Call failed", true, false}
}

func toolState(m map[string]any) string {
	if userCancelledMessage(m) || userCancelledMessage(toolResult(m)) {
		return "cancelled"
	}
	status := strings.ToLower(str(m, "status"))
	state := str(m, "toolState")
	switch {
	case (status == "error" || status == "failed") || state == "tool:error":
		return "error"
	case (status == "success" || status == "completed" || status == "done") || state == "tool:result":
		result := toolResult(m)
		if boolOf(result, "cancelled") {
			return "cancelled"
		}
		if value, exists := result["ok"]; exists && value == false {
			return "error"
		}
		return "success"
	case state == "tool:start" || state == "tool:update" || status == "running":
		return "running"
	}
	return "pending"
}

// isFoldableReadTool reports whether a message joins main's read-grep-list
// folding: consecutive read/grep/list tool calls collapse into one group row.
// The backend sends `list` for directory listing; main also folds
// `batch_read`, which arrives here as plain `read`. A locally trimmed card keeps
// its membership: dropping it out of the run spreads one collapsed row into as
// many rows as the run held, right after every compaction.
func isFoldableReadTool(m map[string]any) bool {
	name := str(m, "name")
	if name == "" {
		name = str(m, "toolName")
	}
	switch toolKind(name) {
	case "read", "remote_read", "batch_read", "grep", "list", "glob":
		return true
	}
	return false
}

// toolGroupEntry is one tree item in a consecutive read/search/list/command
// run. Reads that return several paths retain one leaf per path; commands keep
// their message so the result can be loaded only when opened.
type toolGroupEntry struct {
	index   int
	message map[string]any
	name    string
	verb    string
	title   string
	summary string
	detail  string
	state   string
}

func isFoldableToolGroupItem(m map[string]any) bool {
	return isToolRecord(m) && (isFoldableReadTool(m) || isFoldableCommand(m))
}

func readGrepGroupName(m map[string]any) string {
	name := str(m, "name")
	if name == "" {
		name = str(m, "toolName")
	}
	return toolKind(name)
}

func toolGroupReadSummary(result map[string]any) string {
	if str(result, "error") != "" {
		return ""
	}
	start, end := int(num(result, "startLine")), int(num(result, "endLine"))
	readLines := -1
	if boolOf(result, "emptyRange") {
		readLines = 0
	} else if start > 0 {
		if end >= start {
			readLines = end - start + 1
		} else if end == 0 {
			readLines = 0
		}
	} else if _, exists := result["totalLines"]; exists {
		readLines = int(num(result, "totalLines"))
	}
	if readLines < 0 {
		return ""
	}
	unit := "lines"
	if readLines == 1 {
		unit = "line"
	}
	return fmt.Sprintf("%d %s", readLines, unit)
}

func toolGroupGrepHits(result map[string]any) int {
	hits := -1
	if _, exists := result["hits"]; exists {
		hits = int(num(result, "hits"))
	} else if _, exists := result["count"]; exists {
		hits = int(num(result, "count"))
	} else if _, exists := result["matches"]; exists {
		hits = 0
		for _, raw := range list(result, "matches") {
			if match, ok := raw.(map[string]any); ok {
				hits += len(list(match, "lines"))
			} else {
				hits++
			}
		}
	}
	return hits
}

func toolGroupGrepSummary(result map[string]any) string {
	if hits := toolGroupGrepHits(result); hits >= 0 {
		return fmt.Sprintf("%d hits", hits)
	}
	return ""
}

func toolGroupListSummary(result map[string]any) string {
	count, exists := result["count"]
	if !exists || count == nil {
		count = len(list(result, "entries"))
		if count == 0 {
			count = len(list(result, "matches"))
		}
	}
	parts := []string{fmt.Sprintf(locales.Text("%v 项"), count)}
	if boolOf(result, "truncated") {
		parts = append(parts, locales.Text("已截断"))
	}
	return strings.Join(parts, "，")
}

func toolGroupResultSummary(name string, result map[string]any) string {
	switch name {
	case "read", "remote_read", "batch_read":
		return toolGroupReadSummary(result)
	case "grep":
		return toolGroupGrepSummary(result)
	case "list", "glob":
		return toolGroupListSummary(result)
	}
	return ""
}

type toolGroupSummaryKey struct {
	historyKey
	toolRev          uint64
	index, windowEnd int
}

type toolGroupSummary struct {
	end, reads, greps, lists, commands, trimmed int
	status, lastStatus                          string
	running, queued                             int
	changeRev                                   uint64
}

// A bounded journal lets visible group headers apply changed scalar counts.
// It never retains arguments, outputs or message maps. A frame that misses
// the entire journal simply rebuilds its summary from the transcript.
const toolGroupChangeLimit = 64

type toolGroupItem struct {
	kind, state     string
	folded, trimmed bool
}

type toolGroupChange struct {
	index         int
	before, after toolGroupItem
}

type toolGroupChanges struct {
	rev     uint64
	entries [toolGroupChangeLimit]toolGroupChange
}

func groupItem(m map[string]any) toolGroupItem {
	if !isFoldableToolGroupItem(m) {
		return toolGroupItem{}
	}
	return toolGroupItem{readGrepGroupName(m), toolState(m), true, boolOf(m, "fastCompacted")}
}

func recordToolGroupChange(t *conversation, index int, before toolGroupItem, m map[string]any) {
	after := groupItem(m)
	if before == after {
		return
	}
	if t.groupChanges == nil {
		t.groupChanges = &toolGroupChanges{}
	}
	log := t.groupChanges
	log.rev++
	log.entries[log.rev%toolGroupChangeLimit] = toolGroupChange{index, before, after}
}

func (s *toolGroupSummary) count(item toolGroupItem, delta int) {
	if !item.folded {
		return
	}
	if item.trimmed {
		s.trimmed += delta
	}
	switch item.state {
	case "running":
		s.running += delta
	case "pending":
		s.queued += delta
	}
	switch item.kind {
	case "grep":
		s.greps += delta
	case "list", "glob":
		s.lists += delta
	case "command":
		s.commands += delta
	default:
		s.reads += delta
	}
}

func (s *toolGroupSummary) finish(t *conversation) {
	s.status = s.lastStatus
	if s.running > 0 {
		s.status = "running"
	} else if s.queued > 0 {
		s.status = "pending"
	}
	if t.groupChanges != nil {
		s.changeRev = t.groupChanges.rev
	}
}

func appendToolGroupSummary(s *toolGroupSummary, t *conversation, windowEnd int) {
	for s.end < windowEnd && isFoldableToolGroupItem(t.Messages[s.end]) {
		item := groupItem(t.Messages[s.end])
		s.count(item, 1)
		s.lastStatus = item.state
		s.end++
	}
	s.finish(t)
}

func summarizeToolGroup(t *conversation, index, windowEnd int) toolGroupSummary {
	s := toolGroupSummary{end: index, lastStatus: "pending"}
	appendToolGroupSummary(&s, t, windowEnd)
	return s
}

func cachedToolGroupSummary(memo *memoOf[toolGroupSummaryKey, toolGroupSummary], t *conversation, key toolGroupSummaryKey) toolGroupSummary {
	return memo.get(key, func(toolGroupSummaryKey) toolGroupSummary {
		s := memo.value
		previous := memo.key
		if !memo.ready || previous.rev != key.rev || previous.index != key.index || previous.windowEnd > key.windowEnd || previous.rows > key.rows {
			return summarizeToolGroup(t, key.index, key.windowEnd)
		}
		if log := t.groupChanges; log != nil {
			if s.changeRev > log.rev || log.rev-s.changeRev > toolGroupChangeLimit {
				return summarizeToolGroup(t, key.index, key.windowEnd)
			}
			for rev := s.changeRev + 1; rev <= log.rev; rev++ {
				change := log.entries[rev%toolGroupChangeLimit]
				if change.index < key.index || change.index >= s.end {
					continue
				}
				s.count(change.before, -1)
				s.count(change.after, 1)
				if change.index == s.end-1 {
					s.lastStatus = change.after.state
				}
			}
		}
		if s.end == previous.windowEnd {
			appendToolGroupSummary(&s, t, key.windowEnd)
		} else {
			s.finish(t)
		}
		return s
	})
}

// toolActivityGroupCard draws one card for a consecutive run of read, grep,
// list and command calls. The head message decides the wording; entries load
// their output only when opened. a11yLabel prefixes the hidden control label
// so the header stays findable by tests and assistive tooling.
func (a *desktop) toolActivityGroupCard(c *ui.Context, t *conversation, index int) {
	if index < 0 || index >= len(t.Messages) || !isFoldableToolGroupItem(t.Messages[index]) {
		return
	}
	headIsCommand := isFoldableCommand(t.Messages[index])
	a11yLabel := locales.Text("读取分组 ")
	groupType := "readgroup"
	if headIsCommand {
		groupType, a11yLabel = "commandgroup", locales.Text("命令分组 ")
	}
	key := fmt.Sprintf("%s-%s-%d", t.ID, groupType, index)
	open := t.uiState().ToolOpen[key]
	container := ui.Column(c.Key("tool-" + key)).FillWidth().MinWidth(0).Gap(2)
	extra := memoAt[int](container, "entries-extra")
	// An open group rebuilds its rows on every frame. Keep one parsed-argument
	// memo per visible row beside the widget, so each row decodes its JSON only
	// when that row's raw arguments actually change.
	entryArgs := memoAt[[]argsMemo](container, "entry-args")
	if !open {
		*extra = 0
		// Releasing the memos together with the rows keeps a long transcript from
		// holding one decoded map per row of every group the user ever opened.
		*entryArgs = nil
	}

	_, windowEnd := a.chatMessageWindow(t)
	// This widget keeps scalar header metadata, including while collapsed.
	// Events retire/update tool states through toolRev; topology and compaction
	// changes use historyRev. No row arguments or output are retained here.
	summaryMemo := memoAt[memoOf[toolGroupSummaryKey, toolGroupSummary]](container, "summary")
	summary := cachedToolGroupSummary(summaryMemo, t, toolGroupSummaryKey{historyKey{len(t.Messages), t.historyRev}, t.toolRev, index, windowEnd})
	end := summary.end
	reads, greps, lists, commands, trimmed := summary.reads, summary.greps, summary.lists, summary.commands, summary.trimmed
	if end == index {
		return
	}
	hasReadTools := reads+greps+lists > 0
	if commands > 0 && hasReadTools {
		a11yLabel = locales.Text("工具分组 ")
	}
	// A group's mark follows its calls: it breathes while any call runs, waits
	// grey while one is still queued, and otherwise keeps the last result. None
	// of that is repeated in words beside the counts.
	groupStatus := summary.status
	rowLabel := a11yLabel + key

	visibleEnd := end
	if open {
		visibleEnd = min(end, index+100+*extra)
	}
	// A collapsed group draws no rows, so only allocate them when it is open.
	var entries []toolGroupEntry
	if open {
		if count := visibleEnd - index; len(*entryArgs) > count {
			// "Show less" drops hidden parsed arguments and their backing slots,
			// not just the output widgets. The surviving rows keep their memos.
			kept := make([]argsMemo, count)
			copy(kept, *entryArgs)
			*entryArgs = kept
		}
		entries = make([]toolGroupEntry, 0, visibleEnd-index)
		for i := index; i < visibleEnd; i++ {
			m := t.Messages[i]
			name := readGrepGroupName(m)
			state := toolState(m)
			slot := i - index
			for len(*entryArgs) <= slot {
				*entryArgs = append(*entryArgs, argsMemo{})
			}
			args := (*entryArgs)[slot].get(str(m, "args"), parseToolArgs)
			title := toolArgSummary(name, args)
			if title == "" {
				// A trimmed card's arguments are gone; its stored title is all that is
				// left to name it.
				title = str(m, "title")
			}
			if name == "command" {
				entries = append(entries, toolGroupEntry{index: i, message: m, name: name, title: title, state: state})
				continue
			}
			entry := toolGroupEntry{index: i, message: m, name: name, title: title, state: state}
			switch name {
			case "grep":
				entry.verb = "Grep"
			case "list":
				entry.verb = "List"
			case "glob":
				entry.verb = "Glob"
			default:
				entry.verb = "Read"
			}
			result := toolResult(m)
			entry.summary = toolGroupResultSummary(name, result)
			if state == "error" {
				entry.detail = str(result, "error")
				if entry.detail == "" {
					entry.detail = str(m, "error")
				}
			}
			if files := list(result, "files"); len(files) > 0 && (name == "read" || name == "remote_read" || name == "batch_read") {
				for _, raw := range files {
					if file, ok := raw.(map[string]any); ok {
						fileState, fileDetail := state, entry.detail
						if message := str(file, "error"); message != "" {
							fileState, fileDetail = "error", message
						}
						entries = append(entries, toolGroupEntry{index: i, message: m, name: name, verb: "Read", title: str(file, "path"), summary: toolGroupReadSummary(file), state: fileState, detail: fileDetail})
					}
				}
				continue
			}
			entries = append(entries, entry)
		}
	}

	container.Children(func() {
		row := ui.ButtonBase(c.Key(key+"-header")).FillWidth().Height(24).Padding(0, 8, 0, 0).Gap(gapTight).AlignItems(ui.Center).Justify(ui.Start).Cursor(ui.CursorPointer).Label(rowLabel)
		row.Children(func() {
			ui.Box(c).Absolute().Left(-20).Top(0).Width(16).HeightPercent(100).Center().Children(func() {
				toolStatusIcon(c, groupStatus)
			})
			separator := false
			if reads > 0 {
				disclosureIcon(c, "book", a.currentPalette().accent)
				ui.Text(c, fmt.Sprintf(locales.Text("读取 %d 次"), reads)).FontSize(fsTool).TextColor(a.secondaryInk()).LineHeight(1.4).SingleLine()
				separator = true
			}
			if greps > 0 {
				if separator {
					ui.Text(c, "，").FontSize(fsTool).TextColor(a.secondaryInk()).LineHeight(1.4).SingleLine()
				}
				disclosureIcon(c, "search", a.currentPalette().accent)
				ui.Text(c, fmt.Sprintf(locales.Text("搜索 %d 次"), greps)).FontSize(fsTool).TextColor(a.secondaryInk()).LineHeight(1.4).SingleLine()
				separator = true
			}
			if lists > 0 {
				if separator {
					ui.Text(c, "，").FontSize(fsTool).TextColor(a.secondaryInk()).LineHeight(1.4).SingleLine()
				}
				disclosureIcon(c, "list", a.currentPalette().accent)
				ui.Text(c, fmt.Sprintf(locales.Text("罗列 %d 次"), lists)).FontSize(fsTool).TextColor(a.secondaryInk()).LineHeight(1.4).SingleLine()
				separator = true
			}
			if commands > 0 {
				if separator {
					ui.Text(c, "，").FontSize(fsTool).TextColor(a.secondaryInk()).LineHeight(1.4).SingleLine()
				}
				disclosureIcon(c, "terminal", a.currentPalette().accent)
				ui.Text(c, fmt.Sprintf(locales.Text("执行命令 %d 次"), commands)).FontSize(fsTool).TextColor(a.secondaryInk()).LineHeight(1.4).SingleLine()
			}
			if trimmed == end-index {
				// Nothing in this run kept its body, so opening it shows excerpts; the
				// chip keeps the report the trimmed cards used to carry one by one.
				neutralChip(c, locales.Text(fastCompactChip))
			}
			caret := "chevron"
			if open {
				caret = "chevrondown"
			}
			iconGlyph(c, caret, sizeCaretGroupCard).TextColor(a.secondaryInk())
		})
		if row.Clicked() {
			t.uiState().ToolOpen[key] = !open
			if open {
				for i := index; i < end; i++ {
					if !isFoldableCommand(t.Messages[i]) {
						continue
					}
					child := toolCardKey(t, i, t.Messages[i])
					delete(t.uiState().ToolOpen, child)
					delete(t.uiState().CommandDetails, child)
				}
			}
		}
		uikit.Reveal(c.Key(key+"-reveal"), open, func() {
			height := float32(len(entries)) * 28
			for _, entry := range entries {
				if entry.name == "command" && t.uiState().ToolOpen[toolCardKey(t, entry.index, entry.message)] {
					height += 240
					break
				}
			}
			ui.Scroll(c.Key(key + "-entries")).FillWidth().Height(min(float32(360), max(float32(28), height))).Shrink(0).Clip().Children(func() {
				ui.Column(c).FillWidth().MinWidth(0).Children(func() {
					for n, entry := range entries {
						last := n == len(entries)-1
						prefix := "├─"
						if last {
							prefix = "└─"
						}
						if entry.name == "command" {
							a.toolGroupCommandEntry(c, t, entry, prefix)
							continue
						}
						ui.Row(c).Padding(1, 8, 1, 16).Gap(gapInline).AlignItems(ui.Center).Children(func() {
							ui.Text(c, prefix).Font(monoFamily).FontSize(fsSmall).TextColor(a.secondaryInk())
							ui.Text(c, entry.verb).FontSize(fsTool).TextColor(a.secondaryInk())
							color := a.secondaryInk()
							if entry.state == "error" {
								color = c.Theme().Danger
							}
							if boolOf(entry.message, "fastCompacted") {
								toolGroupCompactedChip(c, entry.message)
							} else {
								uikit.Tooltip(c, ui.Text(c, short(entry.title, 80)).FontSize(fsTool).TextColor(color).SingleLine().MinWidth(0).Shrink(1), entry.title)
							}
							if entry.detail != "" {
								uikit.ErrorText(c, entry.detail).FontSize(fsTool).MinWidth(0).Grow(1).Shrink(1)
							}
							if entry.summary != "" {
								summary := "(" + entry.summary + ")"
								uikit.Tooltip(c, ui.Text(c, short(summary, 72)).FontSize(fsTool).TextColor(a.secondaryInk()).SingleLine().Shrink(0), summary)
							}
						})
					}
				})
			})
			if remaining := end - visibleEnd; remaining > 0 {
				transcriptAction(c, key+"-more", fmt.Sprintf("Show more (%d)", remaining)).OnClick(func() { *extra += 100 })
			}
			if *extra > 0 {
				transcriptAction(c, key+"-less", "Show less").OnClick(func() {
					*extra = 0
					for child, detail := range t.uiState().CommandDetails {
						if detail != nil && detail.Index >= index+100 && detail.Index < end {
							delete(t.uiState().CommandDetails, child)
							delete(t.uiState().ToolOpen, child)
						}
					}
				})
			}
		})
	})
}

// toolGroupCompactedChip labels an entry whose arguments and result were
// replaced by a local trim, and keeps the excerpt it was replaced with on hover:
// the group row no longer draws that body anywhere else.
func toolGroupCompactedChip(c *ui.Context, m map[string]any) {
	chip := neutralChip(c, locales.Text(fastCompactChip))
	if body := str(m, "body"); body != "" {
		uikit.Tooltip(c, chip, body)
	}
}

func (a *desktop) toolGroupCommandEntry(c *ui.Context, t *conversation, entry toolGroupEntry, prefix string) {
	m := entry.message
	key := toolCardKey(t, entry.index, m)
	state := entry.state
	result := toolResult(m)
	open := t.uiState().ToolOpen[key]
	metadataInk := a.secondaryInk()
	if state == "error" || (result["exitCode"] != nil && num(result, "exitCode") != 0) {
		metadataInk = c.Theme().Danger
	}
	verb := toolTable["command"].doneVerb
	if state == "pending" || state == "running" {
		verb = toolTable["command"].runningVerb
	} else if state == "error" {
		verb = toolTable["command"].failedVerb
	}
	command := entry.title
	if command == "" {
		command = locales.Text("命令")
	}
	button := ui.ButtonBase(c.Key(key+"-group-row")).FillWidth().MinWidth(0).Height(24).Padding(1, 8, 1, 16).Gap(gapInline).AlignItems(ui.Center).Justify(ui.Start).Cursor(ui.CursorPointer).Label(locales.Text("命令结果 ") + key + " " + verb + " " + short(command, 120))
	// Same cache as toolCard: an expanded group redraws every frame, so the
	// arguments must not be decoded again on each one.
	args := memoAt[argsMemo](button, "parsed-args").get(str(m, "args"), parseToolArgs)
	button.Children(func() {
		ui.Text(c, prefix).Font(monoFamily).FontSize(fsSmall).TextColor(a.secondaryInk())
		commandRow := ui.Row(c.Key(key + "-group-command-text")).Grow(1).MinWidth(0).Gap(gapInline).AlignItems(ui.Center)
		commandRow.Children(func() {
			ink := a.secondaryInk()
			ui.Text(c, verb).FontSize(fsTool).TextColor(ink).Shrink(0).NoWrap()
			uikit.Tooltip(c, ui.Text(c, short(command, 120)).Font(monoFamily).FontSize(fsTool).TextColor(ink).SingleLine().MinWidth(0).Shrink(1), command)
			if boolOf(m, "fastCompacted") {
				toolGroupCompactedChip(c, m)
			}
			var resultParts []string
			if code, exists := result["exitCode"]; exists {
				resultParts = append(resultParts, fmt.Sprintf("exit %v", code))
			}
			if boolOf(result, "timedOut") {
				resultParts = append(resultParts, locales.Text("超时"))
			}
			if boolOf(result, "promotedToService") {
				resultParts = append(resultParts, locales.Text("已转后台服务"))
			}
			if ms := toolDuration(m, result); ms > 0 {
				resultParts = append(resultParts, fmtDur(ms))
			}
			if len(resultParts) > 0 {
				suffix := "(" + strings.Join(resultParts, " · ") + ")"
				uikit.Tooltip(c, ui.Text(c, short(suffix, 72)).FontSize(fsTool).TextColor(metadataInk).SingleLine().Shrink(0), suffix)
			}
		})
		caret := "chevron"
		if open {
			caret = "chevrondown"
		}
		iconGlyph(c, caret, sizeCaretRow).Shrink(0).TextColor(a.secondaryInk())
	})
	if button.Clicked() {
		t.uiState().ToolOpen[key] = !open
		if open {
			delete(t.uiState().CommandDetails, key)
		} else if state != "pending" && state != "running" {
			a.loadCommandDetail(t, entry.index, key)
		}
	}
	if open {
		ui.Row(c.Key(key + "-group-output-row")).FillWidth().MinWidth(0).Gap(gapInline).Children(func() {
			ui.Text(c, " ").Padding(0, 8, 0, 16).Font(monoFamily).FontSize(fsSmall).TextColor(a.secondaryInk())
			ui.Column(c.Key(key+"-group-output")).FillWidth().MinWidth(0).Padding(0, 8, 0, 0).Children(func() {
				a.commandDetailView(c, t, entry.index, key, args, m)
			})
		})
	}
}

func (a *desktop) toolCard(c *ui.Context, t *conversation, index int, m map[string]any) {
	name := str(m, "name")
	if name == "" {
		name = str(m, "toolName")
	}
	if name == "" {
		name = "tool"
	}
	name = toolKind(name)
	state := toolState(m)
	desc := describeTool(name)
	verb := desc.doneVerb
	switch state {
	case "running", "pending":
		verb = desc.runningVerb
	case "error":
		verb = desc.failedVerb
	}
	container := ui.Column(c.Key("tool-" + toolCardKey(t, index, m))).Gap(2)
	parsed := memoAt[argsMemo](container, "parsed-args")
	args := parsed.get(str(m, "args"), parseToolArgs)
	summary := toolArgSummary(name, args)
	isMCP := str(m, "mcpServer") != "" || str(m, "mcpTool") != "" || strings.HasPrefix(name, "mcp")
	if isMCP {
		// A failed MCP call must not read as "Called": the state word comes from
		// the same table the builtin cards use.
		verb = ""
		if state == "error" {
			verb = "MCP call failed"
		}
		if state == "running" || state == "pending" {
			verb = "Calling MCP"
		}
		if verb == "" {
			verb = "Called MCP"
		}
		summary = strings.Trim(str(m, "mcpServer")+"/"+str(m, "mcpTool"), "/")
		if summary == "" {
			summary = name
		}
	}
	if name == "subagent" {
		// The header says what the card is; the child rows below carry the
		// role, state, task and counters.
		verb = "Subagent"
		summary = ""
		desc.showTiming = false
	}
	result := toolResult(m)
	filePath := ""
	if (name == "edit" || name == "create") && !isMCP {
		filePath = toolFilePath(args, result)
		if summary == "" {
			summary = filePath
			if files := list(args, "files"); len(files) > 1 {
				summary = fmt.Sprintf("%d files", len(files))
			}
		}
	}
	if name == "scheduled_task" {
		summary = a.scheduledTaskSummary(args, result)
	}
	expandable := desc.expandable && !isMCP && name != "screenshot" && name != "image_generate" && name != "image_edit"
	if isMCP {
		desc.showTiming = true
	}
	key := str(m, "toolCallId")
	if key == "" {
		key = fmt.Sprintf("%s-%d", t.ID, index)
	}
	open := expandable && t.uiState().ToolOpen[key]
	p := a.currentPalette()
	// Tool rows mirror main's .rich-tool-card: transparent, borderless, with a
	// hanging status icon; the row itself carries the 6px rhythm below.
	container.Children(func() {
		row := ui.ButtonBase(c.Key(key+"-header")).FillWidth().Height(24).Padding(0, 8, 0, 0).Gap(gapTight).AlignItems(ui.Center).Justify(ui.Start).Label(locales.Text("工具详情 ") + key)
		if expandable || filePath != "" {
			row.Cursor(ui.CursorPointer)
		}
		if filePath != "" {
			uikit.Tooltip(c, row, locales.Text("在编辑器打开 ")+filePath)
			row.OnClick(func() { a.openToolFile(t, filePath) })
		}
		row.Hidden(name == "suggest").Children(func() {
			if name != "subagent" {
				ui.Box(c).Absolute().Left(-20).Top(0).Width(16).HeightPercent(100).Center().Children(func() { toolStatusIcon(c, state) })
			}
			verbColor := p.accent
			if state == "error" || name == "delete" || name == "remote_delete_path" {
				verbColor = c.Theme().Danger
			}
			if verb != "" {
				ui.Text(c, verb).FontSize(fsTool).TextColor(verbColor).Shrink(0).LineHeight(1.4).NoWrap()
			}
			if summary != "" {
				if name == "command" || name == "remote_run_command" {
					text := short(summary, 120)
					uikit.Tooltip(c, ui.RichText(c.Key(key+"-summary"), codeSpansWithBase(c, text, "command.sh", commandLineInk(c.Theme(), a.secondaryInk()))...).FontSize(fsTool).SingleLine().MinWidth(0).Shrink(1), summary)
				} else {
					text := "(" + short(summary, 120) + ")"
					color := a.secondaryInk()
					if name == "edit" || name == "create" || name == "delete" || name == "remote_edit" || name == "remote_create_file" || name == "remote_delete_path" {
						color = color.Mix(c.Theme().Text, 0.4)
					}
					uikit.Tooltip(c, ui.Text(c.Key(key+"-summary"), text).FontSize(fsTool).TextColor(color).LineHeight(1.4).SingleLine().MinWidth(0).Shrink(1), summary)
				}
			}
			if isMCP {
				label := map[string]string{"success": locales.Text("成功"), "error": locales.Text("失败"), "running": locales.Text("运行中"), "pending": locales.Text("等待中"), "cancelled": locales.Text("已取消")}[state]
				ui.Text(c, label).FontSize(fsAux).TextColor(a.secondaryInk()).Shrink(0).NoWrap()
			}
			if name == "screenshot" || name == "image_generate" || name == "image_edit" {
				if w, h := int(num(result, "width")), int(num(result, "height")); w > 0 && h > 0 {
					ui.Text(c, fmt.Sprintf("%d × %d", w, h)).FontSize(fsAux).TextColor(a.secondaryInk()).Shrink(0).NoWrap()
				}
				if size := int64(num(result, "bytes")); size > 0 {
					ui.Text(c, fmtBytes(size)).FontSize(fsAux).TextColor(a.secondaryInk()).Shrink(0).NoWrap()
				}
			}
			if name == "command" || name == "remote_run_command" {
				// Exit status and duration describe the command, so they sit right
				// after it instead of being pushed to the row's far edge.
				if code, exists := result["exitCode"]; exists {
					ui.Text(c, fmt.Sprintf("exit %v", code)).FontSize(fsTool - 2).TextColor(a.secondaryInk()).Shrink(0).NoWrap()
				}
				if boolOf(result, "timedOut") {
					neutralChip(c, locales.Text("超时"))
				}
				if boolOf(result, "promotedToService") {
					neutralChip(c, locales.Text("已转后台服务"))
				}
				if ms := toolDuration(m, result); ms > 0 || state == "success" || state == "error" || state == "cancelled" {
					ui.Text(c, fmtDur(ms)).FontSize(fsTool - 2).TextColor(a.secondaryInk()).Shrink(0).NoWrap()
				}
			}
			if name == "wait" && state == "running" {
				if started, ok := m["startedAt"].(time.Time); ok {
					remaining := int(num(args, "seconds")) - int(time.Since(started).Seconds())
					if remaining < 0 {
						remaining = 0
					}
					neutralChip(c, fmt.Sprintf(locales.Text("剩余 %ds"), remaining))
					c.After(time.Second)
				}
			}
			if name == "http_request" || name == "web_fetch" {
				if status := int(num(result, "status")); status > 0 {
					ui.Text(c, fmt.Sprintf("%d", status)).FontSize(fsAux).TextColor(a.secondaryInk()).Shrink(0).NoWrap()
				}
				if size := int64(num(result, "bytesRead")); size > 0 {
					ui.Text(c, fmtBytes(size)).FontSize(fsAux).TextColor(a.secondaryInk()).Shrink(0).NoWrap()
				}
			}
			chips := toolChips(name, state, args, result)
			if isMCP || name == "subagent" {
				chips = nil
			}
			for _, extra := range chips {
				if (name == "edit" || name == "remote_edit") && strings.HasPrefix(extra, "+") {
					parts := strings.Fields(extra)
					if len(parts) > 0 {
						ui.Text(c, parts[0]).FontSize(fsAux).TextColor(c.Theme().Success).NoWrap()
					}
					if len(parts) > 1 {
						ui.Text(c, parts[1]).FontSize(fsAux).TextColor(c.Theme().Danger).NoWrap()
					}
				} else if name == "calculate" {
					ui.Text(c, extra).FontSize(fsTool).TextColor(a.secondaryInk()).NoWrap()
				} else {
					neutralChip(c, extra)
				}
			}
			if boolOf(m, "fastCompacted") {
				neutralChip(c, locales.Text(fastCompactChip))
			}
			// A command's duration already sits next to its exit status.
			if desc.showTiming && name != "command" && name != "remote_run_command" {
				if ms := toolDuration(m, result); ms > 0 || state == "success" || state == "error" || state == "cancelled" {
					ui.Text(c, fmtDur(ms)).FontSize(fsTool - 2).TextColor(a.secondaryInk()).Shrink(0).NoWrap()
				}
			}
		})
		if isMCP {
			return
		} // Result payloads stay in model context, outside the UI.
		if expandable && row.Clicked() {
			t.uiState().ToolOpen[key] = !open
			if name == "command" {
				if open {
					delete(t.uiState().CommandDetails, key)
				} else if state != "pending" && state != "running" {
					a.loadCommandDetail(t, index, key)
				}
			}
		}
		// Local command output (including failures) is entirely demand-loaded.
		if name == "command" {
			if open {
				a.commandDetailView(c, t, index, key, args, m)
			}
			return
		}
		body := ui.Column(c.Key(key + "-body")).FillWidth().MinWidth(0).Gap(gapTight)
		inset := float32(0)
		if name == "create" || name == "remote_create_file" {
			inset += 4
		}
		body.Padding(0, 0, 0, inset)
		body.Children(func() {
			switch {
			case state == "cancelled":
				cancelledResult(c, a.secondaryInk())
			case state == "error" && name != "delete" && name != "remote_delete_path":
				errText := str(m, "body")
				if errText == "" {
					errText = str(m, "error")
				}
				if boolOf(result, "cancelled") {
					errText = locales.Text("已取消")
				}
				if reason := str(result, "error"); reason != "" {
					errText = reason
				}
				// Keep diagnostics in context; the visible error is one ellipsized line.
				ui.Row(c).FillWidth().Padding(4, 9).Gap(gapField).Radius(6).Background(ui.RGBA(239, 68, 68, 0.08)).
					Border(1, ui.RGBA(239, 68, 68, 0.2)).Children(func() {
					uikit.ErrorText(c, errText).FontSize(fsAux)
				})
			case (name == "edit" || name == "remote_edit") && len(list(result, "files")) > 0:
				for n, item := range list(result, "files") {
					if file, ok := item.(map[string]any); ok {
						if len(list(result, "files")) > 1 {
							path := str(file, "path")
							if name == "edit" && path != "" {
								transcriptAction(c, fmt.Sprintf("%s-file-%d-open", key, n), path).OnClick(func() { a.openToolFile(t, path) })
							} else {
								ui.Text(c, path).FontSize(fsSmall).TextColor(a.secondaryInk())
							}
						}
						a.diffBlock(c, fmt.Sprintf("%s-file-%d", key, n), str(file, "diff"), true)
					}
				}
			case name == "delete" || name == "remote_delete_path":
				toolDeleteTree(c, args, result, a.secondaryInk())
				if state == "error" {
					reason := str(m, "error")
					if reason == "" {
						reason = str(result, "error")
					}
					notice(c, "error", reason, a.secondaryInk())
				}
			case (name == "edit" || name == "remote_edit") && str(result, "diff") != "":
				a.diffBlock(c, key+"-diff", str(result, "diff"), true)
			case name == "create" || name == "remote_create_file":
				content := str(args, "content")
				if content == "" {
					content = str(result, "content")
				}
				preview := memoAt[createPreviewMemo](body, "create-preview").get(content, func(content string) createPreview {
					return createPreview{
						startLine: max(1, strings.Count(strings.TrimRight(content, "\n"), "\n")+2-createPreviewLines),
						content:   tailLines(content, createPreviewLines),
					}
				})
				if start := int(num(m, "createPreviewStartLine")); boolOf(m, "createPreviewTrimmed") && start > 0 {
					preview.startLine = start
				}
				content = preview.content
				if content != "" {
					// One shape from the first streamed line to the result: a fixed-height
					// box while the file was still arriving left a short card taller than its
					// finished form, and finishing pulled everything below it up.
					toolSourceWindow(c, key+"-source", content, str(args, "path"), preview.startLine, a.secondaryInk())
				}
			case name == "suggest":
				ui.Row(c).Wrap().Gap(gapInline).Children(func() {
					for n, raw := range list(result, "items") {
						text := fmt.Sprint(raw)
						suggestionButton(c.Key(fmt.Sprintf("%s-suggest-%d", key, n)), text, a.secondaryInk()).OnClick(func() {
							t.Draft = text
							if a.core != nil {
								a.send(t, text)
							}
						})
					}
				})
			case name == "service" || name == "scheduled_task" || name == "ssh_cluster":
				a.toolResourceCards(c, key, name, result, open)
			case name == "remote_transfer":
				toolFields(c, result, []string{"action", "target", "localPath", "remotePath", "fileCount", "dirCount", "totalBytes"}, a.secondaryInk())
			case name == "http_request" || name == "web_fetch":
				// Network tool summaries stay on the header line.
			case name == "render_html":
				html := str(args, "html")
				if state == "running" || state == "pending" {
					// 工具还没回，没有结果就没有可载入的页面；把已经到达的 HTML 尾部显示
					// 出来，让人看见页面在长，而不是只看到一个等着点的空按钮。
					if strings.TrimSpace(html) == "" {
						ui.Text(c, locales.Text("正在生成页面…")).FontSize(fsSmall).TextColor(a.secondaryInk())
					} else {
						toolCodeWindow(c, key+"-html-stream", tailLines(html, 18), true, "preview.html", a.secondaryInk())
					}
					break
				}
				a.toolHTMLView(c, t, m, args)
				ui.Row(c).Gap(gapField).Children(func() {
					flat(c, locales.Text("导出 HTML")).OnClick(func() { a.exportHTML("preview.html", html) })
					flat(c, locales.Text("在浏览器打开")).OnClick(func() { a.openHTMLPreview(html) })
				})
				if open {
					toolCodeWindow(c, key+"-html-source", html, true, "preview.html", a.secondaryInk())
				}

			case name == "image_generate" || name == "image_edit" || name == "screenshot":
				// Metadata belongs to the title; captured/generated images remain below.
			case name == "ask":
				if state == "running" && len(t.Questions) > 0 {
					a.askComposer(c, t)
				} else {
					toolAskSummary(c, key, args, result)
				}
			case name == "subagent":
				// The card draws its children through the same judgement the
				// transcript uses, so a child is either shown once or not at all.
				rows := t.uiState().transcriptRows
				if rows == nil {
					rows = newTranscriptRows(t)
				}
				children := rows.childrenOf(str(m, "toolCallId"))
				for _, n := range children {
					a.subagentRow(c, t, n, t.Messages[n])
				}
				if len(children) == 0 && open {
					if summary := str(result, "summary"); summary != "" {
						fieldNote(c, summary, a.secondaryInk())
					}
				}
			case name == "skill":
				if path := str(result, "path"); path != "" {
					fieldNote(c, path, a.secondaryInk())
				}
			case name == "plan":
				// The shared plan panel below the timeline owns the steps.
			case expandable && !open:
				lines := 4
				if name == "command" || name == "remote_run_command" {
					// A command's detail is the tail of its output; one line says what
					// it printed, and expanding the card still shows all of it.
					lines = 1
				}
				if preview := toolPreview(name, args, result, lines); preview != "" {
					toolCodeWindow(c, key+"-preview", preview, false, "", a.secondaryInk())
				}
			case expandable && open:
				a.toolBody(c, t, index, name, args, result, m)
			}
		})
		a.messageAttachments(c, t, m)
		for _, warning := range list(result, "warnings") {
			toolWarning(c, fmt.Sprint(warning), a.secondaryInk())
		}
		if boolOf(result, "sandboxDenied") {
			toolWarning(c, str(result, "deniedWriteHint"), a.secondaryInk())
		}
		if validation := str(result, "validation"); strings.HasPrefix(validation, "自动校验失败") {
			toolWarning(c, strings.TrimSpace(strings.TrimPrefix(validation, "自动校验失败（文件已写入）：")), a.secondaryInk())
		}
	})
}

func smallMono() string { return monoFamily }

// easeInOut is the cubic ease-in-out main's tool-svg-blink runs under:
// slow at both ends, so the dot dwells at full and dim instead of sweeping
// past them.
func easeInOut(t float32) float32 {
	if t < 0.5 {
		return 4 * t * t * t
	}
	u := 2 - 2*t
	return 1 - u*u*u/2
}

// toolStatusDot is the mark in the transcript gutter: the same filled dot the
// rest of the app marks status with (pillDot), kept small beside command text and
// the same size in every state. A tick and a cross read as two different shapes
// that also shift the line as a call finishes; a dot says the state by colour.
const toolStatusDot = 6

// toolStatusIcon draws the state of a tool call: green once it finished, red when
// it failed, the accent blinking while it runs, and the palette's grey while it
// waits — pillDotColor keeps the grey readable on a light page. Every state owns
// a 16x16 box, so the line never moves.
//
// Running fades 1<->0.3 on a 1.2s ease-in-out loop (main's tool-svg-blink), not a
// cosine sweep — the cosine never sat at either end, so it read as too fast.
// Opacity only: no scale, no rotation (a growing/spinning dot reads as urgency
// instead of "still working").
// toolStatusLabel names a state for assistive technology: the mark is a dot, so
// the colour is the only thing telling a reader which state it is.
func toolStatusLabel(state string) string {
	switch state {
	case "success":
		return locales.Text("已完成")
	case "error":
		return locales.Text("失败")
	case "running":
		return locales.Text("运行中")
	}
	return locales.Text("等待中")
}

func toolStatusIcon(c *ui.Context, state string) {
	ui.Box(c).Size(16, 16).Center().Label(toolStatusLabel(state)).Children(func() {
		switch state {
		case "running":
			color := c.Theme().Accent
			// Drawn instead of a Loop on a box: Loop asks for a frame that
			// rebuilds the whole view, which a thinking model would do at the
			// display's rate. Reduced motion keeps the dot steady.
			still := uikit.StillMotion(c)
			ui.Box(c).Size(toolStatusDot, toolStatusDot).Draw(func(p *ui.Painter, r ui.Rect) {
				level := float32(0.75)
				if !still {
					p.AnimationFrameRate(30)
					ms := float64(p.Now().UnixMilli()%1200) / 1200
					tri := ms * 2
					if tri > 1 {
						tri = 2 - tri
					}
					level = 0.3 + 0.7*easeInOut(float32(tri))
				}
				p.Fill(r, color.Alpha(level), toolStatusDot/2)
			})
		default:
			tone := pillNeutral
			switch state {
			case "success":
				tone = pillOK
			case "error":
				tone = pillDanger
			}
			ui.Box(c).Size(toolStatusDot, toolStatusDot).Radius(toolStatusDot / 2).Background(pillDotColor(c, tone))
		}
	})
}

func parseToolArgs(raw string) map[string]any {
	out := map[string]any{}
	if strings.TrimSpace(raw) == "" {
		return out
	}
	if json.Unmarshal([]byte(raw), &out) != nil {
		// Streaming arguments may end inside a JSON string. Decode complete
		// fields and the available content prefix without inventing a suffix.
		dec := json.NewDecoder(strings.NewReader(raw))
		if _, err := dec.Token(); err == nil {
			for dec.More() {
				token, err := dec.Token()
				if err != nil {
					break
				}
				key, ok := token.(string)
				if !ok {
					break
				}
				var value any
				if err := dec.Decode(&value); err != nil {
					// 流式参数会在某个字符串中间断掉，断在哪个字段都可能：把已经到达的
					// 那一段交出去，而不是整个字段丢掉。以前这里只认 content，于是
					// render_html 的 html 字段永远取不到前缀，卡片只能显示一个空按钮，
					// 直到工具跑完才突然出现成品。
					if err == io.ErrUnexpectedEOF || err == io.EOF {
						rest := strings.TrimSpace(raw[dec.InputOffset():])
						rest = strings.TrimSpace(strings.TrimPrefix(rest, ":"))
						if strings.HasPrefix(rest, "\"") {
							for cut := len(rest); cut > 1 && cut >= len(rest)-6; cut-- {
								var text string
								if json.Unmarshal([]byte(rest[:cut]+"\""), &text) == nil {
									out[key] = text
									break
								}
							}
						}
					}
					break
				}
				out[key] = value
			}
		}
		out["_raw"] = raw
		return out
	}
	return out
}

func toolArgSummary(name string, args map[string]any) string {
	pick := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := args[k]; ok {
				switch x := v.(type) {
				case string:
					if strings.TrimSpace(x) != "" {
						return x
					}
				case float64:
					return trimZero(x)
				}
			}
		}
		return ""
	}
	switch name {
	case "command", "remote_run_command":
		return pick("command", "cmd")
	case "edit", "create", "read", "delete", "remote_read", "remote_edit", "remote_create_file", "remote_delete_path":
		if p := pick("path", "file_path", "file"); p != "" {
			return p
		}
		if paths, ok := args["paths"].([]any); ok && len(paths) > 0 {
			return fmt.Sprintf("%d paths", len(paths))
		}
	case "grep":
		return pick("pattern")
	case "glob":
		return pick("pattern")
	case "list":
		return pick("path")
	case "http_request", "web_fetch":
		return pick("url")
	case "calculate":
		return pick("expression")
	case "plan":
		for _, step := range list(args, "steps") {
			if title, ok := step.(string); ok && title != "" {
				return title
			}
		}
		return pick("finish")
	case "skill":
		return pick("name", "skill")
	case "ask":
		return pick("question")
	case "subagent":
		return pick("description", "task", "prompt")
	case "ssh_cluster":
		return pick("action", "alias")
	case "service":
		return pick("action", "name", "id")
	case "scheduled_task":
		return pick("action", "name")
	case "render_html":
		return pick("title")
	case "image_generate", "image_edit":
		return pick("prompt")
	case "wait":
		if v := pick("seconds"); v != "" {
			return v + "s"
		}
	}

	return pick("path", "name", "title", "action")
}

// normalizeToolMessage runs at ingestion, replacing the wire JSON with its
// decoded value. Keep one representation rather than a raw string plus a cache.
func normalizeToolMessage(m map[string]any) {
	raw, ok := m["result"].(string)
	if !ok {
		if _, exists := m["result"].(map[string]any); exists {
			return
		}
		raw, ok = m["body"].(string)
	}
	if !ok || raw == "" {
		return
	}
	var decoded map[string]any
	if json.Unmarshal([]byte(raw), &decoded) != nil || decoded == nil {
		decoded = map[string]any{"_text": raw}
	}
	m["result"] = decoded
	// Result events used to retain the same wire string in body as well.
	if body, ok := m["body"].(string); ok && body == raw && str(m, "toolState") != "tool:error" {
		delete(m, "body")
	}
}

// toolResult also accepts legacy wire values; live/history ingestion normalizes
// them once, so drawing reads maps without decoding JSON.

func toolResult(m map[string]any) map[string]any {
	switch raw := m["result"].(type) {
	case string:
		var out map[string]any
		if json.Unmarshal([]byte(raw), &out) == nil {
			return unwrapToolResult(out)
		}
		return map[string]any{"_text": raw}
	case map[string]any:
		return unwrapToolResult(raw)
	}
	if body, ok := m["body"].(string); ok && body != "" {
		var out map[string]any
		if json.Unmarshal([]byte(body), &out) == nil {
			return unwrapToolResult(out)
		}
		return map[string]any{"_text": body}
	}
	return nil
}

// The runtime wraps every tool payload in {ok, data}; keep the envelope
// out of presentation while retaining errors for failed operations.
func unwrapToolResult(raw map[string]any) map[string]any {
	if data, ok := raw["data"].(map[string]any); ok {
		// These are UI-owned maps. Merge envelope metadata in place instead of
		// allocating and copying the entire payload on every render.
		out := data
		for _, k := range []string{"error", "errorCode", "ok", "warnings"} {
			if _, exists := out[k]; !exists && raw[k] != nil {
				if k != "ok" || raw[k] == false {
					out[k] = raw[k]
				}
			}
		}
		return out
	}
	return raw
}

func resultText(result map[string]any) string {
	for _, k := range []string{"output", "stdout", "text", "content", "body", "_text", "summary", "message", "error"} {
		if v, ok := result[k].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	if raw, err := json.MarshalIndent(result, "", "  "); err == nil && len(result) > 0 {
		return string(raw)
	}
	return ""
}

// commandOutput keeps protocol metadata out of the terminal transcript.
func commandOutput(result map[string]any) string {
	return commandOutputFromKey(commandKey(result))
}

// Walk the original fields from the end, without copying a potentially large
// command transcript just to display its last few nonblank lines.
func commandResultPreview(result map[string]any, lines int) string {
	if lines <= 0 {
		return ""
	}
	key := commandKey(result)
	kept := make([]string, 0, lines)
	for _, part := range [...]string{key.raw, key.text, key.stderr, key.stdout, key.output} {
		for part != "" && len(kept) < lines {
			index := strings.LastIndexByte(part, '\n')
			row := part[index+1:]
			row = strings.Join(strings.FieldsFunc(row, func(r rune) bool { return r == ' ' || r == '\t' || r == '\r' || r == '\f' || r == '\v' }), " ")
			if row != "" {
				kept = append(kept, row)
			}
			if index < 0 {
				break
			}
			part = part[:index]
		}
		if len(kept) >= lines {
			break
		}
	}
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	return strings.Join(kept, "\n")
}

// stripBlankLines drops empty and whitespace-only lines. Shell output arrives
// padded with blank lines that make the transcript look ragged; main's terminal
// view trims them the same way.
func stripBlankLines(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	kept := lines[:0]
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// Match main's collapsed terminal white-space: pre-line. Only the display
// preview collapses horizontal whitespace; expanded output keeps indentation.
// The tail is taken first: blank lines are dropped from it rather than from the
// whole log, which this runs on for every frame while the card is collapsed.
func commandPreviewText(output string, lines int) string {
	if lines <= 0 {
		return ""
	}
	trimmed := strings.TrimRight(output, "\n")
	if trimmed == "" {
		return ""
	}
	// A run of blank lines at the end would leave the window short of rows, so
	// widen it until enough survive or the whole output is in hand.
	for window := lines*4 + 8; ; window *= 4 {
		tail := tailLines(trimmed, window)
		rows := strings.Split(tail, "\n")
		kept := rows[:0]
		for _, row := range rows {
			// \r is a separator too: Windows output arrives as CRLF, and dropping the
			// blank-line pass upstream means each row still carries its own \r.
			row = strings.Join(strings.FieldsFunc(row, func(r rune) bool { return r == ' ' || r == '\t' || r == '\r' || r == '\f' || r == '\v' }), " ")
			if row != "" {
				kept = append(kept, row)
			}
		}
		if len(kept) < lines && len(tail) < len(trimmed) {
			continue
		}
		if len(kept) > lines {
			kept = kept[len(kept)-lines:]
		}
		return strings.Join(kept, "\n")
	}
}

func toolChips(name, state string, args, result map[string]any) []string {
	var chips []string
	if name == "read" || name == "batch_read" || name == "grep" || name == "list" || name == "glob" {
		return nil
	}
	switch name {
	case "edit", "remote_edit":
		if state == "success" {
			added := int(num(result, "addedLines"))
			removed := int(num(result, "removedLines"))
			if added > 0 || removed > 0 {
				chips = append(chips, fmt.Sprintf("+%d -%d", added, removed))
			}
		}
	case "calculate":
		if v, ok := result["text"]; ok {
			chips = append(chips, "= "+fmt.Sprint(v))
		} else if v, ok := result["value"]; ok {
			chips = append(chips, "= "+fmt.Sprint(v))
		} else if v, ok := result["result"]; ok {
			chips = append(chips, fmt.Sprint(v))
		}
	case "ask":
		if qs := list(result, "questions"); len(qs) > 0 {
			chips = append(chips, fmt.Sprintf(locales.Text("%d 个问题"), len(qs)))
		}
	case "subagent":
		if steps := int(num(result, "steps")); steps > 0 {
			chips = append(chips, fmt.Sprintf(locales.Text("%d 步"), steps))
		}
	}
	if trunc, _ := result["truncated"].(bool); trunc {
		if name == "screenshot" {
			// A window enumeration limit is not a truncated screenshot or text.
			if total := int(num(result, "total")); total > len(list(result, "windows")) {
				chips = append(chips, fmt.Sprintf(locales.Text("%d / %d 个窗口"), len(list(result, "windows")), total))
			}
		} else {
			chips = append(chips, locales.Text("已截断"))
		}
	}
	return chips
}

// toolPreview returns the collapsed preview body for a card.
func toolPreview(name string, args, result map[string]any, lines int) string {
	switch name {
	case "command", "remote_run_command":
		return commandResultPreview(result, lines)
	case "edit", "remote_edit":
		if diff := str(result, "diff"); diff != "" {
			return tailLines(unifiedSnippet(diff, 8), lines)
		}
		return tailLines(str(result, "changedLinesBlock"), lines)
	case "create", "remote_create_file":
		return tailLines(resultText(result), lines)
	case "list":
		var entries []string
		for _, item := range list(result, "entries") {
			if m, ok := item.(map[string]any); ok {
				entries = append(entries, str(m, "path"))
			} else {
				entries = append(entries, fmt.Sprint(item))
			}
		}
		if len(entries) == 0 {
			return tailLines(resultText(result), lines)
		}
		return strings.Join(head(entries, 8), "\n")
	case "render_html":
		if html := str(args, "html"); html != "" {
			return tailLines(html, lines)
		}
		return tailLines(str(result, "source"), lines)
	case "delete", "remote_delete_path":
		var paths []string
		for _, item := range list(result, "results") {
			if m, ok := item.(map[string]any); ok {
				paths = append(paths, str(m, "path"))
			}
		}
		if len(paths) == 0 {
			return ""
		}
		return strings.Join(paths, "\n")
	}
	return tailLines(resultText(result), lines)
}

func (a *desktop) toolBody(c *ui.Context, t *conversation, index int, name string, args, result map[string]any, m map[string]any) {
	if boolOf(result, "resultOmitted") {
		fieldNote(c, locales.Text("历史结果正文未保存。"), a.secondaryInk())
		return
	}
	switch name {
	case "edit", "remote_edit":
		diff := str(result, "diff")
		if diff != "" {
			a.diffBlock(c, fmt.Sprintf("tool-diff-%d", index), diff, true)
			return
		}
		if block := str(result, "changedLinesBlock"); block != "" {
			codeBlock(c, fmt.Sprintf("tool-changed-%d", index), block, 150, a.secondaryInk())
			return
		}
		codeBlock(c, fmt.Sprintf("tool-edit-%d", index), resultText(result), 150, a.secondaryInk())
	case "command", "remote_run_command":
		// The transcript is the whole log: joining and stripping it again on every
		// frame copied the entire output, the biggest text a card holds.
		holder := ui.Column(c.Key(fmt.Sprintf("tool-out-holder-%d", index)))
		memo := memoAt[memoOf[commandOutputKey, string]](holder, "output")
		holder.Children(func() {
			output := memo.get(commandKey(result), func(key commandOutputKey) string { return stripBlankLines(commandOutputFromKey(key)) })
			if output == "" {
				output = locales.Text("(没有输出)")
			}
			toolCodeWindow(c, fmt.Sprintf("tool-out-%d", index), output, true, "", a.secondaryInk())
		})
	case "create", "remote_create_file":
		codeBlock(c, fmt.Sprintf("tool-create-%d", index), resultText(result), 150, a.secondaryInk())
	case "list":
		a.toolListBody(c, index, result)
	case "render_html":
		html := str(args, "html")
		if html == "" {
			html = str(result, "source")
		}
		codeBlock(c, fmt.Sprintf("tool-html-%d", index), html, 150, a.secondaryInk())
		ui.Row(c).Gap(gapField).Children(func() {
			flat(c, locales.Text("导出 HTML")).OnClick(func() {
				a.exportHTML(fmt.Sprintf("render-%d.html", index), html)
			})
			flat(c, locales.Text("在浏览器打开")).OnClick(func() { a.openHTMLPreview(html) })
		})
	case "delete", "remote_delete_path":
		for _, item := range list(result, "results") {
			row, ok := item.(map[string]any)
			if !ok {
				continue
			}
			path := str(row, "path")
			okFlag := boolOf(row, "ok")
			absent := boolOf(row, "absent")
			ui.Row(c).Gap(gapField).Children(func() {
				ui.Text(c, "├─").Font(monoFamily).FontSize(fsSmall).TextColor(a.secondaryInk())
				ui.Text(c, path).Font(monoFamily).FontSize(fsSmall)
				if absent {
					neutralChip(c, locales.Text("本就不存在"))
				} else if okFlag {
					chip(c, locales.Text("已删除"), c.Theme().Success, ui.RGBA(78, 200, 126, 0.12))
				} else {
					uikit.ErrorText(c, str(row, "error")).Grow(1)
				}
			})
		}
	case "remote_transfer":
		for _, pair := range [][2]string{{locales.Text("方向"), str(result, "direction")}, {locales.Text("来源"), str(result, "source")}, {locales.Text("目标"), str(result, "target")}, {locales.Text("文件数"), fmt.Sprint(int(num(result, "files")))}, {locales.Text("目录数"), fmt.Sprint(int(num(result, "dirs")))}} {
			if pair[1] != "" && pair[1] != "0" {
				kv(c, pair[0], pair[1], a.secondaryInk())
			}
		}
	case "ask":
		if qs := list(result, "questions"); len(qs) > 0 {
			for _, q := range qs {
				if qm, ok := q.(map[string]any); ok {
					ui.Text(c, str(qm, "question")).FontSize(fsSmall)
				}
			}
		}
	default:
		text := resultText(result)
		if text == "" {
			fieldNote(c, locales.Text("没有可显示的内容。"), a.secondaryInk())
			return
		}
		codeBlock(c, fmt.Sprintf("tool-body-%d", index), text, 150, a.secondaryInk())
	}
}

func (a *desktop) toolListBody(c *ui.Context, index int, result map[string]any) {
	entries := list(result, "entries")
	if len(entries) == 0 {
		fieldNote(c, resultText(result), a.secondaryInk())
		return
	}
	ui.Column(c).Key(fmt.Sprintf("tool-list-%d", index)).Gap(1).Children(func() {
		for _, item := range entries {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			path := str(m, "path")
			row := ui.Row(c).Gap(gapField).AlignItems(ui.Center)
			row.Children(func() {
				glyph := "file"
				if boolOf(m, "dir") {
					glyph = "folder"
				}
				iconGlyph(c, glyph, 12).TextColor(a.secondaryInk())
				ui.Text(c, path).Font(monoFamily).FontSize(fsSmall)
				if size := int64(num(m, "size")); size > 0 && !boolOf(m, "dir") {
					ui.Text(c, fmtBytes(size)).FontSize(fsTiny).TextColor(a.secondaryInk()).Shrink(0).NoWrap()
				}
			})
		}
	})
}

// diffBlock draws a single-column unified diff. Each source line gets the full
// card width, so long lines wrap in place while additions and removals keep
// their own color and line number.
func (a *desktop) diffBlock(c *ui.Context, key, diff string, compact bool) {
	outer := ui.Column(c.Key(key)).FillWidth().MinWidth(0).Shrink(0)
	memo := memoAt[diffMemo](outer, "diff")
	lines := memo.display(diff)
	limit := memo.limit(compact)
	visible := lines[:min(len(lines), limit)]
	outer.Children(func() {
		for _, line := range visible {
			if line.gap > 0 || strings.HasPrefix(line.text, "@@") || strings.HasPrefix(line.text, "---") || strings.HasPrefix(line.text, "+++") {
				ui.Text(c, line.text).FillWidth().Padding(3, 8).Font(monoFamily).FontSize(fsTiny).TextColor(a.secondaryInk()).Selectable()
				continue
			}
			diffLine(c, line.text, line.changed, line.number, a.secondaryInk())
		}
	})
	if len(visible) < len(lines) {
		transcriptAction(c, key+"-more", fmt.Sprintf("Show more %d rows", len(lines)-len(visible))).OnClick(func() { memo.extra += 120 })
	}
	if memo.extra > 0 {
		transcriptAction(c, key+"-less", "Show less").OnClick(func() { memo.extra = 0 })
	}
}

// diffVisibleLine is one rendered diff row, or a collapsed gap of unchanged
// rows summarized by its count.
type diffVisibleLine struct {
	text string
	gap  int
}

// diffVisibleLines mirrors main's DiffView clustering: changed rows show with 3
// context lines around them and the gaps between windows collapse into
// "… N 行未变更 …" markers. File headers (---/+++) and hunk headers (@@) are
// always kept so the gaps between windows stay separated; the card then drops
// the headers themselves. limit trims a diff that has no changes at all;
// limit <= 0 keeps every row.
func diffVisibleLines(lines []string, limit int, full bool) []diffVisibleLine {
	if full || (limit > 0 && len(lines) <= limit) {
		out := make([]diffVisibleLine, 0, len(lines))
		for _, l := range lines {
			out = append(out, diffVisibleLine{text: l})
		}
		return out
	}
	const context = 3
	keep := make([]bool, len(lines))
	changed := 0
	for i, l := range lines {
		if strings.HasPrefix(l, "+++") || strings.HasPrefix(l, "---") || strings.HasPrefix(l, "@@") {
			keep[i] = true
			continue
		}
		if strings.HasPrefix(l, "+") || strings.HasPrefix(l, "-") {
			changed++
			for j := i - context; j <= i+context; j++ {
				if j >= 0 && j < len(lines) {
					keep[j] = true
				}
			}
		}
	}
	if changed == 0 {
		n := len(lines)
		if limit > 0 && limit < n {
			n = limit
		}
		out := make([]diffVisibleLine, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, diffVisibleLine{text: lines[i]})
		}
		return out
	}
	var out []diffVisibleLine
	gap := 0
	flush := func() {
		if gap > 0 {
			out = append(out, diffVisibleLine{gap: gap})
			gap = 0
		}
	}
	for i, l := range lines {
		if !keep[i] {
			gap++
			continue
		}
		flush()
		out = append(out, diffVisibleLine{text: l})
	}
	flush()
	return out
}

func (a *desktop) exportHTML(name, html string) {
	a.job(func() error {
		path, err := a.core.ExportTextFile(name, html, "HTML", "*.html")
		if err == nil && path != "" {
			a.win.Update(func() { a.notify(locales.Text("已导出：") + path) })
		}
		return err
	})
}

func (a *desktop) openHTMLPreview(html string) {
	path, err := a.core.ExportTextFile("ally-preview.html", html, "HTML", "*.html")
	if err != nil {
		a.fail(err)
		return
	}
	a.job(func() error { return a.core.OpenPathInFileManager(path) })
}

// ---- Sub-agent rows ------------------------------------------------------

func (a *desktop) subagentRow(c *ui.Context, t *conversation, index int, m map[string]any) {
	status := strings.ToLower(str(m, "status"))
	if status == "completed" {
		status = "success"
	}
	if status == "failed" {
		status = "error"
	}
	// A running sub-agent may already carry a tool error; that is its own
	// business and it keeps going, so only a finished run can read as failed.
	if str(m, "error") != "" && status != "running" {
		status = "error"
	} else if status == "success" && str(m, "stopReason") == "step_limit" {
		status = "partial"
	}
	role := str(m, "subagentRole")
	if role == "" {
		role = locales.Text("子代理")
	}
	tone := toneColor(c, status, a.secondaryInk())
	label := map[string]string{"running": locales.Text("运行中"), "success": locales.Text("已完成"), "partial": locales.Text("步数已耗尽"), "error": locales.Text("失败"), "pending": locales.Text("等待中")}[status]
	if label == "" {
		label = status
	}
	subID := str(m, "subId")
	// The card hangs off the tool call that spawned it, so it carries the same
	// "├─"/"└─" junction the grouped tool rows spell out; the card gives up that
	// width instead of running edge to edge like the header above it.
	prefix := "└─"
	parentID := str(m, "parentToolCallId")
	if index+1 < len(t.Messages) {
		next := t.Messages[index+1]
		if str(next, "role") == "subagent" && str(next, "parentToolCallId") == parentID {
			prefix = "├─"
		}
	}
	ui.Row(c).Key("sub-line-" + subID).FillWidth().MinWidth(0).Gap(gapInline).Children(func() {
		// The junction sits on the card's title line, like the one in a tool
		// group sits on its first text line.
		ui.Box(c).Shrink(0).AlignSelf(ui.Start).Padding(11, 0).Children(func() {
			ui.Text(c, prefix).Font(monoFamily).FontSize(fsSmall).TextColor(a.secondaryInk())
		})
		ui.Column(c.Key("sub-card-"+subID)).Grow(1).MinWidth(0).Padding(10, 12).Gap(gapInline).
			Border(1, c.Theme().Border).Background(c.Theme().Surface).Radius(radiusCard).Children(func() {
			ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
				// Main's SubagentInlineCard status dot: 6px, running blinks
				// 1<->0.3 on a 1.2s ease-in-out loop in the accent tone;
				// finished states are static dots in their own tones.
				dot := status
				if dot != "running" && dot != "success" && dot != "partial" && dot != "error" {
					dot = "pending"
				}
				if dot == "running" {
					color := c.Theme().Accent
					still := uikit.StillMotion(c)
					ui.Box(c).Size(6, 6).Draw(func(p *ui.Painter, r ui.Rect) {
						level := float32(0.75)
						if !still {
							p.AnimationFrameRate(30)
							ms := float64(p.Now().UnixMilli()%1200) / 1200
							tri := ms * 2
							if tri > 1 {
								tri = 2 - tri
							}
							level = 0.3 + 0.7*easeInOut(float32(tri))
						}
						p.Fill(r, color.Alpha(level), 3)
					})
				} else {
					ui.Box(c).Size(6, 6).Radius(3).Background(pillDotColor(c, pillToneOf(status)))
				}
				ui.Text(c, role).FontSize(fsBody).Bold()
				ui.Spacer(c)
				ui.Text(c, label).FontSize(fsTiny).TextColor(tone)
				ms := int64(num(m, "durationMs"))
				if status == "running" && num(m, "startTime") > 0 {
					ms = time.Now().UnixMilli() - int64(num(m, "startTime"))
					c.After(time.Second)
				}
				if ms > 0 {
					ui.Text(c, fmtDur(ms)).Font(monoFamily).FontSize(fsTiny).TextColor(a.secondaryInk())
				}
			})
			if desc := str(m, "description"); desc != "" {
				uikit.Tooltip(c, ui.Text(c, desc).AlignSelf(ui.Start).FontSize(fsSmall).TextColor(a.secondaryInk()).MaxLines(2), desc)
			}
			ui.Row(c).Gap(gapInline).Wrap().AlignItems(ui.Center).Children(func() {
				if steps := int(num(m, "steps")); steps > 0 {
					label := fmt.Sprintf(locales.Text("%d 步"), steps)
					if limit := int(num(m, "maxSteps")); limit > 0 {
						label = fmt.Sprintf(locales.Text("%d/%d 步"), steps, limit)
					}
					neutralChip(c, label)
				}
				if ctxTokens := int(num(m, "contextTokens")); ctxTokens > 0 {
					neutralChip(c, locales.Text("上下文 ")+fmtK(ctxTokens))
				}
				hit, miss := int(num(m, "cacheHitTokens")), int(num(m, "cacheMissTokens"))
				if hit+miss > 0 {
					neutralChip(c, fmt.Sprintf(locales.Text("缓存 %d%%"), hit*100/(hit+miss)))
				}
				in, out := int(num(m, "inputTokens")), int(num(m, "outputTokens"))
				if in+out > 0 {
					neutralChip(c, fmt.Sprintf("↑%s ↓%s", fmtK(in), fmtK(out)))
				}
				ui.Spacer(c)
				summary, errText := str(m, "summary"), str(m, "error")
				hasReport := (status != "running" && summary != "") || errText != ""
				a.subagentBlackboardButton(c, t, m)
				open := t.uiState().ToolOpen["sub-"+subID]
				reportButton, _ := buttonParts(c, "sub-report-"+subID, "", btnSecondary, true, "")
				reportButton.Label(locales.Text("子代理报告 ") + subID).Disabled(!hasReport).OnClick(func() { toggleSubagentDetail(t, subID, false) })
				reportButton.Children(func() {
					ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
						disclosureIcon(c, "file", a.secondaryInk())
						ui.Text(c, locales.Text("报告")).FontSize(uikit.ButtonFontSmall)
						caret := "chevron"
						if open {
							caret = "chevrondown"
						}
						iconGlyph(c, caret, sizeCaretInline).Shrink(0)
					})
				})
			})
			a.subagentBlackboardDetails(c, t, m)
			uikit.Reveal(c.Key("sub-report-reveal"), t.uiState().ToolOpen["sub-"+subID], func() {
				if !t.uiState().ToolOpen["sub-"+subID] {
					return
				}
				if errText := str(m, "error"); errText != "" {
					notice(c, "error", errText, a.secondaryInk())
				}
				if summary := str(m, "summary"); summary != "" {
					ui.Scroll(c.Key("sub-report-body-" + subID)).FillWidth().Height(240).Shrink(0).Clip().Children(func() { ui.Text(c, summary).FontSize(fsSmall).Selectable().LineHeight(1.6) })
				}
			})
		})
	})
}

// ---- Small string helpers used by cards ---------------------------------

func head(items []string, n int) []string {
	if len(items) > n {
		return items[:n]
	}
	return items
}

// tailLines returns the last n lines by scanning back from the end. A collapsed
// card asks for this on every frame and its output can be a whole build log, so
// splitting the entire string and joining it again was the expensive part.
func tailLines(s string, n int) string {
	if n <= 0 {
		return ""
	}
	end := len(s)
	for end > 0 && s[end-1] == '\n' {
		end--
	}
	if end == 0 {
		return ""
	}
	start := strings.LastIndexByte(s[:end], '\n') + 1
	for kept := 1; kept < n && start > 0; kept++ {
		previous := strings.LastIndexByte(s[:start-1], '\n')
		if previous < 0 {
			return s[:end]
		}
		start = previous + 1
	}
	return s[start:end]
}

func shortBlock(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + locales.Text("\n…（内容已截断）")
	}
	return s
}

func unifiedSnippet(diff string, context int) string {
	var out []string
	// Scanned line by line instead of split into a slice of every line: this runs
	// for a collapsed card on every frame, and only the +/- rows are kept.
	for rest := diff; rest != ""; {
		line := rest
		if i := strings.IndexByte(rest, '\n'); i >= 0 {
			line, rest = rest[:i], rest[i+1:]
		} else {
			rest = ""
		}
		line = strings.TrimSuffix(line, "\r")
		if strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") || strings.HasPrefix(line, "@@") {
			out = append(out, line)
		}
	}
	if len(out) == 0 {
		return diff
	}
	return strings.Join(out, "\n")
}

var _ = markdown.View

func isFoldableCommand(m map[string]any) bool {
	if str(m, "role") != "tool_call" && str(m, "toolCallId") == "" {
		return false
	}
	name := str(m, "name")
	if name == "" {
		name = str(m, "toolName")
	}
	// Membership depends on tool kind, never execution state: pending and
	// streaming calls enter the same group they keep after completion.
	return toolKind(name) == "command"
}

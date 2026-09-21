// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	toolerrors "ally-dev/internal/tools/shared"
)

type toolResult struct {
	OK        bool   `json:"ok"`
	Data      any    `json:"data,omitempty"`
	Error     string `json:"error,omitempty"`
	ErrorCode string `json:"errorCode,omitempty"`
	Details   any    `json:"details,omitempty"`
	// Warnings carries non-fatal notices (e.g. ignored unknown tool
	// arguments) on successful results; error results never set it.
	Warnings []string `json:"warnings,omitempty"`
}

// codedToolError wraps err with a stable tool error code. It delegates to the
// shared internal/tools/shared package so tool packages can produce the same
// envelope without depending on App.
func codedToolError(code string, err error) error {
	return toolerrors.New(code, err)
}

func toolErrorCode(err error) string {
	return toolerrors.Code(err)
}

func toolErrorResult(err error) toolResult {
	if err == nil {
		return toolResult{OK: true}
	}
	return toolResult{OK: false, Error: err.Error(), ErrorCode: toolErrorCode(err), Details: toolerrors.Details(err)}
}

// toolResultSummary returns a short human-readable summary for a tool result.
func toolResultSummary(name string, result *toolResult) string {
	if result == nil || result.Data == nil {
		return ""
	}
	switch name {
	case "read", "remote_read":
		data, _ := json.Marshal(result.Data)
		var r BatchReadResult
		if json.Unmarshal(data, &r) == nil {
			failed := 0
			for _, file := range r.Files {
				if file.Error != "" {
					failed++
				}
			}
			if failed > 0 {
				return fmt.Sprintf("%d files, %d failed", len(r.Files), failed)
			}
			return fmt.Sprintf("%d files", len(r.Files))
		}
	case "edit", "remote_edit":
		var r MultiEditResult
		if decodeToolData(result.Data, &r) {
			return fmt.Sprintf("%d files · +%d -%d", r.FileCount, r.AddedLines, r.RemovedLines)
		}
	case "replace_exact", "replace_lines":
		data, _ := json.Marshal(result.Data)
		var r EditResult
		if json.Unmarshal(data, &r) == nil {
			parts := []string{}
			if r.AddedLines > 0 {
				parts = append(parts, "+"+strconv.Itoa(r.AddedLines))
			}
			if r.RemovedLines > 0 {
				parts = append(parts, "-"+strconv.Itoa(r.RemovedLines))
			}
			return strings.Join(parts, " ")
		}
	case "grep":
		data, _ := json.Marshal(result.Data)
		var r GrepResult
		if json.Unmarshal(data, &r) == nil {
			if r.Hits > 0 && r.Hits != r.MatchedLines {
				return fmt.Sprintf("%d hits in %d matching lines", r.Hits, r.MatchedLines)
			}
			return fmt.Sprintf("%d matches", r.MatchedLines)
		}
	case "command", "remote_run_command":
		data, _ := json.Marshal(result.Data)
		var r CommandResult
		if json.Unmarshal(data, &r) == nil {
			if r.ExitCode == 0 {
				return fmt.Sprintf("exit 0 (%dms)", r.DurationMS)
			}
			return fmt.Sprintf("exit %d (%dms)", r.ExitCode, r.DurationMS)
		}
	case "service":
		switch typed := result.Data.(type) {
		case ServiceReadResult:
			return fmt.Sprintf("read %d bytes (status %s)", typed.ReturnedBytes, typed.Status)
		case ServiceListToolResult:
			return fmt.Sprintf("%d service(s), %d active", len(typed.Services), typed.ActiveCount)
		case ServiceInfo:
			if typed.PID > 0 {
				return fmt.Sprintf("%s (pid %d)", typed.Status, typed.PID)
			}
			return typed.Status
		default:
			var r ServiceInfo
			if decodeToolData(result.Data, &r) {
				if r.PID > 0 {
					return fmt.Sprintf("%s (pid %d)", r.Status, r.PID)
				}
				return r.Status
			}
		}
	case "wait":
		var r WaitResult
		if decodeToolData(result.Data, &r) {
			return fmt.Sprintf("waited %ds", r.RequestedSeconds)
		}
	case "ask":
		var r AskResult
		if decodeToolData(result.Data, &r) {
			return fmt.Sprintf("answered %d questions", len(r.Answers))
		}
	case "scheduled_task":
		var r ScheduledTaskToolResult
		if decodeToolData(result.Data, &r) {
			if r.Task != nil {
				return "created " + r.Task.Name
			}
			if r.Deleted != "" {
				return "deleted " + r.Deleted
			}
			return fmt.Sprintf("%d scheduled tasks", r.Count)
		}
	case "list_files":
		data, _ := json.Marshal(result.Data)
		var r ListFilesResult
		if json.Unmarshal(data, &r) == nil {
			return fmt.Sprintf("%d entries", r.Count)
		}
	case "calculate":
		var r CalculateResult
		if decodeToolData(result.Data, &r) {
			return "= " + r.Text
		}
	case "create", "remote_create_file":
		return "created"
	case "delete":
		var r DeleteResult
		if decodeToolData(result.Data, &r) {
			if r.Kind != "" {
				return fmt.Sprintf("deleted %s", r.Kind)
			}
		}
		return "deleted"
	case "remote_delete_path":
		return "deleted"
	}
	return ""
}

// compactToolResultForModel returns the model-facing payload for a tool
// result: a tag block for the covered tools, the JSON envelope otherwise.
// Per-tool compaction runs first; the envelope warnings (unknown-argument
// notices and validation notes) are then re-injected so they survive
// even when a tool's compact payload drops the top-level envelope fields.
func compactToolResultForModel(name string, result toolResult, fullJSON string) string {
	if !result.OK || result.Data == nil {
		return fullJSON
	}
	compact := compactToolDataForModel(name, result, fullJSON)
	if len(result.Warnings) > 0 {
		compact = injectEnvelopeWarnings(compact, result.Warnings)
	}
	return compact
}

// injectEnvelopeWarnings parses a model-facing tool-result JSON and adds the
// envelope warnings under data.warnings, merging with any warnings the tool
// already produced. Falls back to appending a plain-text note when parsing
// fails, so the notice always reaches the model.
func injectEnvelopeWarnings(compactJSON string, warnings []string) string {
	var decoded struct {
		OK   bool `json:"ok"`
		Data struct {
			Warnings []string `json:"warnings"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(compactJSON), &decoded); err != nil || !decoded.OK {
		return compactJSON + "\nwarnings: " + strings.Join(warnings, " | ")
	}
	merged := append(decoded.Data.Warnings, warnings...)
	var generic map[string]any
	if err := json.Unmarshal([]byte(compactJSON), &generic); err != nil {
		return compactJSON + "\nwarnings: " + strings.Join(warnings, " | ")
	}
	if dataMap, ok := generic["data"].(map[string]any); ok {
		dataMap["warnings"] = merged
	}
	raw, err := json.Marshal(generic)
	if err != nil {
		return compactJSON + "\nwarnings: " + strings.Join(warnings, " | ")
	}
	return string(raw)
}

func compactToolDataForModel(name string, result toolResult, fullJSON string) string {
	// MCP tool output is third-party text with no producer-side cap; clamp it
	// to the built-in bound so one runaway server cannot flood model context.
	if strings.HasPrefix(name, "mcp__") {
		return renderMcpResultForModel(result, fullJSON)
	}
	switch name {
	case "read", "remote_read":
		var r BatchReadResult
		if !decodeToolData(result.Data, &r) {
			return fullJSON
		}
		return renderReadResultForModel(r)
	case "list_files":
		// The UI explorer consumes the full FileEntry structs (name/size/
		// modTime/symlink); the model only needs the tree shape: a plain path
		// list (trailing slash marks directories) inside a <ally-files> block.
		var r ListFilesResult
		if !decodeToolData(result.Data, &r) {
			return fullJSON
		}
		return renderListFilesResultForModel(r)
	case "edit", "remote_edit":
		var r MultiEditResult
		if !decodeToolData(result.Data, &r) {
			return fullJSON
		}
		return renderMultiEditResultForModel(r)
	case "replace_exact", "replace_lines", "create":
		var r EditResult
		if !decodeToolData(result.Data, &r) {
			return fullJSON
		}
		return renderEditResultForModel(r)
	case "command", "remote_run_command":
		var r CommandResult
		if !decodeToolData(result.Data, &r) {
			return fullJSON
		}
		return renderCommandResultForModel(r)
	case "service":
		// start/stop return ServiceInfo, list returns ServiceListToolResult,
		// read returns ServiceReadResult. Discriminate by concrete type (not
		// JSON reshaping, which would silently succeed for overlapping fields).
		// All three render as tag blocks; list keeps one summary row per service.
		switch typed := result.Data.(type) {
		case ServiceReadResult:
			return renderServiceReadResultForModel(typed)
		case ServiceListToolResult:
			return renderServiceListResultForModel(typed)
		case ServiceInfo:
			return renderServiceInfoResultForModel(typed)
		}
		// Fallback for any unexpected shape: try legacy ServiceInfo decode.
		var r ServiceInfo
		if !decodeToolData(result.Data, &r) {
			return fullJSON
		}
		return renderServiceInfoResultForModel(r)
	case "delete":
		var r DeleteResult
		if !decodeToolData(result.Data, &r) {
			return fullJSON
		}
		return renderDeleteResultForModel(r)
	case "grep":
		var r GrepResult
		if !decodeToolData(result.Data, &r) {
			return fullJSON
		}
		return renderGrepResultForModel(r)
	case "http_request":
		var r HTTPRequestToolResult
		if !decodeToolData(result.Data, &r) {
			return fullJSON
		}
		return renderHTTPResultForModel(r)
	case "web_fetch":
		var r WebFetchResult
		if !decodeToolData(result.Data, &r) {
			return fullJSON
		}
		return renderWebFetchResultForModel(r)
	case "calculate":
		var r CalculateResult
		if !decodeToolData(result.Data, &r) {
			return fullJSON
		}
		return renderCalculateResultForModel(r)
	case "scheduled_task":
		var r ScheduledTaskToolResult
		if !decodeToolData(result.Data, &r) {
			return fullJSON
		}
		return renderScheduledTaskResultForModel(r)
	case "wait":
		var r WaitResult
		if !decodeToolData(result.Data, &r) {
			return fullJSON
		}
		return renderWaitResultForModel(r)
	case "ask":
		var r AskResult
		if !decodeToolData(result.Data, &r) {
			return fullJSON
		}
		return renderAskResultForModel(r)
	case "plan":
		var r struct {
			Todos    []TodoEntry `json:"todos"`
			Revision int64       `json:"revision"`
			Message  string      `json:"message"`
		}
		if !decodeToolData(result.Data, &r) {
			return fullJSON
		}
		return renderPlanResultForModel(r.Todos, r.Revision, r.Message)
	case "subagent", "agent_delegate":
		var r AgentDelegateResult
		if !decodeToolData(result.Data, &r) {
			return fullJSON
		}
		return renderSubagentResultForModel(r)
	default:
		return fullJSON
	}
}

// renderCalculateResultForModel renders a calculate result as a self-closing
// <ally-calc> tag: the expression plus its value. Self-closing (no body), so
// there is no closing-marker collision surface; the expression is attr-escaped.
func renderCalculateResultForModel(r CalculateResult) string {
	return `<ally-calc expression="` + attrEscape(r.Expression) + `" value="` + attrEscape(r.Text) + `"/>`
}

// renderServiceListResultForModel renders a service list as an <ally-svcs>
// block: one summary row per service (id, optional name, status, pid/exit,
// buffered output size, error). Output tails stay out on purpose — the model
// must action=read a specific id to inspect output.
func renderServiceListResultForModel(r ServiceListToolResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<ally-svcs active="%d" max="%d">`+"\n", r.ActiveCount, r.MaxActive)
	for _, s := range r.Services {
		row := "  " + s.ID
		if s.Name != "" {
			row += " (" + s.Name + ")"
		}
		row += " " + s.Status
		if s.PID > 0 {
			row += fmt.Sprintf(" pid=%d", s.PID)
		}
		if s.ExitCode != 0 {
			row += fmt.Sprintf(" exit=%d", s.ExitCode)
		}
		if s.OutputBytes > 0 {
			row += fmt.Sprintf(" bytes=%d", s.OutputBytes)
		}
		if s.OutputTruncated {
			row += " truncated"
		}
		if s.Command != "" {
			row += " cmd=" + strconv.Quote(s.Command)
		}
		if s.Error != "" {
			row += " error=" + strconv.Quote(s.Error)
		}
		b.WriteString(neutralizeRowBreaks(row) + "\n")
	}
	if len(r.Services) == 0 {
		b.WriteString("  (none)\n")
	}
	return strings.TrimRight(escapeClosingMarker(b.String(), "</ally-svcs"), "\n") + "\n</ally-svcs>"
}

// scheduledTaskScheduleSpec compacts a schedule struct into one attribute
// value ("cron:0 3 * * *", "every:30m", "at:2026-01-02T15:04:05Z").
func scheduledTaskScheduleSpec(s ScheduledTaskSchedule) string {
	switch s.Type {
	case "cron":
		return "cron:" + s.Cron
	case "every":
		return "every:" + s.Every
	case "at":
		return "at:" + s.At
	}
	return s.Type
}

// renderScheduledTaskRow renders one list row: id, name, schedule, last
// status, run count, and optional running/max-steps/timeout flags.
func renderScheduledTaskRow(t ScheduledTaskToolView) string {
	row := fmt.Sprintf("  %s: name=%s schedule=%s status=%s runs=%d", t.ID, strconv.Quote(t.Name), strconv.Quote(scheduledTaskScheduleSpec(t.Schedule)), strconv.Quote(t.LastStatus), t.RunCount)
	if t.Running {
		row += " running"
	}
	if t.MaxSteps > 0 {
		row += fmt.Sprintf(" max-steps=%d", t.MaxSteps)
	}
	if t.TimeoutSeconds > 0 {
		row += fmt.Sprintf(" timeout=%ds", t.TimeoutSeconds)
	}
	return neutralizeRowBreaks(row)
}

// renderScheduledTaskResultForModel renders the scheduled_task tool result:
// a self-closing <ally-task deleted> for deletes, a single <ally-task> block
// (instruction/command as the body) for creates, and an <ally-tasks> list
// block with one row per task otherwise.
func renderScheduledTaskResultForModel(r ScheduledTaskToolResult) string {
	switch {
	case r.Deleted != "":
		out := fmt.Sprintf(`<ally-task deleted=%s`, strconv.Quote(r.Deleted))
		if r.Count > 0 {
			out += fmt.Sprintf(` count="%d"`, r.Count)
		}
		return out + "/>"
	case r.Task != nil:
		t := r.Task
		var b strings.Builder
		fmt.Fprintf(&b, `<ally-task id=%s name=%s schedule=%s status=%s runs="%d"`, strconv.Quote(t.ID), strconv.Quote(t.Name), strconv.Quote(scheduledTaskScheduleSpec(t.Schedule)), strconv.Quote(t.LastStatus), t.RunCount)
		if t.Running {
			b.WriteString(` running`)
		}
		if t.MaxSteps > 0 {
			fmt.Fprintf(&b, ` max-steps="%d"`, t.MaxSteps)
		}
		if t.TimeoutSeconds > 0 {
			fmt.Fprintf(&b, ` timeout="%ds"`, t.TimeoutSeconds)
		}
		body, kind := t.Instruction, "instruction"
		if body == "" {
			body, kind = t.Command, "command"
		}
		fmt.Fprintf(&b, ` kind=%s`, strconv.Quote(kind))
		if strings.TrimSpace(body) == "" {
			return b.String() + "/>"
		}
		b.WriteString(">\n")
		b.WriteString(escapeClosingMarker(neutralizeRowBreaks(body), "</ally-task"))
		return b.String() + "\n</ally-task>"
	default:
		var b strings.Builder
		fmt.Fprintf(&b, `<ally-tasks count="%d"`, r.Count)
		if r.Truncated {
			b.WriteString(` truncated`)
		}
		b.WriteString(">\n")
		for _, t := range r.Tasks {
			b.WriteString(renderScheduledTaskRow(t) + "\n")
		}
		if len(r.Tasks) == 0 {
			b.WriteString("  (none)\n")
		}
		return strings.TrimRight(escapeClosingMarker(b.String(), "</ally-tasks"), "\n") + "\n</ally-tasks>"
	}
}

// renderWaitResultForModel renders a wait result as <ally-wait> with the
// requested/elapsed durations and the reason as the body (single row).
func renderWaitResultForModel(r WaitResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<ally-wait seconds="%d" elapsed-ms="%d"`, r.RequestedSeconds, r.ElapsedMS)
	if !r.Completed {
		b.WriteString(` interrupted`)
	}
	b.WriteByte('>')
	if r.Reason != "" {
		b.WriteString(escapeClosingMarker(neutralizeRowBreaks(r.Reason), "</ally-wait"))
	}
	return b.String() + "</ally-wait>"
}

// renderAskResultForModel renders resolved ask answers as an <ally-ask>
// block: one indented "qid: question" row per answer, then one indented
// "selected: label" row per chosen option.
func renderAskResultForModel(r AskResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<ally-ask id=%s>`+"\n", strconv.Quote(r.AskID))
	for _, ans := range r.Answers {
		fmt.Fprintf(&b, "  %s: %s\n", neutralizeRowBreaks(ans.QuestionID), neutralizeRowBreaks(ans.Question))
		for _, sel := range ans.Selections {
			row := "  selected: " + sel.Label
			if sel.Recommended {
				row += " (recommended)"
			}
			if sel.Custom {
				row += " [custom]"
			}
			if sel.Description != "" {
				row += " — " + sel.Description
			}
			b.WriteString(neutralizeRowBreaks(row) + "\n")
		}
	}
	if len(r.Answers) == 0 {
		b.WriteString("  (no answers)\n")
	}
	return strings.TrimRight(escapeClosingMarker(b.String(), "</ally-ask"), "\n") + "\n</ally-ask>"
}

// renderPlanResultForModel renders the plan tool result as an <ally-plan>
// block: the outcome message, then one indented "[status] title" row per
// todo entry.
func renderPlanResultForModel(todos []TodoEntry, revision int64, message string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<ally-plan revision="%d">`+"\n", revision)
	if message != "" {
		b.WriteString(neutralizeRowBreaks(message) + "\n")
	}
	for _, td := range todos {
		fmt.Fprintf(&b, "  [%s] %s\n", td.Status, neutralizeRowBreaks(td.Title))
	}
	if len(todos) == 0 {
		b.WriteString("  (empty)\n")
	}
	return strings.TrimRight(escapeClosingMarker(b.String(), "</ally-plan"), "\n") + "\n</ally-plan>"
}

// renderSubagentResultForModel renders a sub-agent result as an
// <ally-subagent> block: identity/status attrs on the opening tag, the
// summary as the body, then one "read:"/"edited:" row per touched file.
func renderSubagentResultForModel(r AgentDelegateResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<ally-subagent id=%s role=%s status=%s steps="%d" model=%s`, strconv.Quote(r.AgentID), strconv.Quote(r.Role), strconv.Quote(r.Status), r.Steps, strconv.Quote(r.Model))
	if r.Description != "" {
		fmt.Fprintf(&b, ` description=%s`, strconv.Quote(r.Description))
	}
	if r.Error != "" {
		fmt.Fprintf(&b, ` error=%s`, strconv.Quote(r.Error))
	}
	body := strings.TrimSpace(r.Summary)
	if body == "" && len(r.FilesRead) == 0 && len(r.FilesEdited) == 0 {
		return b.String() + "/>"
	}
	b.WriteString(">\n")
	rows := make([]string, 0, 1+len(r.FilesRead)+len(r.FilesEdited))
	if body != "" {
		rows = append(rows, body) // summary keeps its own line breaks
	}
	for _, f := range r.FilesRead {
		rows = append(rows, "read: "+neutralizeRowBreaks(f))
	}
	for _, f := range r.FilesEdited {
		rows = append(rows, "edited: "+neutralizeRowBreaks(f))
	}
	b.WriteString(escapeClosingMarker(strings.Join(rows, "\n"), "</ally-subagent"))
	return b.String() + "\n</ally-subagent>"
}

// renderGrepResultForModel renders a grep result as a <ally-grep> tag block:
// lines mode groups matches by file — one bare path row per file group, then
// one indented "line: text" row per matching line (the colon is present only
// when the text preview survived the byte budget) — while count mode keeps
// one "path: count=N" row per file. Exact totals, the explicit mode, and the
// paging metadata ride on the opening tag. The caps are identical to the
// previous JSON envelope — file groups, total lines, and the global
// text-preview byte budget — with reduction notes as trailing lines. A path
// with leading whitespace would blur the indent-based row grammar, so in that
// case the whole block falls back to flat "path:line: text" rows; any row,
// path, count, or note that would forge the closing marker is escaped.
func renderGrepResultForModel(r GrepResult) string {
	mode := r.Mode
	if mode == "" {
		// Legacy results predate outputMode and default to lines today.
		mode = "lines"
	}
	lineHits := r.LineHits
	fileCounts := r.FileCounts
	nextOffset := r.NextOffset
	var notes []string
	switch mode {
	case "lines":
		sampledLines := 0
		for _, fh := range lineHits {
			sampledLines += len(fh.Lines)
		}
		if len(lineHits) > maxModelGrepFileGroups {
			lineHits = lineHits[:maxModelGrepFileGroups]
			notes = append(notes, "[some file groups omitted]")
		}
		totalLines := 0
		for _, fh := range lineHits {
			totalLines += len(fh.Lines)
		}
		if totalLines > maxModelGrepMatches {
			capped := make([]GrepFileMatch, 0, len(lineHits))
			remaining := maxModelGrepMatches
			keptLines := 0
			for _, fh := range lineHits {
				if remaining <= 0 {
					break
				}
				lines := fh.Lines
				if len(lines) > remaining {
					lines = lines[:remaining]
				}
				remaining -= len(lines)
				keptLines += len(lines)
				// Trim the previews with the kept line numbers; legacy
				// results without texts keep none.
				texts := fh.Texts
				if len(texts) > len(lines) {
					texts = texts[:len(lines)]
				}
				capped = append(capped, GrepFileMatch{Path: fh.Path, Lines: lines, Texts: texts})
			}
			lineHits = capped
			notes = append(notes, fmt.Sprintf("[%d of %d matching lines shown]", keptLines, totalLines))
		}
		// Text previews carry a global byte budget on top of the line count:
		// once exceeded, later entries keep their line numbers but drop the
		// text (read tool covers them when needed).
		var textsReduced bool
		lineHits, textsReduced = capGrepLineTexts(lineHits, maxModelGrepTextBytes)
		if textsReduced {
			notes = append(notes, "[some text previews dropped for budget; use read for full lines]")
		}
		// nextOffset resumes after the tool-sampled page; when compaction
		// dropped lines (file-group or line cap), resume after the lines the
		// model actually saw so paging never skips unseen lines.
		modelLines := 0
		for _, fh := range lineHits {
			modelLines += len(fh.Lines)
		}
		if dropped := sampledLines - modelLines; dropped > 0 && nextOffset > dropped {
			nextOffset -= dropped
		}
	case "count_matches":
		// Deliberately no cap and no reduction note: the page already carries at
		// most the grep tool's pagination width, and every row in it fits model
		// context. Trimming rows here would drop files the tool counted while
		// next-offset still resumes after the whole page, so the model would skip
		// those files with no notice.
	default:
		// Unknown mode: render a minimal block rather than a malformed one.
		return fmt.Sprintf("<ally-grep mode=\"%s\">unknown mode</ally-grep>", mode)
	}
	var b strings.Builder
	// The mode is spelled out on every block: a count row ("path: count=N")
	// must never be mistaken for a lines row whose text preview was dropped by
	// the byte budget ("path:12"), and a bare flag is only visible to a reader
	// that already knows which mode it asked for.
	fmt.Fprintf(&b, `<ally-grep mode="%s"`, mode)
	fmt.Fprintf(&b, ` matched="%d"`, r.MatchedLines)
	if r.Hits != r.MatchedLines {
		fmt.Fprintf(&b, ` hits="%d"`, r.Hits)
	}
	fmt.Fprintf(&b, ` files="%d"`, r.Files)
	if r.Truncated {
		b.WriteString(` truncated`)
	}
	if nextOffset > 0 {
		fmt.Fprintf(&b, ` next-offset="%d"`, nextOffset)
	}
	if !r.StatsExact {
		b.WriteString(` stats-approx`)
	}
	if r.OffsetExhausted {
		b.WriteString(` offset-exhausted`)
	}
	b.WriteString(">\n")
	// Leading-whitespace paths would break the indent grammar (a match row is
	// recognized by its two-space indent); render the whole block flat instead.
	flatRows := false
	for _, fh := range lineHits {
		if strings.HasPrefix(fh.Path, " ") || strings.HasPrefix(fh.Path, "\t") {
			flatRows = true
			break
		}
	}
	for _, fh := range lineHits {
		path := neutralizeRowBreaks(fh.Path)
		if !flatRows {
			b.WriteString(path + "\n")
		}
		for i, line := range fh.Lines {
			if flatRows {
				fmt.Fprintf(&b, "%s:%d", path, line)
			} else {
				fmt.Fprintf(&b, "  %d", line)
			}
			if i < len(fh.Texts) && fh.Texts[i] != "" {
				b.WriteString(": " + neutralizeRowBreaks(fh.Texts[i]))
			}
			b.WriteByte('\n')
		}
	}
	for _, fc := range fileCounts {
		fmt.Fprintf(&b, "%s: count=%d\n", neutralizeRowBreaks(fc.Path), fc.Count)
	}
	for _, s := range r.Skipped {
		notes = append(notes, "[skipped: "+s+"]")
	}
	for _, w := range r.Warnings {
		notes = append(notes, "warning: "+w)
	}
	for _, n := range notes {
		b.WriteString(n + "\n")
	}
	// Rows, counts, and notes carry outside-injection text (paths, match
	// previews, skip reasons); a literal closing marker in them would forge
	// the block boundary, so the marker shape is escaped (readable but inert).
	return strings.TrimRight(escapeClosingMarker(b.String(), "</ally-grep"), "\n") + "\n</ally-grep>"
}

// renderHTTPResultForModel renders an HTTP response as an <ally-http> block: the
// body drops in verbatim (no JSON escaping) and status rides on the opening
// tag. The request url is not echoed (the model just sent it) and statusText
// is inferable from status, so both are dropped. A closing-marker shape in
// the body is escaped (see escapeClosingMarker) — no fallback path.
func renderHTTPResultForModel(r HTTPRequestToolResult) string {
	body, reduced := compactTextForModel(r.Body, compactTextSpec{limit: maxModelWebOutput})
	if body == "" {
		// A body-less response (binary or JSON-only shape) still needs a payload;
		// the substituted preview passes the same model-side cap as the body path.
		substitute := r.JSONPreview
		if substitute == "" && r.JSON != nil {
			if raw, err := json.Marshal(r.JSON); err == nil {
				substitute = string(raw)
			}
		}
		if substitute != "" {
			var subReduced bool
			body, subReduced = compactTextForModel(substitute, compactTextSpec{limit: maxModelWebOutput})
			reduced = reduced || subReduced
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<ally-http status="%d"`, r.Status)
	if r.FinalURL != "" && r.FinalURL != r.URL {
		b.WriteString(` final-url="` + attrEscape(r.FinalURL) + `"`)
	}
	if r.ContentType != "" {
		b.WriteString(` content-type="` + attrEscape(r.ContentType) + `"`)
	}
	if r.Truncated || reduced {
		b.WriteString(` truncated`)
	}
	b.WriteString(">\n")
	b.WriteString(escapeClosingMarker(body, "</ally-http"))
	b.WriteString("\n</ally-http>")
	return b.String()
}

// renderWebFetchResultForModel renders a readable-page fetch as a <ally-fetch>
// block: the article text drops in verbatim and links append as trailing
// "link: text <url>" rows. Closing-marker shapes in the text or link rows are
// escaped (see escapeClosingMarker) — no fallback path.
func renderWebFetchResultForModel(r WebFetchResult) string {
	text, reduced := compactTextForModel(r.Text, compactTextSpec{limit: maxModelWebOutput})
	var b strings.Builder
	fmt.Fprintf(&b, `<ally-fetch status="%d"`, r.Status)
	if r.Title != "" {
		b.WriteString(` title="` + attrEscape(r.Title) + `"`)
	}
	if r.FinalURL != "" && r.FinalURL != r.URL {
		b.WriteString(` final-url="` + attrEscape(r.FinalURL) + `"`)
	}
	if r.Truncated || reduced {
		b.WriteString(` truncated`)
	}
	b.WriteString(">\n")
	b.WriteString(strings.TrimRight(text, "\n"))
	for _, link := range r.Links {
		b.WriteString("\nlink: " + neutralizeRowBreaks(link.Text) + " <" + neutralizeRowBreaks(link.URL) + ">")
	}
	b.WriteString("\n</ally-fetch>")
	// Escape only the content portion; the renderer's own closing tag above must
	// stay literal, so strip it before escaping and append it back.
	return escapeClosingMarker(strings.TrimSuffix(b.String(), "\n</ally-fetch>"), "</ally-fetch") + "\n</ally-fetch>"
}

// renderReadResultForModel renders a batch read result as <ally-file> tag blocks
// instead of a JSON envelope: metadata rides on the opening tag's attributes
// and the line-numbered body drops in verbatim, so the payload pays no JSON
// escaping tax (every newline/quote/tab doubles inside a JSON string) and no
// repeated envelope keys. The line numbers come from the read pipeline
// (ContentFormat "line_numbers") and pass through untouched — the edit
// lineRange contract depends on them. A closing-marker shape in the body is
// escaped (see escapeClosingMarker): the text stays readable and the block
// boundary stays unforgeable.
func renderReadResultForModel(r BatchReadResult) string {
	// The omission is decided on the returned payload, so it only ever claims that
	// THIS path/range reads the same as before — a change elsewhere in the file
	// keeps the version attribute (and the version really did change). Promising
	// the whole file or the version is unchanged would be a claim the cache cannot
	// back up.
	const reusedNote = "[Content omitted: this exact path/range reads the same as what you were already sent earlier in this conversation, so it is not repeated. The version attribute above is the current file version — safe to reuse for edit. Ask for a narrower line range if you need the text itself.]"
	if len(r.Files) == 0 {
		return "(no readable files returned)"
	}
	var b strings.Builder
	injected := false
	for _, f := range r.Files {
		body := f.Content
		if f.Reused {
			body = reusedNote
		}
		body = escapeClosingMarker(body, "</ally-file")
		b.WriteString(`<ally-file path="` + attrEscape(f.Path) + `"`)
		if f.Error != "" {
			code := f.ErrorCode
			if code == "" {
				code = "E_READ_FAILED"
			}
			message := escapeClosingMarker(f.Error, "</ally-file")
			b.WriteString(` error="` + attrEscape(code) + `">` + message + "</ally-file>\n")
			continue
		}
		b.WriteString(` version="` + attrEscape(f.Version) + `"`)
		if f.StartLine > 0 && f.EndLine > 0 {
			fmt.Fprintf(&b, ` lines="%d-%d"`, f.StartLine, f.EndLine)
		}
		if f.TotalLines > 0 {
			fmt.Fprintf(&b, ` total="%d"`, f.TotalLines)
		}
		if f.Truncated {
			b.WriteString(` truncated`)
		}
		if f.Reused {
			b.WriteString(` reused`)
		}
		if f.DataURL != "" {
			b.WriteString(` image="sent as image input in following message"`)
			injected = true
		}
		b.WriteString(">\n")
		b.WriteString(body)
		b.WriteString("\n</ally-file>\n")
	}
	if injected {
		b.WriteString("Image file(s) injected as user image input.\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderCommandResultForModel renders a command result as a <ally-cmd> block with
// the merged stdout/stderr verbatim in the body. The command text and cwd are
// deliberately not echoed — the model just sent them in the tool call one
// message earlier, and re-quoting them only doubles long commands in history.
// A zero exit code is the implicit success path (attribute omitted); non-zero
// exit, timeout, truncation, the spilled-output pointer, and a promotion to a
// background service (`promoted-to-service`) stay as attributes. A
// closing-marker shape in the output is escaped (see escapeClosingMarker).
func renderCommandResultForModel(r CommandResult) string {
	body := "(no output)"
	if r.Output != "" {
		body = strings.TrimRight(r.Output, "\n")
	}
	body = escapeClosingMarker(body, "</ally-cmd")
	var b strings.Builder
	b.WriteString("<ally-cmd")
	if r.ExitCode != 0 {
		fmt.Fprintf(&b, ` exit="%d"`, r.ExitCode)
	}
	if r.TimedOut {
		b.WriteString(` timed-out`)
	}
	if r.PromotedToService {
		b.WriteString(` promoted-to-service`)
	}
	if r.Truncated {
		b.WriteString(` truncated`)
	}
	if r.OutputFilePath != "" {
		b.WriteString(` full="` + attrEscape(r.OutputFilePath) + `"`)
	}
	b.WriteString(">\n")
	b.WriteString(body)
	b.WriteString("\n</ally-cmd>")
	return b.String()
}

func renderListFilesResultForModel(r ListFilesResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<ally-files count="%d"`, r.Count)
	if r.Truncated {
		b.WriteString(` truncated`)
	}
	b.WriteString(">\n")
	for _, entry := range r.Entries {
		if entry.MoreFiles > 0 {
			// Per-directory overflow placeholder: same wording as the
			// workspace map legend.
			fmt.Fprintf(&b, "+%d more files\n", entry.MoreFiles)
			continue
		}
		b.WriteString(neutralizeRowBreaks(entry.Path))
		if entry.Dir {
			b.WriteByte('/')
		}
		b.WriteByte('\n')
	}
	switch {
	case r.Count == 0:
		b.WriteString("Empty listing: the directory is empty or everything was filtered as hidden/ignored. Use includeHidden/includeIgnored to widen it.\n")
	case r.Truncated:
		b.WriteString("Entry limit reached; narrow path or raise limit to see the rest.\n")
	}
	return strings.TrimRight(escapeClosingMarker(b.String(), "</ally-files"), "\n") + "\n</ally-files>"
}

// renderMultiEditResultForModel renders a batch edit as one self-closing
// <ally-edit path version/> line per file (the version is the edit contract) plus
// trailing summary/validation/warning lines for the batch as a whole.
func renderMultiEditResultForModel(r MultiEditResult) string {
	var b strings.Builder
	for _, file := range r.Files {
		b.WriteString(`<ally-edit path="` + attrEscape(file.Path) + `" version="` + attrEscape(file.Version) + `"/>` + "\n")
	}
	b.WriteString("all files above are current in your context; reuse each version for the next edit — no need to Read them back.\n")
	if r.Summary != "" {
		b.WriteString("edit summary: " + neutralizeClosingMarkers(r.Summary) + "\n")
	}
	if r.Validation != "" {
		b.WriteString("edit validation: " + neutralizeClosingMarkers(r.Validation) + "\n")
	}
	for _, w := range r.Warnings {
		b.WriteString("warning: " + neutralizeClosingMarkers(w) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderEditResultForModel renders a single-file edit/create as one
// self-closing tag: version is the edit contract, summary/validation ride on
// attributes, warnings append as trailing lines.
// editNoRereadNote 告诉模型编辑后不必回读验证：磁盘内容就是它提交的 newText
// 的应用结果，下一次编辑直接用本次返回的 version。Claude 系模型编辑后习惯性
// 整文件复读，去重机制因内容已变拦不住，整文件会再进一遍上下文。
const editNoRereadNote = "file content is current in your context (your edit was applied verbatim); reuse the version above for the next edit — no need to Read this file back."

func renderEditResultForModel(r EditResult) string {
	var b strings.Builder
	b.WriteString(`<ally-edit path="` + attrEscape(r.Path) + `" version="` + attrEscape(r.Version) + `"`)
	if r.Summary != "" {
		b.WriteString(` summary="` + attrEscape(r.Summary) + `"`)
	}
	if r.Created != nil {
		if *r.Created {
			b.WriteString(` created="true"`)
		} else {
			b.WriteString(` created="false"`)
		}
	}
	if len(r.CreatedDirs) > 0 {
		b.WriteString(` dirs="` + attrEscape(strings.Join(r.CreatedDirs, ",")) + `"`)
	}
	if r.Validation != "" {
		b.WriteString(` validation="` + attrEscape(r.Validation) + `"`)
	}
	b.WriteString("/>")
	b.WriteString("\n" + editNoRereadNote)
	for _, w := range r.Warnings {
		b.WriteString("\nwarning: " + neutralizeClosingMarkers(w))
	}
	return b.String()
}

func renderDeleteResultForModel(r DeleteResult) string {
	var b strings.Builder
	b.WriteString(`<ally-deleted path="` + attrEscape(r.Path) + `"`)
	if r.Kind != "" {
		b.WriteString(` kind="` + attrEscape(r.Kind) + `"`)
	}
	if r.RemovedFiles > 0 {
		fmt.Fprintf(&b, ` files="%d"`, r.RemovedFiles)
	}
	if r.RemovedDirs > 0 {
		fmt.Fprintf(&b, ` dirs="%d"`, r.RemovedDirs)
	}
	b.WriteString("/>")
	return b.String()
}

// renderServiceReadResultForModel renders a service read as a tag block; the
// output body drops in verbatim and byte accounting rides on attributes. The
// model-side tail clamp stays at 8 KiB; a closing-marker shape in the output
// is escaped (see escapeClosingMarker).
func renderServiceReadResultForModel(r ServiceReadResult) string {
	const maxReadOutputForModel = 8 * 1024
	output := r.Output
	reducedFrom := 0
	if len(output) > maxReadOutputForModel {
		reducedFrom = len(output)
		output = tailString(output, maxReadOutputForModel)
	}
	output = escapeClosingMarker(output, "</ally-svc-read")
	var b strings.Builder
	b.WriteString(`<ally-svc-read id="` + attrEscape(r.ID) + `"`)
	if r.Status != "" {
		b.WriteString(` status="` + attrEscape(r.Status) + `"`)
	}
	fmt.Fprintf(&b, ` returned="%d" buffer="%d" total="%d"`, r.ReturnedBytes, r.BufferBytes, r.TotalBytes)
	if r.Truncated {
		b.WriteString(` truncated`)
	}
	if r.FromByte > 0 {
		fmt.Fprintf(&b, ` from-byte="%d"`, r.FromByte)
	}
	if reducedFrom > 0 {
		fmt.Fprintf(&b, ` reduced-from="%d"`, reducedFrom)
	}
	b.WriteString(">\n")
	b.WriteString(strings.TrimRight(output, "\n"))
	b.WriteString("\n</ally-svc-read>")
	return b.String()
}

// renderServiceInfoResultForModel renders a start/stop result. The command
// and cwd are not echoed (the model just sent them) and RFC3339 timestamps
// are noise; only identity, liveness, and the output tail reach the model.
// A closing-marker shape in the tail is escaped (see escapeClosingMarker).
func renderServiceInfoResultForModel(r ServiceInfo) string {
	outputTail := tailString(r.OutputTail, 4*1024)
	reduced := len(outputTail) < len(r.OutputTail)
	outputTail = escapeClosingMarker(outputTail, "</ally-svc>")
	var b strings.Builder
	b.WriteString(`<ally-svc id="` + attrEscape(r.ID) + `"`)
	if r.Name != "" {
		b.WriteString(` name="` + attrEscape(r.Name) + `"`)
	}
	if r.Status != "" {
		b.WriteString(` status="` + attrEscape(r.Status) + `"`)
	}
	if r.PID > 0 {
		fmt.Fprintf(&b, ` pid="%d"`, r.PID)
	}
	if r.ExitCode != 0 {
		fmt.Fprintf(&b, ` exit="%d"`, r.ExitCode)
	}
	if r.Error != "" {
		b.WriteString(` error="` + attrEscape(r.Error) + `"`)
	}
	if reduced {
		fmt.Fprintf(&b, ` reduced-from="%d"`, len(r.OutputTail))
	}
	b.WriteString(">\n")
	if strings.TrimSpace(outputTail) != "" {
		b.WriteString(strings.TrimRight(outputTail, "\n"))
		b.WriteString("\n")
	}
	b.WriteString("</ally-svc>")
	return b.String()
}

// mcpTruncationNote explains a capped third-party output to the model.
const mcpTruncationNote = "Output exceeded the model-context safety cap and was truncated (head+tail kept). Narrow the tool arguments or paginate via the server if it supports it."

func renderMcpResultForModel(result toolResult, fullJSON string) string {
	var r struct {
		Output string `json:"output"`
	}
	if !decodeToolData(result.Data, &r) {
		return fullJSON
	}
	capped, reduced := compactTextForModel(r.Output, compactTextSpec{limit: maxModelToolOutput, head: modelToolHeadBytes, tail: modelToolTailBytes})
	capped = escapeClosingMarker(capped, "</ally-mcp")
	var b strings.Builder
	b.WriteString("<ally-mcp")
	if reduced {
		b.WriteString(` truncated`)
	}
	b.WriteString(">\n")
	b.WriteString(capped)
	if reduced {
		b.WriteString("\n" + mcpTruncationNote)
	}
	b.WriteString("\n</ally-mcp>")
	return b.String()
}

// escapeClosingMarker neutralizes a literal closing-marker shape inside a tag
// body: external text (file contents, command output, web pages, service
// logs) may carry "</ally-cmd" etc., and only this renderer's own closing tag
// may announce the block end. Escaping the "<" of the marker shape keeps the
// text readable while making the boundary unforgeable — one rule for every
// renderer, no fallback paths.
func escapeClosingMarker(body, marker string) string {
	if !strings.Contains(body, marker) {
		return body
	}
	return strings.ReplaceAll(body, marker, "&lt;"+marker[1:])
}

// neutralizeClosingMarkers makes closing-marker-shaped text inert in the free
// text lines of a payload that has no enclosing block (edit summaries,
// validation output, warnings). Those lines carry outside text — compiler and
// linter output can contain anything — and a literal "</ally-cmd" there would read
// as another block's boundary. Only the marker shape is escaped, so ordinary
// "<" text stays readable.
func neutralizeClosingMarkers(v string) string {
	if !strings.Contains(v, "</") {
		return v
	}
	return strings.ReplaceAll(v, "</", "&lt;/")
}

// neutralizeRowBreaks makes a value safe inside a one-item-per-line row (grep's
// "path:line: text", a <ally-files> entry, a "link: text <url>" row): a path or link
// carrying a line break would read as another row, and Unix filenames may well
// contain '\n'. Line breaks become the literal \n escape models already read;
// forging the block boundary is a different matter, handled by the
// closing-marker fallbacks.
func neutralizeRowBreaks(v string) string {
	if !strings.ContainsAny(v, "\n\r") {
		return v
	}
	return strings.NewReplacer("\n", `\n`, "\r", `\r`).Replace(v)
}

// attrEscape makes a value safe inside a double-quoted tag attribute; quotes,
// angle brackets, and raw newlines would otherwise break the boundary.
func attrEscape(v string) string {
	if !strings.ContainsAny(v, `"<>`+"\n\r") {
		return v
	}
	return strings.NewReplacer(
		`"`, "&quot;",
		"<", "&lt;",
		">", "&gt;",
		"\n", "&#10;",
		"\r", "&#13;",
	).Replace(v)
}

func decodeToolData(data any, target any) bool {
	raw, err := json.Marshal(data)
	if err != nil {
		return false
	}
	return json.Unmarshal(raw, target) == nil
}

func marshalToolResultOrFallback(result toolResult, fallback string) string {
	raw, err := json.Marshal(result)
	if err != nil {
		return fallback
	}
	return string(raw)
}

func compactTextForModel(output string, spec compactTextSpec) (string, bool) {
	limit := spec.limit
	if limit <= 0 || len(output) <= limit {
		return output, false
	}
	runes := []rune(output)
	if len(runes) <= limit {
		return output, false
	}
	head := spec.head
	if head <= 0 || head >= limit {
		head = limit / 3
	}
	if head > len(runes) {
		head = len(runes)
	}
	tail := spec.tail
	if tail <= 0 || head+tail > limit {
		tail = limit - head
	}
	if tail > len(runes)-head {
		tail = len(runes) - head
	}
	omitted := len(runes) - head - tail
	return string(runes[:head]) +
		fmt.Sprintf("\n\n[... %d characters omitted from model context ...]\n\n", omitted) +
		string(runes[len(runes)-tail:]), true
}

// compactTextSpec 声明 compactTextForModel 的切分策略：limit 是总预算，
// head/tail 是可选的定制切分（0 表示按 limit/3 均分）。head/tail 作为显式
// 参数传入，通用函数不再按 "limit 恰好等于某个魔数" 推断调用方身份。
type compactTextSpec struct {
	limit int
	head  int
	tail  int
}

// capGrepLineTexts enforces a global byte budget on the per-line text
// previews of a grep lines result. Entries keep their path and line numbers;
// once the budget is spent, later texts are dropped so the model still sees
// where the matches are (and can read the lines it cares about). reduced
// reports whether any text was dropped.
func capGrepLineTexts(hits []GrepFileMatch, budget int) ([]GrepFileMatch, bool) {
	remaining := budget
	reduced := false
	for i := range hits {
		kept := make([]string, 0, len(hits[i].Texts))
		for _, text := range hits[i].Texts {
			if remaining <= 0 {
				reduced = true
				kept = append(kept, "")
				continue
			}
			remaining -= len(text)
			kept = append(kept, text)
		}
		if reduced {
			hits[i].Texts = kept
		}
	}
	return hits, reduced
}

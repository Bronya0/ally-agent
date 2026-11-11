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
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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
	// Recovery source is private to the model's tool message. Never serialize
	// it into UI/API results or concatenate it into the displayed error.
	modelRead *BatchReadResult
}

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
	result := toolResult{OK: false, Error: err.Error(), ErrorCode: toolErrorCode(err), Details: toolerrors.Details(err)}
	var recovery *editRecoveryError
	if errors.As(err, &recovery) {
		result.modelRead = &recovery.read
	}
	return result
}

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
			exit := "exit 0"
			if r.ExitCode != 0 {
				exit = fmt.Sprintf("exit %d", r.ExitCode)
			}
			if dur := FormatDurationMS(r.DurationMS); dur != "" {
				return exit + " (" + dur + ")"
			}
			return exit
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
	case "screenshot":
		// One tool, two result shapes: a list result names its shape with a
		// windows array, a capture result with source.
		var list ListWindowsResult
		if decodeToolData(result.Data, &list) && list.Windows != nil {
			if list.Truncated {
				return fmt.Sprintf("%d of %d windows", len(list.Windows), list.Total)
			}
			return fmt.Sprintf("%d windows", list.Total)
		}
		var r ScreenshotResult
		if decodeToolData(result.Data, &r) {
			switch r.Source {
			case "window":
				return fmt.Sprintf("%dx%d · %q", r.Width, r.Height, r.Title)
			case "region":
				return fmt.Sprintf("%dx%d · region", r.Width, r.Height)
			default:
				return fmt.Sprintf("%dx%d · display %d", r.Width, r.Height, r.Display)
			}
		}
	case "calculate":
		var r CalculateResult
		if decodeToolData(result.Data, &r) {
			return "= " + r.Text
		}
	case "create", "remote_create_file":
		return "created"
	case "delete", "remote_delete_path":
		var r DeletePathsResult
		if decodeToolData(result.Data, &r) {
			if r.FailedCount > 0 {
				return fmt.Sprintf("%d paths, %d failed", len(r.Paths), r.FailedCount)
			}
			if r.AbsentCount == len(r.Paths) {
				return "already absent"
			}
			if len(r.Paths) > 1 {
				return fmt.Sprintf("%d paths", len(r.Paths))
			}
		}
		return "deleted"
	}
	return ""
}

func compactToolResultForModel(name string, result toolResult, fullJSON string) string {

	if !result.OK {
		body := result.Error
		if result.Details != nil {
			raw, _ := json.Marshal(result.Details)
			body += "\ndetails: " + string(raw)
		}
		body, reduced := compactTextForModel(body, compactTextSpec{limit: maxModelToolOutput, head: modelToolHeadBytes, tail: modelToolTailBytes})
		header := "// " + normalizeToolName(name) + " | status=error"
		if result.ErrorCode != "" {
			header += " | error=" + strconv.Quote(result.ErrorCode)
		}
		if reduced {
			header += " | truncated"
		}
		out := header + "\n\n" + body
		if result.modelRead != nil {
			out += "\n\nCurrent source for recovery; no extra read is needed:\n\n" + renderReadResultForModel(*result.modelRead)
		}
		return out
	}
	out := compactToolDataForModel(name, result, fullJSON)
	if !strings.HasPrefix(out, "// ") {
		out = "// " + normalizeToolName(name) + " | status=ok\n\n" + out
	}
	if len(result.Warnings) > 0 {
		i := strings.IndexByte(out, '\n')
		if i < 0 {
			i = len(out)
		}
		raw, _ := json.Marshal(result.Warnings)
		out = out[:i] + " | warnings=" + string(raw) + out[i:]
	}
	return out

}

func compactToolDataForModel(name string, result toolResult, fullJSON string) string {
	if name == "image_generate" || name == "image_edit" {
		var image GeneratedImage
		if decodeToolData(result.Data, &image) {
			return generatedImageHistoryMarker + image.Ref + fmt.Sprintf(" | size=%dx%d | saved=true | sources=%s", image.Width, image.Height, strconv.Quote(strings.Join(image.Sources, ", ")))
		}
	}
	// MCP tool output is third-party text with no producer-side cap; clamp it
	// to the built-in bound so one runaway server cannot flood model context.
	if isMcpToolFunctionName(name) {
		return renderMcpResultForModel(result, fullJSON)
	}
	switch name {
	case "render_html":
		var r struct {
			Rendered bool
			Length   int
			Title    string
		}
		if decodeToolData(result.Data, &r) {
			return fmt.Sprintf("// render_html | rendered=%t | length=%d | title=%q", r.Rendered, r.Length, r.Title)
		}
		return modelFallbackResult(name, result.Data, fullJSON)
	case "suggest":
		var r struct{ Items []string }
		if decodeToolData(result.Data, &r) {
			return modelResultBody(fmt.Sprintf("// suggest | count=%d", len(r.Items)), strings.Join(r.Items, "\n"))
		}
		return modelFallbackResult(name, result.Data, fullJSON)
	case "remote_transfer":
		var r RemoteTransferResult
		if decodeToolData(result.Data, &r) {
			return modelResultBody(fmt.Sprintf("// remote_transfer | action=%q | target=%q | local=%q | remote=%q | bytes=%d | files=%d | dirs=%d", r.Action, r.Target, r.LocalPath, r.RemotePath, r.TotalBytes, r.FileCount, r.DirCount), r.Summary)
		}
		return modelFallbackResult(name, result.Data, fullJSON)
	case "read", "remote_read":
		var r BatchReadResult
		if !decodeToolData(result.Data, &r) {
			return modelFallbackResult(name, result.Data, fullJSON)
		}
		return renderReadResultForModel(r)
	case "list_files":
		// The UI explorer consumes the full FileEntry structs (name/size/
		// modTime/symlink); the model only needs the tree shape: a plain path
		// list (trailing slash marks directories) below the result metadata.
		var r ListFilesResult
		if !decodeToolData(result.Data, &r) {
			return modelFallbackResult(name, result.Data, fullJSON)
		}
		return renderListFilesResultForModel(r)
	case "screenshot":
		// One tool, two result shapes. A capture result carries source; its
		// image travels as an image part injected right after the tool
		// results (collectReadImages), so the model-facing block keeps only
		// the metadata and megabytes of base64 never enter the message
		// history as text. A list result is small and bounded — plain JSON.
		var r ScreenshotResult
		if !decodeToolData(result.Data, &r) || r.Source == "" {
			return modelFallbackResult(name, result.Data, fullJSON)
		}
		return renderScreenshotResultForModel(r)
	case "edit", "remote_edit":
		var r MultiEditResult
		if !decodeToolData(result.Data, &r) {
			return modelFallbackResult(name, result.Data, fullJSON)
		}
		return renderMultiEditResultForModel(r)
	case "replace_exact", "replace_lines", "create", "remote_create_file":
		var r EditResult
		if !decodeToolData(result.Data, &r) {
			return modelFallbackResult(name, result.Data, fullJSON)
		}
		return renderEditResultForModel(name, r)
	case "command", "remote_run_command":
		var r CommandResult
		if !decodeToolData(result.Data, &r) {
			return modelFallbackResult(name, result.Data, fullJSON)
		}
		return renderCommandResultForModel(r)
	case "service":
		// start/stop return ServiceInfo, list returns ServiceListToolResult,
		// read returns ServiceReadResult. Discriminate by concrete type (not
		// JSON reshaping, which would silently succeed for overlapping fields).
		// All three render as plain text; list keeps one summary row per service.
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
			return modelFallbackResult(name, result.Data, fullJSON)
		}
		return renderServiceInfoResultForModel(r)
	case "delete", "remote_delete_path":
		var r DeletePathsResult
		if !decodeToolData(result.Data, &r) {
			return modelFallbackResult(name, result.Data, fullJSON)
		}
		return renderDeleteResultForModel(r)
	case "grep":
		var r GrepResult
		if !decodeToolData(result.Data, &r) {
			return modelFallbackResult(name, result.Data, fullJSON)
		}
		return renderGrepResultForModel(r)
	case "http_request":
		var r HTTPRequestToolResult
		if !decodeToolData(result.Data, &r) {
			return modelFallbackResult(name, result.Data, fullJSON)
		}
		return renderHTTPResultForModel(r)
	case "web_fetch":
		var r WebFetchResult
		if !decodeToolData(result.Data, &r) {
			return modelFallbackResult(name, result.Data, fullJSON)
		}
		return renderWebFetchResultForModel(r)
	case "calculate":
		var r CalculateResult
		if !decodeToolData(result.Data, &r) {
			return modelFallbackResult(name, result.Data, fullJSON)
		}
		return renderCalculateResultForModel(r)
	case "scheduled_task":
		var r ScheduledTaskToolResult
		if !decodeToolData(result.Data, &r) {
			return modelFallbackResult(name, result.Data, fullJSON)
		}
		return renderScheduledTaskResultForModel(r)
	case "wait":
		var r WaitResult
		if !decodeToolData(result.Data, &r) {
			return modelFallbackResult(name, result.Data, fullJSON)
		}
		return renderWaitResultForModel(r)
	case "ask":
		var r AskResult
		if !decodeToolData(result.Data, &r) {
			return modelFallbackResult(name, result.Data, fullJSON)
		}
		return renderAskResultForModel(r)
	case "plan":
		var r struct {
			Plan     []PlanStep `json:"plan"`
			Revision int64      `json:"revision"`
			Action   string     `json:"action"`
		}
		if !decodeToolData(result.Data, &r) {
			return modelFallbackResult(name, result.Data, fullJSON)
		}
		return renderPlanResultForModel(r.Action, r.Plan, r.Revision)
	case "subagent", "agent_delegate":
		var r AgentDelegateResult
		if !decodeToolData(result.Data, &r) {
			return modelFallbackResult(name, result.Data, fullJSON)
		}
		return renderSubagentResultForModel(r)
	case "skill":
		var r struct {
			Loaded  bool   `json:"loaded"`
			Content string `json:"content"`
		}
		if decodeToolData(result.Data, &r) && r.Loaded && r.Content != "" {
			return r.Content
		}
		return modelFallbackResult(name, result.Data, fullJSON)
	default:
		return modelFallbackResult(name, result.Data, fullJSON)
	}
}

func renderCalculateResultForModel(r CalculateResult) string {
	return "// calculate | expression=" + strconv.Quote(r.Expression) + " | value=" + strconv.Quote(r.Text)
}

func renderServiceListResultForModel(r ServiceListToolResult) string {
	header := fmt.Sprintf("// service | action=list | workspace-active=%d | global-active=%d | global-max=%d", r.ActiveCount, r.GlobalActiveCount, r.MaxActive)
	var rows []string
	for _, v := range r.Services {
		row := fmt.Sprintf("%s | status=%s | pid=%d", v.ID, v.Status, v.PID)
		if v.ExitCode != 0 {
			row += fmt.Sprintf(" | exit=%d", v.ExitCode)
		}
		if v.Name != "" {
			row += " | name=" + strconv.Quote(v.Name)
		}
		if v.Description != "" {
			row += " | description=" + strconv.Quote(v.Description)
		}
		if v.Command != "" {
			row += " | command=" + strconv.Quote(v.Command)
		}
		if v.Error != "" {
			row += " | error=" + strconv.Quote(v.Error)
		}
		if v.Promoted {
			row += fmt.Sprintf(" | promoted=true timeout-seconds=%d", v.TimeoutSeconds)
		}
		if v.OutputTruncated {
			row += " | truncated"
		}
		rows = append(rows, row)
	}
	return modelResultBody(header, strings.Join(rows, "\n"))
}

func scheduledTaskScheduleSpec(s ScheduledTaskSchedule) string {
	switch s.Type {
	case "cron":
		return "cron:" + s.Cron
	case "interval":
		return "every:" + s.Every
	case "once":
		return "at:" + s.At
	}
	return s.Type
}

func renderScheduledTaskRow(t ScheduledTaskToolView) string {
	row := fmt.Sprintf("  %s: name=%s schedule=%s status=%s runs=%d", t.ID, strconv.Quote(t.Name), strconv.Quote(scheduledTaskScheduleSpec(t.Schedule)), strconv.Quote(t.LastStatus), t.RunCount)
	if t.NextRunAt > 0 {
		row += " next=" + time.UnixMilli(t.NextRunAt).UTC().Format(time.RFC3339)
	}
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

func renderScheduledTaskResultForModel(r ScheduledTaskToolResult) string {
	if r.Deleted != "" {
		return "// scheduled_task | deleted=" + strconv.Quote(r.Deleted)
	}
	tasks := r.Tasks
	if r.Task != nil {
		tasks = []ScheduledTaskToolView{*r.Task}
	}
	count := r.Count
	if r.Task != nil {
		count = 1
	}
	header := fmt.Sprintf("// scheduled_task | count=%d", count)
	if r.Truncated {
		header += " | truncated"
	}
	var rows []string
	for _, t := range tasks {
		row := renderScheduledTaskRow(t)
		if t.LastRunAt > 0 {
			row += " last=" + time.UnixMilli(t.LastRunAt).UTC().Format(time.RFC3339)
		}
		content := t.Instruction
		if content == "" {
			content = t.Command
		}
		preview, reduced := compactTextForModel(content, compactTextSpec{limit: 1024})
		if preview != "" {
			row += " content=" + strconv.Quote(preview)
		}
		if reduced {
			row += " content-truncated"
		}
		rows = append(rows, row)
	}
	return modelResultBody(header, strings.Join(rows, "\n"))
}

func renderWaitResultForModel(r WaitResult) string {
	header := fmt.Sprintf("// wait | seconds=%d | elapsed-ms=%d", r.RequestedSeconds, r.ElapsedMS)
	if !r.Completed {
		header += " | interrupted"
	}
	return modelResultBody(header, r.Reason)
}

func renderAskResultForModel(r AskResult) string {
	var rows []string
	for _, a := range r.Answers {
		rows = append(rows, a.QuestionID+": "+a.Question)
		if len(a.Selections) == 0 {
			rows = append(rows, "(no answers)")
		}
		for _, v := range a.Selections {
			row := fmt.Sprintf("selected: %s | recommended=%t | custom=%t", strconv.Quote(v.Label), v.Recommended, v.Custom)
			if v.Description != "" {
				row += " | description=" + strconv.Quote(v.Description)
			}
			rows = append(rows, row)
		}
	}
	if len(r.Answers) == 0 {
		rows = append(rows, "(no answers)")
	}
	return modelResultBody("// ask | id="+strconv.Quote(r.AskID), strings.Join(rows, "\n"))
}

const planModelRowLimit = 30

const (
	planStateEmpty   = "empty"
	planStateRunning = "running"
	planStateClosed  = "closed"
)

func planStateOf(plan []PlanStep) string {
	if len(plan) == 0 {
		return planStateEmpty
	}
	if firstPlanStepWithStatus(plan, "in_progress") >= 0 {
		return planStateRunning
	}
	return planStateClosed
}

func renderPlanResultForModel(action string, plan []PlanStep, revision int64) string {
	done := 0
	for _, td := range plan {
		if td.Status == "done" {
			done++
		}
	}
	open := fmt.Sprintf("// plan | action=%s | revision=%d | state=%s | done=%d | total=%d", strconv.Quote(action), revision, strconv.Quote(planStateOf(plan)), done, len(plan))
	if len(plan) == 0 {
		return open
	}
	current := firstPlanStepWithStatus(plan, "in_progress")
	start, end := planModelWindow(len(plan), current)
	var b strings.Builder
	b.WriteString(open + "\n\n")
	if start > 0 {
		fmt.Fprintf(&b, "  (%d earlier steps omitted)\n", start)
	}
	for i := start; i < end; i++ {
		fmt.Fprintf(&b, "  %d [%s] %s\n", i+1, plan[i].Status, neutralizeRowBreaks(plan[i].Title))
	}
	if end < len(plan) {
		fmt.Fprintf(&b, "  (%d later steps omitted)\n", len(plan)-end)
	}
	if current >= 0 {
		// The call that moves the plan on is the one the model is most likely to
		// need next, and the title is the same either way: reporting the current
		// step marks it, reporting a later one marks everything through it.
		fmt.Fprintf(&b, "When %s is done: send {\"finish\":%s} to mark it — and every step before it — done.\n",
			strconv.Quote(plan[current].Title), strconv.Quote(plan[current].Title))
	} else {
		// The plan is closed: nothing is current, so there is no step left to
		// report. Say that in words as well as in the state attribute — the
		// absence of the hint line is the same absence a window that left the
		// current step out of view shows. A plan ends only by the work reaching
		// its last step, so a closed plan carries no pending row.
		b.WriteString("This plan is closed: every step is done.\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func planModelWindow(total, current int) (start, end int) {
	if total <= planModelRowLimit {
		return 0, total
	}
	start = total - planModelRowLimit
	if current >= 0 && current < start {
		start = current
	}
	end = start + planModelRowLimit
	if end > total {
		end = total
	}
	return start, end
}

func renderSubagentResultForModel(r AgentDelegateResult) string {
	header := fmt.Sprintf("// subagent | id=%s | status=%s | steps=%d | model=%s", strconv.Quote(r.AgentID), strconv.Quote(r.Status), r.Steps, strconv.Quote(r.Model))
	header += " | role=" + strconv.Quote(r.Role)
	if r.Name != "" {
		header += " | name=" + strconv.Quote(r.Name)
	}
	if r.StopReason != "" {
		header += " | stop-reason=" + strconv.Quote(r.StopReason)
	}
	body := r.Summary
	if r.Description != "" {
		body = "Description: " + r.Description + "\n" + body
	}
	if r.Error != "" {
		body += "\nError: " + r.Error
	}
	for _, v := range r.FilesRead {
		body += "\nread: " + v
	}
	for _, v := range r.FilesEdited {
		body += "\nedited: " + v
	}
	return modelResultBody(header, body)
}

func renderGrepResultForModel(r GrepResult) string {
	mode := r.Mode
	if mode == "" {
		// Legacy results predate outputMode and default to lines today.
		mode = "lines"
	}
	lineHits := r.LineHits
	fileCounts := r.FileCounts
	nextOffset := r.NextOffset
	truncated := r.Truncated
	// Flat rows repeat paths; count those repetitions against the path budget.
	flatRows := false
	for _, fh := range lineHits {
		if strings.HasPrefix(fh.Path, " ") || strings.HasPrefix(fh.Path, "\t") {
			flatRows = true
			break
		}
	}
	var notes []string
	switch mode {
	case "lines":
		sampledLines := 0
		for _, fh := range lineHits {
			sampledLines += len(fh.Lines)
		}
		capped := make([]GrepFileMatch, 0, len(lineHits))
		keptLines, pathBytes := 0, 0
		pathLimited := false
		for _, fh := range lineHits {
			keep := min(len(fh.Lines), maxModelGrepMatches-keptLines)
			if keep <= 0 {
				break
			}
			pathCost := len(neutralizeRowBreaks(fh.Path)) + 1
			if flatRows {
				keep = min(keep, max(0, (maxModelGrepPathBytes-pathBytes)/pathCost))
			} else if pathBytes+pathCost > maxModelGrepPathBytes {
				keep = 0
			}
			if keep == 0 && keptLines == 0 {
				// Never shorten a usable path or return a page that cannot advance.
				keep = min(len(fh.Lines), maxModelGrepMatches)
				if flatRows {
					keep = 1
				}
				notes = append(notes, "[one oversized path retained intact to allow progress]")
			}
			if keep == 0 {
				pathLimited = true
				break
			}
			texts := fh.Texts
			if len(texts) > keep {
				texts = texts[:keep]
			}
			capped = append(capped, GrepFileMatch{Path: fh.Path, Lines: fh.Lines[:keep], Texts: texts})
			keptLines += keep
			if flatRows {
				pathCost *= keep
			}
			pathBytes += pathCost
			if keep < len(fh.Lines) {
				pathLimited = flatRows && keptLines < maxModelGrepMatches
				break
			}
		}
		lineHits = capped
		if keptLines < sampledLines {
			truncated = true
			notes = append(notes, fmt.Sprintf("[%d of %d matching lines shown]", keptLines, sampledLines))
			if pathLimited {
				notes = append(notes, "[path byte budget reached; use next-offset to continue]")
			}
			pageOffset := r.Offset
			if r.NextOffset >= sampledLines && r.NextOffset > 0 {
				pageOffset = r.NextOffset - sampledLines
			}
			nextOffset = pageOffset + keptLines
		}
		// Text previews carry a global byte budget on top of the line count:
		// once exceeded, later entries keep their line numbers but drop the
		// text (read tool covers them when needed).
		var textsReduced bool
		lineHits, textsReduced = capGrepLineTexts(lineHits, maxModelGrepTextBytes)
		if textsReduced {
			notes = append(notes, "[some text previews dropped for budget; use read for full lines]")
		}
	case "count_matches":
		// Deliberately no cap and no reduction note: the page already carries at
		// most the grep tool's pagination width, and every row in it fits model
		// context. Trimming rows here would drop files the tool counted while
		// next-offset still resumes after the whole page, so the model would skip
		// those files with no notice.
	default:
		// Unknown mode: render a minimal block rather than a malformed one.
		return "// grep | status=error | error=unknown-mode"
	}
	var b strings.Builder
	// The mode is spelled out on every block: a count row ("path: count=N")
	// must never be mistaken for a lines row whose text preview was dropped by
	// the byte budget ("path:12"), and a bare flag is only visible to a reader
	// that already knows which mode it asked for.
	fmt.Fprintf(&b, "// grep | mode=%s", mode)
	fmt.Fprintf(&b, ` matched="%d"`, r.MatchedLines)
	if r.Hits != r.MatchedLines {
		fmt.Fprintf(&b, ` hits="%d"`, r.Hits)
	}
	fmt.Fprintf(&b, ` files="%d"`, r.Files)
	if truncated {
		b.WriteString(` truncated`)
	}
	if nextOffset > 0 {
		fmt.Fprintf(&b, ` next-offset="%d"`, nextOffset)
	}
	if r.OffsetExhausted {
		b.WriteString(` offset-exhausted`)
	}
	b.WriteString("\n\n")
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
	if nextOffset > 0 {
		fmt.Fprintf(&b, "[Truncated. Use offset=%d to continue.]\n", nextOffset)
	}
	return b.String()
}

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
	fmt.Fprintf(&b, "// http_request | status=%d | duration=%dms", r.Status, r.DurationMS)
	if r.FinalURL != "" && r.FinalURL != r.URL {
		b.WriteString(" | final-url=" + strconv.Quote(r.FinalURL))
	}
	if r.ContentType != "" {
		b.WriteString(" | content-type=" + strconv.Quote(r.ContentType))
	}
	if r.SavedPath != "" {
		b.WriteString(" | saved=" + strconv.Quote(r.SavedPath))
	}
	if r.BytesRead > 0 {
		fmt.Fprintf(&b, ` bytes="%d"`, r.BytesRead)
	}
	if r.BodyEncoding != "" && r.BodyEncoding != "text" {
		b.WriteString(" | encoding=" + strconv.Quote(r.BodyEncoding))
	}
	if r.Truncated || reduced {
		b.WriteString(` truncated`)
	}
	b.WriteString("\n\n")
	// Keep headers that determine the next action, with a shared byte budget.
	remaining := 4 * 1024
	for _, name := range []string{"Location", "Retry-After", "Link", "ETag", "Last-Modified", "WWW-Authenticate"} {
		for key, value := range r.Headers {
			if remaining <= 0 || !strings.EqualFold(key, name) || value == "" {
				continue
			}
			if len(value) > remaining {
				value = utf8Prefix(value, remaining) + " [truncated]"
			}
			remaining -= len(value)
			b.WriteString("header " + name + ": " + neutralizeRowBreaks(value) + "\n")
			break
		}
	}
	b.WriteString(body)

	return b.String()
}

func renderWebFetchResultForModel(r WebFetchResult) string {
	body, reduced := compactTextForModel(r.Text, compactTextSpec{limit: maxModelWebOutput})
	header := fmt.Sprintf("// web_fetch | status=%d | duration=%dms", r.Status, r.DurationMS)
	if r.Title != "" {
		header += " | title=" + strconv.Quote(r.Title)
	}
	if r.FinalURL != "" && r.FinalURL != r.URL {
		header += " | final-url=" + strconv.Quote(r.FinalURL)
	}
	if r.Truncated || reduced {
		header += " | truncated"
	}
	for _, link := range r.Links {
		body += "\nlink: " + neutralizeRowBreaks(link.Text) + " <" + neutralizeRowBreaks(link.URL) + ">"
	}
	return header + "\n\n" + body
}

func renderReadResultForModel(r BatchReadResult) string {
	var blocks []string
	for _, f := range r.Missing {
		blocks = append(blocks, "// read | path="+strconv.Quote(f.Path)+" | error="+strconv.Quote(f.ErrorCode))
	}
	for _, f := range r.Files {
		header := "// read | path=" + strconv.Quote(f.Path)
		if f.Error != "" {
			code := f.ErrorCode
			if code == "" {
				code = "E_READ_FAILED"
			}
			blocks = append(blocks, header+" | error="+strconv.Quote(code)+"\n\n"+f.Error)
			continue
		}
		if f.Version != "" {
			header += " | version=" + strconv.Quote(f.Version)
		}
		if !f.Editable && f.Version == "" {
			header += " | editable=false"
		}
		if !f.Reused && f.StartLine > 0 && f.EndLine > 0 {
			header += fmt.Sprintf(" | lines=%d-%d", f.StartLine, f.EndLine)
		}
		if !f.Reused && f.TotalLines > 0 {
			header += fmt.Sprintf(" | total=%d", f.TotalLines)
		}
		if f.Truncated {
			header += " | truncated"
		}
		if len(f.TruncatedLines) > 0 {
			header += " | truncated-lines=" + strconv.Quote(formatTruncatedLines(f.TruncatedLines, f.TruncatedLinesOmitted))
		}
		if f.Reused {
			header += " | reused"
			blocks = append(blocks, header)
			continue
		}
		if f.DataURL != "" {
			header += " | image=sent-as-image-input-in-following-message"
		}
		blocks = append(blocks, header+"\n\n"+f.Content)
	}
	if len(blocks) == 0 {
		return "// read | files=0"
	}
	return strings.Join(blocks, "\n\n")
}

func formatTruncatedLines(lines []int, omitted bool) string {
	parts := make([]string, 0, len(lines)+1)
	for _, line := range lines {
		parts = append(parts, strconv.Itoa(line))
	}
	if omitted {
		parts = append(parts, "...")
	}
	return strings.Join(parts, ",")
}

func renderCommandResultForModel(r CommandResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "// command | exit=%d | duration=%dms", r.ExitCode, r.DurationMS)
	if r.TimedOut {
		b.WriteString(" | timed-out")
	}
	if r.Cancelled {
		b.WriteString(" | cancelled")
	}
	if r.PromotedToService {
		b.WriteString(" | promoted-to-service")
	}
	if r.Truncated {
		b.WriteString(" | truncated")
	}
	output := r.Output
	if output == "" {
		output = "(no output)"
	}
	output, reduced := compactTextForModel(output, compactTextSpec{limit: maxModelToolOutput, head: modelToolHeadBytes, tail: modelToolTailBytes})
	if reduced {
		fmt.Fprintf(&b, " | reduced-from=%d", len(r.Output))
	}
	if r.OutputFilePath != "" {
		key := "full"
		if r.OutputFileIncomplete {
			key = "partial"
		}
		fmt.Fprintf(&b, " | %s=%s", key, strconv.Quote(r.OutputFilePath))
	}
	b.WriteString("\n\n" + output)
	if r.DeniedWriteHint != "" {
		b.WriteString("\n\n" + strings.TrimRight(r.DeniedWriteHint, "\n"))
	}
	if r.OutputWarning != "" {
		b.WriteString("\n\n" + r.OutputWarning)
	}
	if !r.OutputFileIncomplete && (reduced || r.OutputFilePath != "") {
		b.WriteString("\n" + commandModelReducedNote(r.OutputFilePath))
	}
	return b.String()
}

func commandModelReducedNote(outputFilePath string) string {
	if outputFilePath != "" {
		return "[中间段已从模型上下文省略；完整输出在 " + outputFilePath + "（见首行 full 字段）。本地用 read/grep，远端用同一 target 的 remote_read 或只读命令检索；日志过大时用 tailLines，不要整读]"
	}
	return "[中间段已从模型上下文省略，未提供完整日志文件；不要仅为查看输出重复执行有副作用的命令]"
}

func renderScreenshotResultForModel(r ScreenshotResult) string {
	if r.SourceWidth <= 0 || r.SourceHeight <= 0 {
		r.SourceWidth, r.SourceHeight = r.Width, r.Height
	}
	return fmt.Sprintf("// screenshot | source=%s | window=%d | title=%s | display=%d | rect=%d,%d,%d,%d | size=%dx%d | scaled=%t | image=attached", strconv.Quote(r.Source), r.WindowID, strconv.Quote(r.Title), r.Display, r.X, r.Y, r.SourceWidth, r.SourceHeight, r.Width, r.Height, r.Scaled)
}

func renderListFilesResultForModel(r ListFilesResult) string {
	header := fmt.Sprintf("// list_files | count=%d", r.Count)
	if r.Truncated {
		header += " | truncated"
	}
	var rows []string
	for _, e := range r.Entries {
		if e.MoreFiles > 0 {
			rows = append(rows, fmt.Sprintf("+%d more files", e.MoreFiles))
			continue
		}
		path := neutralizeRowBreaks(e.Path)
		if e.Dir {
			path += "/"
		}
		rows = append(rows, path)
	}
	if r.Count == 0 {
		rows = append(rows, "No visible entries.")
	}
	if r.Truncated {
		rows = append(rows, "Entry limit reached; narrow the path to see the rest.")
	}
	return modelResultBody(header, strings.Join(rows, "\n"))
}

const editNoRereadNote = "The edits above are current in your context; do not reread solely to confirm the write. Reread before a later line-number edit if the required lines are no longer known or may have changed."

func renderMultiEditResultForModel(r MultiEditResult) string {
	var blocks []string
	for _, f := range r.Files {
		blocks = append(blocks, "// edit | path="+strconv.Quote(f.Path)+" | version="+strconv.Quote(f.Version))
	}
	body := r.Summary
	if r.Validation != "" {
		body += "\nValidation: " + r.Validation
	}
	for _, w := range r.Warnings {
		body += "\nWarning: " + w
	}
	if len(blocks) == 0 {
		blocks = append(blocks, "// edit | files=0")
	}
	if len(r.Files) > 0 {
		body += "\n" + editNoRereadNote
	}
	return modelResultBody(strings.Join(blocks, "\n\n"), strings.TrimSpace(body))
}

func renderEditResultForModel(name string, r EditResult) string {
	header := "// " + name + " | path=" + strconv.Quote(r.Path) + " | version=" + strconv.Quote(r.Version)
	if r.Created != nil {
		header += fmt.Sprintf(" | created=%t", *r.Created)
	}
	header += fmt.Sprintf(" | added=%d | removed=%d", r.AddedLines, r.RemovedLines)
	var rows []string
	if r.Summary != "" {
		rows = append(rows, r.Summary)
	}
	if r.Validation != "" {
		rows = append(rows, "Validation: "+r.Validation)
	}
	if len(r.CreatedDirs) > 0 {
		header += " | dirs=" + strconv.Quote(strings.Join(r.CreatedDirs, ","))
	}
	rows = append(rows, r.Warnings...)
	rows = append(rows, editNoRereadNote)
	return modelResultBody(header, strings.Join(rows, "\n"))
}

func renderDeleteResultForModel(r DeletePathsResult) string {
	header := fmt.Sprintf("// delete | deleted=%d | failed=%d | absent=%d", r.DeletedCount, r.FailedCount, r.AbsentCount)
	var rows []string
	for _, p := range r.Paths {
		state := "OK"
		if p.Absent {
			state = "ABSENT"
		}
		if !p.OK {
			state = "FAILED"
		}
		row := fmt.Sprintf("%s %s | kind=%s | files=%d | dirs=%d | bytes=%d", state, strconv.Quote(p.Path), strconv.Quote(p.Kind), p.RemovedFiles, p.RemovedDirs, p.RemovedBytes)
		if p.ErrorCode != "" {
			row += " [" + p.ErrorCode + "]"
		}
		if p.Error != "" {
			row += ": " + p.Error
		}
		rows = append(rows, row)
	}
	return modelResultBody(header, strings.Join(rows, "\n"))
}

func renderServiceReadResultForModel(r ServiceReadResult) string {
	output := r.Output
	reduced := len(output) > 8*1024
	if reduced {
		output = tailString(output, 8*1024)
	}
	header := fmt.Sprintf("// service | action=read | id=%s | status=%s | shown=%d | returned=%d | buffer=%d | total=%d", strconv.Quote(r.ID), strconv.Quote(r.Status), len(output), r.ReturnedBytes, r.BufferBytes, r.TotalBytes)
	if r.Truncated {
		header += " | truncated"
	}
	if reduced {
		header += " | tail-only"
	}
	if r.FromByte > 0 {
		header += fmt.Sprintf(" | from-byte=%d", r.FromByte)
	}
	if r.DeniedWriteHint != "" {
		output += "\n\n" + r.DeniedWriteHint
	}
	return modelResultBody(header, output)
}

func renderServiceInfoResultForModel(r ServiceInfo) string {
	header := fmt.Sprintf("// service | id=%s | status=%s | pid=%d", strconv.Quote(r.ID), strconv.Quote(r.Status), r.PID)
	if r.ExitCode != 0 {
		header += fmt.Sprintf(" | exit=%d", r.ExitCode)
	}
	if r.Promoted {
		header += fmt.Sprintf(" | promoted=true timeout-seconds=%d", r.TimeoutSeconds)
	}
	if r.OutputTruncated {
		header += " | truncated"
	}
	if r.Description != "" {
		header += " | description=" + strconv.Quote(r.Description)
	}
	body := tailString(r.OutputTail, 4*1024)
	if len(body) < len(r.OutputTail) {
		header += " | tail-only"
	}
	if r.Error != "" {
		body += "\n" + r.Error
	}
	if r.DeniedWriteHint != "" {
		body += "\n" + r.DeniedWriteHint
	}
	return modelResultBody(header, body)
}

const mcpTruncationNote = "Output exceeded the model-context safety cap and was truncated (head+tail kept). Narrow the tool arguments or paginate via the server if it supports it."

func renderMcpResultForModel(result toolResult, fullJSON string) string {
	var r struct{ Output string }
	if !decodeToolData(result.Data, &r) {
		return "// mcp\n\n" + fullJSON
	}
	body, reduced := compactTextForModel(r.Output, compactTextSpec{limit: maxModelToolOutput, head: modelToolHeadBytes, tail: modelToolTailBytes})
	header := "// mcp"
	if reduced {
		header += " | truncated"
		body += "\n" + mcpTruncationNote
	}
	return header + "\n\n" + body
}

var rowBreakReplacer = strings.NewReplacer("\n", `\n`, "\r", `\r`)

func neutralizeRowBreaks(v string) string {
	if !strings.ContainsAny(v, "\n\r") {
		return v
	}
	return rowBreakReplacer.Replace(v)
}

func decodeToolData(data any, target any) bool {
	raw, err := json.Marshal(data)
	if err != nil {
		return false
	}
	return json.Unmarshal(raw, target) == nil
}

func compactTextForModel(output string, spec compactTextSpec) (string, bool) {
	limit := spec.limit
	if limit <= 0 || len(output) <= limit {
		return output, false
	}
	head := spec.head
	if head <= 0 || head >= limit {
		head = limit / 3
	}
	if head > len(output) {
		head = len(output)
	}
	tail := spec.tail
	if tail <= 0 || head+tail > limit {
		tail = limit - head
	}
	if tail > len(output)-head {
		tail = len(output) - head
	}
	prefix, suffix := utf8Prefix(output, head), tailString(output, tail)
	omitted := utf8.RuneCountInString(output[len(prefix) : len(output)-len(suffix)])
	return prefix + fmt.Sprintf("\n\n[... %d characters omitted from model context ...]\n\n", omitted) + suffix, true
}

type compactTextSpec struct {
	limit int
	head  int
	tail  int
}

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

func modelResultBody(header, body string) string {
	if body == "" {
		return header
	}
	return header + "\n\n" + body
}

func modelFallbackResult(name string, data any, fallback string) string {
	if data == nil {
		return modelResultBody("// "+normalizeToolName(name)+" | status=ok", fallback)
	}
	raw, err := json.Marshal(data)
	if err == nil && string(raw) != "null" {
		fallback = string(raw)
	}
	return modelResultBody("// "+normalizeToolName(name)+" | status=ok", fallback)
}

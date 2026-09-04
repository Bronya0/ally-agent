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
	case "read_file":
		data, _ := json.Marshal(result.Data)
		var r ReadFileResult
		if json.Unmarshal(data, &r) == nil {
			if r.EmptyRange || r.EndLine < r.StartLine {
				return fmt.Sprintf("0 lines (%s, %d total)", r.RangeStatus, r.TotalLines)
			}
			count := r.EndLine - r.StartLine + 1
			if r.Truncated && r.NextStartLine > 0 {
				return fmt.Sprintf("%d lines (%d-%d of %d, next %d)", count, r.StartLine, r.EndLine, r.TotalLines, r.NextStartLine)
			}
			return fmt.Sprintf("%d lines (%d-%d of %d)", count, r.StartLine, r.EndLine, r.TotalLines)
		}
	case "read", "batch_read":
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

// compactToolResultForModel returns the model-facing JSON for a tool result.
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
		return compactMcpOutputForModel(result, fullJSON)
	}
	switch name {
	case "read", "batch_read", "read_file":
		var r BatchReadResult
		if !decodeToolData(result.Data, &r) {
			return fullJSON
		}
		injected := false
		files := make([]map[string]any, 0, len(r.Files))
		for _, f := range r.Files {
			content := f.Content
			if f.Reused {
				content = "[Content omitted: this exact path/range was already returned to you earlier in this turn. version is unchanged — safe to reuse for edit. If you need the content again, re-read this same range and it will be returned in full.]"
			}
			item := map[string]any{
				"path":    f.Path,
				"content": content,
				"version": f.Version,
			}
			if f.StartLine > 0 {
				item["startLine"] = f.StartLine
			}
			if f.EndLine > 0 {
				item["endLine"] = f.EndLine
			}
			if f.TotalLines > 0 {
				item["totalLines"] = f.TotalLines
			}
			if f.Truncated {
				item["truncated"] = true
			}
			if f.Reused {
				item["reused"] = true
			}
			if f.Error != "" {
				item["error"] = f.Error
			}
			if f.ErrorCode != "" {
				item["errorCode"] = f.ErrorCode
			}
			if f.DataURL != "" {
				item["image"] = "sent as image input in following message"
				injected = true
			}
			files = append(files, item)
		}
		data := map[string]any{"files": files}
		if injected {
			data["note"] = "Image file(s) injected as user image input."
		}
		return marshalToolResultOrFallback(toolResult{OK: true, Data: data}, fullJSON)
	case "list_files":
		// The UI explorer consumes the full FileEntry structs (name/size/
		// modTime/symlink); the model only needs the tree shape. A
		// newline-joined path list with a trailing slash for directories cuts
		// a typical 200-entry listing to roughly a quarter of the tokens —
		// name duplicates the path suffix and modTime is RFC3339 noise.
		var r ListFilesResult
		if !decodeToolData(result.Data, &r) {
			return fullJSON
		}
		var b strings.Builder
		b.Grow(24 * len(r.Entries))
		for _, entry := range r.Entries {
			if entry.MoreFiles > 0 {
				// Per-directory overflow placeholder: same wording as the
				// workspace map legend.
				fmt.Fprintf(&b, "+%d more files\n", entry.MoreFiles)
				continue
			}
			b.WriteString(entry.Path)
			if entry.Dir {
				b.WriteByte('/')
			}
			b.WriteByte('\n')
		}
		data := map[string]any{"entries": strings.TrimRight(b.String(), "\n"), "count": r.Count, "truncated": r.Truncated}
		switch {
		case r.Count == 0:
			data["note"] = "Empty listing: the directory is empty or everything was filtered as hidden/ignored. Use includeHidden/includeIgnored to widen it."
		case r.Truncated:
			data["note"] = "Entry limit reached; narrow path or raise limit to see the rest."
		}
		return marshalToolResultOrFallback(toolResult{OK: true, Data: data}, fullJSON)
	case "edit", "remote_edit":
		var r MultiEditResult
		if !decodeToolData(result.Data, &r) {
			return fullJSON
		}
		files := make([]map[string]any, 0, len(r.Files))
		for _, file := range r.Files {
			files = append(files, map[string]any{
				"path":    file.Path,
				"version": file.Version,
			})
		}
		data := map[string]any{
			"files":   files,
			"summary": r.Summary,
		}
		if len(r.Warnings) > 0 {
			data["warnings"] = r.Warnings
		}
		if r.Validation != "" {
			data["validation"] = r.Validation
		}
		return marshalToolResultOrFallback(toolResult{OK: true, Data: data}, fullJSON)
	case "replace_exact", "replace_lines", "create":
		var r EditResult
		if !decodeToolData(result.Data, &r) {
			return fullJSON
		}
		data := map[string]any{
			"path":    r.Path,
			"version": r.Version,
			"summary": r.Summary,
		}
		if r.Created != nil {
			data["created"] = *r.Created
		}
		if len(r.CreatedDirs) > 0 {
			data["createdDirs"] = r.CreatedDirs
		}
		if r.Validation != "" {
			data["validation"] = r.Validation
		}
		if len(r.Warnings) > 0 {
			data["warnings"] = r.Warnings
		}
		return marshalToolResultOrFallback(toolResult{OK: true, Data: data}, fullJSON)
	case "command", "remote_run_command":
		var r CommandResult
		if !decodeToolData(result.Data, &r) {
			return fullJSON
		}
		// 模型默认只收到尾部几行 + exitCode（对齐 UI 折叠卡片），调用时
		// 传 fullOutput:true 才内联完整输出。UI 侧始终拿完整 JSON。
		var output string
		var reduced bool
		if r.FullOutput {
			output = r.Output
		} else {
			output, reduced = tailCommandOutputForModel(r.Output, r.ExitCode)
		}
		data := map[string]any{
			"command":  r.Command,
			"cwd":      r.Cwd,
			"output":   output,
			"exitCode": r.ExitCode,
		}
		if r.TimedOut {
			data["timedOut"] = true
		}
		if r.Truncated {
			data["truncated"] = true
		}
		if reduced {
			data["outputReduced"] = true
			data["reductionNote"] = "Pass fullOutput:true for full output, or read outputFilePath."
		}
		if r.OutputFilePath != "" {
			data["outputFilePath"] = r.OutputFilePath
		}
		return marshalToolResultOrFallback(toolResult{OK: true, Data: data}, fullJSON)
	case "service":
		// service now has four actions with distinct result
		// types stored in result.Data: start/stop return ServiceInfo, list
		// returns ServiceListToolResult, read returns ServiceReadResult.
		// Discriminate by concrete type (not JSON reshaping, which would
		// silently succeed for overlapping fields) so the model receives a
		// compact, action-appropriate payload.
		switch typed := result.Data.(type) {
		case ServiceReadResult:
			output := typed.Output
			const maxReadOutputForModel = 8 * 1024
			data := map[string]any{
				"id":            typed.ID,
				"status":        typed.Status,
				"returnedBytes": typed.ReturnedBytes,
				"bufferBytes":   typed.BufferBytes,
				"totalBytes":    typed.TotalBytes,
				"truncated":     typed.Truncated,
				"fromByte":      typed.FromByte,
				"output":        output,
			}
			if len(output) > maxReadOutputForModel {
				data["output"] = tailString(output, maxReadOutputForModel)
				data["outputReduced"] = true
				data["originalOutputChars"] = len(output)
				data["reductionNote"] = "Service output shortened for model context; UI received the full read."
			}
			return marshalToolResultOrFallback(toolResult{OK: true, Data: data}, fullJSON)
		case ServiceListToolResult:
			// list already omits output tails; pass through unchanged so the
			// model sees activeCount/maxActive and the per-service metadata.
			return marshalToolResultOrFallback(toolResult{OK: true, Data: typed}, fullJSON)
		case ServiceInfo:
			r := typed
			outputTail := tailString(r.OutputTail, 4*1024)
			data := map[string]any{
				"id":         r.ID,
				"name":       r.Name,
				"command":    r.Command,
				"cwd":        r.Cwd,
				"pid":        r.PID,
				"status":     r.Status,
				"startedAt":  r.StartedAt,
				"stoppedAt":  r.StoppedAt,
				"exitCode":   r.ExitCode,
				"outputTail": outputTail,
				"error":      r.Error,
			}
			if len(outputTail) < len(r.OutputTail) {
				data["outputReduced"] = true
				data["originalOutputChars"] = len(r.OutputTail)
				data["reductionNote"] = "Startup output shortened for model context; UI received the full output."
			}
			return marshalToolResultOrFallback(toolResult{OK: true, Data: data}, fullJSON)
		}
		// Fallback for any unexpected shape: try legacy ServiceInfo decode.
		var r ServiceInfo
		if !decodeToolData(result.Data, &r) {
			return fullJSON
		}
		outputTail := tailString(r.OutputTail, 4*1024)
		data := map[string]any{
			"id":         r.ID,
			"name":       r.Name,
			"command":    r.Command,
			"cwd":        r.Cwd,
			"pid":        r.PID,
			"status":     r.Status,
			"startedAt":  r.StartedAt,
			"stoppedAt":  r.StoppedAt,
			"exitCode":   r.ExitCode,
			"outputTail": outputTail,
			"error":      r.Error,
		}
		if len(outputTail) < len(r.OutputTail) {
			data["outputReduced"] = true
			data["originalOutputChars"] = len(r.OutputTail)
			data["reductionNote"] = "Startup output shortened for model context; UI received the full output."
		}
		return marshalToolResultOrFallback(toolResult{OK: true, Data: data}, fullJSON)
	case "delete":
		var r DeleteResult
		if !decodeToolData(result.Data, &r) {
			return fullJSON
		}
		data := map[string]any{
			"deleted": r.Deleted,
			"path":    r.Path,
			"kind":    r.Kind,
		}
		if r.RemovedFiles > 0 {
			data["removedFiles"] = r.RemovedFiles
		}
		if r.RemovedDirs > 0 {
			data["removedDirs"] = r.RemovedDirs
		}
		return marshalToolResultOrFallback(toolResult{OK: true, Data: data}, fullJSON)
	case "grep":
		var r GrepResult
		if !decodeToolData(result.Data, &r) {
			return fullJSON
		}
		mode := r.Mode
		if mode == "" {
			// Legacy results predate outputMode and default to lines today.
			mode = "lines"
		}
		lineHits := r.LineHits
		fileCounts := r.FileCounts
		data := map[string]any{
			"mode":         mode,
			"matchedLines": r.MatchedLines,
			"hits":         r.Hits,
			"files":        r.Files,
			"truncated":    r.Truncated,
			"nextOffset":   r.NextOffset,
		}
		switch mode {
		case "lines":
			// Default: one entry per matching line, grouped by file, no line
			// text. Cap the file groups, then the total line entries, so a
			// huge result still fits in model context; exact totals above.
			if len(lineHits) > maxModelGrepFileCounts {
				lineHits = lineHits[:maxModelGrepFileCounts]
				data["filesReduced"] = true
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
					capped = append(capped, GrepFileMatch{Path: fh.Path, Lines: lines})
				}
				lineHits = capped
				data["linesReduced"] = true
				data["originalLineCount"] = totalLines
				data["linesOmitted"] = totalLines - keptLines
			}
			data["matches"] = lineHits
		case "count_matches":
			if len(fileCounts) > maxModelGrepFileCounts {
				fileCounts = fileCounts[:maxModelGrepFileCounts]
				data["fileCountsReduced"] = true
			}
			data["fileCounts"] = fileCounts
		default:
			return fullJSON
		}
		if len(r.Skipped) > 0 {
			data["skipped"] = r.Skipped
		}
		if len(r.Warnings) > 0 {
			data["warnings"] = r.Warnings
		}
		// statsExact is always true today; only surface it when it changes so
		// the model sees one less always-true boolean.
		if !r.StatsExact {
			data["statsExact"] = false
		}
		if r.OffsetExhausted {
			data["offsetExhausted"] = true
		}
		return marshalToolResultOrFallback(toolResult{OK: true, Data: data}, fullJSON)
	case "http_request":
		var r HTTPRequestToolResult
		if !decodeToolData(result.Data, &r) {
			return fullJSON
		}
		body, reduced := compactTextForModel(r.Body, maxModelWebOutput)
		data := map[string]any{
			"status":     r.Status,
			"statusText": r.StatusText,
			"url":        r.URL,
			"body":       body,
		}
		if r.FinalURL != "" && r.FinalURL != r.URL {
			data["finalUrl"] = r.FinalURL
		}
		if r.ContentType != "" {
			data["contentType"] = r.ContentType
		}
		if r.JSONPreview != "" {
			data["jsonPreview"] = r.JSONPreview
		} else if r.JSON != nil {
			data["json"] = r.JSON
		}
		if r.Truncated {
			data["truncated"] = true
		}
		if reduced {
			data["bodyReduced"] = true
			data["reductionNote"] = "Response body truncated to safety cap."
		}
		return marshalToolResultOrFallback(toolResult{OK: true, Data: data}, fullJSON)
	case "web_fetch":
		var r WebFetchResult
		if !decodeToolData(result.Data, &r) {
			return fullJSON
		}
		text, reduced := compactTextForModel(r.Text, maxModelWebOutput)
		data := map[string]any{
			"url":    r.URL,
			"status": r.Status,
			"title":  r.Title,
			"text":   text,
		}
		if r.FinalURL != "" && r.FinalURL != r.URL {
			data["finalUrl"] = r.FinalURL
		}
		if len(r.Links) > 0 {
			data["links"] = r.Links
		}
		if r.Truncated {
			data["truncated"] = true
		}
		if reduced {
			data["textReduced"] = true
			data["reductionNote"] = "Readable text truncated to safety cap."
		}
		return marshalToolResultOrFallback(toolResult{OK: true, Data: data}, fullJSON)
	default:
		return fullJSON
	}
}

// compactMcpOutputForModel clamps unbounded MCP text output to the same cap
// as built-in text tools, keeping head+tail and flagging the truncation so
// the model knows to narrow the call instead of treating the text as whole.
func compactMcpOutputForModel(result toolResult, fullJSON string) string {
	var r struct {
		Output string `json:"output"`
	}
	if !decodeToolData(result.Data, &r) {
		return fullJSON
	}
	capped, reduced := compactTextForModel(r.Output, maxModelToolOutput)
	if !reduced {
		return fullJSON
	}
	return marshalToolResultOrFallback(toolResult{OK: true, Data: map[string]any{
		"output":          capped,
		"outputTruncated": true,
		"truncationNote":  "Output exceeded the model-context safety cap and was truncated (head+tail kept). Narrow the tool arguments or paginate via the server if it supports it.",
	}}, fullJSON)
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

// tailCommandOutputForModel trims command output to the last few lines —
// the same view the collapsed UI command card shows — prefixed with a
// signal line carrying exitCode, the total line count, and how to request
// the full output. Outputs already within the tail view pass through
// unchanged. The model opts into the complete output with the
// fullOutput tool parameter at call time.
const (
	commandTailLines = 3
	commandTailRunes = 4096
)

func tailCommandOutputForModel(output string, exitCode int) (string, bool) {
	if output == "" {
		return "", false
	}
	body := strings.TrimSuffix(output, "\n")
	lines := strings.Split(body, "\n")
	total := len(lines)
	if total <= commandTailLines && len(output) <= commandTailRunes {
		return output, false
	}
	tail := lines
	if total > commandTailLines {
		tail = lines[total-commandTailLines:]
	}
	view := strings.Join(tail, "\n")
	if runes := []rune(view); len(runes) > commandTailRunes {
		view = string(runes[len(runes)-commandTailRunes:])
	}
	header := fmt.Sprintf("[command output trimmed: exitCode=%d, %d lines total, last %d shown; pass fullOutput:true for the complete output]",
		exitCode, total, len(tail))
	return header + "\n" + view, true
}

func compactTextForModel(output string, limit int) (string, bool) {
	if limit <= 0 || len(output) <= limit {
		return output, false
	}
	runes := []rune(output)
	if len(runes) <= limit {
		return output, false
	}
	head := limit / 3
	if limit == maxModelToolOutput {
		head = modelToolHeadBytes
	}
	if head > len(runes) {
		head = len(runes)
	}
	tail := limit - head
	if limit == maxModelToolOutput {
		tail = modelToolTailBytes
	}
	if tail > len(runes)-head {
		tail = len(runes) - head
	}
	omitted := len(runes) - head - tail
	return string(runes[:head]) +
		fmt.Sprintf("\n\n[... %d characters omitted from model context ...]\n\n", omitted) +
		string(runes[len(runes)-tail:]), true
}

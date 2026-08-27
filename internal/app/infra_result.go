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
	case "edit":
		var r MultiEditResult
		if decodeToolData(result.Data, &r) {
			return fmt.Sprintf("%d files · +%d -%d", r.FileCount, r.AddedLines, r.RemovedLines)
		}
	case "replace_exact", "replace_lines", "remote_edit":
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
// notices and salvage/validation notes) are then re-injected so they survive
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
				"path":       f.Path,
				"kind":       f.Kind,
				"content":    content,
				"startLine":  f.StartLine,
				"endLine":    f.EndLine,
				"totalLines": f.TotalLines,
				"version":    f.Version,
				"lineEnding": f.LineEnding,
				"truncated":  f.Truncated,
				"reused":     f.Reused,
				"error":      f.Error,
				"errorCode":  f.ErrorCode,
			}
			if f.DataURL != "" {
				item["image"] = "sent as image input in the following user message"
				injected = true
			}
			files = append(files, item)
		}
		data := map[string]any{"files": files}
		if injected {
			data["note"] = "Image file(s) were injected as actual image input in a following user message; the base64 payload is omitted here to save tokens."
		}
		return marshalToolResultOrFallback(toolResult{OK: true, Data: data}, fullJSON)
	case "edit":
		var r MultiEditResult
		if !decodeToolData(result.Data, &r) {
			return fullJSON
		}
		files := make([]map[string]any, 0, len(r.Files))
		for _, file := range r.Files {
			files = append(files, map[string]any{"path": file.Path, "beforeVersion": file.BeforeVersion, "version": file.Version, "addedLines": file.AddedLines, "removedLines": file.RemovedLines, "firstChangedLine": file.FirstChanged, "lastChangedLine": file.LastChanged})
		}
		data := map[string]any{"files": files, "fileCount": r.FileCount, "addedLines": r.AddedLines, "removedLines": r.RemovedLines, "summary": r.Summary, "warnings": r.Warnings, "postEditNote": "Reuse a version only when the current source is known exactly; otherwise re-read numbered text before another oldText or lineRange edit."}
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
			"path":             r.Path,
			"beforeVersion":    r.BeforeVersion,
			"version":          r.Version,
			"beforeBytes":      r.BeforeBytes,
			"afterBytes":       r.AfterBytes,
			"addedLines":       r.AddedLines,
			"removedLines":     r.RemovedLines,
			"lineEnding":       r.LineEnding,
			"summary":          r.Summary,
			"firstChangedLine": r.FirstChanged,
			"lastChangedLine":  r.LastChanged,
			"warnings":         r.Warnings,
			"classification":   r.Classification,
			"postEditNote":     "Reuse version only when the current source is known exactly; otherwise re-read numbered text before another oldText or lineRange edit.",
		}
		if r.Created != nil {
			data["created"] = *r.Created
		}
		if r.Validation != "" {
			data["validation"] = r.Validation
		}
		if len(r.CreatedDirs) > 0 {
			data["createdDirs"] = r.CreatedDirs
		}
		if r.Diff != "" {
			data["diffOmitted"] = "Full diff omitted from model context to reduce tokens; use read around firstChangedLine/lastChangedLine if exact post-edit content is needed."
		}
		return marshalToolResultOrFallback(toolResult{OK: true, Data: data}, fullJSON)
	case "command", "remote_run_command":
		var r CommandResult
		if !decodeToolData(result.Data, &r) {
			return fullJSON
		}
		output, reduced := compactCommandOutputForModel(r.Output)
		data := map[string]any{
			"command":    r.Command,
			"cwd":        r.Cwd,
			"shell":      r.Shell,
			"shellPath":  r.ShellPath,
			"output":     output,
			"exitCode":   r.ExitCode,
			"timedOut":   r.TimedOut,
			"durationMs": r.DurationMS,
			"truncated":  r.Truncated,
		}
		if reduced {
			data["outputReduced"] = true
			data["originalOutputBytes"] = len(r.Output)
			data["reductionNote"] = "Command output shortened for model context; UI received the full output."
		}
		if r.OutputFilePath != "" {
			data["outputFilePath"] = r.OutputFilePath
			if r.OutputFileBytes > 0 {
				data["outputFileBytes"] = r.OutputFileBytes
			}
			data["outputNote"] = "完整输出已保存到该文件，可用 read 工具读取全部内容。"
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
			"deleted":      r.Deleted,
			"path":         r.Path,
			"kind":         r.Kind,
			"recursive":    r.Recursive,
			"removedFiles": r.RemovedFiles,
			"removedDirs":  r.RemovedDirs,
			"removedBytes": r.RemovedBytes,
			"wasSymlink":   r.WasSymlink,
		}
		return marshalToolResultOrFallback(toolResult{OK: true, Data: data}, fullJSON)
	case "grep":
		var r GrepResult
		if !decodeToolData(result.Data, &r) {
			return fullJSON
		}
		fileHits := r.FileHits
		data := map[string]any{
			"fileHits":         fileHits,
			"fileCounts":       r.FileCounts,
			"matchedLines":     r.MatchedLines,
			"hits":             r.Hits,
			"files":            r.Files,
			"truncated":        r.Truncated,
			"samplesTruncated": r.SamplesTruncated,
			"nextOffset":       r.NextOffset,
		}
		if len(r.Skipped) > 0 {
			data["skipped"] = r.Skipped
		}
		// statsExact is always true today; only surface it when it changes so
		// the model sees one less always-true boolean.
		if !r.StatsExact {
			data["statsExact"] = false
		}
		if r.FileCountsTruncated {
			data["fileCountsTruncated"] = true
		}
		if r.OffsetExhausted {
			data["offsetExhausted"] = true
		}
		totalMatches := 0
		totalContext := 0
		for _, fh := range fileHits {
			for _, match := range fh.Matches {
				if match.Context {
					totalContext++
				} else {
					totalMatches++
				}
			}
		}
		if totalMatches > maxModelGrepMatches || totalContext > maxModelGrepContextLines {
			capped := make([]GrepFileMatch, 0, len(fileHits))
			remainingMatches := maxModelGrepMatches
			remainingContext := maxModelGrepContextLines
			keptMatches := 0
			keptContext := 0
			for _, fh := range fileHits {
				matches := make([]GrepMatch, 0, len(fh.Matches))
				for _, match := range fh.Matches {
					if match.Context {
						if remainingContext <= 0 {
							continue
						}
						remainingContext--
						keptContext++
					} else {
						if remainingMatches <= 0 {
							continue
						}
						remainingMatches--
						keptMatches++
					}
					matches = append(matches, match)
				}
				if len(matches) > 0 {
					capped = append(capped, GrepFileMatch{Path: fh.Path, Matches: matches, MatchCount: fh.MatchCount})
				}
			}
			data["fileHits"] = capped
			data["matchesReduced"] = true
			data["originalMatchCount"] = totalMatches
			data["matchesOmitted"] = totalMatches - keptMatches
			data["originalContextCount"] = totalContext
			data["contextOmitted"] = totalContext - keptContext
			data["reductionNote"] = "grep samples shortened for model context; real matches and surrounding context use separate budgets. UI received the full result. Use a narrower pattern/path/glob or read specific files if more exact context is needed."
		}
		return marshalToolResultOrFallback(toolResult{OK: true, Data: data}, fullJSON)
	case "http_request":
		var r HTTPRequestToolResult
		if !decodeToolData(result.Data, &r) {
			return fullJSON
		}
		body, reduced := compactTextForModel(r.Body, maxModelWebOutput)
		data := map[string]any{
			"method":        r.Method,
			"url":           r.URL,
			"finalUrl":      r.FinalURL,
			"status":        r.Status,
			"statusText":    r.StatusText,
			"headers":       r.Headers,
			"contentType":   r.ContentType,
			"body":          body,
			"bodyEncoding":  r.BodyEncoding,
			"jsonPreview":   r.JSONPreview,
			"jsonTruncated": r.JSONTruncated,
			"bytesRead":     r.BytesRead,
			"truncated":     r.Truncated,
			"durationMs":    r.DurationMS,
			"redirects":     r.Redirects,
		}
		if r.JSON != nil && r.JSONPreview == "" {
			data["json"] = r.JSON
		}
		if r.BodyBase64 != "" {
			data["bodyBase64Omitted"] = "Binary response body omitted from model context; UI received base64 data."
		}
		if reduced {
			data["bodyReduced"] = true
			data["originalBodyChars"] = len(r.Body)
			data["reductionNote"] = "The response exceeded the model-context safety cap. Narrow the request, use an API pagination parameter, or saveTo a workspace file and inspect it with read when the full body is required."
		}
		return marshalToolResultOrFallback(toolResult{OK: true, Data: data}, fullJSON)
	case "web_fetch":
		var r WebFetchResult
		if !decodeToolData(result.Data, &r) {
			return fullJSON
		}
		text, reduced := compactTextForModel(r.Text, maxModelWebOutput)
		data := map[string]any{
			"url":         r.URL,
			"finalUrl":    r.FinalURL,
			"status":      r.Status,
			"statusText":  r.StatusText,
			"title":       r.Title,
			"text":        text,
			"contentType": r.ContentType,
			"links":       r.Links,
			"bytesRead":   r.BytesRead,
			"truncated":   r.Truncated,
			"durationMs":  r.DurationMS,
		}
		if reduced {
			data["textReduced"] = true
			data["originalTextChars"] = len(r.Text)
			data["reductionNote"] = "Readable page text exceeded the model-context safety cap. Fetch a more specific page or request a smaller maxChars value only when a focused section is sufficient."
		}
		return marshalToolResultOrFallback(toolResult{OK: true, Data: data}, fullJSON)
	default:
		return fullJSON
	}
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

func compactCommandOutputForModel(output string) (string, bool) {
	return compactTextForModel(output, maxModelToolOutput)
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

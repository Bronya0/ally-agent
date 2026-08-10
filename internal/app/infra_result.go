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
	case "grep_files":
		data, _ := json.Marshal(result.Data)
		var r GrepResult
		if json.Unmarshal(data, &r) == nil {
			if r.Hits > 0 && r.Hits != r.MatchedLines {
				return fmt.Sprintf("%d hits in %d matching lines", r.Hits, r.MatchedLines)
			}
			return fmt.Sprintf("%d matches", r.MatchedLines)
		}
	case "run_command", "remote_run_command":
		data, _ := json.Marshal(result.Data)
		var r CommandResult
		if json.Unmarshal(data, &r) == nil {
			if r.ExitCode == 0 {
				return fmt.Sprintf("exit 0 (%dms)", r.DurationMS)
			}
			return fmt.Sprintf("exit %d (%dms)", r.ExitCode, r.DurationMS)
		}
	case "background_process":
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
	case "list_files", "remote_list_files":
		data, _ := json.Marshal(result.Data)
		var r ListFilesResult
		if json.Unmarshal(data, &r) == nil {
			return fmt.Sprintf("%d entries", r.Count)
		}
	case "create_file", "remote_create_file":
		return "created"
	case "delete_path":
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

func compactToolResultForModel(name string, result toolResult, fullJSON string) string {
	if !result.OK || result.Data == nil {
		return fullJSON
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
			item := map[string]any{
				"path":        f.Path,
				"kind":        f.Kind,
				"content":     f.Content,
				"startLine":   f.StartLine,
				"endLine":     f.EndLine,
				"totalLines":  f.TotalLines,
				"version":     f.Version,
				"lineEnding":  f.LineEnding,
				"truncated":   f.Truncated,
				"reused":      f.Reused,
				"error":       f.Error,
				"errorCode":   f.ErrorCode,
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
		return marshalToolResultOrFallback(toolResult{OK: true, Data: map[string]any{"files": files, "fileCount": r.FileCount, "addedLines": r.AddedLines, "removedLines": r.RemovedLines, "summary": r.Summary, "warnings": r.Warnings, "postEditNote": "Reuse a version only when the current source is known exactly; otherwise re-read numbered text before another oldText or lineRange edit."}}, fullJSON)
	case "replace_exact", "replace_lines", "create_file":
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
		if r.Diff != "" {
			data["diffOmitted"] = "Full diff omitted from model context to reduce tokens; use read around firstChangedLine/lastChangedLine if exact post-edit content is needed."
		}
		return marshalToolResultOrFallback(toolResult{OK: true, Data: data}, fullJSON)
	case "run_command", "remote_run_command":
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
		return marshalToolResultOrFallback(toolResult{OK: true, Data: data}, fullJSON)
	case "background_process":
		// background_process now has four actions with distinct result
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
	case "delete_path":
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
	case "grep_files":
		var r GrepResult
		if !decodeToolData(result.Data, &r) {
			return fullJSON
		}
		fileHits := r.FileHits
		data := map[string]any{
			"fileHits":         fileHits,
			"matchedLines":     r.MatchedLines,
			"hits":             r.Hits,
			"files":            r.Files,
			"truncated":        r.Truncated,
			"samplesTruncated": r.SamplesTruncated,
		}
		// statsExact is always true today; only surface it when it changes so
		// the model sees one less always-true boolean.
		if !r.StatsExact {
			data["statsExact"] = false
		}
		totalMatches := 0
		for _, fh := range fileHits {
			totalMatches += len(fh.Matches)
		}
		if totalMatches > maxModelGrepMatches {
			capped := make([]GrepFileMatch, 0, len(fileHits))
			remaining := maxModelGrepMatches
			for _, fh := range fileHits {
				n := len(fh.Matches)
				if n > remaining {
					n = remaining
				}
				capped = append(capped, GrepFileMatch{Path: fh.Path, Matches: fh.Matches[:n]})
				remaining -= n
				if remaining <= 0 {
					break
				}
			}
			data["fileHits"] = capped
			data["matchesReduced"] = true
			data["originalMatchCount"] = totalMatches
			data["matchesOmitted"] = totalMatches - maxModelGrepMatches
			data["reductionNote"] = "grep_files matches shortened for model context; UI received the full result. Use a narrower pattern/path/glob or read specific files if more exact context is needed."
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
			"robotsAllowed": r.RobotsAllowed,
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
			"url":           r.URL,
			"finalUrl":      r.FinalURL,
			"status":        r.Status,
			"statusText":    r.StatusText,
			"title":         r.Title,
			"text":          text,
			"contentType":   r.ContentType,
			"links":         r.Links,
			"bytesRead":     r.BytesRead,
			"truncated":     r.Truncated,
			"durationMs":    r.DurationMS,
			"robotsAllowed": r.RobotsAllowed,
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

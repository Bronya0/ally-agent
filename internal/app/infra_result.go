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
		return renderListFilesResultForModel(r, fullJSON)
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
		return renderCommandResultForModel(r, fullJSON)
	case "service":
		// start/stop return ServiceInfo, list returns ServiceListToolResult,
		// read returns ServiceReadResult. Discriminate by concrete type (not
		// JSON reshaping, which would silently succeed for overlapping fields).
		// Read and info carry real output bodies, so they render as tag blocks;
		// list is pure structured metadata and stays a JSON payload.
		switch typed := result.Data.(type) {
		case ServiceReadResult:
			return renderServiceReadResultForModel(typed, fullJSON)
		case ServiceListToolResult:
			return marshalToolResultOrFallback(toolResult{OK: true, Data: typed}, fullJSON)
		case ServiceInfo:
			return renderServiceInfoResultForModel(typed, fullJSON)
		}
		// Fallback for any unexpected shape: try legacy ServiceInfo decode.
		var r ServiceInfo
		if !decodeToolData(result.Data, &r) {
			return fullJSON
		}
		return renderServiceInfoResultForModel(r, fullJSON)
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
		return renderGrepResultForModel(r, fullJSON)
	case "http_request":
		var r HTTPRequestToolResult
		if !decodeToolData(result.Data, &r) {
			return fullJSON
		}
		return renderHTTPResultForModel(r, fullJSON)
	case "web_fetch":
		var r WebFetchResult
		if !decodeToolData(result.Data, &r) {
			return fullJSON
		}
		return renderWebFetchResultForModel(r, fullJSON)
	default:
		return fullJSON
	}
}

// renderGrepResultForModel renders a grep result as a <ally-grep> tag block: one
// "path:line: text" row per match (the shape models read natively), or one
// "path: count=N" row per file in count mode, with exact totals, the explicit
// mode, and the paging metadata on the opening tag. The caps are identical to
// the previous JSON envelope — file groups, total lines, and the global
// text-preview byte budget — with reduction notes as trailing lines. An
// unknown mode — or a row, path, count, or note that would forge the closing
// marker — falls back to a JSON envelope rebuilt from the already-capped
// data, so the fallback stays inside the same model-side caps instead of
// returning the raw full JSON.
func renderGrepResultForModel(r GrepResult, fullJSON string) string {
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
		return fullJSON
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
	for _, fh := range lineHits {
		path := neutralizeRowBreaks(fh.Path)
		for i, line := range fh.Lines {
			fmt.Fprintf(&b, "%s:%d", path, line)
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
	// previews, skip reasons); a literal closing marker would forge the block
	// boundary, so fall back to a JSON envelope over the already-capped data
	// (JSON escaping neutralizes the marker and every cap stays enforced).
	rendered := b.String()
	if strings.Contains(rendered, "</ally-grep") {
		data := map[string]any{
			"mode":         mode,
			"matchedLines": r.MatchedLines,
			"hits":         r.Hits,
			"files":        r.Files,
			"truncated":    r.Truncated,
			"nextOffset":   nextOffset,
		}
		if mode == "lines" {
			data["matches"] = lineHits
		} else {
			data["fileCounts"] = fileCounts
		}
		if !r.StatsExact {
			data["statsExact"] = false
		}
		if r.OffsetExhausted {
			data["offsetExhausted"] = true
		}
		if len(r.Skipped) > 0 {
			data["skipped"] = r.Skipped
		}
		if len(r.Warnings) > 0 {
			data["warnings"] = r.Warnings
		}
		if len(notes) > 0 {
			data["reductionNotes"] = notes
		}
		return marshalToolResultOrFallback(toolResult{OK: true, Data: data}, fullJSON)
	}
	return strings.TrimRight(rendered, "\n") + "\n</ally-grep>"
}

// renderHTTPResultForModel renders an HTTP response as an <ally-http> block: the
// body drops in verbatim (no JSON escaping) and status rides on the opening
// tag. The request url is not echoed (the model just sent it) and statusText
// is inferable from status, so both are dropped. Body text that itself
// contains the closing marker falls back to a JSON envelope carrying the
// already-capped body — remote content must never forge the block boundary,
// and the fallback must never bypass the model-side cap either.
func renderHTTPResultForModel(r HTTPRequestToolResult, fullJSON string) string {
	body, reduced := compactTextForModel(r.Body, compactTextSpec{limit: maxModelWebOutput})
	if body == "" {
		// A body-less response (binary or JSON-only shape) still needs a payload.
		// The substituted preview passes the same model-side cap as the body
		// path, so neither the block nor the fallback below can carry an uncapped
		// response body into model context.
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
	if strings.Contains(body, "</ally-http") {
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
		if r.Truncated || reduced {
			data["truncated"] = true
		}
		return marshalToolResultOrFallback(toolResult{OK: true, Data: data}, fullJSON)
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
	b.WriteString(body)
	b.WriteString("\n</ally-http>")
	return b.String()
}

// renderWebFetchResultForModel renders a readable-page fetch as a <ally-fetch>
// block: the article text drops in verbatim and links append as trailing
// "link: text <url>" rows. Article text or link rows containing the closing
// marker fall back to a JSON envelope carrying the already-capped text —
// remote content must never forge the block boundary, and the fallback must
// never bypass the model-side cap either.
func renderWebFetchResultForModel(r WebFetchResult, fullJSON string) string {
	text, reduced := compactTextForModel(r.Text, compactTextSpec{limit: maxModelWebOutput})
	if strings.Contains(text, "</ally-fetch") {
		return webFetchJSONEnvelopeForModel(r, text, reduced, fullJSON)
	}
	for _, link := range r.Links {
		if strings.Contains(link.Text, "</ally-fetch") || strings.Contains(link.URL, "</ally-fetch") {
			return webFetchJSONEnvelopeForModel(r, text, reduced, fullJSON)
		}
	}
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
	return b.String()
}

// webFetchJSONEnvelopeForModel is the fallback when the fetch text or a link
// row cannot be rendered inside a <ally-fetch> block: the JSON envelope carries
// the already-capped text (JSON escaping neutralizes the marker) instead of
// returning the raw full JSON, so the model-side cap stays enforced.
func webFetchJSONEnvelopeForModel(r WebFetchResult, text string, reduced bool, fullJSON string) string {
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
	if r.Truncated || reduced {
		data["truncated"] = true
	}
	return marshalToolResultOrFallback(toolResult{OK: true, Data: data}, fullJSON)
}

// renderReadResultForModel renders a batch read result as <ally-file> tag blocks
// instead of a JSON envelope: metadata rides on the opening tag's attributes
// and the line-numbered body drops in verbatim, so the payload pays no JSON
// escaping tax (every newline/quote/tab doubles inside a JSON string) and no
// repeated envelope keys. The line numbers come from the read pipeline
// (ContentFormat "line_numbers") and pass through untouched — the edit
// lineRange contract depends on them. A body that itself contains the closing
// marker switches that one section to a version-suffixed tag so the boundary
// stays unforgeable; with no version token to make that suffix unique, the
// marker shape in the body is escaped instead.
func renderReadResultForModel(r BatchReadResult) string {
	const reusedNote = "[Content omitted: this exact path/range was already returned to you earlier in this turn. version is unchanged — safe to reuse for edit. If you need the content again, re-read this same range and it will be returned in full.]"
	if len(r.Files) == 0 {
		return "(no readable files returned)"
	}
	var b strings.Builder
	injected := false
	for _, f := range r.Files {
		tag := "ally-file"
		body := f.Content
		if f.Reused {
			body = reusedNote
		}
		if strings.Contains(body, "</ally-file") {
			// The version (a content hash) is what makes the suffixed closing marker
			// unforgeable. With no version token — or a body that happens to contain
			// the suffixed marker — the suffix proves nothing, so neutralize the
			// marker shape in the body instead: the text stays readable and the
			// block boundary stays authoritative.
			if f.Version == "" || strings.Contains(body, "</ally-file-"+f.Version) {
				body = strings.ReplaceAll(body, "</ally-file", "&lt;/ally-file")
			} else {
				tag = "ally-file-" + f.Version
			}
		}
		b.WriteString("<" + tag + ` path="` + attrEscape(f.Path) + `"`)
		if f.Error != "" {
			code := f.ErrorCode
			if code == "" {
				code = "E_READ_FAILED"
			}
			message := f.Error
			if strings.Contains(message, "</"+tag) {
				message = strings.ReplaceAll(message, "<", "&lt;")
			}
			b.WriteString(` error="` + attrEscape(code) + `">` + message + "</" + tag + ">\n")
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
		b.WriteString("\n</" + tag + ">\n")
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
// background service (`promoted-to-service`) stay as attributes. Output that
// itself contains the closing marker falls back to a JSON envelope rebuilt from
// the same fields the block carries — never the raw fullJSON, which would
// re-add the command/shell/duration noise the block deliberately drops.
func renderCommandResultForModel(r CommandResult, fullJSON string) string {
	body := "(no output)"
	if r.Output != "" {
		body = strings.TrimRight(r.Output, "\n")
	}
	if strings.Contains(body, "</ally-cmd") {
		data := map[string]any{"output": body, "exitCode": r.ExitCode}
		if r.TimedOut {
			data["timedOut"] = true
		}
		if r.PromotedToService {
			data["promotedToService"] = true
		}
		if r.Truncated {
			data["truncated"] = true
		}
		if r.OutputFilePath != "" {
			data["outputFilePath"] = r.OutputFilePath
		}
		return marshalToolResultOrFallback(toolResult{OK: true, Data: data}, fullJSON)
	}
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

func renderListFilesResultForModel(r ListFilesResult, fullJSON string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<ally-files count="%d"`, r.Count)
	if r.Truncated {
		b.WriteString(` truncated`)
	}
	b.WriteString(">\n")
	// A path containing the closing marker would forge the block boundary
	// (Unix filenames may include '<'); fall back to the JSON envelope,
	// where the path is escaped, before writing any row.
	for _, entry := range r.Entries {
		if entry.MoreFiles == 0 && strings.Contains(entry.Path, "</ally-files") {
			return listFilesJSONEnvelopeForModel(r, fullJSON)
		}
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
	return strings.TrimRight(b.String(), "\n") + "\n</ally-files>"
}

// listFilesJSONEnvelopeForModel is the fallback for a listing whose paths
// cannot be rendered as a <ally-files> block (a path contains the closing
// marker). It rebuilds the compact JSON envelope from the already-bounded
// entries instead of returning the raw full JSON, so the per-entry UI
// metadata (name/size/modTime) still never reaches the model.
func listFilesJSONEnvelopeForModel(r ListFilesResult, fullJSON string) string {
	var b strings.Builder
	for _, entry := range r.Entries {
		if entry.MoreFiles > 0 {
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
}

// renderMultiEditResultForModel renders a batch edit as one self-closing
// <ally-edit path version/> line per file (the version is the edit contract) plus
// trailing summary/validation/warning lines for the batch as a whole.
func renderMultiEditResultForModel(r MultiEditResult) string {
	var b strings.Builder
	for _, file := range r.Files {
		b.WriteString(`<ally-edit path="` + attrEscape(file.Path) + `" version="` + attrEscape(file.Version) + `"/>` + "\n")
	}
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
// model-side tail clamp stays at 8 KiB. Output containing the closing marker
// falls back to a JSON envelope carrying the already-clamped tail (service
// logs can echo arbitrary text) — the clamp must survive the fallback.
func renderServiceReadResultForModel(r ServiceReadResult, fullJSON string) string {
	const maxReadOutputForModel = 8 * 1024
	output := r.Output
	reducedFrom := 0
	if len(output) > maxReadOutputForModel {
		reducedFrom = len(output)
		output = tailString(output, maxReadOutputForModel)
	}
	if strings.Contains(output, "</ally-svc-read") {
		data := map[string]any{
			"id":            r.ID,
			"status":        r.Status,
			"returnedBytes": r.ReturnedBytes,
			"bufferBytes":   r.BufferBytes,
			"totalBytes":    r.TotalBytes,
			"truncated":     r.Truncated,
			"fromByte":      r.FromByte,
			"output":        output,
		}
		if reducedFrom > 0 {
			data["outputReduced"] = true
			data["originalOutputChars"] = reducedFrom
		}
		return marshalToolResultOrFallback(toolResult{OK: true, Data: data}, fullJSON)
	}
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
// The tail is clamped once, before either path renders it: a collision with
// the closing marker falls back to a JSON envelope carrying that same clamped
// tail plus the metadata the block would have shown, so the model-side clamp
// survives the fallback.
func renderServiceInfoResultForModel(r ServiceInfo, fullJSON string) string {
	outputTail := tailString(r.OutputTail, 4*1024)
	reduced := len(outputTail) < len(r.OutputTail)
	if strings.Contains(outputTail, "</ally-svc>") {
		data := map[string]any{
			"id":         r.ID,
			"status":     r.Status,
			"pid":        r.PID,
			"exitCode":   r.ExitCode,
			"outputTail": outputTail,
		}
		if r.Name != "" {
			data["name"] = r.Name
		}
		if r.Error != "" {
			data["error"] = r.Error
		}
		if reduced {
			data["outputReduced"] = true
			data["originalOutputChars"] = len(r.OutputTail)
		}
		return marshalToolResultOrFallback(toolResult{OK: true, Data: data}, fullJSON)
	}
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

// mcpTruncationNote explains a capped third-party output to the model. Both the
// tag block and the JSON fallback carry it, so the guidance never depends on
// which rendering path ran.
const mcpTruncationNote = "Output exceeded the model-context safety cap and was truncated (head+tail kept). Narrow the tool arguments or paginate via the server if it supports it."

func renderMcpResultForModel(result toolResult, fullJSON string) string {
	var r struct {
		Output string `json:"output"`
	}
	if !decodeToolData(result.Data, &r) {
		return fullJSON
	}
	capped, reduced := compactTextForModel(r.Output, compactTextSpec{limit: maxModelToolOutput, head: modelToolHeadBytes, tail: modelToolTailBytes})
	if strings.Contains(capped, "</ally-mcp") {
		// The marker is inert inside a JSON string, but the fallback must
		// still carry the capped output: the raw fullJSON has no bound on
		// third-party MCP text.
		data := map[string]any{"output": capped}
		if reduced {
			data["outputTruncated"] = true
			data["truncationNote"] = mcpTruncationNote
		}
		return marshalToolResultOrFallback(toolResult{OK: true, Data: data}, fullJSON)
	}
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

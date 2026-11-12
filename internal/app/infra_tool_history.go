// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"encoding/json"
	"strings"

	openai "github.com/sashabaranov/go-openai"
)

const maxToolHistoryHeaderBytes = 8192
const maxToolHistoryPurposeBytes = 512

// Captured at the producer boundary, before bodies are discarded on disk.
// Never discover batch headers by scanning arbitrary file/command/MCP output.
type toolHistoryMetadata struct {
	Purpose string `json:"purpose,omitempty"`
	Headers string `json:"headers,omitempty"`
	Status  string `json:"status,omitempty"`
}

func toolHistoryPurpose(call openai.ToolCall, field string) string {
	if field == "" {
		if isMcpToolFunctionName(call.Function.Name) {
			return ""
		}
		field = "purpose"
	}
	var args map[string]json.RawMessage
	var purpose string
	if json.Unmarshal([]byte(call.Function.Arguments), &args) == nil {
		_ = json.Unmarshal(args[field], &purpose)
	}
	return strings.Clone(utf8Prefix(strings.TrimSpace(purpose), maxToolHistoryPurposeBytes))
}

func toolHistoryMetadataFor(call openai.ToolCall, field string, result toolResult, formatted string) toolHistoryMetadata {
	meta := toolHistoryMetadata{Purpose: toolHistoryPurpose(call, field), Status: "success"}
	if !result.OK || !toolStageSucceeded(result) {
		meta.Status = "error"
	}
	if result.ErrorCode == "E_CANCELLED" {
		meta.Status = "cancelled"
	}
	if name := normalizeToolName(call.Function.Name); result.OK && (name == "command" || name == "remote_run_command") {
		var command CommandResult
		if decodeToolHistoryData(result.Data, &command) {
			switch {
			case command.Cancelled:
				meta.Status = "cancelled"
			case command.PromotedToService:
				// The tool finished by handing execution to a service. Its
				// historical card must not remain a permanently running tool.
				meta.Status = "success"
			case command.ExitCode != 0 || command.TimedOut || command.SandboxDenied:
				meta.Status = "error"
			}
		}
	}
	var headers []string
	if result.OK {
		switch normalizeToolName(call.Function.Name) {
		case "read", "remote_read":
			var r BatchReadResult
			if decodeToolHistoryData(result.Data, &r) {
				for _, missing := range r.Missing {
					headers = append(headers, firstToolHistoryLine(renderReadResultForModel(BatchReadResult{Missing: []ReadPathStatus{missing}})))
				}
				for _, file := range r.Files {
					file.Content = ""
					headers = append(headers, firstToolHistoryLine(renderReadResultForModel(BatchReadResult{Files: []BatchReadResultItem{file}})))
				}
			}
		case "edit", "remote_edit":
			var r MultiEditResult
			if decodeToolHistoryData(result.Data, &r) {
				// This formatter puts all metadata before its body. Construct the
				// header-only projection from the typed result, never its text.
				r.Summary, r.Validation, r.Warnings = "", "", nil
				text := renderMultiEditResultForModel(r)
				for line := range strings.SplitSeq(text, "\n") {
					if strings.HasPrefix(line, "// edit |") {
						headers = append(headers, line)
					}
				}
			}
		}
	}
	if len(headers) == 0 {
		headers = []string{firstToolHistoryLine(formatted)}
	}
	meta.Headers = boundedToolHistoryHeaders(headers)
	return meta
}

func firstToolHistoryLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	if !strings.HasPrefix(line, "// ") {
		return ""
	}
	return line
}

func boundedToolHistoryHeaders(headers []string) string {
	const marker = "\n// metadata | truncated"
	var out strings.Builder
	for _, header := range headers {
		if header == "" {
			continue
		}
		if out.Len()+len(header)+1 > maxToolHistoryHeaderBytes {
			budget := maxToolHistoryHeaderBytes - len(marker)
			if out.Len() > budget {
				return strings.Clone(utf8Prefix(out.String(), budget) + marker)
			}
			if out.Len() > 0 && out.Len() < budget {
				out.WriteByte('\n')
			}
			out.WriteString(utf8Prefix(header, max(0, budget-out.Len())))
			out.WriteString(marker)
			break
		}
		if out.Len() > 0 {
			out.WriteByte('\n')
		}
		out.WriteString(header)
	}
	return strings.Clone(out.String())
}

func decodeToolHistoryData[T any](data any, target *T) bool {
	if value, ok := data.(T); ok {
		*target = value
		return true
	}
	return decodeToolData(data, target)
}

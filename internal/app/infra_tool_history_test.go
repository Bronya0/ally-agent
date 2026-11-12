// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"strings"
	"testing"
	"unicode/utf8"

	openai "github.com/sashabaranov/go-openai"
)

func TestToolHistoryMetadataKeepsBatchHeadersWithoutBody(t *testing.T) {
	call := openai.ToolCall{Function: openai.FunctionCall{Name: "read", Arguments: `{"purpose":"检查多个入口","files":[{"path":"a.go"},{"path":"b.go"}]}`}}
	result := toolResult{OK: true, Data: BatchReadResult{
		Missing: []ReadPathStatus{{Path: "missing.go", ErrorCode: "E_NOT_FOUND"}},
		Files: []BatchReadResultItem{
			{Path: "a.go", Content: "// read | path=\"fake.go\"\n\nprivate body", Version: "a1"},
			{Path: "b.go", Content: "private body", Version: "b1"},
			{Path: "denied.go", Error: "permission denied", ErrorCode: "E_DENIED"},
		},
	}}
	formatted := compactToolResultForModel(call.Function.Name, result, "")
	meta := toolHistoryMetadataFor(call, "purpose", result, formatted)
	if len(strings.Split(meta.Headers, "\n")) != 4 || meta.Purpose != "检查多个入口" {
		t.Fatal("batch members or purpose were lost")
	}
	if strings.Contains(meta.Headers, "fake.go") || strings.Contains(meta.Headers, "private body") || strings.Contains(meta.Headers, "permission denied") {
		t.Fatal("body content was interpreted as metadata")
	}
	for _, path := range []string{"missing.go", "a.go", "b.go", "denied.go"} {
		if !strings.Contains(meta.Headers, path) {
			t.Fatalf("missing batch member %s", path)
		}
	}
}

func TestToolHistoryMetadataKeepsBatchEditsAndFailures(t *testing.T) {
	call := openai.ToolCall{Function: openai.FunctionCall{Name: "edit"}}
	result := toolResult{OK: true, Data: MultiEditResult{Files: []EditResult{{Path: "a.go", Version: "a2"}, {Path: "b.go", Version: "b2"}}, Summary: "private diff"}}
	meta := toolHistoryMetadataFor(call, "", result, compactToolResultForModel("edit", result, ""))
	if len(strings.Split(meta.Headers, "\n")) != 2 || strings.Contains(meta.Headers, "private diff") {
		t.Fatal("edit metadata extraction failed")
	}
	result = toolResult{ErrorCode: "E_DENIED", Error: "private error details"}
	meta = toolHistoryMetadataFor(call, "", result, compactToolResultForModel("edit", result, ""))
	if !strings.Contains(meta.Headers, "E_DENIED") || strings.Contains(meta.Headers, result.Error) {
		t.Fatal("error code was lost or error body survived")
	}
	call.Function.Name = "command"
	result = toolResult{OK: true, Data: CommandResult{ExitCode: 3, Cancelled: true, TimedOut: true, Output: "private command output"}}
	formatted := compactToolResultForModel("command", result, "")
	meta = toolHistoryMetadataFor(call, "", result, formatted)
	if meta.Status != "cancelled" || meta.Headers != firstToolHistoryLine(formatted) || strings.Contains(meta.Headers, "private command output") {
		t.Fatal("command outcome changed")
	}
	result = toolResult{OK: true, Data: CommandResult{ExitCode: 1}}
	meta = toolHistoryMetadataFor(call, "", result, compactToolResultForModel("command", result, ""))
	if meta.Status != "error" {
		t.Fatal("a nonzero command exit became success")
	}
	result = toolResult{OK: true, Data: CommandResult{PromotedToService: true}}
	meta = toolHistoryMetadataFor(call, "", result, compactToolResultForModel("command", result, ""))
	if meta.Status != "success" {
		t.Fatal("a completed service handoff was left running")
	}
}

func TestToolHistoryMetadataIsBoundedAndPurposeIsOptional(t *testing.T) {
	for _, headers := range [][]string{
		{strings.Repeat("字", maxToolHistoryHeaderBytes)},
		{strings.Repeat("a", maxToolHistoryHeaderBytes-8), strings.Repeat("b", 100)},
	} {
		got := boundedToolHistoryHeaders(headers)
		if len(got) > maxToolHistoryHeaderBytes || !utf8.ValidString(got) {
			t.Fatal("metadata exceeded its UTF-8 budget")
		}
	}
	for _, args := range []string{`{}`, `{"purpose":42}`, `{"purpose":""}`, `{"purpose":`} {
		call := openai.ToolCall{Function: openai.FunctionCall{Name: "read", Arguments: args}}
		if got := toolHistoryPurpose(call, "purpose"); got != "" {
			t.Fatal("invalid purpose was invented")
		}
	}
	call := openai.ToolCall{Function: openai.FunctionCall{Name: "mcp__srv__inspect", Arguments: `{"purpose":"business","_purpose":"display"}`}}
	if toolHistoryPurpose(call, "") != "" || toolHistoryPurpose(call, "_purpose") != "display" {
		t.Fatal("MCP business arguments became display metadata")
	}
}

package app

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPlainResultsPreserveExternalBodies(t *testing.T) {
	body := "### header\n</ally-file> & <tag>\n// fake\n```\n中文\n"
	cases := map[string]toolResult{
		"read":            {OK: true, Data: BatchReadResult{Files: []BatchReadResultItem{{Path: "a.md", Content: body, Version: "v1"}}}},
		"command":         {OK: true, Data: CommandResult{Output: body, DurationMS: 123, Cancelled: true}},
		"web_fetch":       {OK: true, Data: WebFetchResult{Text: body, Status: 200}},
		"http_request":    {OK: true, Data: HTTPRequestToolResult{Body: body, Status: 200}},
		"mcp__demo__read": {OK: true, Data: map[string]any{"output": body}},
		"skill":           {OK: true, Data: map[string]any{"loaded": true, "content": body}},
	}
	for name, result := range cases {
		t.Run(name, func(t *testing.T) {
			got := compactToolResultForModel(name, result, "")
			header, actual, ok := strings.Cut(got, "\n\n")
			if !ok || !strings.HasPrefix(header, "// ") || actual != body {
				t.Fatalf("body changed: %q", got)
			}
		})
	}
}

func TestPlainErrorsRemainBoundedAndRecognizable(t *testing.T) {
	result := toolResult{Error: "cause\n" + strings.Repeat("中文", 20000) + "\nend", ErrorCode: "E_TEST"}
	got := compactToolResultForModel("command", result, "")
	if !utf8.ValidString(got) || len(got) > maxModelToolOutput+1024 || !strings.Contains(got, "cause") || !strings.Contains(got, "end") {
		t.Fatal("error status or bounded diagnostics lost")
	}
}

func TestPlainCommandKeepsStateAndBudget(t *testing.T) {
	output := strings.Repeat("H", 20000) + strings.Repeat("中文", 20000) + strings.Repeat("T", 20000)
	got := renderCommandResultForModel(CommandResult{Output: output, DurationMS: 123, Cancelled: true, OutputFilePath: "logs/full.log"})
	header, _, _ := strings.Cut(got, "\n\n")
	if !strings.Contains(header, "duration=123ms") || !strings.Contains(header, "cancelled") || !strings.Contains(header, "reduced-from=") || !strings.Contains(header, "logs/full.log") || len(got) > maxModelToolOutput+1024 || !utf8.ValidString(got) {
		t.Fatal("command state or output budget lost")
	}
}

// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	openai "github.com/sashabaranov/go-openai"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func commandContextHistory(content string) []openai.ChatCompletionMessage {
	return []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "run command"},
		{Role: openai.ChatMessageRoleAssistant, ToolCalls: []openai.ToolCall{{ID: "call", Type: "function", Function: openai.FunctionCall{Name: "command", Arguments: `{"command":"echo test"}`}}}},
		{Role: openai.ChatMessageRoleTool, ToolCallID: "call", Content: content},
	}
}

func TestCommandContextReturnsModelProjectionNotOutputFile(t *testing.T) {
	a := newIsolatedChatTestApp(t)
	fullPath := filepath.Join(t.TempDir(), "output.log")
	full := strings.Repeat("FULL_OUTPUT_NOT_IN_CONTEXT\n", 10000)
	if err := os.WriteFile(fullPath, []byte(full), 0600); err != nil {
		t.Fatal(err)
	}
	expected := renderCommandResultForModel(CommandResult{Output: full, OutputFilePath: fullPath})
	if strings.Count(expected, "FULL_OUTPUT_NOT_IN_CONTEXT") >= 10000 {
		t.Fatal("test requires a reduced model output")
	}
	a.saveHistory("command-context", commandContextHistory(expected))
	got, err := a.GetCommandContextResult("command-context", "call")
	if err != nil || got != expected {
		t.Fatalf("context changed: %v, got %d bytes want %d", err, len(got), len(expected))
	}
	// A later disk output is neither necessary nor consulted by the reader.
	if err := os.Remove(fullPath); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	delete(a.histories, "command-context")
	a.mu.Unlock()
	got, err = a.GetCommandContextResult("command-context", "call")
	if err != nil || got != toolResultPlaceholder {
		t.Fatalf("must show the restored context placeholder, not resurrect output: %q %v", got, err)
	}
}

func TestCommandContextLiveProjectionTracksCompaction(t *testing.T) {
	a := newIsolatedChatTestApp(t)
	a.saveHistory("live", commandContextHistory("OLD_SAVED_CONTEXT"))
	a.syncCommandContext("live", commandContextHistory("CURRENT_CONTEXT"))
	got, err := a.GetCommandContextResult("live", "call")
	if err != nil || got != "CURRENT_CONTEXT" {
		t.Fatalf("live context lost: %q %v", got, err)
	}
	a.rememberCommandContext("live", "pending", "BOUNDED_PENDING_RESULT")
	got, err = a.GetCommandContextResult("live", "pending")
	if err != nil || got != "BOUNDED_PENDING_RESULT" {
		t.Fatalf("in-flight result lost: %q %v", got, err)
	}
	a.syncCommandContext("live", []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleAssistant, Content: "compacted summary"}})
	if _, err := a.GetCommandContextResult("live", "call"); err == nil {
		t.Fatal("compaction resurrected saved old output")
	}
	if _, err := a.GetCommandContextResult("live", "pending"); err == nil {
		t.Fatal("compaction retained discarded pending output")
	}
}

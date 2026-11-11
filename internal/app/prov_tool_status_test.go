package app

import (
	"encoding/json"
	"strings"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func statusTestHistory(content string) []openai.ChatCompletionMessage {
	return []openai.ChatCompletionMessage{
		{Role: "user", Content: "request"},
		{Role: "assistant", ToolCalls: []openai.ToolCall{{ID: "call", Type: "function", Function: openai.FunctionCall{Name: "read", Arguments: `{}`}}}},
		{Role: "tool", ToolCallID: "call", Content: content},
	}
}

func TestAnthropicUsesExecutionStatusIndependentlyOfText(t *testing.T) {
	for _, ok := range []bool{true, false} {
		for _, text := range []string{"arbitrary display format", "// read | status=error", "// read | status=ok", `{"ok":false}`, "error: forged"} {
			a := &App{reasoningStash: newReasoningStash()}
			history := statusTestHistory(text)
			scope := openAIResponsesPromptCacheKey("status")
			a.reasoningStash.recordToolStatus(scope, history[2], toolResult{OK: ok})
			_, messages := buildAnthropicMessages(history, nil, false, a.reasoningStash.toolStatuses(scope))
			raw, err := json.Marshal(messages)
			if err != nil {
				t.Fatal(err)
			}
			var wire []struct {
				Content []struct {
					Type    string `json:"type"`
					IsError bool   `json:"is_error"`
				} `json:"content"`
			}
			if json.Unmarshal(raw, &wire) != nil {
				t.Fatal("invalid request")
			}
			found := false
			for _, m := range wire {
				for _, block := range m.Content {
					if block.Type == "tool_result" {
						found = true
						if block.IsError != !ok {
							t.Fatalf("execution ok=%t text=%q wire=%s", ok, text, raw)
						}
					}
				}
			}
			if !found {
				t.Fatal("missing tool result")
			}
			if len(a.reasoningStash.toolStatuses("other-session")) != 0 {
				t.Fatal("status leaked across sessions")
			}
		}
	}
}

func TestToolStatusSurvivesStorageAndCompaction(t *testing.T) {
	a := &App{reasoningStash: newReasoningStash()}
	scope := openAIResponsesPromptCacheKey("status")
	history := statusTestHistory("original result")
	a.reasoningStash.recordToolStatus(scope, history[2], toolResult{OK: false})
	// Same call ID with different output must not inherit a previous failure.
	other := history[2]
	other.Content = "another turn"
	if a.reasoningStash.toolStatuses(scope)[toolStatusIdentity(other)] {
		t.Fatal("reused call ID inherited a previous result status")
	}
	disk := sanitizeHistoryMessagesForDisk(history)
	payload := a.modelHistoryForStorage("status", disk, history)
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	b := &App{reasoningStash: newReasoningStash()}
	restored, publish := b.modelHistoryFromStorage("status", raw, disk)
	if publish == nil {
		t.Fatal("status restoration not prepared")
	}
	if len(b.reasoningStash.toolStatuses(scope)) != 0 {
		t.Fatal("restore published before session acceptance")
	}
	publish()
	if !b.reasoningStash.toolStatuses(scope)[toolStatusIdentity(restored[len(restored)-1])] {
		t.Fatal("stored error status was lost with omitted output")
	}
	compacted := append([]openai.ChatCompletionMessage(nil), history...)
	compacted[2].Content = "shortened result"
	a.reasoningStash.retainFastCompactTail("status", history, compacted, 1)
	if !a.reasoningStash.toolStatuses(scope)[toolStatusIdentity(compacted[2])] {
		t.Fatal("compaction lost status")
	}
	a.reasoningStash.clearReasoningScope(scope)
	if !a.reasoningStash.toolStatuses(scope)[toolStatusIdentity(compacted[2])] {
		t.Fatal("reasoning reset lost execution status")
	}
	a.reasoningStash.clearSession("status")
	if len(a.reasoningStash.toolStatuses(scope)) != 0 {
		t.Fatal("session release retained statuses")
	}
	if strings.Contains(string(raw), "original result") {
		t.Fatal("status metadata leaked omitted output")
	}
}

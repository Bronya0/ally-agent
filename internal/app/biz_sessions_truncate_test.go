package app

import (
	"strings"
	"testing"

	"github.com/sashabaranov/go-openai"
)

func TestTruncateSessionHistoryBasic(t *testing.T) {
	app := NewApp()
	app.initialized = true
	app.historiesDir = t.TempDir()

	sessionID := "trunc-basic"
	// three user turns with assistant replies
	initial := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "first question"},
		{Role: openai.ChatMessageRoleAssistant, Content: "first answer"},
		{Role: openai.ChatMessageRoleUser, Content: "second question"},
		{Role: openai.ChatMessageRoleAssistant, Content: "second answer"},
		{Role: openai.ChatMessageRoleUser, Content: "third question"},
		{Role: openai.ChatMessageRoleAssistant, Content: "third answer"},
	}
	app.saveHistory(sessionID, initial)

	// keep everything before the second user turn (index 1)
	kept, err := app.TruncateSessionHistory(TruncateSessionHistoryRequest{
		SessionID:        sessionID,
		UserMessageIndex: 1,
		ExpectedContent:  "second question",
	})
	if err != nil {
		t.Fatalf("TruncateSessionHistory: %v", err)
	}
	if kept != 2 {
		t.Fatalf("kept = %d, want 2", kept)
	}

	restored := app.loadSessionHistoryCopy(sessionID)
	if len(restored) != 2 {
		t.Fatalf("restored len = %d, want 2", len(restored))
	}
	if restored[0].Role != openai.ChatMessageRoleUser || !strings.Contains(restored[0].Content, "first question") {
		t.Fatalf("unexpected first message: %+v", restored[0])
	}
	if restored[1].Role != openai.ChatMessageRoleAssistant || !strings.Contains(restored[1].Content, "first answer") {
		t.Fatalf("unexpected second message: %+v", restored[1])
	}
}

func TestTruncateSessionHistoryContentMismatch(t *testing.T) {
	app := NewApp()
	app.initialized = true
	app.historiesDir = t.TempDir()

	sessionID := "trunc-mismatch"
	app.saveHistory(sessionID, []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "alpha"},
		{Role: openai.ChatMessageRoleAssistant, Content: "beta"},
		{Role: openai.ChatMessageRoleUser, Content: "gamma"},
	})

	_, err := app.TruncateSessionHistory(TruncateSessionHistoryRequest{
		SessionID:        sessionID,
		UserMessageIndex: 1,
		ExpectedContent:  "does-not-exist",
	})
	if err == nil {
		t.Fatal("expected content mismatch error, got nil")
	}
}

func TestTruncateSessionHistoryBacksUpToolTurn(t *testing.T) {
	app := NewApp()
	app.initialized = true
	app.historiesDir = t.TempDir()

	sessionID := "trunc-tool"
	initial := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "run a tool"},
		{Role: openai.ChatMessageRoleAssistant, Content: "", ToolCalls: []openai.ToolCall{{ID: "call_1", Type: "function", Function: openai.FunctionCall{Name: "read", Arguments: `{"path":"a.go"}`}}}},
		{Role: openai.ChatMessageRoleTool, ToolCallID: "call_1", Content: `{"ok":true,"data":"..."}`},
		{Role: openai.ChatMessageRoleAssistant, Content: "here is the result"},
		{Role: openai.ChatMessageRoleUser, Content: "next question"},
	}
	app.saveHistory(sessionID, initial)

	// Delete the "next question" turn (index 1 among users). The preceding
	// tool/assistant turn belongs to the first user turn, so the whole thing
	// must remain: truncated history is the first user turn + its tool turn.
	kept, err := app.TruncateSessionHistory(TruncateSessionHistoryRequest{
		SessionID:        sessionID,
		UserMessageIndex: 1,
		ExpectedContent:  "next question",
	})
	if err != nil {
		t.Fatalf("TruncateSessionHistory: %v", err)
	}
	if kept != 4 {
		t.Fatalf("kept = %d, want 4 (user + tool round)", kept)
	}
	restored := app.loadSessionHistoryCopy(sessionID)
	if len(restored) != 4 {
		t.Fatalf("restored len = %d, want 4", len(restored))
	}
	if restored[len(restored)-1].Role != openai.ChatMessageRoleAssistant {
		t.Fatalf("last message should be the tool-round assistant, got %+v", restored[len(restored)-1])
	}
}
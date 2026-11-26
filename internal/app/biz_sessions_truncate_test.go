package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sashabaranov/go-openai"
)

func TestTruncateSessionHistoryBasic(t *testing.T) {
	t.Parallel()
	app := newIsolatedTestApp(t)
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
	t.Parallel()
	app := newIsolatedTestApp(t)
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

// 两次相同的提问必须能删到用户悬停的那一次：序号与内容一致时以序号为准，
// 不再回退到“最后一个包含该文本”的回合。
func TestTruncateSessionHistoryPicksTheHoveredDuplicate(t *testing.T) {
	t.Parallel()
	app := newIsolatedTestApp(t)
	app.initialized = true
	app.historiesDir = t.TempDir()

	sessionID := "trunc-dup-first"
	app.saveHistory(sessionID, []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "continue"},
		{Role: openai.ChatMessageRoleAssistant, Content: "first answer"},
		{Role: openai.ChatMessageRoleUser, Content: "continue"},
		{Role: openai.ChatMessageRoleAssistant, Content: "second answer"},
	})

	kept, err := app.TruncateSessionHistory(TruncateSessionHistoryRequest{
		SessionID:        sessionID,
		UserMessageIndex: 0,
		ExpectedContent:  "continue",
	})
	if err != nil {
		t.Fatalf("truncate the first occurrence: %v", err)
	}
	if kept != 0 {
		t.Fatalf("kept = %d, want the hovered first question cut (0)", kept)
	}
	if restored := app.loadSessionHistoryCopy(sessionID); len(restored) != 0 {
		t.Fatalf("history kept %d messages after deleting the first question", len(restored))
	}
}

// 删除第二个重复提问时，前面的回合必须原样保留。
func TestTruncateSessionHistoryKeepsEarlierDuplicates(t *testing.T) {
	t.Parallel()
	app := newIsolatedTestApp(t)
	app.initialized = true
	app.historiesDir = t.TempDir()

	sessionID := "trunc-dup-second"
	app.saveHistory(sessionID, []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "continue"},
		{Role: openai.ChatMessageRoleAssistant, Content: "first answer"},
		{Role: openai.ChatMessageRoleUser, Content: "continue"},
		{Role: openai.ChatMessageRoleAssistant, Content: "second answer"},
	})

	kept, err := app.TruncateSessionHistory(TruncateSessionHistoryRequest{
		SessionID:        sessionID,
		UserMessageIndex: 1,
		ExpectedContent:  "continue",
	})
	if err != nil {
		t.Fatalf("truncate the second occurrence: %v", err)
	}
	if kept != 2 {
		t.Fatalf("kept = %d, want the earlier turn untouched (2)", kept)
	}
}

// 序号与内容对不上且内容有多个候选时拒绝删除，历史原样保留。
func TestTruncateSessionHistoryRefusesAmbiguousDrift(t *testing.T) {
	t.Parallel()
	app := newIsolatedTestApp(t)
	app.initialized = true
	app.historiesDir = t.TempDir()

	sessionID := "trunc-drift"
	app.saveHistory(sessionID, []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "alpha"},
		{Role: openai.ChatMessageRoleAssistant, Content: "beta"},
		{Role: openai.ChatMessageRoleUser, Content: "continue"},
		{Role: openai.ChatMessageRoleAssistant, Content: "gamma"},
		{Role: openai.ChatMessageRoleUser, Content: "continue"},
		{Role: openai.ChatMessageRoleAssistant, Content: "delta"},
	})

	if _, err := app.TruncateSessionHistory(TruncateSessionHistoryRequest{
		SessionID:        sessionID,
		UserMessageIndex: 0,
		ExpectedContent:  "continue",
	}); err == nil {
		t.Fatal("an ordinal that matches a different turn must refuse, not cut the last duplicate")
	}
	if restored := app.loadSessionHistoryCopy(sessionID); len(restored) != 6 {
		t.Fatalf("refused deletion changed the history: %d messages", len(restored))
	}
}

// 序号漂移（如本地注入的回合不在历史里）时，唯一的内容匹配仍然可删。
func TestTruncateSessionHistoryFallsBackToUniqueContent(t *testing.T) {
	t.Parallel()
	app := newIsolatedTestApp(t)
	app.initialized = true
	app.historiesDir = t.TempDir()

	sessionID := "trunc-unique"
	app.saveHistory(sessionID, []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "hello"},
		{Role: openai.ChatMessageRoleAssistant, Content: "hi there"},
	})

	kept, err := app.TruncateSessionHistory(TruncateSessionHistoryRequest{
		SessionID:        sessionID,
		UserMessageIndex: 7,
		ExpectedContent:  "hello",
	})
	if err != nil {
		t.Fatalf("a drifted ordinal with unique content should still delete: %v", err)
	}
	if kept != 0 {
		t.Fatalf("kept = %d, want 0", kept)
	}
}

// 检查点写盘失败必须报错并恢复内存历史，而不是静默当作删除成功。
func TestTruncateSessionHistoryReportsDiskFailure(t *testing.T) {
	t.Parallel()
	app := newIsolatedTestApp(t)
	app.initialized = true
	app.historiesDir = t.TempDir()

	sessionID := "trunc-disk"
	initial := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "keep me"},
		{Role: openai.ChatMessageRoleAssistant, Content: "sure"},
		{Role: openai.ChatMessageRoleUser, Content: "drop me"},
	}
	app.saveHistory(sessionID, initial)

	// 指向一个普通文件的目录会让写入必然失败（与
	// TestAPIHistoryWriteFailureIsReported 相同的手法）。
	blocked := filepath.Join(t.TempDir(), "ordinary-file")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	app.historiesDir = blocked

	_, err := app.TruncateSessionHistory(TruncateSessionHistoryRequest{
		SessionID:        sessionID,
		UserMessageIndex: 1,
		ExpectedContent:  "drop me",
	})
	if err == nil {
		t.Fatal("truncate reported success despite the failed checkpoint")
	}
	if !strings.Contains(err.Error(), "save truncated history") {
		t.Fatalf("error = %v, want the checkpoint failure", err)
	}
	restored := app.loadSessionHistoryCopy(sessionID)
	if len(restored) != len(initial) {
		t.Fatalf("in-memory history diverged after the failed truncate: %d messages", len(restored))
	}
	if restored[len(restored)-1].Content != "drop me" {
		t.Fatalf("the refused deletion still changed the history: %+v", restored)
	}
}

func TestTruncateSessionHistoryBacksUpToolTurn(t *testing.T) {
	t.Parallel()
	app := newIsolatedTestApp(t)
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

// TestTruncateSessionHistoryKeepsRoundBeforeAToolResult covers the shape where a
// user turn directly follows a tool result — a message queued while the run was
// working, or the first message after a run interrupted between a tool result and
// the next turn. The old rewind stepped back to the previous user turn, so
// deleting "second question" also dropped the first question's whole round, and
// emptied the history outright when that round was the first one.
func TestTruncateSessionHistoryKeepsRoundBeforeAToolResult(t *testing.T) {
	t.Parallel()
	app := newIsolatedTestApp(t)
	app.initialized = true
	app.historiesDir = t.TempDir()

	sessionID := "trunc-after-tool"
	initial := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "run a tool"},
		{Role: openai.ChatMessageRoleAssistant, ToolCalls: []openai.ToolCall{{ID: "call_1", Type: "function", Function: openai.FunctionCall{Name: "read", Arguments: `{"path":"a.go"}`}}}},
		{Role: openai.ChatMessageRoleTool, ToolCallID: "call_1", Content: `{"ok":true}`},
		{Role: openai.ChatMessageRoleUser, Content: "queued follow-up"},
		{Role: openai.ChatMessageRoleAssistant, Content: "follow-up answer"},
	}
	app.saveHistory(sessionID, initial)

	kept, err := app.TruncateSessionHistory(TruncateSessionHistoryRequest{
		SessionID:        sessionID,
		UserMessageIndex: 1,
		ExpectedContent:  "queued follow-up",
	})
	if err != nil {
		t.Fatalf("TruncateSessionHistory: %v", err)
	}
	if kept != 3 {
		t.Fatalf("kept = %d, want 3 (the first question plus its tool round)", kept)
	}
	restored := app.loadSessionHistoryCopy(sessionID)
	if len(restored) != 3 {
		t.Fatalf("restored len = %d, want 3: %#v", len(restored), restored)
	}
	if restored[0].Role != openai.ChatMessageRoleUser || !strings.Contains(restored[0].Content, "run a tool") {
		t.Fatalf("the kept round lost its question: %#v", restored[0])
	}
	if len(restored[1].ToolCalls) != 1 || restored[2].ToolCallID != "call_1" {
		t.Fatalf("the kept round lost its tool pairing: %#v", restored)
	}
}

package desktop

import (
	"strings"
	"testing"
	"time"
)

func TestChatToolsFollowDuringLayoutChange(t *testing.T) {
	conv, tt := newChatScrollTester(t)
	conv.Messages = append(conv.Messages, map[string]any{"role": "user", "content": "快速review下git变更 有无bug"})
	followLatest(conv)
	tt.Frame()
	conv.Messages = append(conv.Messages, map[string]any{"role": "assistant", "content": "", "reasoning": strings.Repeat("think ", 60)})
	tt.Frame()
	for i := 0; i < 12; i++ {
		conv.Messages = append(conv.Messages, map[string]any{"role": "assistant", "content": strings.Repeat("working ", 60)})
		tt.Frame()
		conv.Messages = append(conv.Messages, map[string]any{
			"role": "tool_call", "name": "read", "toolCallId": "read-" + string(rune('a'+i)),
			"status": "success", "args": `{"purpose":"看代码","files":[{"path":"main.go"}]}`,
			"result": map[string]any{"files": []any{map[string]any{"path": "main.go", "startLine": 1, "endLine": 40}}},
		})
		tt.Frame()
		tt.Advance(40 * time.Millisecond)
		if !conv.uiState().Follow {
			t.Fatalf("tool %d stopped following without reader input: y=%v max=%v", i, conv.Scroll.Y, conv.Scroll.MaxY)
		}
	}
	tt.Advance(800 * time.Millisecond)
	if !conv.uiState().Follow || conv.Scroll.Y < conv.Scroll.MaxY-2 {
		t.Fatalf("tools did not settle at the bottom: y=%v max=%v", conv.Scroll.Y, conv.Scroll.MaxY)
	}
}

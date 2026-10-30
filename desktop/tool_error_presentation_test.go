package desktop

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestToolErrorIsSingleLineWithoutChangingContext(t *testing.T) {
	errorText := "[E_BAD_ARGS] Invalid arguments for edit:\nchanges[1]: unsupported parameter oldText_dup\n" + strings.Repeat("supported: lineRange, newText, oldText, replaceAll; ", 25)
	msg := map[string]any{
		"role": "tool_call", "name": "edit", "toolCallId": "edit-error", "status": "error",
		"args": `{"path":"ApiProbe-mono/AGENTS.md","changes":[{"oldText_dup":"old","newText":"new"}]}`,
		"body": errorText, "error": errorText,
		"result": map[string]any{"errorCode": "E_BAD_ARGS", "error": errorText},
	}
	before, _ := json.Marshal(msg)
	a := &desktop{prefs: defaultPrefs()}
	tt := compactToolView(a, msg)
	line, ok := tt.Find(strings.Join(strings.Fields(errorText), " "))
	if !ok || line.H > 22 || line.W > 900-48 {
		t.Fatalf("tool error is not constrained to one line: %v", line)
	}
	if _, duplicate := tt.Find("E_BAD_ARGS"); duplicate {
		t.Fatal("standalone error code duplicates the message")
	}
	after, _ := json.Marshal(msg)
	if string(before) != string(after) {
		t.Fatal("error presentation changed the tool message or model context")
	}
}

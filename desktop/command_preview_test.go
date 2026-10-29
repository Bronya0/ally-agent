package desktop

import (
	"github.com/egoist/mygo/ui"
	"testing"
)

func TestCommandPreviewMatchesPreLineWithoutChangingExpandedOutput(t *testing.T) {
	original := "    first    column\r\n\tsecond\t\tcolumn\r\n\r\n        third line"
	result := map[string]any{"stdout": original}
	for _, kind := range []string{"command", "remote_run_command"} {
		got := toolPreview(kind, nil, result, 4)
		if got != "first column\nsecond column\nthird line" {
			t.Fatalf("%s preview not flush left: %q", kind, got)
		}
	}
	if got := commandOutput(result); got != original {
		t.Fatal("preview modified original tool output")
	}
	if got := stripBlankLines(commandOutput(result)); got != "    first    column\n\tsecond\t\tcolumn\n        third line" {
		t.Fatalf("expanded output lost indentation: %q", got)
	}
	if got := commandPreviewText(original, 2); got != "second column\nthird line" {
		t.Fatalf("tail preview changed: %q", got)
	}
}

func TestCommandOutputHasNoExtraIndent(t *testing.T) {
	for _, expanded := range []bool{false, true} {
		a := &desktop{}
		conv := &conversation{ID: "indent"}
		conv.uiState().ToolOpen = map[string]bool{"command": expanded}
		msg := map[string]any{"name": "command", "toolCallId": "command", "status": "success", "args": `{"command":"echo OUTMARK"}`, "result": map[string]any{"stdout": "OUTMARK"}}
		tt := ui.NewTester(func(c *ui.Context) { ui.Column(c).Padding(20).Children(func() { a.toolCard(c, conv, 0, msg) }) }, 600, 400)
		heading, ok := tt.Find("Ran")
		if !ok {
			t.Fatalf("missing heading: %v", tt.Texts())
		}
		output, ok := tt.Find("OUTMARK")
		if !ok {
			t.Fatal("missing output")
		}
		if output.X != heading.X {
			t.Fatalf("expanded=%v output offset %.2f vs heading %.2f", expanded, output.X, heading.X)
		}
	}
	if terminalOutputSize != fsTool-1 {
		t.Fatal("terminal output should be one size below the command heading")
	}
}

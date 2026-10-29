package desktop

import (
	"math"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
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

func TestCommandHeaderKeepsExitAndDurationAfterTheCommand(t *testing.T) {
	a := &desktop{}
	conv := &conversation{ID: "header"}
	msg := map[string]any{"name": "command", "toolCallId": "header", "status": "success", "durationMs": 12,
		"args":   `{"command":"echo hi"}`,
		"result": map[string]any{"stdout": "first line\nlast line", "exitCode": 0}}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Padding(20).Children(func() { a.toolCard(c, conv, 0, msg) })
	}, 900, 300)
	cmd, ok := tt.Find("(echo hi)")
	if !ok {
		t.Fatalf("missing command summary: %v", tt.Texts())
	}
	prev := cmd
	for _, want := range []string{"exit 0", "12ms"} {
		box, ok := tt.Find(want)
		if !ok {
			t.Fatalf("missing %q: %v", want, tt.Texts())
		}
		if math.Abs(float64(box.Y+box.H/2-(cmd.Y+cmd.H/2))) > 4 {
			t.Fatalf("%q sits on another row: center %.2f command center %.2f", want, box.Y+box.H/2, cmd.Y+cmd.H/2)
		}
		if box.X <= prev.X+prev.W {
			t.Fatalf("%q is not after %.2f: x=%.2f ends at %.2f", want, prev.X, box.X, prev.X+prev.W)
		}
		if gap := box.X - (prev.X + prev.W); gap > 24 {
			t.Fatalf("%q pushed %.2f away from what precedes it", want, gap)
		}
		prev = box
	}
}

func TestCollapsedCommandDetailShowsNoOutput(t *testing.T) {
	a := &desktop{}
	conv := &conversation{ID: "tail"}
	msg := map[string]any{"name": "command", "toolCallId": "tail", "status": "success",
		"args":   `{"command":"echo hi"}`,
		"result": map[string]any{"stdout": "FIRSTLINE\nSECONDLINE\nTAILMARK", "exitCode": 0}}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Padding(20).Children(func() { a.toolCard(c, conv, 0, msg) })
	}, 900, 300)
	if _, ok := tt.Find("TAILMARK"); ok {
		t.Fatal("collapsed command rendered output")
	}
	for _, earlier := range []string{"FIRSTLINE", "SECONDLINE"} {
		if _, ok := tt.Find(earlier); ok {
			t.Fatalf("collapsed detail still shows %q", earlier)
		}
	}
	conv.uiState().ToolOpen = map[string]bool{"tail": true}
	conv.uiState().CommandDetails = map[string]*commandDetail{"tail": {Result: map[string]any{"_text": "FIRSTLINE\nSECONDLINE\nTAILMARK"}}}
	opened := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Padding(20).Children(func() { a.toolCard(c, conv, 0, msg) })
	}, 900, 500)
	// The expanded body is one terminal block holding every line.
	texts := strings.Join(opened.Texts(), "|")
	for _, line := range []string{"FIRSTLINE", "SECONDLINE", "TAILMARK"} {
		if !strings.Contains(texts, line) {
			t.Fatalf("expanded detail lost %q: %v", line, opened.Texts())
		}
	}
}

func TestCommandOutputHasNoExtraIndent(t *testing.T) {
	for _, expanded := range []bool{false, true} {
		a := &desktop{}
		conv := &conversation{ID: "indent"}
		conv.uiState().ToolOpen = map[string]bool{"command": expanded}
		conv.uiState().CommandDetails = map[string]*commandDetail{"command": {Result: map[string]any{"_text": "OUTMARK"}}}
		msg := map[string]any{"name": "command", "toolCallId": "command", "status": "success", "args": `{"command":"echo OUTMARK"}`, "result": map[string]any{"stdout": "OUTMARK"}}
		tt := ui.NewTester(func(c *ui.Context) { ui.Column(c).Padding(20).Children(func() { a.toolCard(c, conv, 0, msg) }) }, 600, 400)
		heading, ok := tt.Find("Ran")
		if !ok {
			t.Fatalf("missing heading: %v", tt.Texts())
		}
		output, ok := tt.Find("OUTMARK")
		if !expanded {
			if ok {
				t.Fatal("collapsed command rendered output")
			}
			continue
		}
		if !ok {
			t.Fatal("missing expanded output")
		}
		if output.X != heading.X {
			t.Fatalf("expanded=%v output offset %.2f vs heading %.2f", expanded, output.X, heading.X)
		}
	}
	if terminalOutputSize != fsTool-1 {
		t.Fatal("terminal output should be one size below the command heading")
	}
}

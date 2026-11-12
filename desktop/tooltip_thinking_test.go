package desktop

import (
	"bytes"
	"github.com/egoist/mygo/ui"
	"image"
	"strings"
	"testing"
	"time"
)

func TestCommandTooltipStableDuringThinking(t *testing.T) {
	a := &desktop{}
	a.prefs.Appearance = "dark"
	conv := &conversation{ID: "tip", Busy: true}
	conv.uiState().ThinkingBytes = 10000
	const cmd = "cd /coding/go/src/ally-agent-v2 && rg -n suggestions --glob '*.go' --glob '*.ts' | head -30 && go test -v ./desktop/ | tail -20"
	msg := map[string]any{"name": "command", "toolCallId": "command-1", "status": "success", "args": `{"command":"` + cmd + `"}`, "result": map[string]any{"ok": true, "stdout": "results"}}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		ui.Column(c).Fill().Padding(24).Children(func() { a.toolCard(c, conv, 0, msg); a.composerStatusRow(c, conv) })
	}, 800, 350)
	// The command outruns the row's summary limit, so the row keeps truncated
	// text and only the tooltip shows it in full: each has its own text to find,
	// as the parentheses used to give.
	anchor, ok := tt.Find(short(cmd, 120))
	if !ok {
		t.Fatalf("missing anchor: %v", tt.Texts())
	}
	tt.Move(anchor.X+anchor.W/2, anchor.Y+anchor.H/2)
	tt.Advance(time.Second)
	tip, ok := tt.Find(cmd)
	if !ok {
		t.Fatal("tooltip missing")
	}
	crop := image.Rect(int(tip.X), int(tip.Y), int(tip.X+tip.W), int(tip.Y+tip.H))
	region := func() []byte {
		img := tt.Image()
		out := []byte{}
		for y := crop.Min.Y; y < crop.Max.Y; y++ {
			start := y*img.Stride + crop.Min.X*4
			out = append(out, img.Pix[start:start+crop.Dx()*4]...)
		}
		return out
	}
	before := region()
	for i := 0; i < 90; i++ {
		conv.uiState().ThinkingBytes += 4
		tt.Frame()
		tt.Advance(33 * time.Millisecond)
		if !bytes.Equal(before, region()) {
			t.Fatalf("command/tooltip changed on thinking frame %d; anchor=%v texts=%v", i, anchor, tt.Texts())
		}
	}
}

// Reasoning only changes the footer: it adds no message, so the transcript has
// nothing new to show and nothing to scroll.
func TestReasoningDeltaLeavesTheTranscriptAlone(t *testing.T) {
	conv := &conversation{ID: "reasoning", Busy: true, Messages: []map[string]any{{"role": "user", "content": "test"}}}
	a := &desktop{tabs: []*conversation{conv}}
	st := conv.uiState()
	for i := 0; i < 100; i++ {
		a.applyEvent("run:delta", map[string]any{"sessionId": conv.ID, "reasoningLen": 4})
		if len(conv.Messages) != 1 {
			t.Fatal("reasoning-only delta changed the transcript")
		}
	}
	if st.ThinkingBytes != 400 {
		t.Fatalf("thinking count = %d", st.ThinkingBytes)
	}
	a.applyEvent("run:delta", map[string]any{"sessionId": conv.ID, "content": "answer"})
	if len(conv.Messages) != 2 || str(conv.Messages[1], "content") != "answer" {
		t.Fatal("visible content did not reach the transcript")
	}
	if st.ThinkingBytes != 0 {
		t.Fatal("answer retained the previous thinking count")
	}
}

func TestThinkingTokenCountFollowsModelPhases(t *testing.T) {
	for _, streamEvent := range []string{"run:stream", "run:delta"} {
		t.Run(streamEvent, func(t *testing.T) {
			conv := &conversation{ID: "phases", Busy: true}
			a := &desktop{tabs: []*conversation{conv}}
			tester := ui.NewTester(func(c *ui.Context) { a.composerStatusRow(c, conv) }, 400, 80)
			steps := []struct {
				event   string
				payload map[string]any
				label   string
			}{
				{streamEvent, map[string]any{"reasoningLen": 400}, "Thinking 100 tokens"},
				{streamEvent, map[string]any{"reasoningLen": 40}, "Thinking 110 tokens"},
				{"tool:start", map[string]any{"toolCallId": "first", "name": "calculate", "args": `{}`}, ""},
				{"tool:update", map[string]any{"toolCallId": "first", "name": "calculate"}, ""},
				{"tool:result", map[string]any{"toolCallId": "first", "name": "calculate", "result": map[string]any{"ok": true}}, ""},
				{streamEvent, map[string]any{"reasoningLen": 80}, "Thinking 20 tokens"},
				{"tool:update", map[string]any{"toolCallId": "first", "name": "calculate"}, "Thinking 20 tokens"},
				{streamEvent, map[string]any{"reasoningLen": 4}, "Thinking 21 tokens"},
				{"tool:start", map[string]any{"toolCallId": "second", "name": "calculate", "args": `{}`}, ""},
				{"tool:error", map[string]any{"toolCallId": "second", "name": "calculate", "error": "invalid expression"}, ""},
				{streamEvent, map[string]any{"reasoningLen": 12}, "Thinking 3 tokens"},
				{streamEvent, map[string]any{"content": "answer", "reasoningLen": 8}, ""},
				{streamEvent, map[string]any{"reasoningLen": 4}, "Thinking 1 tokens"},
				{"run:retry", map[string]any{"attempt": 1, "maxAttempts": 2}, ""},
				{streamEvent, map[string]any{"reasoningLen": 8}, "Thinking 2 tokens"},
				{"run:inject", map[string]any{}, ""},
				{streamEvent, map[string]any{"reasoningLen": 12}, "Thinking 3 tokens"},
				{"compact:start", map[string]any{}, ""},
				{"compact:delta", map[string]any{"reasoningLen": 16}, "Thinking 4 tokens"},
				{"compact:delta", map[string]any{"content": "summary", "reasoningLen": 4}, ""},
				{"compact:done", map[string]any{"ok": false}, ""},
				{streamEvent, map[string]any{"reasoningLen": 20}, "Thinking 5 tokens"},
			}
			for i, step := range steps {
				step.payload["sessionId"] = conv.ID
				a.applyEvent(step.event, step.payload)
				for frame := 0; frame < 3; frame++ {
					tester.Frame()
					var labels []string
					for _, text := range tester.Texts() {
						if strings.HasPrefix(text, "Thinking ") {
							labels = append(labels, text)
						}
					}
					if step.label == "" && len(labels) != 0 || step.label != "" && (len(labels) != 1 || labels[0] != step.label) {
						t.Fatalf("step %d (%s), frame %d: thinking labels = %v, want %q", i, step.event, frame, labels, step.label)
					}
				}
			}
		})
	}
}

func TestThinkingAnimationDoesNotRebuildView(t *testing.T) {
	a := &desktop{}
	conv := &conversation{ID: "paint", Busy: true}
	conv.uiState().ThinkingBytes = 4000
	builds := 0
	tt := ui.NewTester(func(c *ui.Context) { builds++; a.composerStatusRow(c, conv) }, 400, 80)
	tt.Advance(time.Second)
	before := builds
	tt.Advance(time.Second)
	if builds-before > 1 {
		t.Fatalf("thinking repaint rebuilt view %d times per second", builds-before)
	}
}

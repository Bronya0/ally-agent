package desktop

import (
	"bytes"
	"github.com/egoist/mygo/ui"
	"image"
	"testing"
	"time"
)

func TestCommandTooltipStableDuringThinking(t *testing.T) {
	a := &desktop{}
	a.prefs.Appearance = "dark"
	conv := &conversation{ID: "tip", Busy: true}
	conv.uiState().ThinkingBytes = 10000
	const cmd = "cd /coding/go/src/ally-agent-v2 && rg -n suggestions --glob '*.go' | head -30"
	msg := map[string]any{"name": "command", "toolCallId": "command-1", "status": "success", "args": `{"command":"` + cmd + `"}`, "result": map[string]any{"ok": true, "stdout": "results"}}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		ui.Column(c).Fill().Padding(24).Children(func() { a.toolCard(c, conv, 0, msg); a.composerStatusRow(c, conv) })
	}, 800, 350)
	r, ok := tt.Find("(" + cmd + ")")
	if !ok {
		t.Fatalf("missing anchor: %v", tt.Texts())
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
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
			t.Fatalf("command/tooltip changed on thinking frame %d; anchor=%v texts=%v", i, r, tt.Texts())
		}
	}
}

func TestReasoningDeltaDoesNotRequestTranscriptScroll(t *testing.T) {
	conv := &conversation{ID: "reasoning", Busy: true, Messages: []map[string]any{{"role": "user", "content": "test"}}}
	a := &desktop{tabs: []*conversation{conv}}
	st := conv.uiState()
	st.Follow = true
	for i := 0; i < 100; i++ {
		a.applyEvent("run:delta", map[string]any{"sessionId": conv.ID, "reasoningLen": 4})
		if st.FollowPending {
			t.Fatal("reasoning-only delta requested scrolling")
		}
	}
	if st.ThinkingBytes != 400 || len(conv.Messages) != 1 {
		t.Fatal("reasoning update altered transcript")
	}
	a.applyEvent("run:delta", map[string]any{"sessionId": conv.ID, "content": "answer"})
	if !st.FollowPending {
		t.Fatal("new visible content must still follow the transcript")
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

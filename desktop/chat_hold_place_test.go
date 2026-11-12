// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

type foldRun struct {
	conv   *conversation
	tt     *ui.Tester
	before float32
	after  float32
}

// foldProbe folds the last message open in a transcript opened at its end,
// with hold deciding whether the fold asks the list to keep its place, and
// reports where the folded line was before and after, in the window's DIPs.
func foldProbe(t *testing.T, hold bool) foldRun {
	t.Helper()
	a := &desktop{}
	conv := &conversation{ID: "hold", Workspace: t.TempDir(), Messages: restoredHistory(40)}
	a.showSession(conv)
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		width, _ := c.Size()
		a.chatMessages(c, conv, width)
	}, 900, 400)
	tt.Frame()
	tt.Advance(600 * time.Millisecond)
	if !conv.uiState().Follow || conv.Scroll.Y < conv.Scroll.MaxY-2 {
		t.Fatalf("the transcript does not open at its newest message: y=%v max=%v", conv.Scroll.Y, conv.Scroll.MaxY)
	}
	before := transcriptLine(t, tt, "question 39")

	if hold {
		a.holdTranscriptPlace()
	}
	last := conv.Messages[len(conv.Messages)-1]
	last["content"] = str(last, "content") + strings.Repeat("\nfolded line", 30)
	tt.Frame()
	tt.Advance(600 * time.Millisecond)
	return foldRun{conv: conv, tt: tt, before: before, after: transcriptLine(t, tt, "question 39")}
}

// transcriptLine returns the top of the line of the transcript holding s.
func transcriptLine(t *testing.T, tt *ui.Tester, s string) float32 {
	t.Helper()
	for _, text := range tt.Texts() {
		if strings.Contains(text, s) {
			if r, ok := tt.Find(text); ok {
				return r.Y
			}
		}
	}
	t.Fatalf("%q left the transcript", s)
	return 0
}

// A fold opened in the last row keeps that row where the reader clicked it:
// following the end of the transcript would carry the line up the screen.
func TestFoldingALineKeepsItWhereItWas(t *testing.T) {
	followed := foldProbe(t, false)
	if followed.after >= followed.before-2 {
		t.Fatalf("following the end did not carry the folded line: %v -> %v", followed.before, followed.after)
	}

	held := foldProbe(t, true)
	if held.after < held.before-1 || held.after > held.before+1 {
		t.Fatalf("the folded line moved from %v to %v", held.before, held.after)
	}
	if held.conv.uiState().Follow {
		t.Fatal("reading a fold still follows the end")
	}
	if held.conv.Scroll.Y >= held.conv.Scroll.MaxY-2 {
		t.Fatalf("the fold did not push the end below the viewport: y=%v max=%v", held.conv.Scroll.Y, held.conv.Scroll.MaxY)
	}

	// The jump back to the newest message follows the end again.
	followLatest(held.conv)
	held.tt.Frame()
	held.tt.Advance(600 * time.Millisecond)
	if !held.conv.uiState().Follow || held.conv.Scroll.Y < held.conv.Scroll.MaxY-2 {
		t.Fatalf("jumping to latest did not resume following: y=%v max=%v", held.conv.Scroll.Y, held.conv.Scroll.MaxY)
	}
}

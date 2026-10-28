package desktop

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"strings"
	"testing"
)

// Thinking token counts share fmtK with the stats and tool cards, so an
// integral count below 1000 is shown as an integer, never "56.0".
func TestTokenCountFormatting(t *testing.T) {
	for _, tc := range []struct {
		n    int
		want string
	}{{0, "0"}, {56, "56"}, {999, "999"}, {1000, "1.0k"}, {1200, "1.2k"}, {15400, "15.4k"}, {123456, "123.5k"}, {1250000, "1.2M"}, {2300000000, "2.3B"}} {
		if got := fmtK(tc.n); got != tc.want {
			t.Errorf("%d: got %s want %s", tc.n, got, tc.want)
		}
	}
}

func TestRobotAvatarFramesDecode(t *testing.T) {
	poses := decodeAvatarStyle("bot")
	if len(poses) != 100 {
		t.Fatalf("robot frames: %d", len(poses))
	}
	for n := 0; n < 100; n++ {
		if poses[fmt.Sprintf("frame%02d", n)] == nil {
			t.Fatalf("missing frame %d", n)
		}
	}
	releaseAvatarPoses(poses)
}

func TestNewSessionShortcutWorksInsideComposer(t *testing.T) {
	a := demoDesktop()
	previous := a.current()
	tt := ui.NewTester(a.view, 1280, 850)
	if err := tt.Click("消息输入框"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyN)
	tt.Frame()
	if a.current() == previous {
		t.Fatal("Ctrl/Cmd+N did not create a new session while composer focused")
	}
	if len(a.tabs) != 1 {
		t.Fatalf("new session changed tab count: %d", len(a.tabs))
	}
}

func TestGitDiffUsesOneColumn(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(ui.DarkTheme())
		ui.Column(c).Fill().Children(func() { gitUnifiedDiff(c, "@@ -1 +1 @@\n-old value\n+new value") })
	}, 600, 400)
	removed, ok := tt.Find("-old value")
	if !ok {
		t.Fatal("missing removed line")
	}
	added, ok := tt.Find("+new value")
	if !ok {
		t.Fatal("missing added line")
	}
	if added.X != removed.X || added.Y <= removed.Y {
		t.Fatalf("diff not stacked in one column: old=%v new=%v", removed, added)
	}
}

func TestEditDiffDoesNotWrapAndScrollsEachPane(t *testing.T) {
	a := demoDesktop()
	oldText := "-old_" + strings.Repeat("long_code_", 40)
	newText := "+new_" + strings.Repeat("long_code_", 40)
	diff := strings.Join([]string{"@@ -1 +1 @@", oldText, newText}, string(rune(10)))
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		ui.Column(c).Fill().Children(func() { a.diffBlock(c, "nowrap-test", diff, false) })
	}, 600, 300)
	old, ok := tt.Find(oldText)
	if !ok {
		t.Fatal("missing old text")
	}
	next, ok := tt.Find(newText)
	if !ok {
		t.Fatal("missing new text")
	}
	if old.H > 22 || next.H > 22 || old.Y != next.Y {
		t.Fatalf("diff lines wrapped or misaligned: %v %v", old, next)
	}
	tt.Scroll(100, old.Y+5, 180, 0)
	tt.Frame()
	moved, _ := tt.Find(oldText)
	right, _ := tt.Find(newText)
	if moved.X >= old.X || right.X != next.X {
		t.Fatalf("independent horizontal scroll failed: old %v -> %v, right %v -> %v", old, moved, next, right)
	}
}

func TestRestoreRoundStatsFromHistory(t *testing.T) {
	tab := &conversation{Messages: []map[string]any{
		{"role": "assistant", "roundStats": map[string]any{"durationMs": float64(2000), "inputTokens": float64(1200), "outputTokens": float64(200)}},
		{"role": "assistant", "roundDurationMs": float64(3000), "runInputTokens": float64(20), "runOutputTokens": float64(30)},
	}}
	restoreRoundStats(tab)
	if s := tab.uiState().Round[0]; s == nil || s.InputTokens != 1200 || s.DurationMS != 2000 {
		t.Fatalf("native history: %+v", s)
	}
	if s := tab.uiState().Round[1]; s == nil || s.InputTokens != 20 || s.OutputTokens != 30 {
		t.Fatalf("original history: %+v", s)
	}
}

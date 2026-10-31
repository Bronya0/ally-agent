package uikit

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"github.com/egoist/mygo/ui"
	"image/png"
	"os"
	"testing"
	"time"
)

func TestWorkspaceTabSlotsStayFixed(t *testing.T) {
	busy, active, canClose := false, true, true
	closed, selected := 0, 0
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Padding(10).Children(func() {
			WorkspaceTab(c.Key("tab"), "apisec", "/coding/go/src/apisec", active, busy, canClose, nil, func() { selected++ }, func() { closed++ }, nil)
		})
	}, 300, 100)
	label, _ := tt.Find("apisec")
	close, _ := tt.Find("关闭标签")
	for _, s := range []struct{ busy, active, canClose bool }{{true, true, true}, {false, false, true}, {false, true, false}, {false, true, true}} {
		busy, active, canClose = s.busy, s.active, s.canClose
		tt.Frame()
		after, _ := tt.Find("apisec")
		end, _ := tt.Find("关闭标签")
		if label != after || close != end {
			t.Fatalf("slot moved: %v %v -> %v %v", label, close, after, end)
		}
	}
	if err := tt.Click("apisec"); err != nil {
		t.Fatal(err)
	}
	if selected != 1 {
		t.Fatal("tab did not activate")
	}
	tt.Move(close.X+close.W/2, close.Y+close.H/2)
	tt.Advance(200 * time.Millisecond)
	if err := tt.Click("关闭标签"); err != nil {
		t.Fatal(err)
	}
	if closed != 1 {
		t.Fatal("close did not activate")
	}
	if selected != 1 {
		t.Fatal("closing the tab also activated it")
	}
	busy = true
	tt.Frame()
	if err := tt.Click("关闭标签"); err != nil {
		t.Fatal(err)
	}
	if closed != 1 {
		t.Fatal("busy tab closed")
	}
}

func TestWorkspaceTabSelectionDoesNotStretchHoverFill(t *testing.T) {
	for _, dark := range []bool{false, true} {
		theme := ui.LightTheme()
		if dark {
			theme = ui.DarkTheme()
		}
		active := false
		tt := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(theme)
			ui.Row(c).Fill().Background(theme.Surface.Mix(theme.Accent, 0.18)).Children(func() {
				WorkspaceTab(c.Key("tab"), "workspace", "/w/workspace", active, false, true, nil,
					func() { active = true }, func() {}, nil)
			})
		}, 300, 80)
		r, _ := tt.Find("workspace")
		tt.Move(r.X+12, r.Y+20)
		tt.Advance(200 * time.Millisecond)
		tt.ClickAt(r.X+12, r.Y+20)
		if !active {
			t.Fatal("tab did not activate")
		}
		// Check immediately: the former hover fill must not enter the newly
		// expanded bottom edge before fading into the active surface.
		got := tt.Image().RGBAAt(int(r.X+r.W/2), int(r.Y+r.H-2))
		want := theme.Background
		if got.R != want.R || got.G != want.G || got.B != want.B {
			t.Fatalf("dark=%t selection retained hover fill: %v want %v", dark, got, want)
		}
	}
}

func TestWorkspaceTabStatusAndPaddingActivate(t *testing.T) {
	for _, busy := range []bool{false, true} {
		t.Run(fmt.Sprintf("busy=%t", busy), func(t *testing.T) {
			selected := 0
			tt := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).Padding(10).Children(func() {
					WorkspaceTab(c.Key("tab"), "apisec", "/w/apisec", false, busy, true, nil, func() { selected++ }, func() {}, nil).Label("workspace-tab")
				})
			}, 300, 100)
			tab, ok := tt.Find("workspace-tab")
			if !ok {
				t.Fatal("tab not found")
			}
			for i, point := range [][2]float32{
				{tab.X + 1, tab.Y + tab.H/2},         // left padding
				{tab.X + 10, tab.Y + tab.H/2},        // status dot, including when transparent
				{tab.X + 15, tab.Y + tab.H/2},        // gap before the title
				{tab.X + tab.W/2, tab.Y + 1},         // top edge
				{tab.X + tab.W/2, tab.Y + tab.H - 1}, // bottom underline
				{tab.X + tab.W - 1, tab.Y + tab.H/2}, // right padding
			} {
				tt.ClickAt(point[0], point[1])
				if selected != i+1 {
					t.Fatalf("click at %v did not activate exactly once: got %d activations, want %d", point, selected, i+1)
				}
			}
		})
	}
}
func TestWorkspaceTabPreview(t *testing.T) {
	if os.Getenv("ALLY_WORKSPACE_TABS_PREVIEW") == "" {
		t.Skip("visual preview")
	}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			for _, dark := range []bool{false, true} {
				theme := ui.LightTheme()
				if dark {
					theme = ui.DarkTheme()
				}
				theme.Accent = ui.Hex("#365dcc")
				if dark {
					theme.Accent = ui.Hex("#9eb7f1")
				}
				c.SetTheme(theme)
				c.DisableAnimations()
				ui.Column(c.Key(dark)).FillWidth().Height(125).Background(theme.Background).Children(func() {
					ui.Row(c).FillWidth().Height(44).Padding(0, 12).Gap(0).AlignItems(ui.Center).Background(theme.Surface.Mix(theme.Accent, 0.18)).Children(func() {
						ui.Text(c, "Ally").FontSize(20).FontWeight(700).TextColor(theme.Accent).Margin(0, 10, 0, 0)
						for i, label := range []string{"apisec", "ally-agent-v2", "另一个工作区"} {
							WorkspaceTab(c.Key(i), label, "/w/"+label, i == 0, i == 1, true, nil, func() {}, func() {}, nil)
						}
					})
				})
			}
		})
	}, 700, 250)
	var b bytes.Buffer
	if err := png.Encode(&b, tt.Image()); err != nil {
		t.Fatal(err)
	}
	fmt.Println("WORKSPACE_PREVIEW=" + base64.StdEncoding.EncodeToString(b.Bytes()))
}

func TestWorkspaceTabsUseIntrinsicWidth(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Row(c).Children(func() {
			WorkspaceTab(c.Key("short"), "a", "/w/a", true, false, true, nil, func() {}, func() {}, nil).Label("short-tab")
			WorkspaceTab(c.Key("long"), "ally-agent-workspace", "/w/ally-agent-workspace", false, false, true, nil, func() {}, func() {}, nil).Label("long-tab")
		})
	}, 600, 80)
	short, _ := tt.Find("short-tab")
	long, _ := tt.Find("long-tab")
	if short.W >= long.W || short.W >= 100 || long.W > 220 {
		t.Fatalf("width not intrinsic: short=%v long=%v", short, long)
	}
}

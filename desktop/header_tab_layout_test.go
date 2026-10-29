package desktop

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"testing"
	"time"
)

func TestWorkspaceTabsFillHeaderAndShareBottomBaseline(t *testing.T) {
	a := &desktop{ready: true, page: "chat", tabs: []*conversation{{ID: "first", Workspace: "apisec"}, {ID: "second", Workspace: "ally-agent"}}}
	tt := ui.NewTester(func(c *ui.Context) { c.SetTheme(a.theme()); a.headerBar(c) }, 900, 100)
	first, ok := tt.Find("工作区标签 first")
	if !ok {
		t.Fatal("missing first tab")
	}
	second, _ := tt.Find("工作区标签 second")
	if first.Y != 0 || first.H != 44 || second.Y != 0 || second.H != 44 || second.X != first.X+first.W {
		t.Fatalf("header has gaps: %v %v", first, second)
	}
	color := tt.Image().RGBAAt(int(first.X+first.W/2), 43)
	accent := a.currentPalette().accent
	if color.R != accent.R || color.G != accent.G || color.B != accent.B {
		t.Fatalf("underline does not share header bottom: %v want %v", color, accent)
	}
	tt.Move(second.X+3, second.Y+3)
	tt.Advance(200 * time.Millisecond)
	color = tt.Image().RGBAAt(int(second.X+3), 1)
	hover := a.currentPalette().hover
	if color.R != hover.R || color.G != hover.G || color.B != hover.B {
		t.Fatalf("hover does not fill header height: %v want %v", color, hover)
	}
}

func TestHeaderDividerContinuesUnderCaptionControls(t *testing.T) {
	for _, appearance := range []string{"light", "dark"} {
		a := &desktop{ready: true, page: "chat", win: &mygo.Window{}, tabs: []*conversation{{ID: "tab", Workspace: "cam"}}}
		a.prefs.Appearance = appearance
		tt := ui.NewTester(func(c *ui.Context) { c.SetTheme(a.theme()); a.headerBar(c) }, 900, 100)
		for _, hover := range []string{"最小化", "最大化", "关闭窗口"} {
			r, ok := tt.Find(hover)
			if !ok {
				t.Fatal("missing window control")
			}
			if r.Y != 0 || r.H != 44 {
				t.Fatalf("caption top/bottom mismatch: %v", r)
			}
			tt.Move(r.X+r.W/2, r.Y+r.H/2)
			tt.Advance(200 * time.Millisecond)
			for _, name := range []string{"最小化", "最大化", "关闭窗口"} {
				b, _ := tt.Find(name)
				for x := int(b.X) + 1; x < int(b.X+b.W)-1; x++ {
					v := tt.Image().RGBAAt(x, 43)
					want := a.theme().Border
					if v.R != want.R || v.G != want.G || v.B != want.B {
						t.Fatalf("%s bottom line missing on %s x=%d color=%v want=%v", appearance, name, x, v, want)
					}
				}
			}
		}
	}
}

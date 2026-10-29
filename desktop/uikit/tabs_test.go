package uikit

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"github.com/egoist/mygo/ui"
	"image/png"
	"os"
	"testing"
)

func TestTabStylesNavigationAndStableLayout(t *testing.T) {
	for _, style := range []TabStyle{TabLine, TabSegmented} {
		for _, secondary := range []bool{false, true} {
			selected, changes := 0, 0
			tt := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).Children(func() {
					if TabsWithOptions(c.Key("tabs"), &selected, TabOptions{Style: style, Secondary: secondary}, "First", "Second", "Third").Changed() {
						changes++
					}
				})
			}, 420, 100)
			before, _ := tt.Find("Second")
			if err := tt.Click("Second"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			after, _ := tt.Find("Second")
			if selected != 1 || changes != 1 || before != after {
				t.Fatalf("style %d secondary=%v unstable selection: %d %d %v %v", style, secondary, selected, changes, before, after)
			}
			tt.Key(0, ui.KeyRight)
			tt.Frame()
			if selected != 2 || changes != 2 {
				t.Fatal("keyboard navigation failed")
			}
			tt.Key(0, ui.KeyHome)
			tt.Frame()
			if selected != 0 {
				t.Fatal("Home failed")
			}
		}
	}
}

func TestNativeTabStylesPreview(t *testing.T) {
	if os.Getenv("ALLY_TABS_PREVIEW") == "" {
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
				ui.Column(c.Key(dark)).FillWidth().Padding(20).Gap(16).Background(theme.Background).Children(func() {
					for _, style := range []TabStyle{TabLine, TabSegmented} {
						ui.Column(c.Key(style)).Gap(10).Children(func() {
							name := "A · 细线导航"
							if style == TabSegmented {
								name = "C · 紧凑分段（当前默认）"
							}
							ui.Text(c, name).FontSize(12).TextColor(theme.TextMuted)
							first, sub := 0, 0
							TabsWithOptions(c.Key("top"), &first, TabOptions{Style: style}, "对话模型", "图片模型")
							TabsWithOptions(c.Key("sub"), &sub, TabOptions{Style: style, Secondary: true}, "OpenCode Go", "anyrouter", "福利", "tabitoken", "gorouter", "v2ex")
						})
					}
				})
			}
		})
	}, 760, 560)
	var b bytes.Buffer
	if err := png.Encode(&b, tt.Image()); err != nil {
		t.Fatal(err)
	}
	fmt.Println("TABS_PREVIEW=" + base64.StdEncoding.EncodeToString(b.Bytes()))
}

func TestSegmentedTabsFollowAccentAtBothLevels(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, secondary := range []bool{false, true} {
			accent := ui.Hex("#365dcc")
			selected := 0
			tt := ui.NewTester(func(c *ui.Context) {
				theme := ui.LightTheme()
				if dark {
					theme = ui.DarkTheme()
				}
				theme.Accent = accent
				c.SetTheme(theme)
				c.DisableAnimations()
				ui.Column(c).Fill().Background(theme.Background).Children(func() {
					TabsWithOptions(c.Key("tabs"), &selected, TabOptions{Style: TabSegmented, Secondary: secondary}, "Selected", "Idle")
				})
			}, 360, 100)
			chosen, _ := tt.Find("Selected")
			idle, _ := tt.Find("Idle")
			pick := func(r ui.Rect) [4]uint8 {
				v := tt.Image().RGBAAt(int(r.X+6), int(r.Y+4))
				return [4]uint8{v.R, v.G, v.B, v.A}
			}
			chosenBefore, idleBefore := pick(chosen), pick(idle)
			accent = ui.Hex("#9b6218")
			tt.Frame()
			if pick(chosen) == chosenBefore {
				t.Fatalf("selected fill ignored accent: dark=%v secondary=%v", dark, secondary)
			}
			if pick(idle) != idleBefore {
				t.Fatal("unselected tab changed color with accent")
			}
		}
	}
}

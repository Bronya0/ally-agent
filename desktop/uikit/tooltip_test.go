package uikit

import (
	"bytes"
	"github.com/egoist/mygo/ui"
	"testing"
	"time"
)

func TestTooltipAppearsOnRight(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Padding(12).Children(func() {
			b := ui.ButtonBase(c.Key("menu")).Size(34, 34).Label("menu")
			RightTooltip(c, b, "插件提示")
		})
	}, 300, 160)
	r, _ := tt.Find("menu")
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Advance(700 * time.Millisecond)
	tip, ok := tt.Find("插件提示")
	if !ok {
		t.Fatal("tooltip missing")
	}
	if tip.X <= r.X+r.W {
		t.Fatalf("tooltip is not right of menu: %+v %+v", r, tip)
	}
	tt.Move(250, 140)
	tt.Frame()
	if _, ok := tt.Find("插件提示"); ok {
		t.Fatal("tooltip did not dismiss")
	}
}

func TestTooltipFadesInAndOut(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(ui.LightTheme())
		ui.Column(c).Fill().Background(ui.Hex("#ffffff")).Padding(16).Children(func() {
			b := ui.ButtonBase(c.Key("item")).Size(34, 34).Label("item")
			RightTooltip(c, b, "提示内容")
		})
	}, 300, 160)
	pixels := func() []byte { return append([]byte(nil), tt.Image().Pix...) }
	empty := pixels()
	r, _ := tt.Find("item")
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Advance(610 * time.Millisecond)
	start := pixels()
	tt.Advance(90 * time.Millisecond)
	middle := pixels()
	tt.Advance(200 * time.Millisecond)
	full := pixels()
	if bytes.Equal(start, full) || bytes.Equal(middle, full) || bytes.Equal(middle, empty) {
		t.Fatalf("tooltip appeared without fading: startFull=%v midFull=%v midEmpty=%v", bytes.Equal(start, full), bytes.Equal(middle, full), bytes.Equal(middle, empty))
	}
	tt.Move(260, 140)
	tt.Frame()
	exit := pixels()
	if bytes.Equal(exit, empty) {
		t.Fatal("tooltip disappeared without fading")
	}
	tt.Advance(90 * time.Millisecond)
	half := pixels()
	if bytes.Equal(half, empty) || bytes.Equal(half, exit) {
		t.Fatal("tooltip exit has no intermediate frame")
	}
	tt.Advance(200 * time.Millisecond)
	if !bytes.Equal(pixels(), empty) {
		t.Fatal("tooltip retained exit snapshot")
	}
}

func TestTooltipDoesNotStealHoverInShortWindow(t *testing.T) {
	const text = "A long command tooltip that wraps across several lines. A long command tooltip that wraps across several lines. A long command tooltip that wraps across several lines."
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Selectable().Padding(20).Children(func() { b := ui.ButtonBase(c.Key("anchor")).Size(150, 24).Label("anchor"); Tooltip(c, b, text) })
	}, 360, 85)
	r, _ := tt.Find("anchor")
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Advance(time.Second)
	for i := 0; i < 60; i++ {
		tt.Advance(33 * time.Millisecond)
		tt.Frame()
		if _, ok := tt.Find(text); !ok {
			t.Fatalf("tooltip lost hover on redraw %d", i)
		}
	}
}

func TestTooltipContentChangeDoesNotRestartFade(t *testing.T) {
	text := "AAAA"
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(ui.LightTheme())
		ui.Column(c).Fill().Background(ui.Hex("#ffffff")).Padding(20).Children(func() {
			anchor := ui.ButtonBase(c.Key("anchor")).Size(100, 24).Label("anchor")
			RightTooltip(c, anchor, text)
		})
	}, 300, 120)
	r, _ := tt.Find("anchor")
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Advance(time.Second)
	tip, _ := tt.Find(text)
	x, y := int(tip.X)-3, int(tip.Y)-2
	before := tt.Image().RGBAAt(x, y)
	text = "BBBB"
	tt.Frame()
	if got := tt.Image().RGBAAt(x, y); got != before {
		t.Fatalf("content update restarted fade: %v -> %v", before, got)
	}
}

func TestTooltipForIntrinsicLabelInWideColumn(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Padding(80, 24).Children(func() {
			RightTooltip(c, ui.Text(c.Key("label"), "展示块数 ⓘ").AlignSelf(ui.Start), "标题的计算方式说明")
		})
	}, 900, 400)
	r, ok := tt.Find("展示块数 ⓘ")
	if !ok || r.W > 200 {
		t.Fatalf("label stretched: %+v", r)
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Advance(time.Second)
	tip, ok := tt.Find("标题的计算方式说明")
	if !ok || tip.X <= r.X+r.W || tip.X > r.X+r.W+40 {
		t.Fatalf("tip detached from label: anchor=%+v tip=%+v", r, tip)
	}
}

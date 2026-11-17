package main

import (
	"testing"
	"time"

	"ally-dev/desktop/uikit"
	"github.com/egoist/mygo/ui"
)

func TestGalleryThemeAndMessage(t *testing.T) {
	g := newGallery()
	tt := ui.NewTester(g.view, 820, 760)
	if !tt.HasText("Ally UI Kit") {
		t.Fatal("gallery title is missing")
	}
	if err := tt.Click("深色主题"); err != nil {
		t.Fatal(err)
	}
	if !g.dark {
		t.Fatal("theme switch did not change the theme")
	}
	if err := tt.Click("主要操作"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("操作完成") {
		t.Fatal("action did not show its message")
	}
}

func TestComparisonInputsHaveIndependentValues(t *testing.T) {
	g := newGallery()
	tt := ui.NewTester(g.view, 1200, 1000)
	if err := tt.Click("原生输入"); err != nil {
		t.Fatal(err)
	}
	tt.Type("native")
	if g.native.text != "native" || g.text != "" {
		t.Fatalf("native input changed the wrong state: native=%q kit=%q", g.native.text, g.text)
	}
	if err := tt.Click("输入"); err != nil {
		t.Fatal(err)
	}
	tt.Type("kit")
	if g.native.text != "native" || g.text != "kit" {
		t.Fatalf("kit input changed the wrong state: native=%q kit=%q", g.native.text, g.text)
	}
}

func TestGalleryAccentSelection(t *testing.T) {
	g := newGallery()
	tt := ui.NewTester(g.view, 1200, 800)
	before := galleryTheme(false, g.accent).Accent
	if err := tt.Click("蓝色"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("森林"); err != nil {
		t.Fatal(err)
	}
	if g.accent != string(uikit.ThemeForest) || galleryTheme(false, g.accent).Accent == before {
		t.Fatalf("accent selection did not update the theme: %q", g.accent)
	}
	if galleryTheme(true, g.accent).Accent == galleryTheme(false, g.accent).Accent {
		t.Fatal("dark and light appearances should use their respective accent colors")
	}
}

func TestGalleryPagePalettes(t *testing.T) {
	for _, p := range uikit.BuiltinThemes() {
		light, dark := galleryTheme(false, string(p.ID)), galleryTheme(true, string(p.ID))
		if !dark.Dark || dark.Background == light.Background || dark.Accent == light.Accent {
			t.Fatalf("%s does not switch to its dark palette", p.Name)
		}
	}
}

func TestComparisonColumnsScroll(t *testing.T) {
	g := newGallery()
	tt := ui.NewTester(g.view, 1200, 800)
	if g.scroll.MaxY <= 0 {
		t.Fatal("comparison must overflow")
	}
	leftBefore, ok := tt.Find("原生主要操作")
	if !ok {
		t.Fatal("native button missing")
	}
	rightBefore, ok := tt.Find("主要操作")
	if !ok {
		t.Fatal("kit button missing")
	}
	tt.Scroll(200, 600, 0, 180)
	if g.scroll.Y <= 0 {
		t.Fatalf("left scroll did not move comparison: %v", g.scroll.Y)
	}
	leftAfter, lok := tt.Find("原生主要操作")
	rightAfter, rok := tt.Find("主要操作")
	if !lok || !rok || leftAfter.Y >= leftBefore.Y || rightAfter.Y >= rightBefore.Y {
		t.Fatalf("both columns must move: left %v -> %v; right %v -> %v", leftBefore.Y, leftAfter.Y, rightBefore.Y, rightAfter.Y)
	}
	before := g.scroll.Y
	tt.Wheel(900, 600, 0, 180)
	tt.Advance(time.Second)
	if g.scroll.Y <= before {
		t.Fatalf("right scroll did not move comparison: %v -> %v", before, g.scroll.Y)
	}
}

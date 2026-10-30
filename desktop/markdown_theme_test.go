package desktop

import (
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"math"
	"strings"
	"testing"
)

func colorLuminance(c ui.Color) float64 {
	channel := func(v uint8) float64 {
		x := float64(v) / 255
		if x <= 0.04045 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return .2126*channel(c.R) + .7152*channel(c.G) + .0722*channel(c.B)
}
func TestReadingThemesHaveLegibleIndependentColors(t *testing.T) {
	for index := range palettes {
		for _, appearance := range []string{"dark", "light"} {
			a := &desktop{}
			a.prefs.Palette = index
			a.prefs.Appearance = appearance
			colors := a.readingColors()
			bg := a.currentPalette().bg
			for _, ink := range []ui.Color{colors.text, colors.code, colors.heading, colors.strong, colors.link} {
				x, y := colorLuminance(ink), colorLuminance(bg)
				if x < y {
					x, y = y, x
				}
				if (x+.05)/(y+.05) < 4.5 {
					t.Errorf("%s %s has low contrast: %+v", palettes[index].name, appearance, ink)
				}
			}
		}
	}
}

func TestReadingInkMatchesLegacyPalette(t *testing.T) {
	a := &desktop{}
	a.prefs.Appearance = "dark"
	a.prefs.Palette = 2
	colors := a.readingColors()
	if colors.text != ui.Hex("#d7dedc") || colors.strong != ui.Hex("#c4b894") || colors.code != ui.Hex("#d4a574") || colors.codeBG.A != 0 {
		t.Fatalf("amber palette: %+v", colors)
	}
	a.prefs.Palette = 3
	colors = a.readingColors()
	if colors.code != ui.Hex("#8da5df").Mix(ui.Hex("#ffffff"), 0.28) {
		t.Fatalf("retired ocean fallback code: %+v", colors.code)
	}
}
func TestReadingTypographySeparatesInlineAndFencedCode(t *testing.T) {
	a := &desktop{}
	a.prefs.Palette = 0
	conversation := &conversation{Messages: []map[string]any{{"role": "assistant"}}}
	options := a.markdownOptions(conversation, 0)
	if options.Theme.Font != "Inter" || options.Theme.CodeFont != "Inter" || options.Theme.FontSize != 16 || options.Theme.CodeSize != 1 {
		t.Fatalf("inline typography: %+v", options.Theme)
	}
	if body, code := readingSizes(18, 15); body != 18 || code != 15 {
		t.Fatal("custom sizes were lost")
	}
	source := "正文和 `inline code` 应保持同样字号。\n\n```go\nvar count = 1\n```"
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		ui.Column(c).Fill().Children(func() { markdown.View(c, source, options).FillWidth() })
	}, 800, 400)
	tt.Frame()
	tt.Frame()
	found := false
	for _, text := range tt.Texts() {
		found = found || strings.Contains(text, "var count = 1")
	}
	if !found {
		t.Fatalf("native fenced-code renderer lost code: %v", tt.Texts())
	}
	a.prefs.Palette = 4
	options = a.markdownOptions(conversation, 0)
	if options.Theme.Code != a.readingColors().code {
		t.Fatal("cached message did not follow palette change")
	}
}

// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"ally-dev/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

func TestNativePaletteKeepsPersistedIDs(t *testing.T) {
	// Display order must not shift the IDs already saved by older clients.
	for id, want := range []int{paletteBlue, paletteLight, paletteAmber, paletteOcean, paletteForest, paletteViolet, paletteMeadow, paletteNative} {
		if want != id {
			t.Fatalf("persisted palette ID changed: %d != %d", want, id)
		}
	}
	if paletteChoices[0] != paletteNative {
		t.Fatal("native must be the first displayed choice")
	}
	seen := map[int]bool{}
	for _, id := range paletteChoices {
		if id < 0 || id >= len(palettes) || seen[id] {
			t.Fatalf("invalid or duplicate displayed palette: %d", id)
		}
		seen[id] = true
	}
	path := filepath.Join(t.TempDir(), "native-ui.json")
	for id := range palettes {
		prefs := defaultPrefs()
		prefs.Palette = id
		data, err := json.Marshal(prefs)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		restored := &desktop{prefs: readPrefs(path)}
		if restored.prefs.Palette != id {
			t.Fatalf("saved palette %d did not round-trip", id)
		}
		want := palettes[id].accent
		if id == paletteOcean {
			want = palettes[paletteBlue].accent
		}
		if got := restored.currentPalette().accent; got != want {
			t.Fatalf("saved palette %d resolved to %v, want %v", id, got, want)
		}
	}
}

func TestNativeReadingAndControlsShareNeutralTokens(t *testing.T) {
	for _, tc := range []struct {
		appearance, bg, panel, codeBG, text, heading, muted, hint, link, accent, accentText string
		border                                                                              ui.Color
	}{
		{"dark", "#151517", "#1b1b1c", "#292929", "#e1e5ee", "#ebeef2", "#cfd3d6", "#adb2b8", "#679efe", "#e1e5ee", "#151517", ui.RGBA(255, 255, 255, 0.1)},
		{"light", "#ffffff", "#f9fafb", "#f1f3f5", "#0f1115", "#0f1115", "#61666b", "#adb2b8", "#3964fe", "#0f1115", "#f9fafb", ui.RGBA(0, 0, 0, 0.1)},
	} {
		t.Run(tc.appearance, func(t *testing.T) {
			a := &desktop{prefs: defaultPrefs()}
			a.prefs.Palette, a.prefs.Appearance = paletteNative, tc.appearance
			p, th := a.currentPalette(), a.theme()
			options := a.readingMarkdownOptions()
			a.configureReadingMarkdown(options)
			md := options.Theme
			for name, pair := range map[string][2]ui.Color{
				"background":        {th.Background, ui.Hex(tc.bg)},
				"surface":           {th.Surface, ui.Hex(tc.panel)},
				"text":              {th.Text, ui.Hex(tc.text)},
				"secondary":         {p.muted, ui.Hex(tc.muted)},
				"placeholder":       {th.TextMuted, ui.Hex(tc.hint)},
				"accent":            {th.Accent, ui.Hex(tc.accent)},
				"accent text":       {th.AccentText, ui.Hex(tc.accentText)},
				"markdown text":     {md.Text, th.Text},
				"strong":            {md.Strong, th.Text},
				"inline ink":        {md.Code, th.Text},
				"inline background": {md.CodeBackground, ui.Hex(tc.codeBG)},
				"code background":   {md.CodeBlockBackground, th.Surface},
				"links":             {md.Link, ui.Hex(tc.link)},
				"quote":             {md.QuoteText, p.muted},
				"table border":      {md.TableBorder, tc.border},
				"code border":       {md.CodeBlockBorder, tc.border},
				"quote bar":         {md.QuoteBar, tc.border},
				"rule":              {md.Rule, tc.border},
			} {
				if pair[0] != pair[1] {
					t.Errorf("%s: %v != %v", name, pair[0], pair[1])
				}
			}
			for n, heading := range md.Headings {
				if heading.Color != ui.Hex(tc.heading) {
					t.Errorf("heading %d lost neutral ink: %v", n, heading.Color)
				}
			}
			if md.Link == th.Accent || md.CodeColors == nil {
				t.Fatal("blue links must be separate from neutral controls, with code syntax retained")
			}
			// Check ink against its actual surface, not just the page background.
			for _, pair := range [][2]ui.Color{{th.AccentText, th.Accent}, {th.Accent, p.headerColor()}, {th.AccentHover, p.headerColor()}, {md.Code, md.CodeBackground}, {md.Link, th.Background}, {md.QuoteText, th.Background}} {
				x, y := colorLuminance(pair[0]), colorLuminance(pair[1])
				if x < y {
					x, y = y, x
				}
				if (x+.05)/(y+.05) < 4.5 {
					t.Errorf("native ink has low contrast on its surface: %v on %v", pair[0], pair[1])
				}
			}
		})
	}
}

func TestNativeSettingsFirstChoiceAndAppearanceSwitch(t *testing.T) {
	for _, width := range []int{480, 1100} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			a := settingsLayoutFixture()
			a.settings.Modules["settingsGeneral"] = 0
			tt := settingsLayoutTester(a, width, 2200)
			tt.Frame()
			var previous ui.Rect
			for _, id := range paletteChoices {
				bounds, ok := tt.Find(locales.Text(palettes[id].name))
				if !ok || bounds.X < 0 || bounds.X+bounds.W > float32(width) {
					t.Fatalf("palette choice %d is missing or outside the page: %v", id, bounds)
				}
				if previous.H != 0 && (bounds.Y < previous.Y || (bounds.Y == previous.Y && bounds.X <= previous.X)) {
					t.Fatalf("palette choices are not in display order: %v after %v", bounds, previous)
				}
				previous = bounds
			}
			if err := tt.Click(locales.Text(palettes[paletteNative].name)); err != nil {
				t.Fatal(err)
			}
			if a.prefs.Palette != paletteNative || !a.currentPalette().dark {
				t.Fatal("selecting native changed appearance or failed to select")
			}
			if err := tt.Click(locales.Text("浅色")); err != nil {
				t.Fatal(err)
			}
			if a.prefs.Palette != paletteNative || a.currentPalette().dark || a.theme().Background != ui.Hex("#ffffff") {
				t.Fatal("appearance switch lost the native palette")
			}
			a.prefs.Appearance = "auto"
			for _, mode := range []string{"dark", "light"} {
				a.autoAppearance.Mode = mode
				if a.currentPalette().dark != (mode == "dark") || a.prefs.Palette != paletteNative {
					t.Fatal("automatic appearance did not retain the native palette")
				}
			}
		})
	}
}

func TestNativeCachedMarkdownFollowsPaletteAndAppearance(t *testing.T) {
	a := &desktop{prefs: defaultPrefs()}
	conv := &conversation{Messages: []map[string]any{{"role": "assistant"}}}
	var options *markdown.Options
	for _, tc := range []struct {
		id   int
		mode string
	}{{paletteBlue, "dark"}, {paletteNative, "dark"}, {paletteNative, "light"}, {paletteForest, "light"}} {
		a.prefs.Palette, a.prefs.Appearance = tc.id, tc.mode
		options = a.markdownOptions(conv, 0)
		colors := a.readingColors()
		if options.Theme.CodeBackground != colors.codeBG || options.Theme.Link != colors.link || options.Theme.Text != colors.text {
			t.Fatal("cached Markdown retained the previous palette")
		}
	}
}

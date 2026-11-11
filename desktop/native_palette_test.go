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

func TestNativeAccentSharesCommonControlTokens(t *testing.T) {
	for _, mode := range []string{"dark", "light"} {
		t.Run(mode, func(t *testing.T) {
			a := &desktop{prefs: defaultPrefs()}
			a.prefs.Palette, a.prefs.Appearance = paletteNative, mode
			p, th := a.currentPalette(), a.theme()
			for _, id := range []int{paletteBlue, paletteAmber, paletteForest, paletteViolet} {
				reference := &desktop{prefs: a.prefs}
				reference.prefs.Palette = id
				want := reference.currentPalette()
				// An accent choice may change its name and accent, not base tokens.
				want.name, want.accent = p.name, p.accent
				if p != want {
					t.Fatalf("native overrides shared palette %d tokens: got %+v, want %+v", id, p, want)
				}
				base := reference.theme()
				for name, pair := range map[string][2]ui.Color{
					"background":  {th.Background, base.Background},
					"surface":     {th.Surface, base.Surface},
					"hover":       {th.SurfaceHover, base.SurfaceHover},
					"pressed":     {th.SurfacePressed, base.SurfacePressed},
					"text":        {th.Text, base.Text},
					"placeholder": {th.TextMuted, base.TextMuted},
					"border":      {th.Border, base.Border},
					"scrollbar":   {th.Scrollbar, base.Scrollbar},
					"accent text": {th.AccentText, base.AccentText},
					"success":     {th.Success, base.Success},
					"warning":     {th.Warning, base.Warning},
					"danger":      {th.Danger, base.Danger},
				} {
					if pair[0] != pair[1] {
						t.Errorf("%s differs from palette %d: %v != %v", name, id, pair[0], pair[1])
					}
				}
				if p.headerColor() != want.headerColor() {
					t.Fatal("native bypasses the shared header rule")
				}
				for _, at := range []float32{0.14, 0.43, 0.61, 0.84} {
					if wordmarkGradientColor(p, at) != wordmarkGradientColor(want, at) {
						t.Fatal("native bypasses the shared wordmark gradient")
					}
				}
			}
			if th.Accent != p.accent || th.Focus != p.accent || th.Selection != p.accent.Alpha(0.30) {
				t.Fatal("native controls must use the shared accent rules")
			}
		})
	}
}

func TestNativeReadingUsesSharedRules(t *testing.T) {
	for _, mode := range []string{"dark", "light"} {
		t.Run(mode, func(t *testing.T) {
			a := &desktop{prefs: defaultPrefs()}
			a.prefs.Palette, a.prefs.Appearance = paletteNative, mode
			reference := &desktop{prefs: a.prefs}
			reference.prefs.Palette = paletteBlue
			options, base := a.readingMarkdownOptions(), reference.readingMarkdownOptions()
			a.configureReadingMarkdown(options)
			reference.configureReadingMarkdown(base)
			md, want := options.Theme, base.Theme
			for name, pair := range map[string][2]ui.Color{
				"text":              {md.Text, want.Text},
				"secondary":         {md.Muted, want.Muted},
				"inline background": {md.CodeBackground, want.CodeBackground},
				"code background":   {md.CodeBlockBackground, want.CodeBlockBackground},
				"code border":       {md.CodeBlockBorder, want.CodeBlockBorder},
				"table background":  {md.TableHeader, want.TableHeader},
				"table border":      {md.TableBorder, want.TableBorder},
				"quote":             {md.QuoteText, want.QuoteText},
				"quote bar":         {md.QuoteBar, want.QuoteBar},
				"rule":              {md.Rule, want.Rule},
			} {
				if pair[0] != pair[1] {
					t.Errorf("%s lost the shared reading token: %v != %v", name, pair[0], pair[1])
				}
			}
			if md.Headings != want.Headings || md.CodeColors == nil {
				t.Fatal("native must retain shared headings and code syntax")
			}
			if md.Link == want.Link || md.Code == want.Code || md.CodeBackground.A != 0 {
				t.Fatal("native must change reading accents without adding an inline background")
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
			if a.prefs.Palette != paletteNative || a.currentPalette().dark || a.theme().Background != palettes[paletteLight].bg {
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

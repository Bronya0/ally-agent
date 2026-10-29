package desktop

import (
	"github.com/egoist/mygo/ui"
	"testing"
)

func TestStatusInkAndIndicatorsUseSeparateLightColors(t *testing.T) {
	a := &desktop{}
	a.prefs.Appearance = "light"
	theme := a.theme()
	ui.NewTester(func(c *ui.Context) {
		c.SetTheme(theme)
		for _, pair := range []struct {
			status string
			tone   pillTone
		}{{"connected", pillOK}, {"warning", pillWarn}, {"error", pillDanger}, {"disabled", pillNeutral}} {
			ink, dot := toneColor(c, pair.status), pillDotColor(c, pair.tone)
			if colorLuminance(dot) <= colorLuminance(ink) {
				t.Errorf("%s indicator should not use deep text ink", pair.status)
			}
		}
	}, 200, 100)
	for _, ink := range []ui.Color{theme.Success, theme.Warning, theme.Danger} {
		contrast := (colorLuminance(theme.Background) + .05) / (colorLuminance(ink) + .05)
		if contrast < 4.5 {
			t.Errorf("status ink contrast %.2f: %v", contrast, ink)
		}
	}
	a.prefs.Appearance = "dark"
	dark := a.theme()
	if dark.Success != okColor || dark.Warning != warnCol || dark.Danger != errCol {
		t.Fatal("dark status colors changed")
	}
}

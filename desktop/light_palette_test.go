package desktop

import (
	"github.com/egoist/mygo/ui"
	"testing"
)

func TestLightAccentsRemainColoredAndReadable(t *testing.T) {
	for index := range palettes {
		a := &desktop{}
		a.prefs.Palette = index
		a.prefs.Appearance = "light"
		p := a.currentPalette()
		lo, hi := p.accent.R, p.accent.R
		for _, v := range []uint8{p.accent.G, p.accent.B} {
			if v < lo {
				lo = v
			}
			if v > hi {
				hi = v
			}
		}
		if hi-lo < 60 {
			t.Errorf("%s accent became grey: %+v", p.name, p.accent)
		}
		for _, bg := range []ui.Color{p.bg, p.panel} {
			contrast := (colorLuminance(bg) + .05) / (colorLuminance(p.accent) + .05)
			if contrast < 4.5 {
				t.Errorf("palette %d contrast %.2f", index, contrast)
			}
		}
	}
	a := &desktop{}
	a.prefs.Palette = 2
	a.prefs.Appearance = "light"
	if a.currentPalette().accent != ui.Hex("#9b6218") {
		t.Fatal("amber changed")
	}
}

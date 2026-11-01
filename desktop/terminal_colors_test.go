package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestTerminalRolesAreDistinctAndReadable(t *testing.T) {
	for index := range palettes {
		for _, appearance := range []string{"light", "dark"} {
			a := &desktop{}
			a.prefs.Palette = index
			a.prefs.Appearance = appearance
			theme := a.theme()
			secondary := a.secondaryInk()
			command, output := commandLineInk(theme, secondary), terminalOutputInk(theme, secondary)
			if appearance == "dark" {
				if command != secondary || output != secondary {
					t.Fatal("dark terminal palette changed")
				}
				continue
			}
			if colorLuminance(command) >= colorLuminance(output) {
				t.Fatal("command heading must have stronger ink than output")
			}
			for _, ink := range []ui.Color{command, output} {
				contrast := (colorLuminance(theme.Background) + .05) / (colorLuminance(ink) + .05)
				if contrast < 4.5 {
					t.Fatalf("%s %s terminal contrast %.2f", palettes[index].name, appearance, contrast)
				}
			}
			ui.NewTester(func(c *ui.Context) {
				c.SetTheme(theme)
				one := codeSpansWithBase(c, "same text", "", command)
				two := codeSpansWithBase(c, "same text", "", output)
				if one[0].Color != command || two[0].Color != output {
					t.Fatal("highlight cache mixed terminal role colors")
				}
			}, 100, 100)
		}
	}
}

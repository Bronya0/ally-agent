package desktop

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"github.com/egoist/mygo/ui"
	"image/png"
	"os"
	"testing"
)

func TestTerminalRolesAreDistinctAndReadable(t *testing.T) {
	for index := range palettes {
		for _, appearance := range []string{"light", "dark"} {
			a := &desktop{}
			a.prefs.Palette = index
			a.prefs.Appearance = appearance
			theme := a.theme()
			command, output := commandLineInk(theme), terminalOutputInk(theme)
			if appearance == "dark" {
				if command != theme.TextMuted || output != theme.TextMuted {
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

func TestTerminalRolePreview(t *testing.T) {
	if os.Getenv("ALLY_TERMINAL_PREVIEW") == "" {
		t.Skip("visual preview")
	}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			for _, appearance := range []string{"light", "dark"} {
				a := &desktop{}
				a.prefs.Appearance = appearance
				a.prefs.Palette = 0
				c.SetTheme(a.theme())
				c.DisableAnimations()
				ui.Column(c.Key(appearance)).FillWidth().Height(160).Padding(20, 35).Gap(10).Background(c.Theme().Background).Children(func() {
					m := map[string]any{"name": "command", "toolCallId": "command", "status": "success", "args": `{"command":"go build ./... && go vet ./desktop/..."}`, "result": map[string]any{"stdout": "BUILD_OK\nVET_OK\nok ally-dev/desktop 0.340s"}}
					a.toolCard(c, &conversation{ID: "preview"}, 0, m)
				})
			}
		})
	}, 800, 320)
	var b bytes.Buffer
	if err := png.Encode(&b, tt.Image()); err != nil {
		t.Fatal(err)
	}
	fmt.Println("TERMINAL_PREVIEW=" + base64.StdEncoding.EncodeToString(b.Bytes()))
}

// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image/png"
	"os"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestComposerSelectorsKeepAppearanceWhileBusy(t *testing.T) {
	for _, appearance := range []string{"dark", "light"} {
		for _, hover := range []string{"", "provider · model", "high"} {
			t.Run(fmt.Sprintf("%s/hover=%s", appearance, hover), func(t *testing.T) {
				a := &desktop{}
				a.prefs.Appearance = appearance
				conv := &conversation{Model: "provider / model", Reasoning: "high"}
				tester := ui.NewTester(func(c *ui.Context) {
					c.SetTheme(a.theme())
					c.DisableAnimations()
					ui.Row(c).Padding(12).Gap(2).Children(func() {
						a.modelSelector(c, conv)
						a.reasoningSelector(c, conv)
					})
				}, 480, 96)
				if hover != "" {
					r, ok := tester.Find(hover)
					if !ok {
						t.Fatalf("missing selector %q: %v", hover, tester.Texts())
					}
					tester.Move(r.X+r.W/2, r.Y+r.H/2)
				}
				idle := append([]byte(nil), tester.Image().Pix...)
				conv.Busy = true
				for frame := 0; frame < 3; frame++ {
					tester.Frame()
					if !bytes.Equal(idle, tester.Image().Pix) {
						t.Fatalf("selector appearance changed while busy on frame %d", frame)
					}
				}
				conv.Busy = false
				tester.Frame()
				if !bytes.Equal(idle, tester.Image().Pix) {
					t.Fatal("selector appearance changed after the run ended")
				}
			})
		}
	}
}

// The value is drawn once and the select has a stable accessible name. The
// caret must not add a second value label.
func TestReasoningSelectorDrawsLevelOnce(t *testing.T) {
	a := &desktop{}
	conv := &conversation{Model: "provider / model", Reasoning: "high"}
	tester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		a.reasoningSelector(c, conv)
	}, 240, 48)
	count := 0
	for _, text := range tester.Texts() {
		if text == "high" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("thinking level drawn %d times, want one text: %v", count, tester.Texts())
	}
	if _, ok := tester.Find("思考强度"); !ok {
		t.Fatal("missing accessible select name")
	}
}

func TestReasoningSelectorUsesThemedPopup(t *testing.T) {
	for _, appearance := range []string{"dark", "light"} {
		t.Run(appearance, func(t *testing.T) {
			a := &desktop{}
			a.prefs.Appearance = appearance
			conv := &conversation{Reasoning: "low"}
			tester := ui.NewTester(func(c *ui.Context) {
				c.SetTheme(a.theme())
				c.DisableAnimations()
				ui.Column(c).Fill().Justify(ui.End).Padding(12).Children(func() {
					a.reasoningSelector(c, conv)
				})
			}, 320, 360)
			trigger, ok := tester.Find("思考强度")
			if !ok {
				t.Fatal("missing select trigger")
			}
			if err := tester.Click("思考强度"); err != nil {
				t.Fatal(err)
			}
			for _, level := range reasoningLevels {
				if !tester.HasText(level) {
					t.Fatalf("missing popup option %q: %v", level, tester.Texts())
				}
			}
			option, ok := tester.Find("max")
			if !ok || option.Y+option.H > trigger.Y || option.Y < 0 {
				t.Fatalf("bottom select did not fit its popup above the trigger: option=%+v trigger=%+v", option, trigger)
			}
			if !tester.HasText("✓") {
				t.Fatal("popup does not mark the current level")
			}
			if os.Getenv("ALLY_REASONING_PREVIEW") != "" {
				var preview bytes.Buffer
				if err := png.Encode(&preview, tester.Image()); err != nil {
					t.Fatal(err)
				}
				fmt.Printf("REASONING_PREVIEW_%s=%s\n", appearance, base64.StdEncoding.EncodeToString(preview.Bytes()))
			}
			if err := tester.Click("max"); err != nil {
				t.Fatal(err)
			}
			if conv.Reasoning != "max" || tester.HasText("medium") {
				t.Fatalf("pointer selection failed to commit and close: value=%q texts=%v", conv.Reasoning, tester.Texts())
			}
			if err := tester.Click("思考强度"); err != nil {
				t.Fatal(err)
			}
			tester.Key(0, ui.KeyEscape)
			if conv.Reasoning != "max" || tester.HasText("medium") {
				t.Fatal("Escape did not cancel the popup without changing the value")
			}
			if err := tester.Click("思考强度"); err != nil {
				t.Fatal(err)
			}
			tester.ClickAt(4, 4)
			if tester.HasText("medium") {
				t.Fatal("clicking outside did not close the popup")
			}
		})
	}
}

func TestReasoningSelectorKeyboardAndRunLock(t *testing.T) {
	a := &desktop{}
	conv := &conversation{}
	tester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		a.reasoningSelector(c, conv)
	}, 320, 360)
	if conv.Reasoning != "auto" {
		t.Fatalf("empty effort did not initialize to auto: %q", conv.Reasoning)
	}
	tester.Key(0, ui.KeyTab)
	tester.Key(0, ui.KeyDown)
	if !tester.HasText("medium") {
		t.Fatal("keyboard did not open the select")
	}
	tester.Key(0, ui.KeyEnd)
	tester.Key(0, ui.KeyEnter)
	if conv.Reasoning != "max" || tester.HasText("medium") {
		t.Fatal("keyboard did not choose the last option and close")
	}
	tester.Key(0, ui.KeySpace)
	tester.Key(0, ui.KeyHome)
	tester.Key(0, ui.KeyDown)
	tester.Key(0, ui.KeySpace)
	if conv.Reasoning != "off" {
		t.Fatalf("Home/Down/Space did not choose off: %q", conv.Reasoning)
	}
	if err := tester.Click("思考强度"); err != nil {
		t.Fatal(err)
	}
	conv.Busy = true
	tester.Frame()
	if tester.HasText("medium") {
		t.Fatal("starting a run did not dismiss the open popup")
	}
	if err := tester.Click("思考强度"); err != nil {
		t.Fatal(err)
	}
	for _, key := range []ui.Key{ui.KeyEnter, ui.KeySpace, ui.KeyDown, ui.KeyEnd} {
		tester.Key(0, key)
	}
	if conv.Reasoning != "off" || tester.HasText("medium") {
		t.Fatal("locked select responded to pointer or keyboard input")
	}
	conv.Busy = false
	tester.Frame()
	if tester.HasText("medium") {
		t.Fatal("unlocking restored an obsolete popup")
	}
	if err := tester.Click("思考强度"); err != nil {
		t.Fatal(err)
	}
	tester.Key(0, ui.KeyHome)
	tester.Key(0, ui.KeyEnter)
	if conv.Reasoning != "auto" {
		t.Fatal("select did not recover after the run ended")
	}
}

func TestComposerSettingButtonLocksPointerAndKeyboard(t *testing.T) {
	locked, activations := false, 0
	tester := ui.NewTester(func(c *ui.Context) {
		c.DisableAnimations()
		composerSettingButton(c.Key("setting"), "high ▾", locked, func() { activations++ })
	}, 240, 96)
	if err := tester.Click("high ▾"); err != nil {
		t.Fatal(err)
	}
	if activations != 1 {
		t.Fatal("the idle setting did not activate")
	}
	locked = true
	tester.Frame()
	if err := tester.Click("high ▾"); err != nil {
		t.Fatal(err)
	}
	tester.Key(0, ui.KeyEnter)
	tester.Key(0, ui.KeySpace)
	if activations != 1 {
		t.Fatal("the locked setting activated by pointer or keyboard")
	}
	locked = false
	tester.Frame()
	tester.Key(0, ui.KeyEnter)
	tester.Key(0, ui.KeySpace)
	if activations != 3 {
		t.Fatalf("keyboard activation did not recover after unlocking: %d", activations)
	}
}

func TestModelSelectorRemainsLockedDuringRun(t *testing.T) {
	a := &desktop{}
	conv := &conversation{Model: "provider / model", Busy: true}
	tester := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		a.modelSelector(c, conv)
	}, 480, 400)
	if err := tester.Click("provider · model"); err != nil {
		t.Fatal(err)
	}
	if conv.uiState().ModelOpen {
		t.Fatal("the model menu opened during a run")
	}
	conv.Busy = false
	tester.Frame()
	if err := tester.Click("provider · model"); err != nil {
		t.Fatal(err)
	}
	if !conv.uiState().ModelOpen {
		t.Fatal("the model menu stayed locked after the run ended")
	}
}

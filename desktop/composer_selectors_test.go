// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"bytes"
	"fmt"
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
		t.Fatalf("thinking level drawn %d times: %v", count, tester.Texts())
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

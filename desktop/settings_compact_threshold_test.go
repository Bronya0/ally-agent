// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"testing"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

func TestSettingsCompactThresholdSwitchPreservesValues(t *testing.T) {
	saved := backend.ConfigState{
		CompactThreshold: .35, CompactThresholdTokens: 82_345,
		SendSessionIDHeader: boolPtr(true), SendXSessionIDHeader: boolPtr(true),
	}
	a := &desktop{config: saved, configDraft: saved}
	a.syncSettingsForm()
	if a.settings.ThresholdMode != backend.CompactThresholdModeRatio || a.settingsPendingEdits() {
		t.Fatal("legacy percentage config must open unchanged in ratio mode")
	}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Padding(16).Gap(gapTight).Children(func() { a.settingsCompactThreshold(c) })
	}, 400, 220)
	wantTokens := 82_345
	for i, mode := range []string{backend.CompactThresholdModeTokens, backend.CompactThresholdModeRatio, backend.CompactThresholdModeTokens} {
		label := compactThresholdModes[0].Label
		if mode == backend.CompactThresholdModeTokens {
			label = compactThresholdModes[1].Label
		}
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		tt.Frame() // Rebuilding after the conditional field changes must retain its state.
		_, hasTokens := tt.Find("压缩阈值（tokens）")
		_, hasRatio := tt.Find("压缩阈值")
		if hasTokens != (mode == backend.CompactThresholdModeTokens) || hasRatio == hasTokens {
			t.Fatalf("only the selected mode's control may be visible: tokens=%v ratio=%v", hasTokens, hasRatio)
		}
		if i == 0 {
			r, _ := tt.Find("压缩阈值（tokens）")
			tt.ClickAt(r.X+20, r.Y+r.H/2)
			tt.Key(ui.Cmd, ui.KeyA)
			tt.Type("97531")
			wantTokens = 97_531
		}
		draft := a.settingsConfigDraft()
		if draft.CompactThresholdMode != mode || draft.CompactThreshold != .35 || draft.CompactThresholdTokens != wantTokens {
			t.Fatalf("switching mode lost threshold values: %+v", draft)
		}
	}
	if !a.settingsPendingEdits() {
		t.Fatal("changing the threshold mode must be saved on leave")
	}
	a.config, a.configDraft = a.settingsConfigDraft(), a.settingsConfigDraft()
	a.syncSettingsForm()
	if a.settings.ThresholdMode != backend.CompactThresholdModeTokens || a.settingsPendingEdits() {
		t.Fatal("reopening saved absolute mode must preserve it without pending edits")
	}
}

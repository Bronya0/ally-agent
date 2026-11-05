// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"testing"

	backend "ally-dev/internal/app"
)

// Leaving the settings page must not report a save when the page holds nothing
// new: the projection from the form back onto the config has to round-trip the
// saved config byte for byte, or every visit would toast "设置已保存".
func TestSettingsDraftRoundTripsSavedConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	core := backend.NewApp()
	saved, err := core.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	a := &desktop{core: core, config: saved, configDraft: saved}
	a.syncSettingsForm()
	if a.settingsPendingEdits() {
		t.Fatalf("an untouched settings page already looks edited: %+v", a.settingsConfigDraft())
	}
	a.settings.Retries = a.settings.Retries + 1
	if !a.settingsPendingEdits() {
		t.Fatal("a changed field must be reported as pending")
	}
}

// A draft written back unchanged must still look clean after the reload, which
// is what a silent save on a settings sub-tab switch does.
func TestSettingsDraftStaysCleanAfterSave(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	core := backend.NewApp()
	saved, err := core.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	a := &desktop{core: core, config: saved, configDraft: saved}
	a.syncSettingsForm()
	draft := a.settingsConfigDraft()
	if err := core.SaveConfig(draft); err != nil {
		t.Fatal(err)
	}
	fresh, err := core.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	a.config, a.configDraft = fresh, fresh
	a.syncSettingsForm()
	if a.settingsPendingEdits() {
		t.Fatalf("a saved settings page reopened as edited: %+v", a.settingsConfigDraft())
	}
}

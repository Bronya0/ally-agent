package desktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo"
)

func TestInitialAppearanceAndAvatar(t *testing.T) {
	path := filepath.Join(t.TempDir(), "native-ui.json")
	for _, prefs := range []uiPrefs{defaultPrefs(), readPrefs(path)} {
		if prefs.Appearance != "dark" || prefs.Palette != defaultPalette || prefs.Avatar != "whale" {
			t.Fatalf("wrong initial preferences: %+v", prefs)
		}
	}
	if err := os.WriteFile(path, []byte(`{"appearance":"light","palette":5,"avatar":"bot"}`), 0600); err != nil {
		t.Fatal(err)
	}
	saved := readPrefs(path)
	if saved.Appearance != "light" || saved.Palette != 5 || saved.Avatar != "bot" {
		t.Fatalf("saved choice overwritten: %+v", saved)
	}
	if err := os.WriteFile(path, []byte(`{"avatar":"image"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := readPrefs(path).Avatar; got != "glm" {
		t.Fatalf("old avatar was not converted at load: %q", got)
	}
	if err := os.WriteFile(path, []byte(`{"avatar":""}`), 0600); err != nil {
		t.Fatal(err)
	}
	if readPrefs(path).Avatar != "whale" {
		t.Fatal("empty avatar must use whale")
	}
	if err := os.WriteFile(path, []byte(`{"palette":1,"avatar":"eye"}`), 0600); err != nil {
		t.Fatal(err)
	}
	legacy := &desktop{prefs: readPrefs(path)}
	if legacy.currentPalette().dark {
		t.Fatal("legacy light selection was overwritten")
	}
}

func TestVoiceInputPreferencesDefaultAndRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "native-ui.json")
	prefs := readPrefs(path)
	if prefs.VoiceInputEnabled || prefs.VoiceHotkey != "Alt" || prefs.VoiceInputDeviceID != "" {
		t.Fatalf("unexpected voice defaults: %+v", prefs)
	}
	if !prefs.VoiceModifierLeft || !prefs.VoiceModifierRight {
		t.Fatalf("modifier sides should default to both: %+v", prefs)
	}
	if err := os.WriteFile(path, []byte(`{"voiceInputEnabled":true,"voiceModelId":"qwen","voiceHotkey":"Ctrl+Alt+R","voiceInputDeviceId":"endpoint-1"}`), 0600); err != nil {
		t.Fatal(err)
	}
	prefs = readPrefs(path)
	if !prefs.VoiceInputEnabled || prefs.VoiceModelID != "qwen" || prefs.VoiceHotkey != "Ctrl+Alt+R" || prefs.VoiceInputDeviceID != "endpoint-1" {
		t.Fatalf("voice preferences did not load: %+v", prefs)
	}
	if err := os.WriteFile(path, []byte(`{"voiceHotkey":"RightAlt","voiceModifierLeft":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	legacy := readPrefs(path)
	if legacy.VoiceHotkey != "Alt" {
		t.Fatalf("legacy Alt shortcut migrated to %q, want Alt", legacy.VoiceHotkey)
	}
	if legacy.VoiceModifierLeft || !legacy.VoiceModifierRight {
		t.Fatalf("explicit modifier side preference was not preserved: %+v", legacy)
	}
	if err := os.WriteFile(path, []byte(`{"voiceModifierLeft":false,"voiceModifierRight":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := readPrefs(path); !got.VoiceModifierLeft || !got.VoiceModifierRight {
		t.Fatalf("both sides off must fall back to both on: %+v", got)
	}
}

func TestCloseToTrayPreferenceDefaultsOnAndPersistsTheOptOut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "native-ui.json")
	// A record written before the preference existed must keep the documented
	// close-to-tray behavior, not flip it off through the zero value.
	if err := os.WriteFile(path, []byte(`{"avatar":"bot"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := readPrefs(path); !got.CloseToTray {
		t.Fatalf("missing closeToTray must default on: %+v", got)
	}
	if err := os.WriteFile(path, []byte(`{"closeToTray":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	off := readPrefs(path)
	if off.CloseToTray {
		t.Fatalf("an explicit opt-out was overwritten: %+v", off)
	}
	// Writing it back must keep the opt-out: with omitempty the false would
	// vanish and the next read would restore the default.
	data, err := json.MarshalIndent(off, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"closeToTray": false`) {
		t.Fatalf("the opt-out must be written out, not omitted: %s", data)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if got := readPrefs(path); got.CloseToTray {
		t.Fatalf("the opt-out did not survive a save: %+v", got)
	}
}

func TestCloseButtonQuitsWithoutATrayOrAnOptOut(t *testing.T) {
	// No tray means no way back into a hidden window, so the close button quits
	// even with the preference on.
	if (&desktop{prefs: defaultPrefs()}).closeHidesToTray() {
		t.Fatal("no tray must quit")
	}
	saved := defaultPrefs()
	saved.CloseToTray = false
	if (&desktop{prefs: saved, tray: &mygo.Tray{}}).closeHidesToTray() {
		t.Fatal("the opt-out must quit even with a tray")
	}
	if !(&desktop{prefs: defaultPrefs(), tray: &mygo.Tray{}}).closeHidesToTray() {
		t.Fatal("tray plus the default preference must hide")
	}
}

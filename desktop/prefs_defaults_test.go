package desktop

import (
	"os"
	"path/filepath"
	"testing"
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

func TestHistoryPageSizePreference(t *testing.T) {
	path := filepath.Join(t.TempDir(), "native-ui.json")
	for _, tc := range []struct {
		data string
		want int
	}{
		{`{}`, 80}, {`{"historyPageSize":20}`, 20}, {`{"historyPageSize":140}`, 140}, {`{"historyPageSize":999999}`, 80},
	} {
		if err := os.WriteFile(path, []byte(tc.data), 0600); err != nil {
			t.Fatal(err)
		}
		if got := readPrefs(path).HistoryPageSize; got != tc.want {
			t.Fatalf("%s: %d", tc.data, got)
		}
	}
}

// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type fakeAutostartHost struct {
	enabled        bool
	getErr, setErr error
	sets           []bool
}

func (h *fakeAutostartHost) GetAutostartEnabled() (bool, error) {
	return h.enabled, h.getErr
}

func (h *fakeAutostartHost) SetAutostartEnabled(enabled bool) error {
	h.sets = append(h.sets, enabled)
	if h.setErr != nil {
		return h.setErr
	}
	h.enabled = enabled
	return nil
}

func TestAutostartDefaultOnlyRegistersOnce(t *testing.T) {
	host := &fakeAutostartHost{}
	enabled, initialized, err := initializeAutostart(host, defaultPrefs().AutostartInitialized)
	if err != nil || !enabled || !initialized || len(host.sets) != 1 || !host.sets[0] {
		t.Fatalf("first launch did not enable autostart: enabled=%v initialized=%v sets=%v err=%v", enabled, initialized, host.sets, err)
	}
	// A subsequent app or OS opt-out must not be overwritten on relaunch.
	host.enabled = false
	enabled, initialized, err = initializeAutostart(host, initialized)
	if err != nil || enabled || !initialized || len(host.sets) != 1 {
		t.Fatalf("relaunch overwrote the opt-out: enabled=%v initialized=%v sets=%v err=%v", enabled, initialized, host.sets, err)
	}
}

func TestAutostartDefaultKeepsExistingRegistration(t *testing.T) {
	host := &fakeAutostartHost{enabled: true}
	enabled, initialized, err := initializeAutostart(host, false)
	if err != nil || !enabled || !initialized || len(host.sets) != 0 {
		t.Fatalf("existing registration was rewritten: enabled=%v initialized=%v sets=%v err=%v", enabled, initialized, host.sets, err)
	}
}

func TestAutostartDefaultFailureCanRetry(t *testing.T) {
	failure := errors.New("autostart unavailable")
	for _, readFailure := range []bool{false, true} {
		host := &fakeAutostartHost{setErr: failure}
		if readFailure {
			host.getErr = failure
		}
		enabled, initialized, err := initializeAutostart(host, false)
		if !errors.Is(err, failure) || enabled || initialized {
			t.Fatalf("failure consumed the default: enabled=%v initialized=%v err=%v", enabled, initialized, err)
		}
		if readFailure && len(host.sets) != 0 {
			t.Fatal("failed state read must not register autostart")
		}
		host.getErr, host.setErr = nil, nil
		if enabled, initialized, err := initializeAutostart(host, initialized); err != nil || !enabled || !initialized {
			t.Fatalf("retry failed: enabled=%v initialized=%v err=%v", enabled, initialized, err)
		}
	}
}

func TestAutostartPreferencesPreserveUpgradeState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "native-ui.json")
	if readPrefs(path).AutostartInitialized {
		t.Fatal("new installation must apply the default")
	}
	for _, data := range []string{`{}`, `{"appearance":"light"}`, `{"autostartInitialized":true}`, `{"autostartInitialized":false,`} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		prefs := readPrefs(path)
		host := &fakeAutostartHost{}
		enabled, initialized, err := initializeAutostart(host, prefs.AutostartInitialized)
		if err != nil || enabled || !initialized || len(host.sets) != 0 {
			t.Fatalf("upgrade overwrote existing OS state for %s: enabled=%v initialized=%v sets=%v err=%v", data, enabled, initialized, host.sets, err)
		}
	}
}

func TestAutostartUnreadablePreferencesKeepSystemState(t *testing.T) {
	// Reading a directory fails on every supported platform without requiring
	// permissions or touching a real user's preferences.
	prefs := readPrefs(t.TempDir())
	host := &fakeAutostartHost{}
	enabled, initialized, err := initializeAutostart(host, prefs.AutostartInitialized)
	if err != nil || enabled || !initialized || len(host.sets) != 0 {
		t.Fatalf("unreadable preferences overwrote OS state: enabled=%v initialized=%v sets=%v err=%v", enabled, initialized, host.sets, err)
	}
}

func TestAutostartInitializationMarkerRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "native-ui.json")
	for _, initialized := range []bool{false, true} {
		prefs := defaultPrefs()
		prefs.AutostartInitialized = initialized
		data, err := json.Marshal(prefs)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if got := readPrefs(path).AutostartInitialized; got != initialized {
			t.Fatalf("initialization marker changed on reload: got %v want %v", got, initialized)
		}
	}
}

func TestAutostartSettingsReflectSystemState(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		a := &desktop{prefs: defaultPrefs(), autostartValue: enabled}
		a.settings.Autostart = !enabled
		a.syncSettingsForm()
		if a.settings.Autostart != enabled {
			t.Fatalf("settings show %v instead of OS state %v", a.settings.Autostart, enabled)
		}
	}
}

// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"encoding/json"
	"github.com/egoist/mygo/ui"
	"os"
	"path/filepath"
	"testing"
	"time"
	_ "time/tzdata"
)

func TestAutoAppearanceLocalTimeBoundaries(t *testing.T) {
	for _, zoneName := range []string{"Asia/Shanghai", "America/New_York", "Europe/London", "Pacific/Auckland"} {
		zone, err := time.LoadLocation(zoneName)
		if err != nil {
			t.Fatal(err)
		}
		for _, date := range []struct {
			month time.Month
			day   int
		}{{time.March, 10}, {time.November, 3}} {
			a := &desktop{prefs: defaultPrefs()}
			a.prefs.Appearance = "auto"
			for _, tc := range []struct {
				hour, minute, second int
				dark                 bool
			}{
				{0, 0, 0, true}, {6, 59, 59, true}, {7, 0, 0, false}, {17, 59, 59, false}, {18, 0, 0, true}, {23, 59, 59, true},
			} {
				now := time.Date(2024, date.month, date.day, tc.hour, tc.minute, tc.second, 0, zone)
				a.refreshAutoAppearance(now)
				if a.currentPalette().dark != tc.dark {
					t.Fatalf("%v: dark=%v want %v", now, a.currentPalette().dark, tc.dark)
				}
				if a.refreshAutoAppearance(now) {
					t.Fatal("unchanged time reported a theme change")
				}
				a.syncSettingsForm()
				if a.settings.AppearanceMode != 2 || a.prefs.Appearance != "auto" {
					t.Fatal("automatic selection was lost")
				}
			}
		}
	}
}

func TestAutoAppearanceIgnoresLegacyLocation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "native-ui.json")
	if err := os.WriteFile(path, []byte(`{"appearance":"auto","solarManual":true,"solarLocation":{"city":"北京","latitude":39.9,"longitude":116.4}}`), 0600); err != nil {
		t.Fatal(err)
	}
	a := &desktop{prefs: readPrefs(path)}
	a.refreshAutoAppearance(time.Date(2024, time.January, 1, 7, 0, 0, 0, time.UTC))
	if a.currentPalette().dark {
		t.Fatal("legacy position changed local-time rule")
	}
	data, err := json.Marshal(a.prefs)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if _, ok := saved["solarLocation"]; ok {
		t.Fatal("retired location was persisted")
	}
}

func TestAutomaticAppearanceSettingAndManualOverride(t *testing.T) {
	a := &desktop{prefs: defaultPrefs()}
	a.syncSettingsForm()
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		ui.Column(c).Fill().Children(func() { a.settingsGeneral(c) })
	}, 850, 950)
	if err := tt.Click("自动"); err != nil {
		t.Fatal(err)
	}
	if a.prefs.Appearance != "auto" || a.settings.AppearanceMode != 2 {
		t.Fatal("automatic option did not select")
	}
	if err := tt.Click("浅色"); err != nil {
		t.Fatal(err)
	}
	a.refreshAutoAppearance(time.Date(2024, time.January, 1, 23, 0, 0, 0, time.UTC))
	if a.prefs.Appearance != "light" || a.currentPalette().dark {
		t.Fatal("automatic refresh overrode manual choice")
	}
}

func TestStoppingAutoAppearanceInvalidatesTimer(t *testing.T) {
	a := &desktop{autoAppearance: autoAppearanceState{Timer: time.NewTimer(time.Hour), Generation: 2}}
	a.stopAutoAppearance()
	if a.autoAppearance.Timer != nil || a.autoAppearance.Generation == 2 {
		t.Fatal("timer/callback remained active")
	}
}

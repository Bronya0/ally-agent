// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	backend "ally-dev/internal/app"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

type settingsTestHost struct {
	*backend.App
	autostart fakeAutostartHost
}

func (h *settingsTestHost) SetAutostartEnabled(enabled bool) error {
	return h.autostart.SetAutostartEnabled(enabled)
}

func settingsSaveFixture(t *testing.T) *desktop {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("ALLY_DATA_DIR", dir)
	core := backend.NewApp()
	cfg, err := core.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.LLMRetries = 3
	if err := core.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := &desktop{core: core, config: cfg, configDraft: cfg, prefs: defaultPrefs()}
	if err := <-a.savePrefs(); err != nil {
		t.Fatal(err)
	}
	a.page = "settings"
	a.syncSettingsForm()
	return a
}

func TestSettingsPageStagesControlsAndCommitsFinalValues(t *testing.T) {
	a := settingsSaveFixture(t)
	path := filepath.Join(a.core.AppDataDir(), "config.json")
	before, _ := os.ReadFile(path)
	prefsBefore, _ := os.ReadFile(a.prefsPath())
	a.settings.Modules = map[string]int{"settingsGeneral": 3}
	tt := settingsLayoutTester(a, 1100, 1800)
	r := settingsControlBounds(t, tt, ui.RoleGroup, "LLM 请求重试次数")
	for _, text := range []string{"4", "7", "0"} {
		tt.ClickAt(r.X+10, r.Y+r.H/2)
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Type(text)
		tt.Frame()
	}
	a.configDraft.CustomPrompt = "final prompt"
	a.settings.BgOpacity = 0
	a.selectAppearance("light")
	a.setPetSize(220)
	a.selectSettingsSection("高级")
	advanced := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		ui.Column(c).Fill().Children(func() { a.settingsAdvanced(c) })
	}, 1100, 1600)
	control, ok := advanced.AccessByRole(ui.RoleSwitch, "Go")
	if !ok {
		t.Fatal("missing validation control")
	}
	r = control.Bounds
	advanced.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	a.setModeVisible("skills", false)
	a.settings.Autostart = true
	a.selectSettingsSection("通用")
	after, _ := os.ReadFile(path)
	prefsAfter, _ := os.ReadFile(a.prefsPath())
	if string(before) != string(after) || string(prefsBefore) != string(prefsAfter) || a.configSaveTail != nil || !a.prefsPending || a.configDraft.LLMRetries != 3 {
		t.Fatal("editing or switching sections persisted a settings draft")
	}
	if !a.settingsPendingEdits() {
		t.Fatal("final edits are not pending")
	}
	// The leave entry must capture final numeric input. The headless fixture has
	// no native window, so persist that same captured plan explicitly below.
	a.commitSettingsOnLeave()
	if a.configDraft.LLMRetries != 0 {
		t.Fatal("leaving settings did not capture final numeric input")
	}
	plan := a.prepareSettingsCommit()
	host := &settingsTestHost{App: a.core}
	result := plan.persist(host)
	if err := result.err(); err != nil {
		t.Fatal(err)
	}
	a.committingSettings = true
	if err := <-a.savePrefs(); err != nil {
		t.Fatal(err)
	}
	a.committingSettings = false
	a.applySettingsSave(plan, result)
	loaded, err := a.core.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	var disk backend.ConfigState
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &disk); err != nil {
		t.Fatal(err)
	}
	if loaded.LLMRetries != 0 || disk.LLMRetries != 0 || disk.CustomPrompt != "final prompt" || disk.BackgroundOpacity != 0 ||
		!boolValue(disk.AutoValidationGo) || !reflect.DeepEqual(disk.HiddenModes, []string{"skills"}) {
		t.Fatalf("final control values did not reach disk: %+v", disk)
	}
	prefs := readPrefs(a.prefsPath())
	if prefs.Appearance != "light" || prefs.Pet.Size != 220 || len(host.autostart.sets) != 1 || !host.autostart.enabled {
		t.Fatal("preference or autostart edit did not commit")
	}
	if a.settingsPendingEdits() {
		t.Fatal("successful commit remained dirty")
	}
	// The next visit must show the committed zero and final values.
	a.beginSettings()
	if a.settings.Retries != 0 || a.settings.BgOpacity != 0 || !a.settings.Validation["Go"] {
		t.Fatal("reopening settings lost committed values")
	}
}

func TestSettingsFailedCommitRetainsDraftAndCanRetry(t *testing.T) {
	a := settingsSaveFixture(t)
	a.configDraft.CustomPrompt = "pending"
	a.configDraft.ProxyMode, a.configDraft.ProxyURL = "manual", "invalid://proxy"
	a.settings.Autostart = true
	plan := a.prepareSettingsCommit()
	host := &settingsTestHost{App: a.core}
	result := plan.persist(host)
	if result.err() == nil {
		t.Fatal("expected save rejection")
	}
	a.applySettingsSave(plan, result)
	a.beginSettings()
	if !a.settingsPendingEdits() || a.configDraft.CustomPrompt != "pending" || a.autostartValue || len(host.autostart.sets) != 0 {
		t.Fatal("failure discarded the draft or applied autostart")
	}
	a.configDraft.ProxyURL = "http://127.0.0.1:7890"
	plan = a.prepareSettingsCommit()
	result = plan.persist(host)
	if result.err() != nil {
		t.Fatal(result.err())
	}
	a.applySettingsSave(plan, result)
	if a.settingsPendingEdits() {
		t.Fatal("retry remained dirty")
	}
}

func TestSettingsCommitResultPreservesLaterInput(t *testing.T) {
	a := settingsSaveFixture(t)
	a.settings.Retries = 5
	plan := a.prepareSettingsCommit()
	result := plan.persist(&settingsTestHost{App: a.core})
	if result.err() != nil {
		t.Fatal(result.err())
	}
	a.settings.Retries = 8
	a.applySettingsSave(plan, result)
	a.beginSettings()
	if a.settings.Retries != 8 || !a.settingsPendingEdits() {
		t.Fatal("save completion replaced later input")
	}
	// A failure saving only preferences must also retain a pending retry.
	a.applySettingsSave(plan, settingsSaveResult{prefsErr: errors.New("disk unavailable")})
	if !a.prefsPending {
		t.Fatal("failed preference write was marked saved")
	}
}

func TestSettingsLoadsRejectStaleResultsAndPreserveInput(t *testing.T) {
	a := settingsSaveFixture(t)
	sequence := a.settingsLoadSequence
	old := a.config
	a.commitSettingsOnLeave()
	a.beginSettings()
	if a.applySettingsLoad(sequence, nil, old, false) {
		t.Fatal("load from a previous visit replaced the new page")
	}
	a.settings.Retries = 7
	a.settings.Autostart = true
	fresh := old
	fresh.UserAgent = "fresh-agent"
	if !a.applySettingsLoad(a.settingsLoadSequence, nil, fresh, false) {
		t.Fatal("current load was rejected")
	}
	if a.settings.Retries != 7 || !a.settings.Autostart || !a.settingsPendingEdits() || a.config.UserAgent != "fresh-agent" {
		t.Fatal("current load replaced edits or failed to update committed state")
	}
	a.configSaveTail = make(chan struct{})
	if a.applySettingsLoad(a.settingsLoadSequence, nil, old, false) {
		t.Fatal("load begun before a later write replaced newer state")
	}
}

func TestSettingsLoadUpdatesAutostartWithoutOverwritingOtherEdits(t *testing.T) {
	a := settingsSaveFixture(t)
	a.settings.Retries = 7
	if !a.applySettingsLoad(a.settingsLoadSequence, nil, a.config, true) {
		t.Fatal("current load was rejected")
	}
	if a.settings.Retries != 7 || !a.settings.Autostart || !a.autostartValue || a.prepareSettingsCommit().autostart != nil {
		t.Fatal("load replaced input or treated the system autostart state as an edit")
	}
}

func TestSettingsNavigationNoopDoesNotSubmit(t *testing.T) {
	a := settingsSaveFixture(t)
	a.settings.Retries = 7
	sequence := a.settingsLoadSequence
	a.newSession() // No current conversation, so the page does not change.
	a.activateWorkspaceTab(-1)
	if a.page != "settings" || a.settingsLoadSequence != sequence || a.configDraft.LLMRetries != 3 {
		t.Fatal("navigation that kept settings open submitted its draft")
	}
}

func TestSettingsExplicitReadingSizesSurviveReopen(t *testing.T) {
	a := settingsLayoutFixture()
	a.config.MessageFontSize, a.config.CodeFontSize = 15.5, 14
	a.configDraft = a.config
	a.syncSettingsForm()
	if a.settings.FontMessage != 15.5 || a.settings.FontCode != 14 || a.settingsPendingEdits() {
		t.Fatal("opening settings rewrote explicit font sizes")
	}
}

func TestSettingsCommitOwnsMutableSnapshots(t *testing.T) {
	a := settingsSaveFixture(t)
	a.config.Models = []backend.ModelConfig{{ProviderName: "test", Model: "one", ReasoningEffort: "low"}}
	a.configDraft = a.config
	a.syncSettingsForm()
	a.settings.Retries = 5
	plan := a.prepareSettingsCommit()
	a.configDraft.Models[0].ReasoningEffort = "max"
	a.configDraft.HiddenModes = append(a.configDraft.HiddenModes, "skills")
	if plan.request.Base.Models[0].ReasoningEffort != "low" || plan.request.Draft.Models[0].ReasoningEffort != "low" || len(plan.request.Draft.HiddenModes) != 0 {
		t.Fatal("editing UI state changed an already captured commit")
	}
}

func TestSettingsReentryCanRevertAnInFlightCommit(t *testing.T) {
	a := settingsSaveFixture(t)
	a.settings.Retries, a.settings.Autostart = 5, true
	first := a.prepareSettingsCommit()
	first.sequence = 1
	a.settingsSaveSequence = 1
	a.settingsInFlight = &first
	a.beginSettings()
	if a.settings.Retries != 5 || !a.settings.Autostart {
		t.Fatal("reentry replaced a submitted draft with old saved values")
	}
	a.settings.Retries, a.settings.Autostart = 3, false
	if !a.settingsPendingEdits() {
		t.Fatal("reverting a pending save was treated as unchanged")
	}
	second := a.prepareSettingsCommit()
	second.sequence = 2
	a.settingsSaveSequence = 2
	a.settingsInFlight = &second
	host := &settingsTestHost{App: a.core}
	firstResult := first.persist(host)
	if firstResult.err() != nil {
		t.Fatal(firstResult.err())
	}
	a.applySettingsSave(first, firstResult)
	secondResult := second.persist(host)
	if secondResult.err() != nil {
		t.Fatal(secondResult.err())
	}
	a.applySettingsSave(second, secondResult)
	stored, _ := a.core.GetConfig()
	if stored.LLMRetries != 3 || host.autostart.enabled || a.settingsPendingEdits() {
		t.Fatal("older commit overwrote the user's later revert")
	}
}

func TestSettingsNextCommitRetriesEditsAfterEarlierFailure(t *testing.T) {
	a := settingsSaveFixture(t)
	a.settings.Retries = 5
	a.configDraft.ProxyMode, a.configDraft.ProxyURL = "manual", "invalid://proxy"
	first := a.prepareSettingsCommit()
	a.settingsInFlight = &first
	a.configDraft.ProxyMode, a.configDraft.ProxyURL = "off", ""
	a.configDraft.CustomPrompt = "later edit"
	second := a.prepareSettingsCommit()
	host := &settingsTestHost{App: a.core}
	if first.persist(host).err() == nil {
		t.Fatal("expected earlier failure")
	}
	result := second.persist(host)
	if result.err() != nil {
		t.Fatal(result.err())
	}
	a.applySettingsSave(second, result)
	stored, _ := a.core.GetConfig()
	if stored.LLMRetries != 5 || stored.CustomPrompt != "later edit" || a.settingsPendingEdits() {
		t.Fatal("next commit lost edits from the failed pending draft")
	}
}

// Require every scalar form field to be identified as a config mirror or a
// separate UI/service state. A new numeric or boolean setting cannot silently
// miss settingsConfigDraft's conversion back to persisted configuration.
func TestSettingsFormScalarMirrorsReachConfig(t *testing.T) {
	mirrors := map[string]string{
		"Retries": "LLMRetries", "Concurrency": "ToolConcurrency", "CompactTimeout": "CompactTimeoutSeconds",
		"Threshold": "CompactThreshold", "ThresholdTokens": "CompactThresholdTokens",
		"FontMessage": "MessageFontSize", "FontCode": "CodeFontSize", "FontTool": "ToolFontSize",
		"FontSub": "SubFontSize", "FontAux": "AuxFontSize", "BgOpacity": "BackgroundOpacity",
		"AllowPrivate": "AllowPrivateNetwork", "AutoUpdate": "AutoUpdate",
		"SendSessionID": "SendSessionIDHeader", "SendXSessionID": "SendXSessionIDHeader",
	}
	separate := map[string]bool{
		"HistoryPageSize": true, "AppearanceMode": true, "ProxyModeIndex": true, "Autostart": true,
		"UpdateBusy": true, "ProxyBusy": true, "APIPort": true, "APIBusy": true, "APIRevision": true,
		"NewToken": true, "SessionHeaderConfirmOpen": true,
	}
	formType := reflect.TypeOf(settingsForm{})
	for i := 0; i < formType.NumField(); i++ {
		field := formType.Field(i)
		if field.Type.Kind() != reflect.Float64 && field.Type.Kind() != reflect.Bool && field.Type.Kind() != reflect.Int && field.Type.Kind() != reflect.Uint64 {
			continue
		}
		if separate[field.Name] {
			continue
		}
		configField, ok := mirrors[field.Name]
		if !ok {
			t.Fatalf("new scalar form field %s needs persistence coverage", field.Name)
		}
		t.Run(field.Name, func(t *testing.T) {
			a := settingsLayoutFixture()
			value := reflect.ValueOf(&a.settings).Elem().FieldByName(field.Name)
			if value.Kind() == reflect.Bool {
				value.SetBool(!value.Bool())
			} else {
				value.SetFloat(value.Float() + 1)
			}
			projected := reflect.ValueOf(a.settingsConfigDraft()).FieldByName(configField)
			if projected.Kind() == reflect.Pointer {
				projected = projected.Elem()
			}
			switch projected.Kind() {
			case reflect.Bool:
				if projected.Bool() != value.Bool() {
					t.Fatal("boolean mirror was not projected")
				}
			case reflect.Int:
				if projected.Int() != int64(value.Float()) {
					t.Fatal("numeric mirror was not projected")
				}
			case reflect.Float64:
				if projected.Float() != value.Float() {
					t.Fatal("numeric mirror was not projected")
				}
			}
			if !a.settingsPendingEdits() {
				t.Fatal("mirror edit was not detected")
			}
		})
	}
}

func TestSettingsValidationMirrorsEveryConfigLanguage(t *testing.T) {
	cfgType := reflect.TypeOf(backend.ConfigState{})
	for i := 0; i < cfgType.NumField(); i++ {
		field := cfgType.Field(i)
		language, ok := strings.CutPrefix(field.Name, "AutoValidation")
		if !ok {
			continue
		}
		t.Run(language, func(t *testing.T) {
			a := settingsLayoutFixture()
			if _, ok := a.settings.Validation[language]; !ok {
				t.Fatal("new validation field is absent from the form")
			}
			for _, enabled := range []bool{true, false} {
				a.settings.Validation[language] = enabled
				value := reflect.ValueOf(a.settingsConfigDraft()).FieldByName(field.Name)
				if value.IsNil() || value.Elem().Bool() != enabled {
					t.Fatal("validation choice was omitted from the config draft")
				}
			}
		})
	}
}

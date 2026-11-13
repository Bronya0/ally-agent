// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	backend "ally-dev/internal/app"
	"ally-dev/locales"
	"errors"
	"reflect"
)

type settingsConfigHost interface {
	SaveNativeSettings(backend.NativeSettingsRequest) error
	GetConfig() (backend.ConfigState, error)
	SetAutostartEnabled(bool) error
}

type settingsCommit struct {
	request        backend.NativeSettingsRequest
	configChanged  bool
	autostart      *bool
	autostartValue bool
	sequence       uint64
}

type settingsSaveResult struct {
	config                  *backend.ConfigState
	configErr, autostartErr error
	prefsErr                error
}

func (r settingsSaveResult) err() error { return errors.Join(r.configErr, r.autostartErr, r.prefsErr) }

func (a *desktop) settingsBaseline() backend.ConfigState {
	if a.settingsBase != nil {
		return *a.settingsBase
	}
	return normalizeSettingsConfig(a.config)
}

func (a *desktop) settingsConfigPending() bool {
	baseline := a.settingsBaseline()
	backgroundPending := a.settings.BackgroundPath != nil
	if submitted := a.settingsInFlight; submitted != nil {
		baseline = submitted.request.Draft
		backgroundPending = !reflect.DeepEqual(a.settings.BackgroundPath, submitted.request.BackgroundPath)
	}
	return backgroundPending || !reflect.DeepEqual(normalizeSettingsConfig(a.settingsConfigDraft()), baseline)
}

func (a *desktop) settingsPendingEdits() bool {
	autostart := a.autostartValue
	if submitted := a.settingsInFlight; submitted != nil {
		autostart = submitted.autostartValue
	}
	return a.prefsPending || a.settings.Autostart != autostart || a.settingsConfigPending()
}

// Take one final snapshot on the main thread; typing and changing controls do
// not perform I/O. This plan is shared by page leave and the quit/update barrier.
func (a *desktop) prepareSettingsCommit() settingsCommit {
	plan := settingsCommit{autostartValue: a.settings.Autostart}
	plan.request.Base = a.settingsBaseline().Clone()
	plan.request.Draft = normalizeSettingsConfig(a.settingsConfigDraft()).Clone()
	if submitted := a.settingsInFlight; submitted != nil {
		plan.request.Base = settingsRevertBaseline(plan.request.Base, submitted.request.Draft, plan.request.Draft)
	}
	plan.configChanged = a.settings.BackgroundPath != nil || !reflect.DeepEqual(plan.request.Base, plan.request.Draft)
	if path := a.settings.BackgroundPath; path != nil {
		copy := *path
		plan.request.BackgroundPath = &copy
	}
	if a.settings.Autostart != a.autostartValue || a.settingsInFlight != nil && a.settings.Autostart != a.settingsInFlight.autostartValue {
		plan.autostart = boolPtr(a.settings.Autostart)
	}
	a.configDraft = plan.request.Draft.Clone()
	return plan
}

// A user can return while an earlier save is queued and revert a value to its
// original state. Include that revert in the next transaction, while keeping
// other pending edits eligible for retry if the earlier transaction failed.
func settingsRevertBaseline(base, submitted, draft backend.ConfigState) backend.ConfigState {
	result := base.Clone()
	previous, desired := reflect.ValueOf(submitted), reflect.ValueOf(draft)
	dest := reflect.ValueOf(&result).Elem()
	for i := 0; i < dest.NumField(); i++ {
		field := dest.Field(i)
		if field.CanSet() && reflect.DeepEqual(field.Interface(), desired.Field(i).Interface()) &&
			!reflect.DeepEqual(previous.Field(i).Interface(), desired.Field(i).Interface()) {
			field.Set(previous.Field(i))
		}
	}
	return result
}

func (plan settingsCommit) persist(host settingsConfigHost) settingsSaveResult {
	var result settingsSaveResult
	if plan.configChanged {
		result.configErr = host.SaveNativeSettings(plan.request)
		if result.configErr != nil {
			return result
		}
		cfg, err := host.GetConfig()
		result.configErr = err
		if err == nil {
			result.config = &cfg
		}
	}
	if plan.autostart != nil {
		result.autostartErr = host.SetAutostartEnabled(*plan.autostart)
	}
	return result
}

func (a *desktop) saveSettings(notice ...string) {
	plan := a.prepareSettingsCommit()
	if a.core == nil || a.win == nil {
		return
	}
	var prefsResult <-chan error
	if a.prefsPending {
		a.committingSettings = true
		prefsResult = a.savePrefs()
		a.committingSettings = false
	}
	if !plan.configChanged && plan.autostart == nil && prefsResult == nil {
		return
	}
	a.settingsSaveSequence++
	plan.sequence = a.settingsSaveSequence
	a.settingsInFlight = &plan
	previous, done := a.configWriteSlot()
	go func() {
		defer close(done)
		waitConfigWrite(previous)
		result := plan.persist(a.core)
		if plan.configChanged {
			a.writeFailures.record("config", result.configErr)
		}
		if plan.autostart != nil && result.configErr == nil {
			a.writeFailures.record("autostart", result.autostartErr)
		}
		if prefsResult != nil {
			result.prefsErr = <-prefsResult
		}
		a.win.Update(func() {
			a.applySettingsSave(plan, result)
			if err := result.err(); err != nil {
				a.notifyError(locales.Text("保存设置失败：") + err.Error())
				return
			}
			if len(notice) > 0 {
				a.notifySuccess(notice[0])
			}
		})
	}()
}

func (a *desktop) applySettingsSave(plan settingsCommit, result settingsSaveResult) {
	if result.config != nil {
		toolsChanged := !reflect.DeepEqual(a.config.DisabledTools, result.config.DisabledTools)
		a.config = *result.config
		if toolsChanged {
			a.refreshMCPInventory()
		}
	}
	if result.configErr == nil && result.autostartErr == nil && plan.autostart != nil {
		a.autostartValue = *plan.autostart
	}
	if plan.sequence != a.settingsSaveSequence {
		return
	}
	a.settingsInFlight = nil
	if result.configErr == nil {
		baseline := plan.request.Draft
		if path := plan.request.BackgroundPath; path != nil && result.config != nil {
			baseline.BackgroundImage = result.config.BackgroundImage
			if current := a.settings.BackgroundPath; current != nil && *current == *path {
				a.settings.BackgroundPath = nil
				a.configDraft.BackgroundImage = baseline.BackgroundImage
			}
		}
		a.settingsBase = &baseline
	}
	if result.prefsErr != nil {
		a.prefsPending = true
	}
}

func (a *desktop) commitSettingsOnLeave() {
	a.settingsLoadSequence++
	if a.voiceHotkeyCapturing {
		a.finishVoiceHotkeyCapture("")
	}
	if a.settingsPendingEdits() {
		a.saveSettings(locales.Text("设置已保存"))
	}
}

func (a *desktop) beginSettings() {
	a.contextToolsOpen = false
	a.settings.ContextLoadingConfirmOpen = false
	a.settings.ContextLoadingToDisable = ""
	a.settingsLoadSequence++
	if a.settingsInFlight == nil && (a.settingsBase == nil || !a.settingsPendingEdits()) {
		a.configDraft = a.config
		a.syncSettingsForm()
	}
}

// Wait for queued commits and reject stale page loads. A late result updates
// persisted state without replacing anything the user has begun editing.
func (a *desktop) loadSettings() {
	a.settingsLoadSequence++
	sequence := a.settingsLoadSequence
	previous := a.configSaveTail
	go func() {
		waitConfigWrite(previous)
		cfg, err := a.core.GetConfig()
		if err != nil {
			a.win.Update(func() { a.notifyError(err.Error()) })
			return
		}
		sandbox, _ := a.core.GetSandboxStatus()
		api, _ := a.core.GetApiServiceState()
		autostart, _ := a.core.GetAutostartEnabled()
		a.win.Update(func() {
			if !a.applySettingsLoad(sequence, previous, cfg, autostart) {
				return
			}
			a.refreshMCPInventory()
			a.sandboxState = sandbox
			a.apiState = api
			a.apiPort = float64(api.Port)
		})
	}()
}

func (a *desktop) applySettingsLoad(sequence uint64, previous chan struct{}, cfg backend.ConfigState, autostart bool) bool {
	if sequence != a.settingsLoadSequence || a.page != "settings" || a.closing || previous != a.configSaveTail {
		return false
	}
	pending := a.settingsPendingEdits() || a.settingsInFlight != nil
	autostartEdited := a.settings.Autostart != a.autostartValue || a.settingsInFlight != nil
	a.config = cfg
	a.autostartValue = autostart
	if !autostartEdited {
		a.settings.Autostart = autostart
	}
	if !pending {
		a.configDraft = cfg
		a.syncSettingsForm()
	}
	return true
}

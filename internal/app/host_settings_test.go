// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNativeSettingsPreserveConcurrentConfigChanges(t *testing.T) {
	a := configTransactionApp(t)
	model := ModelConfig{ProviderName: "test", Model: "one", ReasoningEffort: "low"}
	if err := a.SaveNativeModel(model, nil); err != nil {
		t.Fatal(err)
	}
	base, _ := a.GetConfig()
	draft := cloneConfig(base)
	draft.CustomPrompt = "edited prompt"
	draft.LLMRetries = 8
	id := identityOfModel(model)
	model.ReasoningEffort = "max"
	if err := a.SaveNativeModel(model, &id); err != nil {
		t.Fatal(err)
	}
	if err := a.updateConfigAndPersist(func(cfg *ConfigState) {
		cfg.DisabledSkills = []string{"later-skill"}
		cfg.SkippedUpdates = []string{"v99"}
		cfg.UserAgent = "later-agent"
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.SaveNativeSettings(NativeSettingsRequest{Base: base, Draft: draft}); err != nil {
		t.Fatal(err)
	}
	stored, err := readConfigFile(a.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if stored.CustomPrompt != draft.CustomPrompt || stored.LLMRetries != 8 || stored.Models[0].ReasoningEffort != "max" ||
		!reflect.DeepEqual(stored.DisabledSkills, []string{"later-skill"}) || !reflect.DeepEqual(stored.SkippedUpdates, []string{"v99"}) || stored.UserAgent != "later-agent" {
		t.Fatalf("native settings reverted another writer or lost an edit: %+v", stored)
	}
}

func TestNativeSettingsRoundTripExplicitClears(t *testing.T) {
	a := configTransactionApp(t)
	if err := a.updateConfigAndPersist(func(cfg *ConfigState) {
		cfg.LLMRetries = 3
		cfg.CustomPrompt = "old prompt"
		cfg.HiddenModes = []string{"skills"}
		cfg.AutoValidationGo = boolPtr(true)
	}); err != nil {
		t.Fatal(err)
	}
	base, _ := a.GetConfig()
	draft := cloneConfig(base)
	draft.LLMRetries, draft.BackgroundOpacity = 0, 0
	draft.CustomPrompt = ""
	draft.HiddenModes = []string{}
	draft.AutoValidationGo, draft.AutoUpdate = boolPtr(false), boolPtr(false)
	if err := a.SaveNativeSettings(NativeSettingsRequest{Base: base, Draft: draft}); err != nil {
		t.Fatal(err)
	}
	loaded, err := readConfigFile(a.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.LLMRetries != 0 || effectiveLLMRetries(loaded) != defaultLLMRetries || loaded.BackgroundOpacity != 0 ||
		loaded.CustomPrompt != "" || len(loaded.HiddenModes) != 0 || *loaded.AutoValidationGo || *loaded.AutoUpdate {
		t.Fatalf("explicit zero, false, empty string or collection did not survive reload: %+v", loaded)
	}
}

func TestNativeSettingsFailedSaveCanRetry(t *testing.T) {
	a := configTransactionApp(t)
	base, _ := a.GetConfig()
	draft := cloneConfig(base)
	draft.CustomPrompt = "pending prompt"
	draft.ProxyMode, draft.ProxyURL = proxyModeManual, "invalid://proxy"
	request := NativeSettingsRequest{Base: base, Draft: draft}
	if err := a.SaveNativeSettings(request); err == nil {
		t.Fatal("expected invalid proxy rejection")
	}
	current, _ := a.GetConfig()
	if !reflect.DeepEqual(current, base) {
		t.Fatal("failed save published its draft")
	}
	request.Draft.ProxyURL = "http://127.0.0.1:7890"
	if err := a.SaveNativeSettings(request); err != nil {
		t.Fatal(err)
	}
	loaded, err := readConfigFile(a.configPath)
	if err != nil || loaded.CustomPrompt != draft.CustomPrompt || loaded.ProxyURL != request.Draft.ProxyURL {
		t.Fatalf("retry lost edits: %+v %v", loaded, err)
	}
}

func TestNativeSettingsBackgroundChangesCommitWithForm(t *testing.T) {
	a := configTransactionApp(t)
	old := filepath.Join(filepath.Dir(a.configPath), "background.png")
	if err := os.WriteFile(old, []byte("old-image"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.updateConfigAndPersist(func(cfg *ConfigState) { cfg.BackgroundImage = "background.png" }); err != nil {
		t.Fatal(err)
	}
	base, _ := a.GetConfig()
	source := filepath.Join(t.TempDir(), "picked.png")
	if err := os.WriteFile(source, []byte("new-image"), 0600); err != nil {
		t.Fatal(err)
	}
	draft := cloneConfig(base)
	draft.ProxyMode, draft.ProxyURL = proxyModeManual, "invalid://proxy"
	request := NativeSettingsRequest{Base: base, Draft: draft, BackgroundPath: &source}
	if err := a.SaveNativeSettings(request); err == nil {
		t.Fatal("expected save rejection")
	}
	if data, err := os.ReadFile(old); err != nil || string(data) != "old-image" {
		t.Fatal("failed save destroyed the previous image")
	}
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(a.configPath), "background.*"))
	if len(files) != 1 {
		t.Fatalf("failed save left draft image files: %v", files)
	}
	request.Draft.ProxyMode, request.Draft.ProxyURL = proxyModeOff, ""
	if err := a.SaveNativeSettings(request); err != nil {
		t.Fatal(err)
	}
	stored, _ := a.GetConfig()
	if stored.BackgroundImage == "background.png" || stored.BackgroundImage == "" {
		t.Fatal("new image was not committed")
	}
	if data, err := os.ReadFile(filepath.Join(filepath.Dir(a.configPath), stored.BackgroundImage)); err != nil || string(data) != "new-image" {
		t.Fatal("committed image is missing")
	}
	clear := ""
	if err := a.SaveNativeSettings(NativeSettingsRequest{Base: stored, Draft: stored, BackgroundPath: &clear}); err != nil {
		t.Fatal(err)
	}
	stored, _ = a.GetConfig()
	files, _ = filepath.Glob(filepath.Join(filepath.Dir(a.configPath), "background.*"))
	if stored.BackgroundImage != "" || len(files) != 0 {
		t.Fatal("clearing the background did not persist and release the asset")
	}
}

// A new persisted setting must be copied by the shared settings transaction.
// Seed every exported field, including numeric fields, so adding one without
// wiring settingsConfig fails here rather than silently dropping user edits.
func TestSettingsConfigCoversEveryEditableField(t *testing.T) {
	base := defaultConfigState()
	var req ConfigState
	seedConfigValue(reflect.ValueOf(&req).Elem())
	seedSettingsNumbers(reflect.ValueOf(&req).Elem())
	req.ProxyMode, req.ProxyURL = proxyModeManual, "http://127.0.0.1:7890"
	req.AutoCompactMethod = autoCompactMethodSummary
	req.CompactThresholdMode = CompactThresholdModeTokens
	expected := normalizeStoredConfig(cloneConfig(req))
	got := settingsConfig(base, req)
	excluded := map[string]bool{
		"AuthType": true, "OAuthAccountID": true, "ProviderName": true, "APIFormat": true,
		"BaseURL": true, "APIKey": true, "APIKeys": true, "Model": true, "MaxTokens": true,
		"ContextWindow": true, "TokenParam": true, "ReasoningTag": true, "VisionCapable": true,
		"ReasoningEffort": true, "CustomHeaders": true, "ExtraRoots": true, "BackgroundImage": true,
	}
	want, actual := reflect.ValueOf(expected), reflect.ValueOf(got)
	for i := 0; i < want.NumField(); i++ {
		field := want.Type().Field(i)
		if field.PkgPath != "" || excluded[field.Name] {
			continue
		}
		t.Run(field.Name, func(t *testing.T) {
			if !reflect.DeepEqual(want.Field(i).Interface(), actual.Field(i).Interface()) {
				t.Fatalf("editable field is omitted or rewritten by settingsConfig: got %#v want %#v", actual.Field(i).Interface(), want.Field(i).Interface())
			}
		})
	}
}

func seedSettingsNumbers(v reflect.Value) {
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).CanSet() {
				seedSettingsNumbers(v.Field(i))
			}
		}
	case reflect.Pointer:
		if !v.IsNil() {
			seedSettingsNumbers(v.Elem())
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			seedSettingsNumbers(v.Index(i))
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(37)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(37)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(37)
	}
}

func TestNativeModelRejectsStaleEdit(t *testing.T) {
	a := configTransactionApp(t)
	if err := a.SaveNativeModel(ModelConfig{ProviderName: "test", Model: "one", ReasoningEffort: "low"}, nil); err != nil {
		t.Fatal(err)
	}
	cfg, err := a.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	baseline := cfg.Models[0]
	updated := baseline
	updated.ReasoningEffort = "high"
	if err := a.SaveNativeModelIfUnchanged(updated, &baseline); err != nil {
		t.Fatal(err)
	}
	stale := baseline
	stale.DisplayName = "stale"
	if err := a.SaveNativeModelIfUnchanged(stale, &baseline); err == nil {
		t.Fatal("stale edit was accepted")
	}
	cfg, err = a.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Models[0].ReasoningEffort != "high" || cfg.Models[0].DisplayName == "stale" {
		t.Fatalf("concurrent update lost: %+v", cfg.Models[0])
	}
}

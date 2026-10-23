// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func configTransactionApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("ALLY_DATA_DIR", dir)
	a := NewApp()
	a.initialized = true
	a.configPath = filepath.Join(dir, "config.json")
	a.config = defaultConfigState()
	return a
}

func TestConfigConcurrentMutationsKeepEveryChange(t *testing.T) {
	a := configTransactionApp(t)
	var wg sync.WaitGroup
	errs := make(chan error, 24)
	for i := 0; i < cap(errs); i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs <- a.updateConfigAndPersist(func(cfg *ConfigState) {
				cfg.SkippedUpdates = append(cfg.SkippedUpdates, fmt.Sprintf("v%d", i))
			})
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	current, err := a.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(current.SkippedUpdates) != cap(errs) {
		t.Fatalf("lost concurrent updates: %d", len(current.SkippedUpdates))
	}
	loaded, err := readConfigFile(a.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loadedConfig(loaded), current) {
		t.Fatal("disk and published config diverged")
	}
}

func TestConfigFailedWriteDoesNotPublish(t *testing.T) {
	a := configTransactionApp(t)
	if err := a.setDisabledSkills([]string{"original"}); err != nil {
		t.Fatal(err)
	}
	before, _ := a.GetConfig()
	blocker := filepath.Join(filepath.Dir(a.configPath), "blocker")
	if err := os.WriteFile(blocker, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	a.configPath = filepath.Join(blocker, "config.json")
	if err := a.updateConfigAndPersist(func(cfg *ConfigState) {
		cfg.DisabledSkills[0] = "changed"
		cfg.ProxyMode = proxyModeManual
		cfg.ProxyURL = "http://127.0.0.1:7890"
	}); err == nil {
		t.Fatal("expected disk error")
	}
	after, _ := a.GetConfig()
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(a.disabledSkills, before.DisabledSkills) {
		t.Fatal("failed save changed active config or mirrors")
	}
}

func TestConfigPendingTransactionKeepsReadersAvailable(t *testing.T) {
	a := configTransactionApp(t)
	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- a.commitConfig(func(cfg *ConfigState) error {
			cfg.CustomPrompt = "committed"
			close(started)
			<-release
			return nil
		})
	}()
	<-started
	before, err := a.GetConfig()
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	if before.CustomPrompt != "" {
		t.Fatal("uncommitted state was published")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	after, _ := a.GetConfig()
	if after.CustomPrompt != "committed" {
		t.Fatal("committed state missing")
	}
}

// Seed every exported field, including newly added reference fields, so a new
// slice/map/pointer omitted from cloneConfig cannot silently introduce aliasing.
func seedConfigValue(v reflect.Value) {
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).CanSet() {
				seedConfigValue(v.Field(i))
			}
		}
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		seedConfigValue(v.Elem())
	case reflect.Slice:
		v.Set(reflect.MakeSlice(v.Type(), 1, 1))
		seedConfigValue(v.Index(0))
	case reflect.Map:
		v.Set(reflect.MakeMap(v.Type()))
		key, value := reflect.New(v.Type().Key()).Elem(), reflect.New(v.Type().Elem()).Elem()
		seedConfigValue(key)
		seedConfigValue(value)
		v.SetMapIndex(key, value)
	case reflect.String:
		v.SetString("original")
	case reflect.Bool:
		v.SetBool(true)
	}
}

func mutateConfigReferences(v reflect.Value) {
	switch v.Kind() {
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).CanSet() {
				mutateConfigReferences(v.Field(i))
			}
		}
	case reflect.Pointer:
		if !v.IsNil() {
			mutateConfigReferences(v.Elem())
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			mutateConfigReferences(v.Index(i))
		}
	case reflect.Map:
		for _, key := range v.MapKeys() {
			v.SetMapIndex(key, reflect.ValueOf("changed"))
		}
	case reflect.String:
		v.SetString("changed")
	case reflect.Bool:
		v.SetBool(false)
	}
}

func TestConfigSnapshotsOwnAllMutableFields(t *testing.T) {
	a := configTransactionApp(t)
	seedConfigValue(reflect.ValueOf(&a.config).Elem())
	before, _ := json.Marshal(a.config)
	snapshot, err := a.GetConfig()
	if err != nil {
		t.Fatal(err)
	}
	mutateConfigReferences(reflect.ValueOf(&snapshot).Elem())
	after, _ := json.Marshal(a.config)
	if string(before) != string(after) {
		t.Fatal("GetConfig returned aliased state")
	}
	_ = a.effectiveConfig(ConfigState{})
	after, _ = json.Marshal(a.config)
	if string(before) != string(after) {
		t.Fatal("request normalization changed stored state")
	}
}

func TestConfigDiskRoundTripPreservesExplicitValues(t *testing.T) {
	a := configTransactionApp(t)
	cfg, _ := a.GetConfig()
	cfg.BackgroundOpacity = 0
	cfg.CustomPrompt, cfg.GitHubToken, cfg.KBRoot = "", "", ""
	cfg.AutoUpdate, cfg.AllowPrivateNetwork = boolPtr(false), boolPtr(false)
	cfg.Models = []ModelConfig{}
	cfg.HiddenModes = []string{}
	if err := a.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := readConfigFile(a.configPath)
	if err != nil {
		t.Fatal(err)
	}
	got := loadedConfig(loaded)
	if got.BackgroundOpacity != 0 || *got.AutoUpdate || *got.AllowPrivateNetwork || len(got.Models) != 0 || got.CustomPrompt != "" {
		t.Fatal("explicit zero/false/empty lost on reload")
	}
	if err := os.WriteFile(a.configPath, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err = readConfigFile(a.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := loadedConfig(loaded); got.BackgroundOpacity != defaultBackgroundOpacity {
		t.Fatal("missing legacy field lost its default")
	}
}

// Fixture-only whole replacement. Production writers always mutate current state.
func (a *App) saveConfig(cfg ConfigState) error {
	return a.commitConfig(func(next *ConfigState) error { *next = cloneConfig(cfg); return nil })
}

func TestConfigRejectedSettingsKeepCommittedState(t *testing.T) {
	a := configTransactionApp(t)
	if err := a.SaveConfig(defaultConfigState()); err != nil {
		t.Fatal(err)
	}
	before, _ := a.GetConfig()
	diskBefore, err := os.ReadFile(a.configPath)
	if err != nil {
		t.Fatal(err)
	}
	invalid := cloneConfig(before)
	invalid.ProxyMode, invalid.ProxyURL = proxyModeManual, "invalid://proxy"
	invalid.CustomPrompt = "must not become active"
	if err := a.SaveConfig(invalid); err == nil {
		t.Fatal("invalid proxy accepted")
	}
	after, _ := a.GetConfig()
	diskAfter, err := os.ReadFile(a.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) || string(diskBefore) != string(diskAfter) {
		t.Fatal("rejected settings changed committed state")
	}
}

func TestConfigSettingsExcludeRequestState(t *testing.T) {
	a := configTransactionApp(t)
	if err := a.updateConfigAndPersist(func(cfg *ConfigState) { cfg.BackgroundImage = "owned.png" }); err != nil {
		t.Fatal(err)
	}
	req, _ := a.GetConfig()
	req.BackgroundImage = "stale.png"
	req.ExtraRoots = []string{"request-only"}
	req.Model, req.APIKey = "request-only", "request-only"
	if err := a.SaveConfig(req); err != nil {
		t.Fatal(err)
	}
	got, _ := a.GetConfig()
	if got.BackgroundImage != "owned.png" || got.ExtraRoots != nil || got.Model != "" || got.APIKey != "" {
		t.Fatal("settings persisted request or picker-owned fields")
	}
}

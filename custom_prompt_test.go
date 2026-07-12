package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCustomPromptPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	// Simulate first startup: no config file exists yet.
	app1 := NewApp()
	app1.initialized = true
	app1.configPath = configPath
	app1.config = defaultConfigState()

	// Verify default config has empty CustomPrompt
	if app1.config.CustomPrompt != "" {
		t.Fatalf("expected empty CustomPrompt in default config, got %q", app1.config.CustomPrompt)
	}

	// Simulate user setting a custom prompt via SaveConfig
	customVal := "role play：我是你哥哥，你是我妹妹~"
	if err := app1.SaveConfig(ConfigState{CustomPrompt: customVal}); err != nil {
		t.Fatal(err)
	}

	// Verify in-memory config has the custom prompt
	if app1.config.CustomPrompt != customVal {
		t.Fatalf("expected CustomPrompt %q after save, got %q", customVal, app1.config.CustomPrompt)
	}

	// Verify config file has the custom prompt
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), customVal) {
		t.Fatalf("expected config file to contain %q, got:\n%s", customVal, string(data))
	}

	// Simulate restart: new App instance, re-read from file
	app2 := NewApp()
	app2.initialized = true
	app2.configPath = configPath
	app2.config = defaultConfigState()

	loaded, err := readConfigFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	app2.config = mergeConfig(app2.config, loaded)

	if app2.config.CustomPrompt != customVal {
		t.Fatalf("expected CustomPrompt %q after restart, got %q", customVal, app2.config.CustomPrompt)
	}
}

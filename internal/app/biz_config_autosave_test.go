// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveConfigPreservesPickerBackground(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("ALLY_DATA_DIR", dir)
	app := NewApp()
	app.initialized = true
	app.configPath = filepath.Join(dir, "config.json")
	for _, current := range []string{"new-background.png", ""} {
		app.config = defaultConfigState()
		app.config.BackgroundImage = current
		stale := app.config
		stale.BackgroundImage = "old-background.jpg"
		stale.UserAgent = "selected-agent/1.0"
		if err := app.SaveConfig(stale); err != nil {
			t.Fatal(err)
		}
		if app.config.BackgroundImage != current {
			t.Fatalf("settings restored a stale image: got %q, want %q", app.config.BackgroundImage, current)
		}
		data, err := os.ReadFile(app.configPath)
		if err != nil {
			t.Fatal(err)
		}
		var saved ConfigState
		if err := json.Unmarshal(data, &saved); err != nil {
			t.Fatal(err)
		}
		if saved.BackgroundImage != current || saved.UserAgent != stale.UserAgent {
			t.Fatalf("saved image/UA = %q/%q, want %q/%q", saved.BackgroundImage, saved.UserAgent, current, stale.UserAgent)
		}
	}
}

func TestSavedUserAgentAppliesToConnectionTest(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("ALLY_DATA_DIR", dir)
	for _, format := range []string{apiFormatOpenAIChat, apiFormatOpenAIResponses, apiFormatAnthropicMessages} {
		t.Run(format, func(t *testing.T) {
			received := make(chan string, 8)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				received <- r.Header.Get("User-Agent")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `{"error":{"type":"invalid_request_error","message":"header probe complete"}}`)
			}))
			defer server.Close()
			app := NewApp()
			app.initialized = true
			app.configPath = filepath.Join(t.TempDir(), "config.json")
			app.config = defaultConfigState()
			model := ModelConfig{Model: "test-model", APIFormat: format, BaseURL: server.URL, APIKey: "test-key", MaxTokens: 64}
			for _, ua := range []string{"selected-agent/1.0", ""} {
				cfg := app.config
				cfg.UserAgent = ua
				if err := app.SaveConfig(cfg); err != nil {
					t.Fatal(err)
				}
				if err := app.TestModelConnection(model); err == nil {
					t.Fatal("expected the probe endpoint's terminal error")
				}
				want := ua
				if want == "" {
					want = defaultHTTPUA
				}
				select {
				case got := <-received:
					if got != want {
						t.Fatalf("received User-Agent = %q, want %q", got, want)
					}
				default:
					t.Fatal("connection test never reached the probe")
				}
			}
		})
	}
}

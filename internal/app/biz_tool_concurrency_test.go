package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestToolConcurrencyConfig(t *testing.T) {
	for _, tc := range []struct{ input, want int }{{0, 4}, {-5, 1}, {1, 1}, {4, 4}, {12, 12}, {16, 16}, {100, 16}} {
		if got := clampToolConcurrency(tc.input); got != tc.want {
			t.Errorf("clamp(%d) = %d, want %d", tc.input, got, tc.want)
		}
		cfg := persistableConfig(ConfigState{ToolConcurrency: tc.input})
		if cfg.ToolConcurrency != tc.want {
			t.Errorf("persist(%d) = %d", tc.input, cfg.ToolConcurrency)
		}
	}
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	path := filepath.Join(dir, "config.json")
	for _, input := range []string{`{}`, `{"toolConcurrency":0}`, `{"toolConcurrency":-1}`, `{"toolConcurrency":8}`, `{"toolConcurrency":99}`} {
		if err := os.WriteFile(path, []byte(input), 0600); err != nil {
			t.Fatal(err)
		}
		cfg, err := readConfigFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var raw ConfigState
		if err := json.Unmarshal([]byte(input), &raw); err != nil {
			t.Fatal(err)
		}
		if cfg.ToolConcurrency != clampToolConcurrency(raw.ToolConcurrency) {
			t.Fatalf("load %s: %d", input, cfg.ToolConcurrency)
		}
		if err := persistConfigFile(path, persistableConfig(cfg)); err != nil {
			t.Fatal(err)
		}
		reloaded, err := readConfigFile(path)
		if err != nil || reloaded.ToolConcurrency != cfg.ToolConcurrency {
			t.Fatalf("roundtrip: %+v, %v", reloaded, err)
		}
	}
	if got := mergeConfig(ConfigState{ToolConcurrency: 8}, ConfigState{ToolConcurrency: 16}); got.ToolConcurrency != 8 {
		t.Fatal("request overlay changed global concurrency")
	}
}

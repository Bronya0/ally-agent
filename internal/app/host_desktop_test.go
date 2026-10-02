// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"os"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// redirectAppStateDir 把 appDataDir() 指向临时目录，测试绝不触碰真实
// ~/.ally_agent。
func redirectAppStateDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	return dir
}

func TestWindowStateRoundTrip(t *testing.T) {
	redirectAppStateDir(t)
	want := windowState{X: 120, Y: 80, Width: 1440, Height: 900, Maximized: true}
	if err := persistWindowState(want); err != nil {
		t.Fatalf("persistWindowState: %v", err)
	}
	got, ok := loadWindowState()
	if !ok {
		t.Fatal("loadWindowState after persist: ok=false")
	}
	if got != want {
		t.Fatalf("round-trip mismatch: got %+v want %+v", got, want)
	}
}

func TestLoadWindowStateRejectsInvalid(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"corrupt json", "{not json"},
		{"width below min", `{"x":10,"y":10,"width":100,"height":900}`},
		{"height below min", `{"x":10,"y":10,"width":1200,"height":10}`},
		{"width above max", `{"x":10,"y":10,"width":99999,"height":900}`},
		{"negative size", `{"x":10,"y":10,"width":-1200,"height":900}`},
		{"empty object", `{}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			redirectAppStateDir(t)
			if err := os.MkdirAll(appDataDir(), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(windowStatePath(), []byte(tc.json), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, ok := loadWindowState(); ok {
				t.Fatalf("expected rejection for %s", tc.json)
			}
		})
	}
}

func TestLoadWindowStateMissingFile(t *testing.T) {
	redirectAppStateDir(t)
	if _, ok := loadWindowState(); ok {
		t.Fatal("expected ok=false with no window.json")
	}
}

func TestApplySavedWindowGeometry(t *testing.T) {
	t.Run("applies persisted normal geometry", func(t *testing.T) {
		redirectAppStateDir(t)
		if err := persistWindowState(windowState{X: 200, Y: 100, Width: 1600, Height: 1000}); err != nil {
			t.Fatal(err)
		}
		opts := application.WebviewWindowOptions{Width: DefaultWindowWidth, Height: DefaultWindowHeight}
		if !ApplySavedWindowGeometry(&opts) {
			t.Fatal("expected applied=true")
		}
		if opts.Width != 1600 || opts.Height != 1000 || opts.X != 200 || opts.Y != 100 {
			t.Fatalf("geometry not applied: %+v", opts)
		}
		if opts.InitialPosition != application.WindowXY {
			t.Fatalf("expected WindowXY for non-zero position, got %v", opts.InitialPosition)
		}
		if opts.StartState != application.WindowStateNormal {
			t.Fatalf("expected normal start state, got %v", opts.StartState)
		}
	})
	t.Run("maximized sets start state without WindowXY", func(t *testing.T) {
		redirectAppStateDir(t)
		if err := persistWindowState(windowState{X: 200, Y: 100, Width: 1600, Height: 1000, Maximized: true}); err != nil {
			t.Fatal(err)
		}
		opts := application.WebviewWindowOptions{}
		if !ApplySavedWindowGeometry(&opts) {
			t.Fatal("expected applied=true")
		}
		if opts.StartState != application.WindowStateMaximised {
			t.Fatalf("expected maximised start state, got %v", opts.StartState)
		}
		if opts.InitialPosition == application.WindowXY {
			t.Fatal("maximized restore must not set WindowXY (setPosition after maximise breaks the maximised state on Windows)")
		}
		if opts.X != 200 || opts.Y != 100 || opts.Width != 1600 || opts.Height != 1000 {
			t.Fatalf("restore bounds missing: %+v", opts)
		}
	})
	t.Run("no state leaves defaults", func(t *testing.T) {
		redirectAppStateDir(t)
		opts := application.WebviewWindowOptions{Width: DefaultWindowWidth, Height: DefaultWindowHeight}
		if ApplySavedWindowGeometry(&opts) {
			t.Fatal("expected applied=false")
		}
		if opts.Width != DefaultWindowWidth || opts.Height != DefaultWindowHeight || opts.X != 0 || opts.Y != 0 {
			t.Fatalf("defaults mutated: %+v", opts)
		}
	})
}

func TestWindowStateTrackerUpdate(t *testing.T) {
	tr := &windowStateTracker{}
	tr.update(false, 40, 30, 1280, 720)
	s, ok := tr.snapshot()
	if !ok || s != (windowState{X: 40, Y: 30, Width: 1280, Height: 720}) {
		t.Fatalf("unexpected snapshot: %+v ok=%v", s, ok)
	}
	// 最大化只更新标志位，不污染普通 bounds。
	tr.update(true, 0, 0, 3840, 2160)
	s, _ = tr.snapshot()
	if s.Maximized != true || s.Width != 1280 || s.X != 40 {
		t.Fatalf("maximize leaked into bounds: %+v", s)
	}
	// 还原后恢复更新。
	tr.update(false, 60, 50, 1300, 740)
	s, _ = tr.snapshot()
	if s.Width != 1300 || s.X != 60 || s.Maximized {
		t.Fatalf("restore update failed: %+v", s)
	}
	// 原点 (0,0) 偏移 1px，规避 Windows CW_USEDEFAULT 级联定位。
	tr.update(false, 0, 0, 1280, 720)
	s, _ = tr.snapshot()
	if s.X != 1 || s.Y != 1 {
		t.Fatalf("origin not nudged off CW_USEDEFAULT: %+v", s)
	}
	// 销毁途中的非正尺寸不落快照。
	tr.update(false, 10, 10, 0, 0)
	s, _ = tr.snapshot()
	if s.Width != 1280 {
		t.Fatalf("non-positive size overwrote snapshot: %+v", s)
	}
}

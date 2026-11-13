// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ally-dev/internal/tools/screenshot"
)

func TestMain(m *testing.M) {
	cleanupEnv, err := isolateProcessEnvironment()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	cleanupEnv()
	os.Exit(code)
}

// isolateProcessEnvironment points every process-wide path a test can reach
// (home, temp root, Go build caches, git config) at one throwaway directory, so
// no case can read or write the user's real ~/.ally_agent, build cache or git
// config. The values are set once, before m.Run, which is exactly what lets
// isolated cases use t.Parallel: t.Setenv is forbidden there, but constants are
// not a race. Per-case separation still comes from injecting a data directory
// into the App (see newAppState).
func isolateProcessEnvironment() (func(), error) {
	root, err := os.MkdirTemp("", "ally-test-env-")
	if err != nil {
		return nil, err
	}
	tempRoot := filepath.Join(root, "tmp")
	goCache := filepath.Join(root, "gocache")
	goMod := filepath.Join(root, "gomod")
	for _, dir := range []string{tempRoot, goCache, goMod} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			_ = os.RemoveAll(root)
			return nil, err
		}
	}
	env := map[string]string{
		"HOME":                root,
		"USERPROFILE":         root,
		"TMP":                 tempRoot,
		"TEMP":                tempRoot,
		"TMPDIR":              tempRoot,
		"GOCACHE":             goCache,
		"GOMODCACHE":          goMod,
		"GOWORK":              "off",
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_CONFIG_GLOBAL":   filepath.Join(root, "gitconfig"),
	}
	for key, value := range env {
		if err := os.Setenv(key, value); err != nil {
			_ = os.RemoveAll(root)
			return nil, err
		}
	}
	return func() { _ = os.RemoveAll(root) }, nil
}

func TestScreenshotScaledMetadataMatchesImage(t *testing.T) {
	t.Parallel()
	frame, scaled := screenshot.ScaleForModel(image.NewRGBA(image.Rect(0, 0, 2560, 1440)))
	img, err := screenshot.EncodeForModel(frame)
	if err != nil {
		t.Fatal(err)
	}
	img.Scaled = scaled
	for _, source := range []screenshot.CaptureSource{
		{Kind: screenshot.TargetWindow, Window: &screenshot.Window{ID: 7, Title: "fixture", X: -100, Y: 200, Width: 2560, Height: 1440}, OriginalWidth: 2560, OriginalHeight: 1440},
		{Kind: screenshot.TargetRegion, Region: &screenshot.Rect{X: -100, Y: 200, Width: 2560, Height: 1440}, OriginalWidth: 2560, OriginalHeight: 1440},
		{Kind: screenshot.TargetDisplay, OriginalWidth: 2560, OriginalHeight: 1440},
	} {
		r := screenshotResult(img, source)
		_, encoded, _ := strings.Cut(r.DataURL, ",")
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		config, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || r.Width != config.Width || r.Height != config.Height || r.Width != 1568 || r.Height != 882 {
			t.Fatalf("metadata must describe actual pixels: result=%dx%d actual=%dx%d error=%v", r.Width, r.Height, config.Width, config.Height, err)
		}
		model := renderScreenshotResultForModel(r)
		if !strings.Contains(model, `size=1568x882`) || !r.Scaled {
			t.Fatalf("model image size lost: %s", model)
		}
		if r.Source != "display" && !strings.Contains(model, `rect=-100,200,2560,1440`) {
			t.Fatalf("source region must keep its original coordinate dimensions: %s", model)
		}
	}
	legacy := renderScreenshotResultForModel(ScreenshotResult{Source: "region", Width: 640, Height: 480})
	if !strings.Contains(legacy, `rect=0,0,640,480`) {
		t.Fatalf("old screenshot metadata must remain readable: %s", legacy)
	}
}

func TestScreenshotCancellationAndTimeoutCodes(t *testing.T) {
	for _, check := range []struct {
		err  error
		code string
	}{{context.Canceled, "E_CANCELLED"}, {context.DeadlineExceeded, "E_CAPTURE_TIMEOUT"}} {
		result := toolErrorResult(screenshotToolError(check.err))
		if result.ErrorCode != check.code {
			t.Fatalf("got %s, want %s", result.ErrorCode, check.code)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a := &App{}
	if _, err := a.captureForScreenshot(ctx, ScreenshotRequest{WindowID: 1}); !errors.Is(err, context.Canceled) {
		t.Fatalf("orchestration must pass run cancellation through: %v", err)
	}
}

func TestScreenshotRejectsConflictingTargets(t *testing.T) {
	t.Parallel()
	app := newIsolatedTestApp(t)
	cfg := ConfigState{Workspace: t.TempDir()}
	for _, args := range []string{
		`{"windowId":-989130788,"windowTitle":"x"}`,
		`{"windowId":1,"windowTitle":"x"}`,
		`{"windowId":1,"region":{"x":0,"y":0,"width":10,"height":10}}`,
		`{"windowTitle":"x","region":{"x":0,"y":0,"width":10,"height":10}}`,
		`{"action":"lst"}`,
	} {
		res := app.executeTool(context.Background(), cfg, "s-1", "screenshot", []byte(args))
		if res.OK || res.ErrorCode != "E_BAD_ARGS" {
			t.Fatalf("args %s must fail with E_BAD_ARGS, got ok=%v code=%q", args, res.OK, res.ErrorCode)
		}
	}
}

func TestScreenshotRejectsBadRegion(t *testing.T) {
	t.Parallel()
	app := newIsolatedTestApp(t)
	cfg := ConfigState{Workspace: t.TempDir()}
	for _, args := range []string{
		`{"region":{"x":0,"y":0,"width":0,"height":10}}`,
		`{"region":{"x":0,"y":0,"width":99999,"height":10}}`,
	} {
		res := app.executeTool(context.Background(), cfg, "s-1", "screenshot", []byte(args))
		if res.OK || res.ErrorCode != "E_BAD_ARGS" {
			t.Fatalf("region %s must fail with E_BAD_ARGS, got ok=%v code=%q", args, res.OK, res.ErrorCode)
		}
	}
}

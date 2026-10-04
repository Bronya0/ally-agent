// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"ally-dev/internal/tools/screenshot"
)

// needGraphicsSession skips the test on machines without an interactive
// desktop session (headless CI): the enumeration error then reports capture
// as unsupported rather than running against a real desktop.
func needGraphicsSession(t *testing.T) {
	t.Helper()
	if _, _, err := screenshot.ListWindows(screenshot.ListOptions{Limit: 1}); err != nil {
		if errors.Is(err, screenshot.ErrCaptureUnsupported) || errors.Is(err, screenshot.ErrCaptureFailed) {
			t.Skipf("no interactive desktop session: %v", err)
		}
	}
}

func TestScreenshotRejectsConflictingTargets(t *testing.T) {
	app := NewApp()
	cfg := ConfigState{Workspace: t.TempDir()}
	for _, args := range []string{
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
	app := NewApp()
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

func TestScreenshotUnknownWindowTitleFailsWithCode(t *testing.T) {
	needGraphicsSession(t)
	app := NewApp()
	cfg := ConfigState{Workspace: t.TempDir()}
	res := app.executeTool(context.Background(), cfg, "s-1", "screenshot",
		[]byte(`{"windowTitle":"ally-agent-definitely-not-a-window-9x7y3z"}`))
	if res.OK {
		t.Fatal("unknown window must fail")
	}
	if res.ErrorCode != "E_WINDOW_NOT_FOUND" && res.ErrorCode != "E_CAPTURE_FAILED" {
		t.Fatalf("unexpected code %q (E_CAPTURE_FAILED tolerated on locked sessions)", res.ErrorCode)
	}
}

func TestScreenshotListActionReturnsBoundedRows(t *testing.T) {
	needGraphicsSession(t)
	app := NewApp()
	cfg := ConfigState{Workspace: t.TempDir()}
	res := app.executeTool(context.Background(), cfg, "s-1", "screenshot", []byte(`{"action":"list"}`))
	if !res.OK {
		t.Fatalf("screenshot action=list failed: %v", res.Error)
	}
	r, ok := res.Data.(ListWindowsResult)
	if !ok {
		t.Fatalf("unexpected data type %T", res.Data)
	}
	if len(r.Windows) > screenshot.MaxListWindows || r.Total < len(r.Windows) {
		t.Fatalf("rows=%d total=%d cap=%d", len(r.Windows), r.Total, screenshot.MaxListWindows)
	}
}

func TestScreenshotCapturesPrimaryDisplay(t *testing.T) {
	needGraphicsSession(t)
	app := NewApp()
	cfg := ConfigState{Workspace: t.TempDir()}
	res := app.executeTool(context.Background(), cfg, "s-1", "screenshot", []byte(`{}`))
	if !res.OK {
		t.Skipf("capture not available: %v code=%v", res.Error, res.ErrorCode)
	}
	r, ok := res.Data.(ScreenshotResult)
	if !ok {
		t.Fatalf("unexpected data type %T", res.Data)
	}
	if r.Width <= 0 || r.Height <= 0 {
		t.Fatalf("frame size %dx%d", r.Width, r.Height)
	}
	if r.DataURL == "" || !strings.HasPrefix(r.DataURL, "data:image/") {
		t.Fatal("result must carry an image data URL")
	}
	if !r.ImageAttached {
		t.Fatal("successful capture must flag the image as attached")
	}
	// The model-facing payload must not carry the base64 blob.
	model := compactToolResultForModel("screenshot", toolResult{OK: true, Data: r}, "{}")
	if strings.Contains(model, "base64,") {
		t.Fatal("model-facing payload must strip the data URL")
	}
	if !strings.Contains(model, `<ally-screenshot source="display"`) {
		t.Fatalf("model-facing payload missing tag block: %q", model)
	}
}

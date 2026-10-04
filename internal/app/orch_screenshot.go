// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

// Section: Screenshot orchestration (orch_<name>.go per AGENTS.md). The pure
// capture logic lives in internal/tools/screenshot; this file binds it to the
// tool surface — request DTOs, argument validation with stable error codes,
// and the result envelope whose DataURL the run loop injects into model
// context (see collectReadImages) and the UI event stream carries to the
// frontend.

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"ally-dev/internal/tools/screenshot"
)

// ListWindowsResult bounds one enumeration response. Windows is capped by
// screenshot.MaxListWindows; Total/Truncated tell the model when the filter
// matched more than the cap so it can narrow the title instead of paging.
type ListWindowsResult struct {
	Windows   []screenshot.Window `json:"windows"`
	Total     int                 `json:"total"`
	Truncated bool                `json:"truncated,omitempty"`
}

// Screenshot action names. The tool is deliberately one tool with an action
// (the service-tool pattern): a standalone list tool would be a permanent
// schema cost for a lookup the capture path already serves — an ambiguous or
// unknown windowTitle fails with the candidate list embedded in the error.
const (
	screenshotActionCapture = "capture"
	screenshotActionList    = "list"
)

// ScreenshotRequest is the screenshot tool's argument surface. Zero capture
// values mean "primary display"; the mutual exclusivity between windowId,
// windowTitle and region is enforced here (the schema cannot express
// at-most-one-of cleanly, so the runtime is the authority). Action-specific
// fields (title/limit for list; window/display/region for capture) are
// validated per action.
type ScreenshotRequest struct {
	Action      string           `json:"action,omitempty"`
	Title       string           `json:"title,omitempty"`
	Limit       int              `json:"limit,omitempty"`
	WindowID    int              `json:"windowId,omitempty"`
	WindowTitle string           `json:"windowTitle,omitempty"`
	Focus       bool             `json:"focus,omitempty"`
	Display     int              `json:"display,omitempty"`
	Region      *screenshot.Rect `json:"region,omitempty"`
}

// ScreenshotResult is the tool's envelope data. DataURL carries the image for
// the frontend event stream and the model-context injection; the model-facing
// compaction (compactToolDataForModel) deliberately strips it — the image
// travels once, as an image part, not as megabytes of base64 inside the tool
// message.
type ScreenshotResult struct {
	Source        string `json:"source"` // "window" | "region" | "display"
	WindowID      int    `json:"windowId,omitempty"`
	Title         string `json:"title,omitempty"`
	Display       int    `json:"display,omitempty"`
	X             int    `json:"x,omitempty"`
	Y             int    `json:"y,omitempty"`
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	Mime          string `json:"mime"`
	Bytes         int    `json:"bytes"`
	Scaled        bool   `json:"scaled,omitempty"`
	ImageAttached bool   `json:"imageAttached"`
	DataURL       string `json:"dataUrl,omitempty"`
}

// screenshotWithConfig dispatches on the action. Both branches share the cfg
// parameter for signature uniformity with every other WithConfig tool; the
// enumeration is machine-scoped, not workspace-scoped. An unrecognized action
// is a usage error, not a silent capture — the schema enum narrows the model,
// and anything that slips past it must fail loudly.
func (a *App) screenshotWithConfig(cfg ConfigState, req ScreenshotRequest) (any, error) {
	action := strings.ToLower(strings.TrimSpace(req.Action))
	switch action {
	case "", screenshotActionCapture:
		return a.captureForScreenshot(req)
	case screenshotActionList:
		return a.listWindowsForScreenshot(req)
	default:
		return ScreenshotResult{}, codedToolError("E_BAD_ARGS",
			fmt.Errorf("unknown action %q (want %q or %q)", req.Action, screenshotActionCapture, screenshotActionList))
	}
}

func (a *App) listWindowsForScreenshot(req ScreenshotRequest) (ListWindowsResult, error) {
	windows, total, err := screenshot.ListWindows(screenshot.ListOptions{Title: req.Title, Limit: req.Limit})
	if err != nil {
		return ListWindowsResult{}, screenshotToolError(err)
	}
	return ListWindowsResult{Windows: windows, Total: total, Truncated: total > len(windows)}, nil
}

// captureForScreenshot validates the request, resolves window targets
// against a fresh enumeration, and captures.
func (a *App) captureForScreenshot(req ScreenshotRequest) (ScreenshotResult, error) {
	// Usage-level target conflicts are refused here with the boundary error
	// code; the pure layer re-checks (Target) as a backstop. The schema
	// documents each capture field as exclusive, so every pair is enforced.
	windowTarget := req.WindowID != 0 || strings.TrimSpace(req.WindowTitle) != ""
	if req.WindowID != 0 && strings.TrimSpace(req.WindowTitle) != "" {
		return ScreenshotResult{}, codedToolError("E_BAD_ARGS",
			errors.New("windowId and windowTitle are mutually exclusive; pass one"))
	}
	if windowTarget && req.Region != nil {
		return ScreenshotResult{}, codedToolError("E_BAD_ARGS",
			errors.New("windowId/windowTitle and region are mutually exclusive; pass one target"))
	}
	opts := screenshot.CaptureOptions{
		WindowID:    req.WindowID,
		WindowTitle: req.WindowTitle,
		Focus:       req.Focus,
		Display:     req.Display,
		Region:      req.Region,
	}
	image, source, err := screenshot.Capture(opts)
	if err != nil {
		return ScreenshotResult{}, screenshotToolError(err)
	}
	result := ScreenshotResult{
		Mime:          image.Mime,
		Bytes:         len(image.Data),
		Scaled:        image.Scaled,
		ImageAttached: true,
		DataURL:       "data:" + image.Mime + ";base64," + base64.StdEncoding.EncodeToString(image.Data),
	}
	switch source.Kind {
	case screenshot.TargetWindow:
		result.Source = "window"
		result.WindowID = source.Window.ID
		result.Title = source.Window.Title
		result.X, result.Y = source.Window.X, source.Window.Y
		result.Width, result.Height = source.Window.Width, source.Window.Height
	case screenshot.TargetRegion:
		result.Source = "region"
		result.X, result.Y = source.Region.X, source.Region.Y
		result.Width, result.Height = source.Region.Width, source.Region.Height
	default:
		result.Source = "display"
		result.Display = req.Display
		// Width/Height come from the encoded frame: the authoritative size
		// the model actually receives (post-downscale).
		result.Width = image.Image.Bounds().Dx()
		result.Height = image.Image.Bounds().Dy()
	}
	return result, nil
}

// screenshotToolError maps package sentinels to stable tool error codes.
func screenshotToolError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, screenshot.ErrWindowNotFound):
		return codedToolError("E_WINDOW_NOT_FOUND", err)
	case errors.Is(err, screenshot.ErrAmbiguousWindow):
		return codedToolError("E_WINDOW_AMBIGUOUS", err)
	case errors.Is(err, screenshot.ErrCaptureUnsupported):
		return codedToolError("E_CAPTURE_UNSUPPORTED", err)
	case errors.Is(err, screenshot.ErrCaptureFailed):
		return codedToolError("E_CAPTURE_FAILED", err)
	case errors.Is(err, screenshot.ErrBadRegion), errors.Is(err, screenshot.ErrDisplayNotFound):
		return codedToolError("E_BAD_ARGS", err)
	default:
		// toolerrors import lives in infra_result.go; reuse the same helper.
		return codedToolError("E_CAPTURE_FAILED", err)
	}
}

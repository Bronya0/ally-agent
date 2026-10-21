// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
//go:build !windows && !darwin && !linux

package screenshot

import (
	"context"
	"image"
)

func listWindows(ctx context.Context) ([]Window, error) {
	return nil, ErrCaptureUnsupported
}

func captureWindow(ctx context.Context, win *Window, focus bool) (image.Image, error) {
	return nil, ErrCaptureUnsupported
}

func captureRegion(ctx context.Context, region Rect) (image.Image, error) {
	return nil, ErrCaptureUnsupported
}

func captureDisplay(ctx context.Context, display int) (image.Image, error) {
	return nil, ErrCaptureUnsupported
}

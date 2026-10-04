// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
//go:build !windows && !darwin && !linux

package screenshot

import "image"

func listWindows() ([]Window, error) {
	return nil, ErrCaptureUnsupported
}

func captureWindow(win Window, focus bool) (image.Image, error) {
	return nil, ErrCaptureUnsupported
}

func captureRegion(region Rect) (image.Image, error) {
	return nil, ErrCaptureUnsupported
}

func captureDisplay(display int) (image.Image, error) {
	return nil, ErrCaptureUnsupported
}

// SPDX-License-Identifier: GPL-3.0-only
//go:build !windows

package screenshot

import "context"

func captureWindowTarget(ctx context.Context, opts CaptureOptions) (ModelImage, CaptureSource, error) {
	img, source, err := captureDirect(ctx, opts)
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return img, source, err
}

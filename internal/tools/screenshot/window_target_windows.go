// SPDX-License-Identifier: GPL-3.0-only
//go:build windows

package screenshot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func captureWindowTarget(ctx context.Context, opts CaptureOptions) (ModelImage, CaptureSource, error) {
	executable, err := os.Executable()
	if err != nil {
		return ModelImage{}, CaptureSource{}, fmt.Errorf("%w: locate capture helper: %v", ErrCaptureFailed, err)
	}
	request, err := json.Marshal(opts)
	if err != nil {
		return ModelImage{}, CaptureSource{}, err
	}
	cmd := exec.CommandContext(ctx, executable, captureHelperFlag, string(request))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000 /* CREATE_NO_WINDOW */}
	cmd.WaitDelay = time.Second
	return runCaptureCommand(ctx, cmd)
}

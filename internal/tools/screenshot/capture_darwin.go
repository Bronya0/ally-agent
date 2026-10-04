// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
//go:build darwin

package screenshot

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// macOS capture rides on the two system-provided entry points, both shipped
// with every macOS install (no Xcode CLT requirement):
//   - screencapture: full screen / region (-R) / one window (-l <CGWindowID>)
//   - osascript (JXA + ObjC bridge): CGWindowListCopyWindowInfo enumeration
//
// Note: window titles (kCGWindowName) are only populated for processes the
// caller may see; without Screen Recording permission the title is empty but
// the process name and id still work, and -l captures keep working.

// listWindows enumerates on-screen, normal-layer windows front to back via
// the JXA bridge. The first row is the frontmost window.
func listWindows() ([]Window, error) {
	script := `ObjC.import('CoreGraphics');
const list = $.CGWindowListCopyWindowInfo($.kCGWindowListOptionOnScreenOnly | $.kCGWindowListExcludeDesktopElements, $.kCGNullWindowID);
const wins = ObjC.deepUnwrap(list) || [];
const out = [];
for (const w of wins) {
  if (w.kCGWindowLayer !== 0) continue;
  const b = w.kCGWindowBounds || {};
  out.push({ id: w.kCGWindowNumber, title: w.kCGWindowName || '', process: w.kCGWindowOwnerName || '',
    x: Math.round(b.X || 0), y: Math.round(b.Y || 0),
    width: Math.round(b.Width || 0), height: Math.round(b.Height || 0) });
}
JSON.stringify(out);`
	out, err := exec.Command("osascript", "-l", "JavaScript", "-e", script).Output()
	if err != nil {
		return nil, fmt.Errorf("%w: window enumeration failed: %v", ErrCaptureUnsupported, err)
	}
	var windows []Window
	if err := json.Unmarshal(out, &windows); err != nil {
		return nil, fmt.Errorf("%w: window enumeration returned unexpected data: %v", ErrCaptureFailed, err)
	}
	for i := range windows {
		windows[i].Title = SanitizeTitle(windows[i].Title)
		windows[i].Focused = i == 0
	}
	return windows, nil
}

func captureWindow(win Window, focus bool) (image.Image, error) {
	// -l captures the window's own content (composited), so occlusion does
	// not corrupt the frame and focusing is unnecessary; a minimized window
	// is not on screen and therefore not enumerable anyway.
	if focus {
		if err := activateWindow(win); err != nil {
			return nil, err
		}
	}
	return screencapture([]string{"-l", strconv.Itoa(win.ID)})
}

func captureRegion(region Rect) (image.Image, error) {
	return screencapture([]string{"-R", fmt.Sprintf("%d,%d,%d,%d", region.X, region.Y, region.Width, region.Height)})
}

func captureDisplay(display int) (image.Image, error) {
	// screencapture -D is 1-based; the tool surface is 0-based.
	if display < 0 {
		return nil, fmt.Errorf("%w: display %d", ErrDisplayNotFound, display)
	}
	return screencapture([]string{"-D", strconv.Itoa(display + 1)})
}

// screencapture runs the system tool into a private temp dir and decodes the
// PNG it produces. Args are a fixed argv array — no shell, no interpolation.
func screencapture(args []string) (image.Image, error) {
	dir, err := os.MkdirTemp("", "ally-screenshot-")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCaptureFailed, err)
	}
	defer os.RemoveAll(dir)
	path := dir + "/capture.png"
	cmd := exec.Command("screencapture", append([]string{"-x", "-t", "png"}, append(args, path)...)...)
	if out, cmdErr := cmd.CombinedOutput(); cmdErr != nil {
		return nil, fmt.Errorf("%w: screencapture: %v: %s", ErrCaptureFailed, cmdErr, tailBytes(out))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: capture file missing: %v", ErrCaptureFailed, err)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: capture not decodable: %v", ErrCaptureFailed, err)
	}
	return img, nil
}

// activateWindow raises the owning application via System Events. Best
// effort: without Accessibility permission the raise silently fails, and a
// refusal here must not fail a capture that would still succeed (window
// captures via -l do not need focus anyway).
func activateWindow(win Window) error {
	if win.Process == "" {
		return nil
	}
	script := fmt.Sprintf(`tell application "System Events" to set frontmost of (first application process whose name is %q) to true`,
		strings.ReplaceAll(win.Process, `"`, ``))
	_ = exec.Command("osascript", "-e", script).Run()
	return nil
}

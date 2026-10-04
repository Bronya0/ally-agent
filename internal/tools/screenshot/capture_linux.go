// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
//go:build linux

package screenshot

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Linux capture shells out to the desktop stack the user already has, in a
// fixed probe order per session type (the pattern proven by the surveyed MCP
// servers). X11 gets window-level capture via xdotool + maim; Wayland gets
// full screen / region via grim (compositor-agnostic via the freedesktop
// screenshot protocols), while per-window capture stays explicitly
// unsupported instead of silently capturing the wrong rectangle.

var linuxFullscreenBackends = []struct {
	binary string
	args   func(path string) []string
}{
	{"maim", func(path string) []string { return []string{path} }},
	{"scrot", func(path string) []string { return []string{path} }},
	{"grim", func(path string) []string { return []string{path} }},
	{"gnome-screenshot", func(path string) []string { return []string{"-f", path} }},
}

var linuxRegionBackends = []struct {
	binary string
	args   func(Rect, string) []string
}{
	{"maim", func(r Rect, path string) []string {
		return []string{"-g", fmt.Sprintf("%dx%d+%d+%d", r.Width, r.Height, r.X, r.Y), path}
	}},
	{"scrot", func(r Rect, path string) []string {
		return []string{"-a", fmt.Sprintf("%d,%d,%d,%d", r.X, r.Y, r.Width, r.Height), path}
	}},
	{"grim", func(r Rect, path string) []string {
		return []string{"-g", fmt.Sprintf("%d,%d %dx%d", r.X, r.Y, r.Width, r.Height), path}
	}},
}

// onWayland reports whether the session talks Wayland. WAYLAND_DISPLAY wins;
// XDG_SESSION_TYPE is the distro fallback.
func onWayland() bool {
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		return true
	}
	return strings.EqualFold(os.Getenv("XDG_SESSION_TYPE"), "wayland")
}

func listWindows() ([]Window, error) {
	if onWayland() {
		return nil, fmt.Errorf("%w: per-window enumeration needs X11 (xdotool); on Wayland capture the full screen instead", ErrCaptureUnsupported)
	}
	if _, err := exec.LookPath("xdotool"); err != nil {
		return nil, fmt.Errorf("%w: xdotool is required for window enumeration (install xdotool)", ErrCaptureUnsupported)
	}
	out, err := exec.Command("xdotool", "search", "--onlyvisible", "--name", "").Output()
	if err != nil {
		return nil, fmt.Errorf("%w: xdotool search failed: %v", ErrCaptureFailed, err)
	}
	focused := 0
	if active, err := exec.Command("xdotool", "getactivewindow").Output(); err == nil {
		focused, _ = strconv.Atoi(strings.TrimSpace(string(active)))
	}
	var windows []Window
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		id, convErr := strconv.Atoi(strings.TrimSpace(line))
		if convErr != nil || id == 0 {
			continue
		}
		title, _ := exec.Command("xdotool", "getwindowname", strconv.Itoa(id)).Output()
		titleStr := SanitizeTitle(strings.TrimSpace(string(title)))
		if titleStr == "" {
			continue
		}
		w := Window{ID: id, Title: titleStr, Focused: id == focused}
		if geo, err := exec.Command("xdotool", "getwindowgeometry", "--shell", strconv.Itoa(id)).Output(); err == nil {
			parseXdotoolGeometry(string(geo), &w)
		}
		if w.Width > 0 && w.Height > 0 {
			windows = append(windows, w)
		}
	}
	return windows, nil
}

func parseXdotoolGeometry(out string, w *Window) {
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(value)
		if err != nil {
			continue
		}
		switch key {
		case "X":
			w.X = n
		case "Y":
			w.Y = n
		case "WIDTH":
			w.Width = n
		case "HEIGHT":
			w.Height = n
		}
	}
}

func captureWindow(win Window, focus bool) (image.Image, error) {
	if onWayland() {
		return nil, fmt.Errorf("%w: per-window capture needs X11 (maim); on Wayland capture the full screen or a region instead", ErrCaptureUnsupported)
	}
	maim, err := exec.LookPath("maim")
	if err != nil {
		return nil, fmt.Errorf("%w: maim is required for window capture (install maim)", ErrCaptureUnsupported)
	}
	if focus {
		if err := runQuiet("xdotool", "windowactivate", "--sync", strconv.Itoa(win.ID)); err == nil {
			time.Sleep(focusSettleDelay)
		}
	}
	return decodeCaptureFile(runCaptureFile(func(path string) *exec.Cmd {
		return exec.Command(maim, "-i", strconv.Itoa(win.ID), path)
	}))
}

func captureRegion(region Rect) (image.Image, error) {
	if onWayland() {
		if _, err := exec.LookPath("grim"); err != nil {
			return nil, fmt.Errorf("%w: region capture on Wayland needs grim (install grim)", ErrCaptureUnsupported)
		}
		return decodeCaptureFile(runCaptureFile(func(path string) *exec.Cmd {
			return exec.Command("grim", "-g", fmt.Sprintf("%d,%d %dx%d", region.X, region.Y, region.Width, region.Height), path)
		}))
	}
	for _, backend := range linuxRegionBackends {
		if _, err := exec.LookPath(backend.binary); err != nil {
			continue
		}
		img, err := decodeCaptureFile(runCaptureFile(func(path string) *exec.Cmd {
			return exec.Command(backend.binary, backend.args(region, path)...)
		}))
		if err == nil {
			return img, nil
		}
		if !errors.Is(err, ErrCaptureFailed) {
			return nil, err
		}
		// Backend present but failed (wrong session type, missing display):
		// try the next one.
	}
	return nil, fmt.Errorf("%w: no working region capture tool (install maim, scrot or grim)", ErrCaptureUnsupported)
}

func captureDisplay(display int) (image.Image, error) {
	if display != 0 {
		// X11 tools capture the whole virtual screen; per-monitor framing on
		// Linux needs randr plumbing that v1 deliberately skips.
		return nil, fmt.Errorf("%w: Linux capture covers the whole desktop; per-display selection is not supported yet", ErrDisplayNotFound)
	}
	for _, backend := range linuxFullscreenBackends {
		if _, err := exec.LookPath(backend.binary); err != nil {
			continue
		}
		img, err := decodeCaptureFile(runCaptureFile(func(path string) *exec.Cmd {
			return exec.Command(backend.binary, backend.args(path)...)
		}))
		if err == nil {
			return img, nil
		}
		if !errors.Is(err, ErrCaptureFailed) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("%w: no working screen capture tool (install maim, scrot, grim or gnome-screenshot)", ErrCaptureUnsupported)
}

// runCaptureFile runs one capture command pointed at a fresh temp file and
// returns the file's bytes. Errors from the command are ErrCaptureFailed so
// probe loops can distinguish "try next backend" from usage errors.
func runCaptureFile(build func(path string) *exec.Cmd) ([]byte, error) {
	dir, err := os.MkdirTemp("", "ally-screenshot-")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCaptureFailed, err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "capture.png")
	cmd := build(path)
	if out, cmdErr := cmd.CombinedOutput(); cmdErr != nil {
		return nil, fmt.Errorf("%w: %s: %v: %s", ErrCaptureFailed, cmd.Path, cmdErr, tailBytes(out))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: capture file missing: %v", ErrCaptureFailed, err)
	}
	return data, nil
}

func decodeCaptureFile(data []byte, err error) (image.Image, error) {
	if err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: capture not decodable: %v", ErrCaptureFailed, err)
	}
	return img, nil
}

func runQuiet(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}

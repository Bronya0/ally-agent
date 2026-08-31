// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

//go:build !windows

package app

// Clipboard file-list reading for the explorer paste-into-workspace flow.
// macOS: JXA reading NSPasteboard directly (multi-file, no Apple Events, so
// no automation-permission prompt), with the classic single-file AppleScript
// as a fallback. Linux: wl-paste (Wayland) or xclip (X11) reading the
// text/uri-list format.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	goruntime "runtime"
	"strings"
)

// clipboardFiles returns file paths from the system clipboard. A nil slice
// with a nil error means the clipboard holds no file list ("nothing to
// paste"); a non-nil error means reading failed and the user should be told.
func clipboardFiles() ([]string, error) {
	switch goruntime.GOOS {
	case "darwin":
		return clipboardFilesMacOS()
	case "linux":
		return clipboardFilesLinux()
	default:
		return nil, nil
	}
}

// clipboardFilesMacOS reads NSPasteboard file URLs through osascript's
// JavaScript for Automation. Unlike the classic `tell application "System
// Events"` form this supports multiple files and does not trigger the
// automation-permission (TCC) prompt, because no Apple Events are sent.
func clipboardFilesMacOS() ([]string, error) {
	const jxa = `ObjC.import('AppKit');
const pb = $.NSPasteboard.generalPasteboard;
const classes = $.NSArray.arrayWithObject($.NSURL);
const objects = pb.readObjectsForClassesOptions(classes, $.NSDictionary.dictionary);
const out = [];
if (objects) {
  const count = objects.count;
  for (let i = 0; i < count; i++) {
    const url = objects.objectAtIndex(i);
    if (url && url.isFileURL) {
      const p = url.path;
      if (p) out.push(p.js);
    }
  }
}
JSON.stringify(out)`
	if out, err := exec.Command("osascript", "-l", "JavaScript", "-e", jxa).Output(); err == nil {
		if files, perr := parseJSONStringArray(string(out)); perr == nil {
			return files, nil
		}
	}
	// Fallback for systems where the JXA bridge misbehaves: the classic
	// AppleScript form (single file only). A coercion error here just means
	// the clipboard holds no file list — report "no files", not an error.
	script := `tell application "System Events" to return POSIX path of (clipboard as «class furl»)`
	if out, err := exec.Command("osascript", "-e", script).Output(); err == nil {
		if path := strings.TrimSpace(string(out)); path != "" {
			return []string{path}, nil
		}
	}
	return nil, nil
}

// clipboardFilesLinux reads the text/uri-list clipboard format through
// wl-paste (Wayland) or xclip (X11), whichever is installed. A tool that runs
// but reports no file list is a legitimate "nothing to paste"; only the case
// where neither tool exists is surfaced as an error.
func clipboardFilesLinux() ([]string, error) {
	candidates := []struct {
		name string
		args []string
	}{
		{"wl-paste", []string{"--no-conversion", "-t", "text/uri-list"}},
		{"xclip", []string{"-selection", "clipboard", "-o", "-target", "text/uri-list"}},
	}
	notFound := 0
	for _, c := range candidates {
		out, err := exec.Command(c.name, c.args...).Output()
		if err != nil {
			var execErr *exec.Error
			if errors.As(err, &execErr) {
				notFound++
			}
			continue
		}
		if files := parseURIList(string(out)); len(files) > 0 {
			return files, nil
		}
		return nil, nil
	}
	if notFound == len(candidates) {
		return nil, fmt.Errorf("neither wl-paste nor xclip is installed")
	}
	// A tool exists but could not talk to its display server (e.g. xclip on
	// a Wayland session without XWayland): treat as no files, not an error.
	return nil, nil
}

// parseURIList turns a text/uri-list payload ("file:///a/b" per line, "#"
// comments allowed) into local file paths; non-file URIs are skipped.
func parseURIList(payload string) []string {
	var files []string
	for _, line := range strings.Split(payload, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if path, ok := strings.CutPrefix(line, "file://"); ok {
			if p := strings.TrimSpace(path); p != "" {
				files = append(files, p)
			}
		}
	}
	return files
}

// parseJSONStringArray decodes the JSON array printed by the JXA reader.
func parseJSONStringArray(payload string) ([]string, error) {
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return nil, fmt.Errorf("empty osascript output")
	}
	var files []string
	if err := json.Unmarshal([]byte(payload), &files); err != nil {
		return nil, fmt.Errorf("parse osascript JSON: %w", err)
	}
	return files, nil
}

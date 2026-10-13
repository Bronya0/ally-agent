// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
// Package memory holds pure frontmatter helpers for Ally's global memory
// Markdown files (YAML frontmatter + body). Nothing here may depend on App
// state, ConfigState, or any *App receiver: callers feed in raw memory file
// text and receive structured results. File listing and the index cache live
// in runtime.go.
package memory

import (
	"strings"

	"ally-dev/internal/tools/shared"
)

// ParseMarkdown splits a memory Markdown file into (description, body).
// `description` is extracted from the YAML frontmatter `description:` field;
// `body` is everything after the closing `---`. If no frontmatter is present,
// description is empty and body is the whole input.
func ParseMarkdown(text string) (string, string) {
	if !strings.HasPrefix(text, "---") {
		return "", text
	}
	rest := text[3:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", text
	}
	front := rest[:end]
	body := rest[end+len("\n---"):]
	body = strings.TrimPrefix(body, "\r\n")
	body = strings.TrimPrefix(body, "\n")
	desc := ""
	for _, line := range strings.Split(front, "\n") {
		if v := parseYAMLField(strings.TrimSpace(line), "description"); v != "" {
			desc = v
			break
		}
	}
	return desc, body
}

// parseYAMLField reads a single `field: value` line from YAML frontmatter.
// It is a minimal implementation covering Ally's memory and skill frontmatter
// usage; full YAML parsing is intentionally avoided. The canonical copy lives
// in internal/tools/shared; memory keeps a thin alias for its callers.
func parseYAMLField(line, field string) string {
	return shared.ParseFrontmatterField(line, field)
}

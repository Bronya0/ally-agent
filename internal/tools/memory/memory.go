// Package memory holds pure parsing/formatting helpers for Ally's global
// memory Markdown files (YAML frontmatter + body). Nothing here may depend
// on App state, ConfigState, or any *App receiver — callers feed in raw
// memory file text and receive structured results. App-owned orchestration
// (file IO, path resolution, version checks, caching) stays in internal/app.
package memory

import (
	"encoding/json"
	"regexp"
	"strings"
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

// FormatMarkdown builds a memory Markdown file with YAML frontmatter from a
// description and body content. CRLF in content is normalized to LF.
func FormatMarkdown(description, content string) string {
	description = strings.TrimSpace(description)
	description = strings.ReplaceAll(description, "\r", " ")
	description = strings.ReplaceAll(description, "\n", " ")
	content = strings.ReplaceAll(content, "\r\n", "\n")
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("description: ")
	if strings.ContainsAny(description, `":#[]{}&*!|>'%@`+"\t") {
		quoted, _ := json.Marshal(description)
		b.Write(quoted)
	} else {
		b.WriteString(description)
	}
	b.WriteString("\n---\n\n")
	b.WriteString(content)
	if !strings.HasSuffix(content, "\n") {
		b.WriteString("\n")
	}
	return b.String()
}

// DefaultPath derives a default .md filename from a description by lowercasing
// and replacing non-alphanumeric runs with hyphens.
func DefaultPath(description string) string {
	slug := strings.ToLower(strings.TrimSpace(description))
	slug = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "memory"
	}
	if len(slug) > 64 {
		slug = strings.Trim(slug[:64], "-")
	}
	return slug + ".md"
}

// parseYAMLField reads a single `field: value` line from YAML frontmatter.
// It is a minimal, dependency-free implementation covering Ally's memory and
// skill frontmatter usage; full YAML parsing is intentionally avoided.
func parseYAMLField(line, field string) string {
	prefix := field + ":"
	prefixAlt := field + " :"
	if strings.HasPrefix(line, prefix) || strings.HasPrefix(line, prefixAlt) {
		idx := strings.Index(line, ":")
		if idx < 0 {
			idx = strings.Index(line, ": ")
		}
		if idx < 0 {
			return ""
		}
		v := strings.TrimSpace(line[idx+1:])
		if strings.HasPrefix(v, `"`) {
			var decoded string
			if err := json.Unmarshal([]byte(v), &decoded); err == nil {
				return decoded
			}
		}
		v = strings.Trim(v, `"'`)
		return v
	}
	return ""
}

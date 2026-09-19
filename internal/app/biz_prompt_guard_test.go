// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"

	toolshared "ally-dev/internal/tools/shared"
)

// backtickToken matches backticked spans that look like a bare snake_case
// identifier — the convention the prompts use for tool names. CamelCase field
// names, paths, flags, and sentences fail this pattern on purpose.
var backtickToken = regexp.MustCompile("`([a-z][a-z0-9_]*)`")

// promptNonToolTokens are backticked lowercase identifiers that are commands,
// executables, or library names rather than tool names. Any other tool-looking
// token that is not a registered builtin tool fails the drift guard below,
// which is the point: advertising a tool that does not exist (or was renamed)
// must turn the suite red.
var promptNonToolTokens = map[string]bool{
	// shell commands / executables named in guidance
	"cat": true, "del": true, "export": true, "git": true, "go": true,
	"gofmt": true, "npm": true, "rg": true, "rm": true, "rmdir": true,
	"unlink": true,
	// toolchain names
	"eslint": true, "ruff": true, "tsc": true,
	// runtime / library names
	"echarts": true, "python": true,
	// plan statuses and request/field identifiers named in guidance
	"changes": true, "done": true, "files": true, "in_progress": true,
	"offset": true, "path": true, "pending": true, "validation": true,
	"version": true,
	// frontmatter fields and IO nouns named in guidance
	"aliases": true, "date": true, "description": true, "output": true,
	"role": true, "source": true, "tags": true, "task": true, "title": true,
}

func builtinToolNames(t *testing.T) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	for _, tool := range toolshared.Builtins() {
		if tool.Function != nil {
			names[tool.Function.Name] = true
		}
	}
	if len(names) < 20 {
		t.Fatalf("expected the builtin tool registry to be populated, got %d entries", len(names))
	}
	return names
}

// TestSystemPromptToolReferencesExist guards against prompt/tool drift: every
// tool-looking name the system prompts advertise must exist in the builtin
// registry (or be an explicitly allowlisted non-tool token).
func TestSystemPromptToolReferencesExist(t *testing.T) {
	registry := builtinToolNames(t)
	kbRoot := t.TempDir()
	prompts := map[string]string{
		"main":      joinSystemPromptParts(buildSystemPromptParts(nil, "", nil, "", "", "")),
		"main-kb":   joinSystemPromptParts(buildSystemPromptParts(nil, kbRoot, nil, "", "", kbRoot)),
		"sub-agent": subagentSystemPrompt(""),
	}
	unknown := map[string]bool{}
	for label, prompt := range prompts {
		for _, m := range backtickToken.FindAllStringSubmatch(prompt, -1) {
			tok := m[1]
			if registry[tok] || promptNonToolTokens[tok] {
				continue
			}
			unknown[label+":"+tok] = true
		}
	}
	if len(unknown) > 0 {
		var found []string
		for tok := range unknown {
			found = append(found, tok)
		}
		sort.Strings(found)
		t.Fatalf("prompts reference tokens that are neither registered builtin tools nor allowlisted non-tool tokens: %s", strings.Join(found, ", "))
	}
}

// TestSubagentPromptDoesNotAdvertiseBlockedTools keeps the sub-agent prompt
// consistent with subagentTools: the shared prompt blocks must not tell a
// sub-agent to use tools it never receives. Explicit prohibition lines
// ("- Do not call `subagent` — ...") are the only allowed mentions.
func TestSubagentPromptDoesNotAdvertiseBlockedTools(t *testing.T) {
	sub := subagentSystemPrompt("")
	for name := range subagentBlockedTools {
		for _, line := range strings.Split(sub, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.Contains(line, "`"+name+"`") && !strings.HasPrefix(trimmed, "- Do not") {
				t.Fatalf("sub-agent prompt references blocked tool %q outside a prohibition line: %q", name, trimmed)
			}
		}
	}
	if strings.Contains(sub, "consider delegating") {
		t.Fatal("sub-agent prompt must not suggest delegating to sub-agents")
	}
}

// TestSkillListingTruncationIsRuneSafe guards the metadata caps: cutting a CJK
// description at a byte boundary would ship invalid UTF-8 into the system
// prompt (JSON-encoded as U+FFFD on the wire).
func TestSkillListingTruncationIsRuneSafe(t *testing.T) {
	cjk := strings.Repeat("转", 400) // 1200 bytes / 400 runes, exceeds both caps
	got := buildSkillListingMeta([]SkillDefinition{
		{Name: "cjk-skill", Source: "project", Description: cjk, WhenToUse: cjk},
	})
	if !utf8.ValidString(got) {
		t.Fatalf("skill listing must stay valid UTF-8 after truncation, got %q", got)
	}
	if n := strings.Count(got, "转"); n == 0 || n > 2*skillListingFieldLimit {
		t.Fatalf("expected both fields truncated to at most %d runes, found %d", skillListingFieldLimit, n)
	}
	if !strings.Contains(got, "...") {
		t.Fatal("expected truncation marker in the skill listing")
	}
}

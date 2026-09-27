// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

// 新建技能（Skills 面板）：元数据进 YAML frontmatter，正文进 SKILL.md，
// 只写 Ally 原生的 .agents/skills 根目录（agentsSkillsSubdir）——也就是扫描
// 顺序里排第一、同名时胜出的那个根；.claude/skills 保持只读兼容。

const (
	// skillTargetUser writes under the home directory (~/.agents/skills), so the
	// skill is available in every workspace.
	skillTargetUser = "user"
	// skillTargetProject writes under the configured workspace
	// (<workspace>/.agents/skills), so only that project sees it.
	skillTargetProject = "project"

	skillNameMaxRunes  = 64
	skillDraftMaxBytes = 256 * 1024
)

// SkillDraft is the create-skill payload from the Skills panel: Target picks the
// scope, the metadata fields become YAML frontmatter, Content becomes the body.
type SkillDraft struct {
	Target      string `json:"target"`
	Name        string `json:"name"`
	Description string `json:"description"`
	WhenToUse   string `json:"whenToUse"`
	Content     string `json:"content"`
}

// skillNamePattern requires a letter or digit first, so ".", ".." and hidden
// names are rejected outright; Unicode classes keep non-ASCII (e.g. Chinese)
// names usable. Path separators can never match, so a name is always a single
// directory name.
var skillNamePattern = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N}._-]*$`)

// SaveSkill writes a new skill as <root>/<name>/SKILL.md and returns the file
// path it created. The listing cache is dropped afterwards, so the new skill is
// visible to the very next ListSkills call (panel refresh, slash command, and
// the model-facing skill listing).
func (a *App) SaveSkill(draft SkillDraft) (string, error) {
	name := strings.TrimSpace(draft.Name)
	if err := validateSkillName(name); err != nil {
		return "", err
	}
	description := collapseToOneLine(draft.Description)
	if description == "" {
		return "", errors.New("skill description is required")
	}
	whenToUse := collapseToOneLine(draft.WhenToUse)
	content := strings.TrimSpace(draft.Content)
	if len(content) > skillDraftMaxBytes {
		return "", fmt.Errorf("skill content is too large: %d bytes (max %d)", len(content), skillDraftMaxBytes)
	}

	root, err := a.skillWriteRoot(draft.Target)
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, name, "SKILL.md")
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("skill %q already exists: %s", name, path)
	} else if !errors.Is(err, fs.ErrNotExist) {
		// Any other stat failure (permission on a parent directory, a broken
		// link on the way) must not be read as "the path is free to write".
		return "", fmt.Errorf("cannot check the skill path %s: %w", path, err)
	}

	// 同名技能一律拒绝（任意来源，含内置）：三个根按顺序去重，放行只会让新技能
	// 静默遮蔽或被遮蔽，而列表上看不出谁真正生效。判定前先清缓存，保证看到的是
	// 刚写下的磁盘状态。
	switch existing, err := a.findSkillByName(name); {
	case err != nil:
		return "", err
	case existing != nil:
		return "", fmt.Errorf("skill %q already exists in the %s scope: %s", name, existing.Source, skillDisplayDir(*existing))
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("failed to create skill directory: %w", err)
	}
	if err := writeAtomicBytes(path, []byte(renderSkillFile(name, description, whenToUse, content)), 0o644); err != nil {
		return "", fmt.Errorf("failed to write skill: %w", err)
	}
	a.clearSkillListCache()
	return path, nil
}

// skillWriteRoot resolves the directory a new skill is written into. An empty
// target means the user scope, so a caller that omits the field cannot end up
// writing into a project by accident.
func (a *App) skillWriteRoot(target string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(target)) {
	case "", skillTargetUser:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot resolve the home directory: %w", err)
		}
		return filepath.Join(home, agentsSkillsSubdir), nil
	case skillTargetProject:
		cfg, err := a.getConfig()
		if err != nil {
			return "", err
		}
		root, err := workspaceRoot(cfg)
		if err != nil {
			return "", fmt.Errorf("no workspace is open: %w", err)
		}
		return filepath.Join(root, agentsSkillsSubdir), nil
	default:
		return "", fmt.Errorf("unknown skill target %q", target)
	}
}

// findSkillByName returns the first definition matching name across all skill
// roots (project > user > builtin, same order as ListSkills), or nil. The cache
// is cleared first so an external edit cannot keep a stale verdict for 30s.
func (a *App) findSkillByName(name string) (*SkillDefinition, error) {
	a.clearSkillListCache()
	skills, err := a.ListSkills()
	if err != nil {
		return nil, err
	}
	for i := range skills {
		if strings.EqualFold(skills[i].Name, name) {
			return &skills[i], nil
		}
	}
	return nil, nil
}

// skillDisplayDir names where an existing skill lives, for error messages.
// Built-in skills are embedded, so their Dir is an alias into the embedded tree
// ("skills/<name>"); only the builtin:// Path shows that it is not on disk.
func skillDisplayDir(sk SkillDefinition) string {
	if sk.Source == "builtin" {
		return sk.Path
	}
	if sk.Dir != "" {
		return sk.Dir
	}
	return sk.Path
}

// clearSkillListCache drops the listing cache so the next ListSkills rescans.
func (a *App) clearSkillListCache() {
	a.skillCacheMu.Lock()
	a.skillCache = map[string]skillListCacheEntry{}
	a.skillCacheMu.Unlock()
}

func validateSkillName(name string) error {
	if name == "" {
		return errors.New("skill name is required")
	}
	if utf8.RuneCountInString(name) > skillNameMaxRunes {
		return fmt.Errorf("skill name is too long (max %d characters)", skillNameMaxRunes)
	}
	if !skillNamePattern.MatchString(name) {
		return errors.New("skill name may only use letters, digits, dot, dash and underscore, and must start with a letter or digit")
	}
	if strings.HasSuffix(name, ".") {
		return errors.New("skill name must not end with a dot")
	}
	return nil
}

// collapseToOneLine flattens a metadata value to a single line: frontmatter
// parsing here is line-based, so an embedded newline would truncate the field
// (and could inject a stray "---" delimiter).
func collapseToOneLine(value string) string {
	value = strings.ReplaceAll(value, "\r\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return strings.Join(strings.Fields(value), " ")
}

// renderSkillFile builds the SKILL.md text: frontmatter (name, description,
// optional whenToUse) followed by the Markdown body.
func renderSkillFile(name, description, whenToUse, body string) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: " + skillFrontmatterValue(name) + "\n")
	b.WriteString("description: " + skillFrontmatterValue(description) + "\n")
	if whenToUse != "" {
		b.WriteString("whenToUse: " + skillFrontmatterValue(whenToUse) + "\n")
	}
	b.WriteString("---\n")
	if body != "" {
		b.WriteString("\n")
		b.WriteString(body)
		b.WriteString("\n")
	}
	return b.String()
}

// skillFrontmatterValue renders one metadata value. Values are read back by
// shared.ParseFrontmatterField, which JSON-decodes a value starting with a double
// quote and otherwise only trims surrounding quotes — so a value holding a quote
// has to be emitted as a JSON string to round-trip byte for byte.
func skillFrontmatterValue(value string) string {
	if value == "" {
		return `""`
	}
	if strings.ContainsAny(value, `"'`) {
		return skillJSONString(value)
	}
	return value
}

// skillJSONString encodes value as a JSON string literal without HTML escaping,
// so "&" and non-ASCII text stay readable in the written file.
func skillJSONString(value string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return value
	}
	return strings.TrimRight(buf.String(), "\n")
}

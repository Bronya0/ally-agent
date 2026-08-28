// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ally-dev/internal/tools/shared"
)

const skillListCacheTTL = 30 * time.Second

var skillScanDirs = []string{
	filepath.Join(".agents", "skills"),
	// Agent Skills open standard / Claude Code convention. Scanned last so
	// the Ally-native .agents/skills path wins on name conflicts.
	filepath.Join(".claude", "skills"),
}

// userSkillScanDirs lists user-level skill roots (under the home directory).
// Ally-native path scanned first so it wins on name conflicts; the Claude
// Code / Agent Skills convention is scanned as a fallback.
var userSkillScanDirs = []struct {
	path   string
	source string
}{
	{filepath.Join(".agents", "skills"), "user"},
	{filepath.Join(".claude", "skills"), "user"},
}

func (a *App) ListSkills() ([]SkillDefinition, error) {
	cfg, err := a.getConfig()
	if err != nil {
		return nil, err
	}
	// If the configured workspace no longer exists, still scan user-level
	// skills so the app remains usable and the user can add a new workspace.
	root, _ := workspaceRoot(cfg)
	cacheKey := skillListCacheKey(root)
	a.skillCacheMu.Lock()
	if cached, ok := a.skillCache[cacheKey]; ok && time.Since(cached.generatedAt) < skillListCacheTTL {
		skills := cloneSkillDefinitions(cached.skills)
		a.skillCacheMu.Unlock()
		return skills, nil
	}
	a.skillCacheMu.Unlock()

	skills := []SkillDefinition{}
	seen := map[string]bool{}

	// User skills (~/.agents/skills/, ~/.claude/skills/)
	if homeDir, err := os.UserHomeDir(); err == nil {
		for _, d := range userSkillScanDirs {
			scanSkillDir(filepath.Join(homeDir, d.path), d.source, &skills, seen)
		}
	}
	// Project skills (<workspace>/.agents/skills/)
	if root != "" {
		for _, sub := range skillScanDirs {
			scanSkillDir(filepath.Join(root, sub), "project", &skills, seen)
		}
	}
	// Built-in skills (embedded). Added last so user/project skills with the
	// same name win via the seen-map dedup, matching buildSkillListingMeta's
	// scope precedence (project > user > extra > builtin).
	for _, b := range builtinSkillEntries() {
		key := strings.ToLower(b.Name)
		if seen[key] {
			continue
		}
		seen[key] = true
		skills = append(skills, b)
	}

	a.skillCacheMu.Lock()
	if a.skillCache == nil {
		a.skillCache = map[string]skillListCacheEntry{}
	}
	if len(a.skillCache) >= 32 {
		a.skillCache = map[string]skillListCacheEntry{}
	}
	a.skillCache[cacheKey] = skillListCacheEntry{
		skills:      cloneSkillDefinitions(skills),
		generatedAt: time.Now(),
	}
	a.skillCacheMu.Unlock()
	return skills, nil
}

func skillListCacheKey(root string) string {
	if strings.TrimSpace(root) == "" {
		return "__no_workspace__"
	}
	return workspaceMapCacheKey(root)
}

func cloneSkillDefinitions(skills []SkillDefinition) []SkillDefinition {
	if len(skills) == 0 {
		return []SkillDefinition{}
	}
	cloned := make([]SkillDefinition, len(skills))
	copy(cloned, skills)
	return cloned
}

// readSkillContent returns the full skill body, preferring embedded content
// for built-in skills and falling back to disk reads for user/project skills.
func readSkillContent(sk SkillDefinition) (string, error) {
	if sk.embeddedContent != "" {
		return sk.embeddedContent, nil
	}
	data, err := os.ReadFile(sk.Path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (a *App) GetSkill(name string) (string, error) {
	skills, err := a.ListSkills()
	if err != nil {
		return "", err
	}
	for _, sk := range skills {
		if strings.EqualFold(sk.Name, name) {
			return readSkillContent(sk)
		}
	}
	return "", fmt.Errorf("skill not found: %s", name)
}

func (a *App) ActivateSkill(name string) (string, error) {
	skills, err := a.ListSkills()
	if err != nil {
		return "", err
	}
	for _, sk := range skills {
		if strings.EqualFold(sk.Name, name) {
			content, err := readSkillContent(sk)
			if err != nil {
				return "", err
			}
			if err := a.enableSkill(sk.Name); err != nil {
				return "", err
			}
			return renderSkillLoadedBlock(sk.Name, sk.Source, sk.Dir, "", content), nil
		}
	}
	return "", fmt.Errorf("skill not found: %s", name)
}

func (a *App) ClearSkills() error {
	skills, _ := a.ListSkills()
	disabled := make([]string, 0, len(skills))
	for _, sk := range skills {
		disabled = append(disabled, sk.Name)
	}
	return a.setDisabledSkills(disabled)
}

func (a *App) GetActiveSkills() []string {
	skills, _ := a.ListSkills()
	a.mu.Lock()
	defer a.mu.Unlock()
	result := make([]string, 0, len(skills))
	for _, sk := range skills {
		if !skillNameInList(a.disabledSkills, sk.Name) {
			result = append(result, sk.Name)
		}
	}
	return result
}

// listCachedSkills returns enabled available skills (calls ListSkills, no lock needed by caller).
func (a *App) listCachedSkills() []SkillDefinition {
	skills, err := a.ListSkills()
	if err != nil {
		return nil
	}
	return a.enabledSkillsFrom(skills)
}

func (a *App) DeactivateSkill(name string) error {
	if strings.TrimSpace(name) == "" {
		return nil
	}
	return a.setDisabledSkillsMutation(func(current []string) []string {
		if !skillNameInList(current, name) {
			current = append(current, name)
		}
		return current
	})
}

func (a *App) enableSkill(name string) error {
	return a.setDisabledSkillsMutation(func(current []string) []string {
		for i, disabled := range current {
			if strings.EqualFold(disabled, name) {
				return append(current[:i], current[i+1:]...)
			}
		}
		return current
	})
}

func (a *App) setDisabledSkillsMutation(mutator func([]string) []string) error {
	if err := a.ensureInitialized(); err != nil {
		return err
	}
	a.mu.Lock()
	current := cloneStringSlice(a.disabledSkills)
	a.mu.Unlock()
	return a.setDisabledSkills(mutator(current))
}

func (a *App) setDisabledSkills(names []string) error {
	if err := a.ensureInitialized(); err != nil {
		return err
	}
	next := normalizeSkillNameList(names)
	a.mu.Lock()
	a.disabledSkills = cloneStringSlice(next)
	a.config.DisabledSkills = cloneStringSlice(next)
	cfg := a.config
	path := a.configPath
	a.mu.Unlock()
	a.invalidateContextStaticCache()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func normalizeSkillNameList(names []string) []string {
	out := make([]string, 0, len(names))
	seen := map[string]bool{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, name)
	}
	return out
}

func cloneStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}

func (a *App) enabledSkillsFrom(skills []SkillDefinition) []SkillDefinition {
	a.mu.Lock()
	disabled := append([]string(nil), a.disabledSkills...)
	a.mu.Unlock()
	if len(disabled) == 0 {
		return skills
	}
	out := make([]SkillDefinition, 0, len(skills))
	for _, sk := range skills {
		if !skillNameInList(disabled, sk.Name) {
			out = append(out, sk)
		}
	}
	return out
}

func skillNameInList(list []string, name string) bool {
	for _, item := range list {
		if strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(name)) {
			return true
		}
	}
	return false
}

// handleSkillToolCall is called when the AI invokes the "skill" tool.
func (a *App) handleSkillToolCall(skillName, skillArgs string) (map[string]any, error) {
	if strings.TrimSpace(skillName) == "" {
		return nil, errors.New("skill is required")
	}
	skills, err := a.ListSkills()
	if err != nil {
		return nil, fmt.Errorf("failed to list skills: %w", err)
	}
	skills = a.enabledSkillsFrom(skills)
	for _, sk := range skills {
		if strings.EqualFold(sk.Name, skillName) {
			content, err := readSkillContent(sk)
			if err != nil {
				return nil, fmt.Errorf("failed to read skill: %w", err)
			}
			if len(content) > maxReadFileBytes {
				return nil, fmt.Errorf("skill file is too large: %d bytes (max %d)", len(content), maxReadFileBytes)
			}

			loadedContent := content
			if tree := buildSkillDirTree(sk.Dir, sk.Path); tree != "" {
				loadedContent = loadedContent + "\n" + tree
			}

			loadedBlock := renderSkillLoadedBlock(sk.Name, sk.Source, sk.Dir, skillArgs, loadedContent)
			return map[string]any{
				"loaded":  true,
				"name":    sk.Name,
				"content": loadedBlock,
				"message": fmt.Sprintf("Skill %q loaded. Follow the instructions in content.", sk.Name),
			}, nil
		}
	}
	return nil, fmt.Errorf("skill %q is disabled or not found in the current skill listing", skillName)
}

// buildSkillDirTree returns a compact listing of the skill directory (excluding
// the already-loaded SKILL.md and hidden files) up to two levels deep, so the
// model can see which additional files it may read under the skill's dir.
// Returns an empty string when the dir is empty, missing, or contains no
// publishable entries.
func buildSkillDirTree(dir, loadedPath string) string {
	if dir == "" {
		return ""
	}
	loadedName := filepath.Base(loadedPath)
	lines := skillDirTreeLines(dir, loadedName, "", 0)
	if len(lines) == 0 {
		return ""
	}
	return "<!-- skill dir tree (up to two levels, use read to read any of these) -->\n" + strings.Join(lines, "\n")
}

// skillDirTreeLines appends a flat, indented listing of dir and, for directories,
// one level of children — i.e. up to two levels total. Hidden files (names
// starting with ".") are skipped at every level; the root SKILL.md whose body was
// already injected is skipped only at the top level.
func skillDirTreeLines(dir, rootLoadedName, indent string, depth int) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var lines []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if depth == 0 && strings.EqualFold(name, rootLoadedName) {
			continue
		}
		if entry.IsDir() {
			lines = append(lines, indent+"- "+name+"/")
			if depth < 1 {
				lines = append(lines, skillDirTreeLines(filepath.Join(dir, name), rootLoadedName, indent+"  ", depth+1)...)
			}
		} else {
			lines = append(lines, indent+"- "+name)
		}
	}
	return lines
}

// renderSkillLoadedBlock builds <skill-loaded name="..." source="..." dir="..." args="...">
func renderSkillLoadedBlock(skillName, source, dir, args, content string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("<skill-loaded name=\"%s\"", xmlEscape(skillName)))
	if source != "" {
		b.WriteString(fmt.Sprintf(" source=\"%s\"", xmlEscape(source)))
	}
	if dir != "" {
		b.WriteString(fmt.Sprintf(" dir=\"%s\"", xmlEscape(dir)))
	}
	if args != "" {
		b.WriteString(fmt.Sprintf(" args=\"%s\"", xmlEscape(args)))
	}
	b.WriteString(">\n")
	b.WriteString(content)
	if !strings.HasSuffix(content, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("</skill-loaded>")
	return b.String()
}

func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	return s
}

// listSkillsUnlocked scans skill dirs (caller must hold mu or be in init context).
func (a *App) listSkillsUnlocked() ([]SkillDefinition, error) {
	root, err := workspaceRoot(a.config)
	if err != nil {
		return nil, err
	}
	skills := []SkillDefinition{}
	seen := map[string]bool{}
	if homeDir, err := os.UserHomeDir(); err == nil {
		for _, d := range userSkillScanDirs {
			scanSkillDir(filepath.Join(homeDir, d.path), d.source, &skills, seen)
		}
	}
	for _, sub := range skillScanDirs {
		scanSkillDir(filepath.Join(root, sub), "project", &skills, seen)
	}
	for _, b := range builtinSkillEntries() {
		key := strings.ToLower(b.Name)
		if seen[key] {
			continue
		}
		seen[key] = true
		skills = append(skills, b)
	}
	return skills, nil
}

func scanSkillDir(dir string, source string, skills *[]SkillDefinition, seen map[string]bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			skillPath := filepath.Join(dir, entry.Name(), "SKILL.md")
			if _, err := os.Stat(skillPath); err == nil {
				meta := parseSkillFile(skillPath)
				// Dedup keys are lowercased so a case-variant duplicate
				// ("Foo" vs "foo") cannot slip past and then shadow the
				// EqualFold lookups consumers rely on. Builtins dedup with
				// the same rule.
				if key := strings.ToLower(meta.Name); meta.Name != "" && !seen[key] {
					seen[key] = true
					meta.Source = source
					meta.Dir = filepath.Join(dir, entry.Name())
					*skills = append(*skills, meta)
				}
			}
		} else if strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			skillPath := filepath.Join(dir, entry.Name())
			meta := parseSkillFile(skillPath)
			if key := strings.ToLower(meta.Name); meta.Name != "" && !seen[key] {
				seen[key] = true
				meta.Source = source
				meta.Dir = dir
				*skills = append(*skills, meta)
			}
		}
	}
}

// commonDocumentNames lists lowercase file stems (without extension) that are
// treated as documentation rather than skills when a Markdown file has no
// usable YAML frontmatter. Files with explicit frontmatter declaring a name
// are still treated as skills even if their stem matches this list.
var commonDocumentNames = map[string]bool{
	"readme":              true,
	"license":             true,
	"licence":             true,
	"copying":             true,
	"notice":              true,
	"changelog":           true,
	"changes":             true,
	"history":             true,
	"contributing":        true,
	"authors":             true,
	"contributors":        true,
	"code_of_conduct":     true,
	"security":            true,
	"third_party":         true,
	"third_party_notices": true,
}

func parseSkillFile(path string) SkillDefinition {
	data, err := os.ReadFile(path)
	if err != nil {
		return SkillDefinition{}
	}
	return parseSkillContent(path, string(data))
}

// parseSkillContent parses a skill's frontmatter and body from an in-memory
// string. It shares the same rules as parseSkillFile without touching disk,
// so built-in (embedded) skills reuse the same parsing path.
func parseSkillContent(path, text string) SkillDefinition {
	meta := SkillDefinition{Path: path}
	if front, ok := splitSkillFrontmatter(text); ok {
		for _, line := range strings.Split(front, "\n") {
			line = strings.TrimSpace(line)
			if v := parseYAMLField(line, "name"); v != "" {
				meta.Name = v
			}
			if v := parseYAMLField(line, "description"); v != "" {
				meta.Description = v
			}
			if v := parseYAMLField(line, "type"); v != "" {
				meta.Type = v
			}
			// whenToUse (Ally-native) and when_to_use (Agent Skills open
			// standard / Claude Code) are both accepted. Ally-native
			// spelling wins when both are present: whenToUse is checked
			// unconditionally and overwrites any earlier when_to_use.
			if v := parseYAMLField(line, "when_to_use"); v != "" && meta.WhenToUse == "" {
				meta.WhenToUse = v
			}
			if v := parseYAMLField(line, "whenToUse"); v != "" {
				meta.WhenToUse = v
			}
		}
		if meta.Name != "" {
			return meta
		}
	}
	// Fallback: use filename (without .md extension)
	base := filepath.Base(path)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	if commonDocumentNames[strings.ToLower(stem)] {
		// Documentation files (readme, license, ...) without usable
		// frontmatter are not skills.
		return SkillDefinition{Path: path}
	}
	meta.Name = stem
	if filepath.Base(filepath.Dir(path)) == meta.Name {
		// Directory skill: SKILL.md -> use parent dir name
		meta.Name = filepath.Base(filepath.Dir(path))
	}
	meta.Description = fmt.Sprintf("Skill loaded from %s", path)
	return meta
}

// splitSkillFrontmatter extracts YAML frontmatter between a standalone opening
// "---" line and the next standalone "---" line. A UTF-8 BOM (common on
// Windows editors) is tolerated, and values containing "---" no longer
// truncate the block: only a whole delimiter line terminates the frontmatter,
// where the previous substring search cut at the first "---" anywhere.
func splitSkillFrontmatter(text string) (string, bool) {
	text = strings.TrimPrefix(text, "\ufeff")
	if !strings.HasPrefix(text, "---") {
		return "", false
	}
	rest := text[3:]
	nl := strings.IndexByte(rest, '\n')
	if nl < 0 {
		return "", false
	}
	if strings.TrimSpace(rest[:nl]) != "" {
		return "", false
	}
	var lines []string
	remaining := rest[nl+1:]
	for remaining != "" {
		line := remaining
		if i := strings.IndexByte(remaining, '\n'); i >= 0 {
			line, remaining = remaining[:i], remaining[i+1:]
		} else {
			remaining = ""
		}
		if strings.TrimSpace(line) == "---" {
			return strings.Join(lines, "\n"), true
		}
		lines = append(lines, line)
	}
	return "", false
}

// ── Skill frontmatter parsing ───────────────────────────────
// parseYAMLField delegates to the single shared implementation in
// internal/tools/shared (also used by the memory frontmatter parser).
func parseYAMLField(line, field string) string {
	return shared.ParseFrontmatterField(line, field)
}

// ── Grep ─────────────────────────────────────────────────

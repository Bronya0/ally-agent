// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

// 技能这一个功能的全部内容：内置 / 用户级 / 项目级（含 .claude / .codex 兼容）技能的发现与加载、
// frontmatter 解析、目录树与“已加载技能”块的渲染，以及新建技能的落盘（SaveSkill）。

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"ally-dev/internal/builtin_skills"
	"ally-dev/internal/tools/shared"

	"go.yaml.in/yaml/v4"
)

const skillListCacheTTL = 30 * time.Second

// agentsSkillsSubdir is the Ally-native skill directory ("<root>/.agents/skills").
// It is listed first in every scan list — so a name conflict resolves in its
// favour — and it is the only root SaveSkill writes to; .claude/skills and
// .codex/skills stay read-only compatibility. Defining it once keeps
// "what is scanned" and "what is written" from drifting apart.
var agentsSkillsSubdir = filepath.Join(".agents", "skills")

var skillScanDirs = []string{
	agentsSkillsSubdir,
	// Compatibility paths follow the Ally-native path so existing skills
	// retain precedence on name conflicts.
	filepath.Join(".claude", "skills"),
	filepath.Join(".codex", "skills"),
}

// userSkillScanDirs lists user-level skill roots (under the home directory).
// Ally-native path scanned first so it wins on name conflicts; the Claude
// Code and Codex conventions are scanned as fallbacks.
var userSkillScanDirs = []struct {
	path   string
	source string
}{
	{agentsSkillsSubdir, "user"},
	{filepath.Join(".claude", "skills"), "user"},
	{filepath.Join(".codex", "skills"), "user"},
}

func (a *App) ListSkills() ([]SkillDefinition, error) {
	cfg, err := a.getConfig()
	if err != nil {
		return nil, err
	}
	return a.listSkillsForConfig(cfg)
}

func (a *App) listSkillsForConfig(cfg ConfigState) ([]SkillDefinition, error) {
	// If the configured workspace no longer exists, still scan user-level
	// skills so the app remains usable and the user can add a new workspace.
	root, _ := workspaceRoot(cfg)
	return a.cachedSkillsForRoot(root, scanAvailableSkills), nil
}

// Concurrent callers share a cold scan. An invalidation during the scan makes
// its result obsolete, so neither the scanner nor its waiters publish it.
func (a *App) cachedSkillsForRoot(root string, scan func(string) []SkillDefinition) []SkillDefinition {
	cacheKey := skillListCacheKey(root)
	for {
		a.skillCacheMu.Lock()
		if cached, ok := a.skillCache[cacheKey]; ok && time.Since(cached.generatedAt) < skillListCacheTTL {
			skills := cloneSkillDefinitions(cached.skills)
			a.skillCacheMu.Unlock()
			return skills
		}
		if done := a.skillScans[cacheKey]; done != nil {
			a.skillCacheMu.Unlock()
			<-done
			continue
		}
		if a.skillScans == nil {
			a.skillScans = map[string]chan struct{}{}
		}
		done := make(chan struct{})
		a.skillScans[cacheKey] = done
		epoch := a.skillCacheEpoch
		a.skillCacheMu.Unlock()
		skills := scan(root)
		a.skillCacheMu.Lock()
		if epoch == a.skillCacheEpoch {
			if a.skillCache == nil || len(a.skillCache) >= 32 {
				a.skillCache = map[string]skillListCacheEntry{}
			}
			a.skillCache[cacheKey] = skillListCacheEntry{skills: cloneSkillDefinitions(skills), generatedAt: time.Now()}
		}
		delete(a.skillScans, cacheKey)
		close(done)
		current := epoch == a.skillCacheEpoch
		a.skillCacheMu.Unlock()
		if current {
			return cloneSkillDefinitions(skills)
		}
	}
}

func scanAvailableSkills(root string) []SkillDefinition {
	skills := []SkillDefinition{}
	seen := map[string]bool{}
	seenDirs := map[string]bool{}

	// Project skills (the same relative roots under <workspace>)
	if root != "" {
		for _, sub := range skillScanDirs {
			scanSkillDir(filepath.Join(root, sub), "project", &skills, seen, seenDirs)
		}
	}
	// User skills (~/.agents/skills/, ~/.claude/skills/, ~/.codex/skills/)
	if homeDir, err := os.UserHomeDir(); err == nil {
		for _, d := range userSkillScanDirs {
			scanSkillDir(filepath.Join(homeDir, d.path), d.source, &skills, seen, seenDirs)
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

	return skills
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
		if len(sk.embeddedContent) > maxSkillFileBytes {
			return "", fmt.Errorf("builtin skill %q is too large (max %d bytes)", sk.Name, maxSkillFileBytes)
		}
		return sk.embeddedContent, nil
	}
	f, err := os.Open(sk.Path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxSkillFileBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxSkillFileBytes {
		return "", fmt.Errorf("skill %q is too large (max %d bytes); use read on %q with targeted line ranges, or move reference material into supporting files", sk.Name, maxSkillFileBytes, sk.Path)
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

type SkillState struct {
	Skills []SkillDefinition `json:"skills"`
	Active []string          `json:"active"`
}

// GetSkillState returns the list and its enabled names from one scan/snapshot.
func (a *App) GetSkillState() (SkillState, error) {
	skills, err := a.ListSkills()
	if err != nil {
		return SkillState{}, err
	}
	active := make([]string, 0, len(skills))
	for _, sk := range a.enabledSkillsFrom(skills) {
		active = append(active, sk.Name)
	}
	return SkillState{Skills: skills, Active: active}, nil
}

// RefreshSkillState bypasses the listing TTL for an explicit user refresh.
func (a *App) RefreshSkillState() (SkillState, error) {
	a.clearSkillListCache()
	return a.GetSkillState()
}

func (a *App) GetActiveSkills() []string {
	state, _ := a.GetSkillState()
	return state.Active
}

// listCachedSkillsForConfig resolves enabled skills for the request workspace.
func (a *App) listCachedSkillsForConfig(cfg ConfigState) []SkillDefinition {
	skills, err := a.listSkillsForConfig(cfg)
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
	return a.updateConfigAndPersist(func(cfg *ConfigState) {
		cfg.DisabledSkills = normalizeSkillNameList(mutator(cfg.DisabledSkills))
	})
}

func (a *App) setDisabledSkills(names []string) error {
	if err := a.ensureInitialized(); err != nil {
		return err
	}
	next := normalizeSkillNameList(names)
	return a.updateConfigAndPersist(func(cfg *ConfigState) {
		cfg.DisabledSkills = cloneStringSlice(next)
	})
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
func (a *App) handleSkillToolCall(cfg ConfigState, sessionID, skillName, skillArgs string) (map[string]any, error) {
	if strings.TrimSpace(skillName) == "" {
		return nil, errors.New("skill is required")
	}
	a.mu.Lock()
	skills, frozen := a.sessionSkills[sessionID]
	a.mu.Unlock()
	if !frozen {
		skills = a.listCachedSkillsForConfig(cfg)
	}
	for _, sk := range skills {
		if strings.EqualFold(sk.Name, skillName) {
			content, err := readSkillContent(sk)
			if err != nil {
				return nil, fmt.Errorf("failed to read skill: %w", err)
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

const (
	// maxSkillFileBytes 是单个技能文件的字节上限，加载与写入共用同一口径：面板存得下的
	// 技能，skill 工具就必须加载得动（历史上 64KB 加载上限配 256KB 保存上限，面板保存
	// 成功的技能反而加载被拒）。正文仍然整篇要么全给、要么报错，不做半截加载。
	maxSkillFileBytes      = 256 * 1024
	skillMetadataByteLimit = 64 * 1024
	skillTreeEntryLimit    = 80
	skillTreeByteLimit     = 8 * 1024
)

// buildSkillDirTree lists up to two levels within one shared entry/byte budget.
// The loaded SKILL.md and hidden files are excluded; empty directories return "".
func buildSkillDirTree(dir, loadedPath string) string {
	if dir == "" {
		return ""
	}
	loadedName := filepath.Base(loadedPath)
	var lines []string
	remaining := skillTreeByteLimit
	truncated := false
	var walk func(string, string, int) bool
	walk = func(current, indent string, depth int) bool {
		entries, err := os.ReadDir(current)
		if err != nil {
			return true
		}
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasPrefix(name, ".") || (depth == 0 && strings.EqualFold(name, loadedName)) {
				continue
			}
			line := indent + "- " + name
			if entry.IsDir() {
				line += "/"
			}
			if len(lines) >= skillTreeEntryLimit || len(line)+1 > remaining {
				truncated = true
				return false
			}
			lines = append(lines, line)
			remaining -= len(line) + 1
			if entry.IsDir() && depth < 1 && !walk(filepath.Join(current, name), indent+"  ", depth+1) {
				return false
			}
		}
		return true
	}
	walk(dir, "", 0)
	if truncated {
		lines = append(lines, "[Directory listing truncated; use list_files on a subdirectory for more.]")
	}
	if len(lines) == 0 {
		return ""
	}
	return "<!-- skill dir tree (up to two levels, use read to read any of these) -->\n" + strings.Join(lines, "\n")
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
	b.WriteString(escapeClosingMarker(content, "</skill-loaded"))
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

func scanSkillDir(dir string, source string, skills *[]SkillDefinition, seen, seenDirs map[string]bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		dirKey := strings.ToLower(entry.Name())
		// Directory identity wins before opening lower-priority copies, even
		// when their frontmatter declares a different skill name.
		if !entry.IsDir() || seenDirs[dirKey] {
			continue
		}
		meta := parseSkillFile(filepath.Join(dir, entry.Name(), "SKILL.md"))
		if meta.Name == "" {
			continue // Missing/unreadable entries must not hide a valid fallback.
		}
		seenDirs[dirKey] = true
		// Different directories may still declare the same name.
		if key := strings.ToLower(meta.Name); !seen[key] {
			seen[key] = true
			meta.Source = source
			meta.Dir = filepath.Join(dir, entry.Name())
			*skills = append(*skills, meta)
		}
	}
}

func parseSkillFile(path string) SkillDefinition {
	if !strings.EqualFold(filepath.Base(path), "SKILL.md") {
		return SkillDefinition{Path: path}
	}
	f, err := os.Open(path)
	if err != nil {
		return SkillDefinition{}
	}
	defer f.Close()
	text, err := readSkillMetadata(f)
	if err != nil {
		return SkillDefinition{}
	}
	return parseSkillContent(path, text)
}

// Listing needs only frontmatter. Limit malformed/oversized headers to 64 KiB;
// they fall back to the directory name. Full bodies are read only on loading.
func readSkillMetadata(r io.Reader) (string, error) {
	const maxMetadataBytes = skillMetadataByteLimit
	reader := bufio.NewReader(io.LimitReader(r, maxMetadataBytes+1))
	var header strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return "", err
		}
		if header.Len()+len(line) > maxMetadataBytes {
			return "", nil
		}
		if header.Len() == 0 {
			if strings.TrimSpace(strings.TrimPrefix(line, "\ufeff")) != "---" {
				return "", nil
			}
		} else if strings.TrimSpace(line) == "---" {
			header.WriteString(line)
			return header.String(), nil
		}
		header.WriteString(line)
		if err == io.EOF {
			return "", nil
		}
	}
}

// parseSkillContent parses a skill's frontmatter and body from an in-memory
// string. It shares the same rules as parseSkillFile without touching disk,
// so built-in (embedded) skills reuse the same parsing path.
func parseSkillContent(path, text string) SkillDefinition {
	meta := SkillDefinition{Path: path}
	if !strings.EqualFold(filepath.Base(path), "SKILL.md") {
		return meta
	}
	if front, ok := splitSkillFrontmatter(text); ok {
		fields := parseSkillMetadata(front)
		meta.Name = strings.TrimSpace(fields["name"])
		meta.Description = fields["description"]
		meta.Type = fields["type"]
		meta.WhenToUse = fields["whenToUse"]
		if meta.WhenToUse == "" {
			meta.WhenToUse = fields["when_to_use"]
		}
		if meta.Name != "" {
			return meta
		}
	}
	// The directory's explicit SKILL.md entry point may use its parent name.
	meta.Name = filepath.Base(filepath.Dir(path))
	meta.Description = fmt.Sprintf("Skill loaded from %s", path)
	return meta
}

// Parse known top-level fields independently so malformed external YAML cannot
// change a skill's identity (and therefore bypass DisabledSkills). YAML still
// decodes quoted strings and multiline blocks; legacy bare scalars retain their
// entire text, including issue numbers introduced by " #". First duplicate wins.
func parseSkillMetadata(front string) map[string]string {
	fields := make(map[string]string)
	var decoded struct {
		Name            string `yaml:"name"`
		Description     string `yaml:"description"`
		Type            string `yaml:"type"`
		WhenToUse       string `yaml:"whenToUse"`
		WhenToUseCompat string `yaml:"when_to_use"`
	}
	validYAML := yaml.Unmarshal([]byte(front), &decoded) == nil
	if validYAML {
		fields = map[string]string{
			"name": decoded.Name, "description": decoded.Description, "type": decoded.Type,
			"whenToUse": decoded.WhenToUse, "when_to_use": decoded.WhenToUseCompat,
		}
	}
	seen := make(map[string]bool)
	lines := strings.Split(front, "\n")
	for i, line := range lines {
		if line == "" || line[0] == ' ' || line[0] == '	' {
			continue
		}
		key, raw, found := strings.Cut(line, ":")
		key = strings.TrimSpace(key)
		switch key {
		case "name", "description", "type", "whenToUse", "when_to_use":
		default:
			continue
		}
		if !found || seen[key] {
			continue
		}
		seen[key] = true
		raw = strings.TrimSpace(raw)
		// Keep full YAML semantics, including multiline values and aliases.
		// Only bare text containing '#' needs legacy comment preservation.
		if validYAML && (raw == "" || !strings.Contains(raw, "#") || strings.ContainsAny(raw[:1], "\"'|>&*![{")) {
			continue
		}
		value := raw
		if strings.HasPrefix(raw, "|") || strings.HasPrefix(raw, ">") || raw == "" {
			end := i + 1
			for end < len(lines) && (strings.TrimSpace(lines[end]) == "" || lines[end][0] == ' ' || lines[end][0] == '	') {
				end++
			}
			var block map[string]string
			if err := yaml.Unmarshal([]byte(strings.Join(lines[i:end], "\n")+"\n"), &block); err == nil {
				value = block[key]
			}
		} else if strings.HasPrefix(raw, "\"") || strings.HasPrefix(raw, "'") {
			if err := yaml.Unmarshal([]byte(raw), &value); err != nil {
				value = parseYAMLField(line, key)
			}
		}
		fields[key] = value
	}
	return fields
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

	skillNameMaxRunes = 64
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
	fileContent := []byte(renderSkillFile(name, description, whenToUse, content))
	// Validate the exact bytes the loader will read, including metadata.
	if len(fileContent) > maxSkillFileBytes {
		return "", fmt.Errorf("skill file is too large: %d bytes (max %d)", len(fileContent), maxSkillFileBytes)
	}
	if len(fileContent) > skillMetadataByteLimit {
		// Metadata must also fit the bounded discovery reader.
		if header, _ := readSkillMetadata(bytes.NewReader(fileContent)); header == "" {
			return "", errors.New("skill metadata is too large (max 64 KiB)")
		}
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
	if err := writeAtomicBytes(path, fileContent, 0o644); err != nil {
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
	a.skillCacheEpoch++
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

// skillFrontmatterValue keeps plain strings readable and quotes values whose
// YAML interpretation would change their contents or type.
func skillFrontmatterValue(value string) string {
	if value == "" {
		return `""`
	}
	var decoded any
	if err := yaml.Unmarshal([]byte(value), &decoded); err != nil {
		return skillJSONString(value)
	}
	if text, ok := decoded.(string); !ok || text != value {
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

// builtinSkillEntries returns SkillDefinitions for all embedded built-in
// skills, with embeddedContent populated so readers skip disk I/O. The
// embedded filesystem itself lives in the leaf package
// ally-dev/internal/builtin_skills; this function only parses it into
// SkillDefinition values that the app layer can consume.
//
// Add a new built-in skill by creating
// `internal/builtin_skills/skills/<name>/SKILL.md` with YAML frontmatter
// (name, description, whenToUse). No other change is needed.
func builtinSkillEntries() []SkillDefinition {
	var out []SkillDefinition
	root := builtin_skills.Root
	_ = fs.WalkDir(builtin_skills.FS, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.EqualFold(path.Base(p), "SKILL.md") {
			return nil
		}
		data, rerr := builtin_skills.FS.ReadFile(p)
		if rerr != nil {
			return nil
		}
		text := string(data)
		meta := parseSkillContent(p, text)
		if meta.Name == "" {
			return nil
		}
		meta.Source = "builtin"
		meta.Path = "builtin://" + strings.TrimPrefix(p, root+"/")
		dir := path.Dir(p)
		if dir == root {
			dir = ""
		}
		meta.Dir = dir
		meta.embeddedContent = text
		out = append(out, meta)
		return nil
	})
	return out
}

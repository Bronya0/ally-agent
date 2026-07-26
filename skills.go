package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var skillScanDirs = []string{
	filepath.Join(".agents", "skills"),
	filepath.Join(".kimi-code", "skills"),
}

func (a *App) ListSkills() ([]SkillDefinition, error) {
	cfg, err := a.getConfig()
	if err != nil {
		return nil, err
	}
	// If the configured workspace no longer exists, still scan user-level
	// skills so the app remains usable and the user can add a new workspace.
	root, _ := workspaceRoot(cfg)
	skills := []SkillDefinition{}
	seen := map[string]bool{}

	// User skills (~/.agents/skills/)
	if homeDir, err := os.UserHomeDir(); err == nil {
		scanSkillDir(filepath.Join(homeDir, ".agents", "skills"), "user", &skills, seen)
	}
	// Project skills (<workspace>/.agents/skills/)
	if root != "" {
		for _, sub := range skillScanDirs {
			scanSkillDir(filepath.Join(root, sub), "project", &skills, seen)
		}
	}

	return skills, nil
}

func (a *App) GetSkill(name string) (string, error) {
	skills, err := a.ListSkills()
	if err != nil {
		return "", err
	}
	for _, sk := range skills {
		if strings.EqualFold(sk.Name, name) {
			content, err := os.ReadFile(sk.Path)
			if err != nil {
				return "", err
			}
			return string(content), nil
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
			content, err := os.ReadFile(sk.Path)
			if err != nil {
				return "", err
			}
			if err := a.enableSkill(sk.Name); err != nil {
				return "", err
			}
			return renderSkillLoadedBlock(sk.Name, sk.Source, sk.Dir, "", string(content)), nil
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
			info, err := os.Stat(sk.Path)
			if err != nil {
				return nil, fmt.Errorf("failed to stat skill: %w", err)
			}
			if info.Size() > maxReadFileBytes {
				return nil, fmt.Errorf("skill file is too large: %d bytes (max %d)", info.Size(), maxReadFileBytes)
			}
			content, err := os.ReadFile(sk.Path)
			if err != nil {
				return nil, fmt.Errorf("failed to read skill: %w", err)
			}

			loadedContent := string(content)
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

// buildSkillDirTree returns a compact one-level listing of the skill directory
// (excluding the already-loaded SKILL.md and hidden files) so the model can see
// which additional files it may read under the skill's dir. Returns an empty
// string when the dir is empty, missing, or contains no publishable entries.
func buildSkillDirTree(dir, loadedPath string) string {
	if dir == "" {
		return ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	loadedName := filepath.Base(loadedPath)
	var lines []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if strings.EqualFold(name, loadedName) {
			continue
		}
		if entry.IsDir() {
			lines = append(lines, "- "+name+"/")
		} else {
			lines = append(lines, "- "+name)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "<!-- skill dir tree (one level, use read to read any of these) -->\n" + strings.Join(lines, "\n")
}

// renderSkillLoadedBlock builds <kimi-skill-loaded name="..." source="..." dir="..." args="...">
func renderSkillLoadedBlock(skillName, source, dir, args, content string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("<kimi-skill-loaded name=\"%s\"", xmlEscape(skillName)))
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
	b.WriteString("</kimi-skill-loaded>")
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
		scanSkillDir(filepath.Join(homeDir, ".agents", "skills"), "user", &skills, seen)
	}
	for _, sub := range skillScanDirs {
		scanSkillDir(filepath.Join(root, sub), "project", &skills, seen)
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
				if meta.Name != "" && !seen[meta.Name] {
					seen[meta.Name] = true
					meta.Source = source
					meta.Dir = filepath.Join(dir, entry.Name())
					*skills = append(*skills, meta)
				}
			}
		} else if strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			skillPath := filepath.Join(dir, entry.Name())
			meta := parseSkillFile(skillPath)
			if meta.Name != "" && !seen[meta.Name] {
				seen[meta.Name] = true
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
	text := string(data)
	meta := SkillDefinition{Path: path}
	// Try YAML frontmatter: ---\n...\n---
	if strings.HasPrefix(text, "---") {
		if end := strings.Index(text[3:], "---"); end >= 0 {
			front := text[3 : 3+end]
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
				if v := parseYAMLField(line, "whenToUse"); v != "" {
					meta.WhenToUse = v
				}
			}
			if meta.Name != "" {
				return meta
			}
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

// ── Grep ─────────────────────────────────────────────────

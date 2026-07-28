package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanSkillDirIncludesClaudeSkillsPath(t *testing.T) {
	root := t.TempDir()
	// Simulate a project-level .claude/skills/<name>/SKILL.md
	skillDir := filepath.Join(root, ".claude", "skills", "claude-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSkillTestFile(t, skillDir, "SKILL.md", "---\nname: claude-skill\ndescription: from .claude/skills\n---\nbody\n")

	var skills []SkillDefinition
	seen := map[string]bool{}
	// Mirror the production scan order: scanSkillDir over .claude/skills.
	scanSkillDir(filepath.Join(root, ".claude", "skills"), "project", &skills, seen)

	found := false
	for _, s := range skills {
		if s.Name == "claude-skill" {
			found = true
			if s.Source != "project" {
				t.Fatalf("expected Source=project, got %q", s.Source)
			}
			if !strings.Contains(s.Description, "from .claude/skills") {
				t.Fatalf("unexpected description: %q", s.Description)
			}
			break
		}
	}
	if !found {
		t.Fatal("expected claude-skill to be discovered under .claude/skills")
	}
}

func TestAllyNativeSkillWinsOverClaudeSkillOnNameConflict(t *testing.T) {
	root := t.TempDir()
	// Ally-native path has a skill named "shared"
	allyDir := filepath.Join(root, ".agents", "skills", "shared")
	if err := os.MkdirAll(allyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSkillTestFile(t, allyDir, "SKILL.md", "---\nname: shared\ndescription: ally-native\n---\nbody\n")
	// Claude path has the same name
	claudeDir := filepath.Join(root, ".claude", "skills", "shared")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSkillTestFile(t, claudeDir, "SKILL.md", "---\nname: shared\ndescription: claude-convention\n---\nbody\n")

	var skills []SkillDefinition
	seen := map[string]bool{}
	// Production order: .agents/skills first, then .kimi-code/skills, then .claude/skills
	for _, sub := range skillScanDirs {
		scanSkillDir(filepath.Join(root, sub), "project", &skills, seen)
	}

	var shared *SkillDefinition
	for i := range skills {
		if skills[i].Name == "shared" {
			shared = &skills[i]
			break
		}
	}
	if shared == nil {
		t.Fatal("expected shared skill to be discovered")
	}
	if shared.Description != "ally-native" {
		t.Fatalf("expected ally-native to win, got description %q from path %q", shared.Description, shared.Path)
	}
	if !strings.Contains(shared.Path, ".agents") {
		t.Fatalf("expected winning path under .agents, got %q", shared.Path)
	}
}

func TestParseSkillContentAcceptsWhenToUseUnderscore(t *testing.T) {
	text := "---\nname: std-skill\ndescription: agent skills open standard\nwhen_to_use: when following the open standard\n---\n# body\n"
	meta := parseSkillContent("builtin://std-skill/SKILL.md", text)
	if meta.Name != "std-skill" {
		t.Fatalf("expected name std-skill, got %q", meta.Name)
	}
	if meta.WhenToUse != "when following the open standard" {
		t.Fatalf("expected WhenToUse from when_to_use, got %q", meta.WhenToUse)
	}
}

func TestParseSkillContentAllySpellingPrecedence(t *testing.T) {
	// When both whenToUse and when_to_use appear, Ally-native spelling wins
	// regardless of the order they appear in the frontmatter.
	t.Run("ally-native first", func(t *testing.T) {
		text := "---\nname: both\nwhenToUse: ally-native\nwhen_to_use: open-standard\n---\nbody\n"
		meta := parseSkillContent("both/SKILL.md", text)
		if meta.WhenToUse != "ally-native" {
			t.Fatalf("expected ally-native spelling to win, got %q", meta.WhenToUse)
		}
	})
	t.Run("standard first", func(t *testing.T) {
		text := "---\nname: both\nwhen_to_use: open-standard\nwhenToUse: ally-native\n---\nbody\n"
		meta := parseSkillContent("both/SKILL.md", text)
		if meta.WhenToUse != "ally-native" {
			t.Fatalf("expected ally-native spelling to win, got %q", meta.WhenToUse)
		}
	})
}

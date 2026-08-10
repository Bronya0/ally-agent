package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuiltinSkillEntriesContainsPlaywrightCLI(t *testing.T) {
	entries := builtinSkillEntries()
	if len(entries) == 0 {
		t.Fatal("expected at least one built-in skill, got none")
	}
	var pw *SkillDefinition
	for i := range entries {
		if entries[i].Name == "playwright-cli" {
			pw = &entries[i]
			break
		}
	}
	if pw == nil {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name)
		}
		t.Fatalf("expected playwright-cli in built-in skills, got %v", names)
	}
	if pw.Source != "builtin" {
		t.Fatalf("expected Source=builtin, got %q", pw.Source)
	}
	if pw.embeddedContent == "" {
		t.Fatal("expected embeddedContent to be populated for built-in skill")
	}
	if !strings.Contains(pw.embeddedContent, "playwright-cli") {
		t.Fatalf("expected embedded content to mention playwright-cli")
	}
	if !strings.HasPrefix(pw.Path, "builtin://") {
		t.Fatalf("expected Path to start with builtin://, got %q", pw.Path)
	}
	if pw.WhenToUse == "" {
		t.Fatal("expected WhenToUse to be populated from frontmatter")
	}
}

func TestReadSkillContentUsesEmbedded(t *testing.T) {
	entries := builtinSkillEntries()
	var pw SkillDefinition
	for _, e := range entries {
		if e.Name == "playwright-cli" {
			pw = e
			break
		}
	}
	if pw.Name == "" {
		t.Fatal("playwright-cli not found in built-in skills")
	}
	// Point Path at a nonexistent disk location to prove we don't hit disk.
	pw.Path = "/nonexistent/should/not/be/read/SKILL.md"
	got, err := readSkillContent(pw)
	if err != nil {
		t.Fatalf("readSkillContent failed: %v", err)
	}
	if got != pw.embeddedContent {
		t.Fatal("expected readSkillContent to return embedded content verbatim")
	}
}

func TestReadSkillContentFallsBackToDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "skill.md")
	content := "# disk skill\nbody\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	sk := SkillDefinition{Path: path}
	got, err := readSkillContent(sk)
	if err != nil {
		t.Fatalf("readSkillContent failed: %v", err)
	}
	if got != content {
		t.Fatalf("expected disk content, got %q", got)
	}
}

func TestParseSkillContentFromMemory(t *testing.T) {
	text := "---\nname: my-skill\ndescription: hello world\nwhenToUse: when testing\n---\n# body\n"
	meta := parseSkillContent("builtin://my-skill/SKILL.md", text)
	if meta.Name != "my-skill" {
		t.Fatalf("expected name my-skill, got %q", meta.Name)
	}
	if meta.Description != "hello world" {
		t.Fatalf("expected description, got %q", meta.Description)
	}
	if meta.WhenToUse != "when testing" {
		t.Fatalf("expected whenToUse, got %q", meta.WhenToUse)
	}
}

func TestBuiltinSkillEntriesContainsCodeGraph(t *testing.T) {
	entries := builtinSkillEntries()
	var cg *SkillDefinition
	for i := range entries {
		if entries[i].Name == "codegraph" {
			cg = &entries[i]
			break
		}
	}
	if cg == nil {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name)
		}
		t.Fatalf("expected codegraph in built-in skills, got %v", names)
	}
	if cg.Source != "builtin" {
		t.Fatalf("expected Source=builtin, got %q", cg.Source)
	}
	if cg.embeddedContent == "" {
		t.Fatal("expected embeddedContent to be populated for built-in skill")
	}
	if !strings.Contains(cg.embeddedContent, "CODEGRAPH.md") {
		t.Fatalf("expected embedded content to reference CODEGRAPH.md")
	}
	if !strings.Contains(cg.embeddedContent, "Module Hierarchy") {
		t.Fatalf("expected embedded content to specify the Module Hierarchy section")
	}
	if !strings.HasPrefix(cg.Path, "builtin://") {
		t.Fatalf("expected Path to start with builtin://, got %q", cg.Path)
	}
	if cg.WhenToUse == "" {
		t.Fatal("expected WhenToUse to be populated from frontmatter")
	}
}

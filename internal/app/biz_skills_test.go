// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkillTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseSkillFileSkipsReadmeWithoutFrontmatter(t *testing.T) {
	root := t.TempDir()
	readmePath := filepath.Join(root, "readme.md")
	writeSkillTestFile(t, root, "readme.md", "# My Skills\n\nSome plain docs.\n")

	meta := parseSkillFile(readmePath)
	if meta.Name != "" {
		t.Fatalf("expected readme without frontmatter to be skipped, got name=%q", meta.Name)
	}
	if meta.Path != readmePath {
		t.Fatalf("expected path to be preserved, got %q", meta.Path)
	}
}

func TestParseSkillFileSkipsCommonDocumentNames(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"README.md", "License.md", "CHANGELOG.md", "Contributing.md", "code_of_conduct.md"} {
		path := filepath.Join(root, name)
		writeSkillTestFile(t, root, name, "plain text content\n")
		meta := parseSkillFile(path)
		if meta.Name != "" {
			t.Fatalf("expected %s without frontmatter to be skipped, got name=%q", name, meta.Name)
		}
	}
}

func TestParseSkillFileKeepsReadmeWithFrontmatter(t *testing.T) {
	root := t.TempDir()
	readmePath := filepath.Join(root, "readme.md")
	content := "---\nname: my-readme-skill\ndescription: a skill despite the filename\n---\n# body\n"
	writeSkillTestFile(t, root, "readme.md", content)

	meta := parseSkillFile(readmePath)
	if meta.Name != "my-readme-skill" {
		t.Fatalf("expected frontmatter-declared skill, got name=%q", meta.Name)
	}
	if meta.Description != "a skill despite the filename" {
		t.Fatalf("expected description from frontmatter, got %q", meta.Description)
	}
}

func TestParseSkillFileFallbackForPlainMarkdown(t *testing.T) {
	root := t.TempDir()
	// A non-document Markdown file without frontmatter still becomes a skill.
	path := filepath.Join(root, "formatter.md")
	writeSkillTestFile(t, root, "formatter.md", "# formatter\nformats code\n")

	meta := parseSkillFile(path)
	if meta.Name != "formatter" {
		t.Fatalf("expected fallback name formatter, got %q", meta.Name)
	}
}

func TestScanSkillDirIgnoresReadme(t *testing.T) {
	root := t.TempDir()
	writeSkillTestFile(t, root, "readme.md", "# Skills\ndocs\n")
	writeSkillTestFile(t, root, "formatter.md", "# formatter\nformats code\n")
	// Directory skill with SKILL.md + frontmatter
	writeSkillTestFile(t, root, "greeter/SKILL.md", "---\nname: greeter\ndescription: says hi\n---\n")

	var skills []SkillDefinition
	seen := map[string]bool{}
	scanSkillDir(root, "user", &skills, seen)

	names := map[string]bool{}
	for _, s := range skills {
		names[s.Name] = true
	}
	if names["readme"] {
		t.Fatalf("readme should not be scanned as a skill, got %v", names)
	}
	if !names["formatter"] {
		t.Fatalf("formatter should be scanned as a skill, got %v", names)
	}
	if !names["greeter"] {
		t.Fatalf("greeter directory skill should be scanned, got %v", names)
	}
}

func TestBuildSkillDirTreeListsOneLevel(t *testing.T) {
	root := t.TempDir()
	// SKILL.md is loaded separately and must be excluded from the tree.
	writeSkillTestFile(t, root, "SKILL.md", "# skill\n")
	// Create a references/ subdir (with a hidden gitkeep) so it shows in the tree.
	writeSkillTestFile(t, root, "references/.gitkeep", "")
	writeSkillTestFile(t, root, "assets.md", "assets body")
	writeSkillTestFile(t, root, ".hidden", "hidden")

	loadedPath := filepath.Join(root, "SKILL.md")
	tree := buildSkillDirTree(root, loadedPath)
	for _, want := range []string{"references/", "assets.md"} {
		if !strings.Contains(tree, want) {
			t.Fatalf("expected tree to contain %q, got:\n%s", want, tree)
		}
	}
	if strings.Contains(tree, "SKILL.md") {
		t.Fatalf("tree should exclude the loaded SKILL.md, got:\n%s", tree)
	}
	if strings.Contains(tree, ".hidden") {
		t.Fatalf("tree should exclude hidden files, got:\n%s", tree)
	}
	if !strings.Contains(tree, "read") {
		t.Fatalf("tree should hint at read, got:\n%s", tree)
	}
}

func TestBuildSkillDirTreeEmptyWhenOnlySkillFile(t *testing.T) {
	root := t.TempDir()
	loadedPath := filepath.Join(root, "SKILL.md")
	writeSkillTestFile(t, root, "SKILL.md", "# only\n")

	if tree := buildSkillDirTree(root, loadedPath); tree != "" {
		t.Fatalf("expected empty tree when only SKILL.md present, got %q", tree)
	}
}

func TestBuildSkillDirTreeEmptyForMissingDir(t *testing.T) {
	if tree := buildSkillDirTree(filepath.Join(t.TempDir(), "nope"), "SKILL.md"); tree != "" {
		t.Fatalf("expected empty tree for missing dir, got %q", tree)
	}
}

// TestParseSkillContentToleratesBOMAndDelimiterInValue 验证两个健壮性场景：
// UTF-8 BOM 开头的 SKILL.md（Windows 编辑器常见）仍能解析 frontmatter；
// 值中包含 "---" 不再提前截断 frontmatter（按整行定界符匹配）。
func TestParseSkillContentToleratesBOMAndDelimiterInValue(t *testing.T) {
	text := "\ufeff---\nname: bom-skill\ndescription: uses --- separators\n---\nBody."
	meta := parseSkillContent(filepath.Join("x", "SKILL.md"), text)
	if meta.Name != "bom-skill" {
		t.Fatalf("BOM must not break frontmatter parsing, got name %q", meta.Name)
	}
	if !strings.Contains(meta.Description, "separators") {
		t.Fatalf("value containing --- must survive, got description %q", meta.Description)
	}
}

// TestScanSkillDirDedupsCaseInsensitively 验证大小写变体的同名 skill 只保留
// 先扫描到的那个，避免列表出现仅大小写不同的重复项。
func TestScanSkillDirDedupsCaseInsensitively(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"a", "b"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, root, "a/SKILL.md", "---\nname: Foo\ndescription: first\n---\nbody")
	writeTestFile(t, root, "b/SKILL.md", "---\nname: foo\ndescription: second\n---\nbody")

	skills := []SkillDefinition{}
	seen := map[string]bool{}
	scanSkillDir(root, "project", &skills, seen)
	if len(skills) != 1 {
		t.Fatalf("case-differing duplicate names must dedup to one skill, got %d: %+v", len(skills), skills)
	}
	if skills[0].Description != "first" {
		t.Fatalf("first scanned skill must win, got description %q", skills[0].Description)
	}
}

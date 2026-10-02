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

func TestParseSkillFileDirectorySkillFallsBackToParentDir(t *testing.T) {
	root := t.TempDir()
	// Documented directory skill layout: <skill-dir>/SKILL.md without
	// frontmatter takes the parent directory as its name. Falling back to the
	// file stem would yield "SKILL" for every such skill.
	writeSkillTestFile(t, root, filepath.Join("my-skill", "SKILL.md"), "# my skill\ndoes things\n")
	meta := parseSkillFile(filepath.Join(root, "my-skill", "SKILL.md"))
	if meta.Name != "my-skill" {
		t.Fatalf("directory skill must be named after its parent directory, got %q", meta.Name)
	}
}

func TestScanSkillDirKeepsMultipleDirectorySkillsWithoutFrontmatter(t *testing.T) {
	root := t.TempDir()
	writeSkillTestFile(t, root, filepath.Join("alpha-skill", "SKILL.md"), "# alpha\n")
	writeSkillTestFile(t, root, filepath.Join("beta-skill", "SKILL.md"), "# beta\n")

	skills := []SkillDefinition{}
	scanSkillDir(root, "project", &skills, map[string]bool{})
	names := map[string]bool{}
	for _, skill := range skills {
		names[skill.Name] = true
	}
	if !names["alpha-skill"] || !names["beta-skill"] {
		t.Fatalf("both directory skills must survive dedup, got %#v", skills)
	}
}

// userSkillsDir 是 user 作用域技能根的期望路径：与生产代码同源
// （agentsSkillsSubdir），HOME/USERPROFILE 已由 newApiTestApp 指向临时目录。
func userSkillsDir(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(home, agentsSkillsSubdir)
}

func findSkill(t *testing.T, skills []SkillDefinition, name string) SkillDefinition {
	t.Helper()
	for _, sk := range skills {
		if strings.EqualFold(sk.Name, name) {
			return sk
		}
	}
	t.Fatalf("skill %q not in listing", name)
	return SkillDefinition{}
}

func TestSaveSkillWritesUserSkillAndListsIt(t *testing.T) {
	app := newApiTestApp(t)
	// 先把列表缓存填上：保存后必须让下一次 ListSkills 立刻看到新技能，
	// 否则面板刷新/斜杠命令要等 30s TTL。
	if _, err := app.ListSkills(); err != nil {
		t.Fatal(err)
	}

	path, err := app.SaveSkill(SkillDraft{
		Target:      "user",
		Name:        "my-skill",
		Description: "does things",
		WhenToUse:   "when testing",
		Content:     "# Body\n\nstep 1\n",
	})
	if err != nil {
		t.Fatalf("SaveSkill: %v", err)
	}
	want := filepath.Join(userSkillsDir(t), "my-skill", "SKILL.md")
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.HasPrefix(text, "---\n") {
		t.Fatalf("expected frontmatter first:\n%s", text)
	}
	for _, fragment := range []string{
		"name: my-skill\n",
		"description: does things\n",
		"whenToUse: when testing\n",
		"# Body\n\nstep 1\n",
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("written file misses %q:\n%s", fragment, text)
		}
	}

	sk := findSkill(t, mustListSkills(t, app), "my-skill")
	if sk.Source != "user" {
		t.Fatalf("source = %q, want user", sk.Source)
	}
	if sk.Description != "does things" || sk.WhenToUse != "when testing" {
		t.Fatalf("metadata round-trip failed: %+v", sk)
	}
	if sk.Dir != filepath.Dir(path) {
		t.Fatalf("dir = %q, want %q", sk.Dir, filepath.Dir(path))
	}
}

func TestSaveSkillRejectsDuplicateNamesAcrossScopes(t *testing.T) {
	app := newApiTestApp(t)
	workspace := t.TempDir()
	app.config.Workspace = workspace

	if _, err := app.SaveSkill(SkillDraft{Name: "dup-skill", Description: "first", Content: "body"}); err != nil {
		t.Fatalf("first save: %v", err)
	}
	// 同一作用域重复：文件已存在。
	if _, err := app.SaveSkill(SkillDraft{Name: "dup-skill", Description: "again"}); err == nil {
		t.Fatal("expected duplicate save in the same scope to fail")
	}
	// 另一作用域大小写变体：仍归同一技能名，拒绝（否则列表上看不出谁生效）。
	if _, err := app.SaveSkill(SkillDraft{Target: "project", Name: "DUP-SKILL", Description: "again"}); err == nil {
		t.Fatal("expected duplicate save in another scope to fail")
	}
	if _, err := os.Stat(filepath.Join(workspace, agentsSkillsSubdir, "DUP-SKILL")); err == nil {
		t.Fatal("rejected save must not leave a directory behind")
	}
}

func TestSaveSkillRejectsUnsafeNames(t *testing.T) {
	app := newApiTestApp(t)
	workspace := t.TempDir()
	app.config.Workspace = workspace
	outside := filepath.Join(workspace, "escape")

	names := []string{
		"",
		"   ",
		"../escape",
		"a/b",
		`a\b`,
		".hidden",
		"..",
		"trailing.",
		"has space",
		strings.Repeat("x", skillNameMaxRunes+1),
	}
	for _, name := range names {
		for _, target := range []string{"user", "project"} {
			if _, err := app.SaveSkill(SkillDraft{Target: target, Name: name, Description: "d"}); err == nil {
				t.Fatalf("expected name %q (target %s) to be rejected", name, target)
			}
		}
	}
	if _, err := os.Stat(outside); err == nil {
		t.Fatalf("rejected names must not create %s", outside)
	}
}

func TestSaveSkillRequiresDescription(t *testing.T) {
	app := newApiTestApp(t)
	if _, err := app.SaveSkill(SkillDraft{Name: "no-desc"}); err == nil {
		t.Fatal("expected missing description to be rejected")
	}
}

func TestSaveSkillProjectTargetNeedsWorkspace(t *testing.T) {
	app := newApiTestApp(t)
	if _, err := app.SaveSkill(SkillDraft{Target: "project", Name: "proj-skill", Description: "d"}); err == nil {
		t.Fatal("expected project target without a workspace to fail")
	}
	if _, err := app.SaveSkill(SkillDraft{Target: "nowhere", Name: "proj-skill", Description: "d"}); err == nil {
		t.Fatal("expected unknown target to fail")
	}
}

func TestSaveSkillWritesProjectSkillUnderWorkspace(t *testing.T) {
	app := newApiTestApp(t)
	workspace := t.TempDir()
	app.config.Workspace = workspace

	path, err := app.SaveSkill(SkillDraft{Target: "project", Name: "proj-skill", Description: "d", Content: "body"})
	if err != nil {
		t.Fatalf("SaveSkill: %v", err)
	}
	want := filepath.Join(workspace, agentsSkillsSubdir, "proj-skill", "SKILL.md")
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	sk := findSkill(t, mustListSkills(t, app), "proj-skill")
	if sk.Source != "project" {
		t.Fatalf("source = %q, want project", sk.Source)
	}
}

// 元数据里的引号必须能原样读回：frontmatter 解析器只对以双引号开头的值做
// JSON 解码，直接拼裸引号会让 description 被静默截断。
func TestSaveSkillRoundTripsQuotedMetadata(t *testing.T) {
	app := newApiTestApp(t)
	description := `他说: "跑测试" 就行 & 别忘记 #1`
	whenToUse := `when the user says "ship it"`
	if _, err := app.SaveSkill(SkillDraft{Name: "quoted", Description: description, WhenToUse: whenToUse}); err != nil {
		t.Fatalf("SaveSkill: %v", err)
	}
	sk := findSkill(t, mustListSkills(t, app), "quoted")
	if sk.Description != description {
		t.Fatalf("description = %q, want %q", sk.Description, description)
	}
	if sk.WhenToUse != whenToUse {
		t.Fatalf("whenToUse = %q, want %q", sk.WhenToUse, whenToUse)
	}
}

// 换行会把 line-based 的 frontmatter 解析截断（甚至注入分隔符），因此元数据
// 一律压成一行。
func TestSaveSkillFlattensMultilineMetadata(t *testing.T) {
	app := newApiTestApp(t)
	if _, err := app.SaveSkill(SkillDraft{
		Name:        "multiline",
		Description: "first line\n---\nname: hijacked\nsecond line",
	}); err != nil {
		t.Fatalf("SaveSkill: %v", err)
	}
	sk := findSkill(t, mustListSkills(t, app), "multiline")
	if strings.Contains(sk.Description, "\n") {
		t.Fatalf("description kept a newline: %q", sk.Description)
	}
	if sk.Name != "multiline" {
		t.Fatalf("name = %q, want multiline (frontmatter must not be hijacked)", sk.Name)
	}
}

// 内置技能没有磁盘目录（Dir 是嵌入树里的相对路径），冲突提示必须指到
// builtin:// 上，否则用户会照着 "skills/codegraph" 去工作区里找。
func TestSaveSkillBuiltinConflictNamesTheVirtualPath(t *testing.T) {
	app := newApiTestApp(t)

	builtin := builtinSkillEntries()[0]
	if builtin.Dir == "" {
		t.Skipf("built-in %q has no embedded dir to confuse", builtin.Name)
	}
	if _, err := app.SaveSkill(SkillDraft{Name: builtin.Name, Description: "d"}); err == nil {
		t.Fatalf("expected %q to collide with the built-in skill", builtin.Name)
	} else {
		const marker = "builtin scope: "
		idx := strings.Index(err.Error(), marker)
		if idx < 0 {
			t.Fatalf("expected the builtin scope in %q", err.Error())
		}
		location := err.Error()[idx+len(marker):]
		if !strings.HasPrefix(location, "builtin://") {
			t.Fatalf("location should be the virtual path, got %q", location)
		}
	}
}

func mustListSkills(t *testing.T, app *App) []SkillDefinition {
	t.Helper()
	skills, err := app.ListSkills()
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	return skills
}

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
	if !strings.Contains(cg.embeddedContent, "速查表") {
		t.Fatalf("expected embedded content to specify the feature-lookup table section")
	}
	if !strings.HasPrefix(cg.Path, "builtin://") {
		t.Fatalf("expected Path to start with builtin://, got %q", cg.Path)
	}
	if cg.WhenToUse == "" {
		t.Fatal("expected WhenToUse to be populated from frontmatter")
	}
}

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
	// Production order: .agents/skills first, then .claude/skills
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

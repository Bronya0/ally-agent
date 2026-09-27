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

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
	"sync"
	"sync/atomic"
	"testing"
)

func TestSkillToggleUsesThePromptSnapshot(t *testing.T) {
	redirectAppStateDir(t)
	a := newIsolatedToolTestApp(t)
	for _, name := range []string{"initial", "later"} {
		writeSkillTestFile(t, a.config.Workspace, ".agents/skills/"+name+"/SKILL.md", "---\nname: "+name+"\ndescription: example\n---\nbody")
	}
	if err := a.DeactivateSkill("later"); err != nil {
		t.Fatal(err)
	}
	prompt := a.sessionSystemPrompt("existing", a.config, a.listCachedSkillsForConfig(a.config))
	if err := a.DeactivateSkill("initial"); err != nil {
		t.Fatal(err)
	}
	if err := a.enableSkill("later"); err != nil {
		t.Fatal(err)
	}
	if got := a.sessionSystemPrompt("existing", a.config, a.listCachedSkillsForConfig(a.config)); got != prompt {
		t.Fatal("toggling skills changed an existing prompt")
	}
	a.sessionSystemPrompt("fresh", a.config, a.listCachedSkillsForConfig(a.config))
	for _, tc := range []struct {
		session, skill string
		allowed        bool
	}{
		{"existing", "initial", true}, {"existing", "later", false},
		{"fresh", "initial", false}, {"fresh", "later", true},
		{"", "initial", false}, {"", "later", true},
	} {
		_, err := a.handleSkillToolCall(a.config, tc.session, tc.skill, "")
		if (err == nil) != tc.allowed {
			t.Errorf("session=%q skill=%q allowed=%v, err=%v", tc.session, tc.skill, tc.allowed, err)
		}
	}
}

func TestManualSkillRefreshReadsDiskBeforeTTL(t *testing.T) {
	redirectAppStateDir(t)
	a := newIsolatedToolTestApp(t)
	const path = ".agents/skills/sample/SKILL.md"
	writeSkillTestFile(t, a.config.Workspace, path, "---\nname: sample\ndescription: before\n---\nbody")
	if _, err := a.GetSkillState(); err != nil {
		t.Fatal(err)
	}
	writeSkillTestFile(t, a.config.Workspace, path, "---\nname: sample\ndescription: after\n---\nbody")
	state, err := a.RefreshSkillState()
	if err != nil {
		t.Fatal(err)
	}
	if sk := findSkill(t, state.Skills, "sample"); sk.Description != "after" {
		t.Fatalf("manual refresh kept stale metadata: %+v", sk)
	}
}

func TestGetSkillStateReturnsOneListingAndItsEnabledNames(t *testing.T) {
	t.Parallel()
	app := newApiTestApp(t)
	writeSkillTestFile(t, app.config.Workspace, ".agents/skills/custom/SKILL.md", "---\nname: custom\n---\nbody")
	app.disabledSkills = []string{"CUSTOM"}
	state, err := app.GetSkillState()
	if err != nil {
		t.Fatal(err)
	}
	findSkill(t, state.Skills, "custom")
	for _, name := range state.Active {
		if strings.EqualFold(name, "custom") {
			t.Fatal("disabled skill is active")
		}
		findSkill(t, state.Skills, name)
	}
	if len(state.Active) != len(state.Skills)-1 {
		t.Fatalf("inconsistent listing/active state: %+v", state)
	}
}

func TestSkillColdScanIsShared(t *testing.T) {
	t.Parallel()
	app := newApiTestApp(t)
	var scans atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	scan := func(string) []SkillDefinition {
		if scans.Add(1) == 1 {
			close(entered)
		}
		<-release
		return []SkillDefinition{{Name: "shared"}}
	}
	const callers = 16
	results := make(chan []SkillDefinition, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- app.cachedSkillsForRoot(app.config.Workspace, scan)
		}()
	}
	<-entered
	close(release)
	wg.Wait()
	if scans.Load() != 1 {
		t.Fatalf("cold scan ran %d times", scans.Load())
	}
	first := <-results
	first[0].Name = "mutated"
	for i := 1; i < callers; i++ {
		if result := <-results; result[0].Name != "shared" {
			t.Fatal("callers share a mutable result")
		}
	}
	if result := app.cachedSkillsForRoot(app.config.Workspace, scan); result[0].Name != "shared" {
		t.Fatal("caller mutated the cache")
	}
}

func TestSkillInvalidationDuringColdScanRejectsOldResult(t *testing.T) {
	t.Parallel()
	app := newApiTestApp(t)
	var scans atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	result := make(chan []SkillDefinition, 1)
	scan := func(string) []SkillDefinition {
		if scans.Add(1) == 1 {
			close(entered)
			<-release
			return []SkillDefinition{{Name: "obsolete"}}
		}
		return []SkillDefinition{{Name: "current"}}
	}
	go func() { result <- app.cachedSkillsForRoot(app.config.Workspace, scan) }()
	<-entered
	app.clearSkillListCache()
	close(release)
	if got := <-result; len(got) != 1 || got[0].Name != "current" {
		t.Fatalf("stale scan survived invalidation: %+v", got)
	}
	if scans.Load() != 2 {
		t.Fatalf("expected one replacement scan, got %d", scans.Load())
	}
}

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

func TestParseSkillFileRejectsStandaloneMarkdownWithFrontmatter(t *testing.T) {
	root := t.TempDir()
	readmePath := filepath.Join(root, "readme.md")
	content := "---\nname: my-readme-skill\ndescription: a skill despite the filename\n---\n# body\n"
	writeSkillTestFile(t, root, "readme.md", content)

	meta := parseSkillFile(readmePath)
	if meta.Name != "" {
		t.Fatalf("standalone Markdown must not become a skill even with frontmatter, got name=%q", meta.Name)
	}
}

func TestParseSkillFileRejectsStandaloneMarkdown(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"LESSONS.md", "lessons.MD", "AGENTS.md", "USER.md", "CODEGRAPH.md", "formatter.md", "my-notes.md"} {
		writeSkillTestFile(t, root, name, "# Plain Markdown\nnot a skill declaration\n")
		if meta := parseSkillFile(filepath.Join(root, name)); meta.Name != "" {
			t.Fatalf("standalone %s must be skipped, got %q", name, meta.Name)
		}
	}
	// Ordinary document metadata is not a skill declaration either.
	writeSkillTestFile(t, root, "notes.md", "---\ntitle: Notes\ndescription: ordinary documentation\n---\nbody\n")
	if meta := parseSkillFile(filepath.Join(root, "notes.md")); meta.Name != "" {
		t.Fatalf("frontmatter without a name must not register a skill, got %q", meta.Name)
	}
}

func TestScanSkillDirIgnoresUnrelatedMarkdown(t *testing.T) {
	root := t.TempDir()
	writeSkillTestFile(t, root, "readme.md", "# Skills\ndocs\n")
	writeSkillTestFile(t, root, "LESSONS.md", "# Lessons\nproject lessons\n")
	writeSkillTestFile(t, root, "other-notes.md", "# Unrelated notes\n")
	writeSkillTestFile(t, root, "formatter.md", "---\nname: formatter\ndescription: formats code\n---\n# formatter\n")
	// A SKILL.md directly in the scan root is not a skill directory either.
	writeSkillTestFile(t, root, "SKILL.md", "---\nname: root-skill\ndescription: invalid layout\n---\n")
	// Directory skill with SKILL.md + frontmatter
	writeSkillTestFile(t, root, "greeter/SKILL.md", "---\nname: greeter\ndescription: says hi\n---\n")
	writeSkillTestFile(t, root, "plain-dir/SKILL.md", "# Directory skill\n")

	var skills []SkillDefinition
	seen := map[string]bool{}
	scanSkillDir(root, "user", &skills, seen, map[string]bool{})

	names := map[string]bool{}
	for _, s := range skills {
		names[s.Name] = true
	}
	if len(skills) != 2 {
		t.Fatalf("only directory skills should be scanned, got %v", names)
	}
	if !names["plain-dir"] {
		t.Fatalf("directory skill without frontmatter should be scanned, got %v", names)
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
	scanSkillDir(root, "project", &skills, seen, map[string]bool{})
	if len(skills) != 1 {
		t.Fatalf("case-differing duplicate names must dedup to one skill, got %d: %+v", len(skills), skills)
	}
	if skills[0].Description != "first" {
		t.Fatalf("first scanned skill must win, got description %q", skills[0].Description)
	}
}

func TestScanSkillDirSkipsDuplicateDirectoriesBeforeParsing(t *testing.T) {
	root := t.TempDir()
	first, second := filepath.Join(root, "first"), filepath.Join(root, "second")
	writeSkillTestFile(t, first, "Shared/SKILL.md", "---\nname: first-name\n---\nbody")
	writeSkillTestFile(t, second, "shared/SKILL.md", "---\nname: different-name\n---\nbody")
	// An earlier directory without a readable SKILL.md must allow fallback.
	if err := os.MkdirAll(filepath.Join(first, "fallback"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeSkillTestFile(t, second, "fallback/SKILL.md", "# fallback")
	var skills []SkillDefinition
	seen, seenDirs := map[string]bool{}, map[string]bool{}
	scanSkillDir(first, "user", &skills, seen, seenDirs)
	scanSkillDir(second, "project", &skills, seen, seenDirs)
	if len(skills) != 2 {
		t.Fatalf("expected original skill and valid fallback, got %+v", skills)
	}
	if skill := findSkill(t, skills, "first-name"); skill.Source != "user" {
		t.Fatalf("earlier directory must win, got %+v", skill)
	}
	findSkill(t, skills, "fallback")
	for _, skill := range skills {
		if skill.Name == "different-name" {
			t.Fatal("duplicate directory must be skipped regardless of its declared name")
		}
	}
}

func TestReadSkillMetadataStopsBeforeLargeBody(t *testing.T) {
	for _, header := range []string{
		"---\nname: header-skill\ndescription: contains --- inline\n---\n",
		"\ufeff---\r\nname: header-skill\r\n---\r\n",
		"# no frontmatter\n",
	} {
		r := strings.NewReader(header + strings.Repeat("body\n", 100000))
		initial := r.Len()
		text, err := readSkillMetadata(r)
		if err != nil {
			t.Fatal(err)
		}
		if consumed := initial - r.Len(); consumed > 4096 {
			t.Fatalf("listing read beyond the first buffer: %d bytes", consumed)
		}
		want := parseSkillContent("directory/SKILL.md", header)
		if got := parseSkillContent("directory/SKILL.md", text); got != want {
			t.Fatalf("streamed metadata differs: got %+v, want %+v", got, want)
		}
	}
}

func TestReadSkillMetadataBoundsMalformedHeaders(t *testing.T) {
	for _, prefix := range []string{"---\nname: incomplete\n", "# no frontmatter"} {
		r := strings.NewReader(prefix + strings.Repeat("x", 1000000))
		initial := r.Len()
		text, err := readSkillMetadata(r)
		if err != nil || text != "" {
			t.Fatalf("expected directory-name fallback, got %q, %v", text, err)
		}
		if consumed := initial - r.Len(); consumed > 64*1024+1 {
			t.Fatalf("malformed metadata read exceeded budget: %d", consumed)
		}
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
	scanSkillDir(root, "project", &skills, map[string]bool{}, map[string]bool{})
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	app := newApiTestApp(t)
	if _, err := app.SaveSkill(SkillDraft{Name: "no-desc"}); err == nil {
		t.Fatal("expected missing description to be rejected")
	}
}

func TestSaveSkillProjectTargetNeedsWorkspace(t *testing.T) {
	t.Parallel()
	app := newApiTestApp(t)
	app.config.Workspace = "" // This case deliberately overrides the helper's sandbox workspace.
	if _, err := app.SaveSkill(SkillDraft{Target: "project", Name: "proj-skill", Description: "d"}); err == nil {
		t.Fatal("expected project target without a workspace to fail")
	}
	if _, err := app.SaveSkill(SkillDraft{Target: "nowhere", Name: "proj-skill", Description: "d"}); err == nil {
		t.Fatal("expected unknown target to fail")
	}
}

func TestSaveSkillWritesProjectSkillUnderWorkspace(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

// 使用手册也是普通的内置技能：目录名即身份，内容里的边界口径是本轮修正过的
// 地方，文档被改回旧说法时这里跟着红。
func TestBuiltinSkillEntriesContainsAllyAgent(t *testing.T) {
	entries := builtinSkillEntries()
	var manual *SkillDefinition
	for i := range entries {
		if entries[i].Name == "ally-agent" {
			manual = &entries[i]
			break
		}
	}
	if manual == nil {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name)
		}
		t.Fatalf("expected ally-agent in built-in skills, got %v", names)
	}
	if manual.Source != "builtin" {
		t.Fatalf("expected Source=builtin, got %q", manual.Source)
	}
	if manual.embeddedContent == "" {
		t.Fatal("expected embeddedContent to be populated for built-in skill")
	}
	if !strings.Contains(manual.embeddedContent, "E_PATH_OUTSIDE") {
		t.Fatal("expected the usage manual to name the fence refusal code")
	}
	if !strings.HasPrefix(manual.Path, "builtin://") {
		t.Fatalf("expected Path to start with builtin://, got %q", manual.Path)
	}
	if manual.WhenToUse == "" {
		t.Fatal("expected WhenToUse to be populated from frontmatter")
	}
}

func TestListSkillsIncludesCodexSkills(t *testing.T) {
	t.Parallel()
	for _, scope := range []string{"user", "project"} {
		t.Run(scope, func(t *testing.T) {
			app := newApiTestApp(t)
			root := app.config.Workspace
			if scope == "user" {
				var err error
				root, err = os.UserHomeDir()
				if err != nil {
					t.Fatal(err)
				}
			}
			writeSkillTestFile(t, root, ".codex/skills/codex-only/SKILL.md", "---\nname: codex-only\ndescription: from codex\n---\nbody\n")
			// Compatibility roots must not shadow skills from existing roots.
			for _, dir := range []string{".agents", ".claude", ".codex"} {
				writeSkillTestFile(t, root, dir+"/skills/shared/SKILL.md", "---\nname: shared\ndescription: "+dir+"\n---\nbody\n")
			}
			for _, dir := range []string{".claude", ".codex"} {
				writeSkillTestFile(t, root, dir+"/skills/compat-shared/SKILL.md", "---\nname: compat-shared\ndescription: "+dir+"\n---\nbody\n")
			}
			skills, err := app.ListSkills()
			if err != nil {
				t.Fatal(err)
			}
			codex := findSkill(t, skills, "codex-only")
			if codex.Source != scope || codex.Path != filepath.Join(root, ".codex", "skills", "codex-only", "SKILL.md") {
				t.Fatalf("unexpected Codex skill: %+v", codex)
			}
			if shared := findSkill(t, skills, "shared"); shared.Description != ".agents" {
				t.Fatalf("expected .agents to retain precedence, got %+v", shared)
			}
			if shared := findSkill(t, skills, "compat-shared"); shared.Description != ".claude" {
				t.Fatalf("expected .claude to retain precedence, got %+v", shared)
			}
		})
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
	scanSkillDir(filepath.Join(root, ".claude", "skills"), "project", &skills, seen, map[string]bool{})

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
	seenDirs := map[string]bool{}
	// Production order: .agents/skills first, then compatibility roots.
	for _, sub := range skillScanDirs {
		scanSkillDir(filepath.Join(root, sub), "project", &skills, seen, seenDirs)
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

func TestSkillRequestWorkspaceAndProjectPrecedence(t *testing.T) {
	t.Parallel()
	a := newApiTestApp(t)
	other := t.TempDir()
	writeSkillTestFile(t, userSkillsDir(t), "shared/SKILL.md", "---\nname: shared\n---\nUSER")
	writeSkillTestFile(t, a.config.Workspace, ".agents/skills/shared/SKILL.md", "---\nname: shared\n---\nGLOBAL")
	writeSkillTestFile(t, other, ".agents/skills/shared/SKILL.md", "---\nname: shared\n---\nREQUEST")
	cfg := a.config
	cfg.Workspace = other
	sk := findSkill(t, a.listCachedSkillsForConfig(cfg), "shared")
	if sk.Source != "project" || sk.Dir != filepath.Join(other, agentsSkillsSubdir, "shared") {
		t.Fatalf("wrong scope: %+v", sk)
	}
	result := a.executeTool(t.Context(), cfg, "", "skill", []byte(`{"skill":"shared"}`))
	if !result.OK {
		t.Fatalf("load failed: %+v", result)
	}
	loaded, ok := result.Data.(map[string]any)
	if !ok || !strings.Contains(loaded["content"].(string), "REQUEST") || strings.Contains(loaded["content"].(string), "GLOBAL") {
		t.Fatalf("wrong workspace: %+v", result)
	}
}

func TestSkillYAMLMetadataUsesOnlyTopLevelFields(t *testing.T) {
	for _, style := range []string{">", "|"} {
		meta := parseSkillContent("sample/SKILL.md", "---\nname: actual\ndescription: "+style+"\n  first line\n  second line\nmetadata:\n  name: nested\n  description: wrong\n---\nbody")
		if meta.Name != "actual" || !strings.Contains(meta.Description, "first line") || !strings.Contains(meta.Description, "second line") {
			t.Fatalf("wrong YAML metadata: %+v", meta)
		}
	}
}

func TestSaveSkillValidatesSerializedSizeBeforeWriting(t *testing.T) {
	t.Parallel()
	a := newApiTestApp(t)
	for _, draft := range []SkillDraft{
		{Name: "body-limit", Description: "description", Content: strings.Repeat("x", maxSkillFileBytes)},
		{Name: "header-limit", Description: strings.Repeat("x", 65*1024), Content: "body"},
	} {
		if _, err := a.SaveSkill(draft); err == nil {
			t.Fatalf("expected size rejection for %s", draft.Name)
		}
		if _, err := os.Stat(filepath.Join(userSkillsDir(t), draft.Name)); !os.IsNotExist(err) {
			t.Fatalf("rejected skill left files: %v", err)
		}
	}
}

func TestSkillWrittenYAMLScalarRoundTrip(t *testing.T) {
	for _, value := range []string{"uses: colon", "text # comment", ">", "true", "null", "[one, two]", "*alias", "plain", "中文"} {
		meta := parseSkillContent("sample/SKILL.md", renderSkillFile("sample", value, value, "body"))
		if meta.Name != "sample" || meta.Description != value || meta.WhenToUse != value {
			t.Fatalf("%q did not round trip: %+v", value, meta)
		}
	}
}

func TestSkillBareColonMetadataCompatibility(t *testing.T) {
	for _, description := range []string{"用途: 处理文件", "Use when: editing files", "Use when: editing #1"} {
		text := "---\nname: actual-name\ndescription: " + description + "\nwhen_to_use: task: files\nmetadata:\n  name: nested-name\n---\nbody"
		meta := parseSkillContent("different-folder/SKILL.md", text)
		if meta.Name != "actual-name" || meta.Description != description || meta.WhenToUse != "task: files" {
			t.Fatalf("bare metadata did not survive: %+v", meta)
		}
	}
	text := "---\nname: actual-name\ndescription: |\n  first line\n  second line\nwhenToUse: task: files\n---\nbody"
	meta := parseSkillContent("different-folder/SKILL.md", text)
	if meta.Name != "actual-name" || meta.Description != "first line\nsecond line\n" || meta.WhenToUse != "task: files" {
		t.Fatalf("compatibility repair changed YAML block text: %+v", meta)
	}
}

func TestSkillMalformedMetadataKeepsIdentityAndOtherFields(t *testing.T) {
	for _, front := range []string{
		"name: actual\ndescription: \"使用 \"read\" 工具\"\nwhenToUse: editing",
		"name: actual\nname: duplicate\ndescription: useful\nwhenToUse: editing",
		"name: actual\ndescription: useful\nmetadata:\n\tbroken: indentation\nwhenToUse: editing",
		"name: actual\ndescription: |\n  first line\n  second line\nmetadata: [broken\nwhenToUse: editing",
	} {
		meta := parseSkillContent("different-folder/SKILL.md", "---\n"+front+"\n---\nbody")
		if meta.Name != "actual" || meta.Description == "" || strings.HasPrefix(meta.Description, "Skill loaded from") || meta.WhenToUse != "editing" {
			t.Fatalf("malformed YAML lost independent fields: %+v", meta)
		}
	}
}

func TestSkillBareMetadataPreservesIssueNumbers(t *testing.T) {
	for _, value := range []string{"修复 issue #42 与 #43", "text # comment", "Use when: editing #1"} {
		meta := parseSkillContent("other/SKILL.md", "---\nname: actual\ndescription: "+value+"\nwhen_to_use: "+value+"\n---\nbody")
		if meta.Description != value || meta.WhenToUse != value {
			t.Fatalf("bare scalar was truncated: %+v", meta)
		}
	}
}

func TestSkillListingFoldsMultilineMetadata(t *testing.T) {
	meta := parseSkillContent("other/SKILL.md", "---\nname: actual\ndescription: |\n  first line\n  second line\nwhenToUse: |\n  when needed\n  for edits\n---\nbody")
	listing := buildSkillListingMeta([]SkillDefinition{meta})
	if !strings.Contains(listing, "- actual: first line second line\n") || !strings.Contains(listing, "  When to use: when needed for edits\n") {
		t.Fatalf("multiline metadata escaped listing entry: %q", listing)
	}
}

func TestSkillValidYAMLRetainsAliasesAndQuotedMultiline(t *testing.T) {
	meta := parseSkillContent("other/SKILL.md", "---\nname: actual\ndescription: &desc \"first line\n  second line\"\nwhenToUse: *desc\n---\nbody")
	if meta.Description != "first line second line" || meta.WhenToUse != meta.Description {
		t.Fatalf("valid YAML semantics changed: %+v", meta)
	}
	meta = parseSkillContent("other/SKILL.md", "---\nname: actual\ndescription: first line\n  second line\n---\nbody")
	if meta.Description != "first line second line" {
		t.Fatalf("plain multiline YAML changed: %+v", meta)
	}
}

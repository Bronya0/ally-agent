package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The skills form displays the active tab's workspace; a project-scope draft
// must land in that workspace even when the global config still points at the
// previously opened project.
func TestSaveSkillProjectDraftWritesToTheDraftWorkspace(t *testing.T) {
	t.Parallel()
	app := newIsolatedTestApp(t)
	app.initialized = true
	workspaceA := t.TempDir()
	workspaceB := filepath.Join(t.TempDir(), "project-b")
	if err := os.MkdirAll(workspaceB, 0o755); err != nil {
		t.Fatal(err)
	}
	app.config.Workspace = workspaceB

	draft := SkillDraft{Target: "project", Workspace: workspaceA, Name: "draft-workspace", Description: "d", Content: "body"}
	path, err := app.SaveSkill(draft)
	if err != nil {
		t.Fatalf("SaveSkill into the draft workspace: %v", err)
	}
	if !strings.HasPrefix(path, filepath.Join(workspaceA, agentsSkillsSubdir)) {
		t.Fatalf("skill escaped the draft workspace: %s", path)
	}
	if strings.HasPrefix(path, filepath.Clean(workspaceB)) {
		t.Fatalf("skill followed the stale global config: %s", path)
	}

	// A draft without a workspace keeps the config fallback.
	fallback := filepath.Join(workspaceB, agentsSkillsSubdir, "config-fallback", "SKILL.md")
	if _, err := os.Stat(fallback); !os.IsNotExist(err) {
		t.Fatalf("unexpected pre-existing fallback skill: %v", err)
	}
	path, err = app.SaveSkill(SkillDraft{Target: "project", Name: "config-fallback", Description: "d", Content: "body"})
	if err != nil {
		t.Fatalf("SaveSkill without a workspace: %v", err)
	}
	if !strings.HasPrefix(path, filepath.Join(filepath.Clean(workspaceB), agentsSkillsSubdir)) {
		t.Fatalf("config fallback changed: %s", path)
	}

	// An inaccessible workspace is refused instead of created implicitly.
	missing := filepath.Join(workspaceA, "does-not-exist")
	if _, err := app.SaveSkill(SkillDraft{Target: "project", Workspace: missing, Name: "missing-root", Description: "d", Content: "body"}); err == nil {
		t.Fatal("an inaccessible draft workspace was accepted")
	}
}

func TestSkillWriteRootRejectsUnknownTarget(t *testing.T) {
	t.Parallel()
	app := newIsolatedTestApp(t)
	if _, err := app.skillWriteRoot("elsewhere", ""); err == nil {
		t.Fatal("an unknown target was accepted")
	}
}

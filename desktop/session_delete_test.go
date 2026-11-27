package desktop

import (
	"errors"
	"testing"
	"time"

	backend "ally-dev/internal/app"
)

// A refused or failed backend sync must not shrink the transcript: the old flow
// cut the visible turns first and only then reported the mismatch, so the UI
// and the model context kept different histories.
func TestFailedDeletionKeepsTheTranscript(t *testing.T) {
	t.Setenv("ALLY_DATA_DIR", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	conv := &conversation{ID: "delete-failure", Workspace: t.TempDir(), Messages: []map[string]any{
		{"role": "user", "content": "first"},
		{"role": "assistant", "content": "answer"},
		{"role": "user", "content": "second"},
	}}
	a := &desktop{core: backend.NewApp()}

	a.applyUserMessageDeletion(conv, 2, errors.New("session is compacting; retry the deletion after the compaction finishes"))
	if len(conv.Messages) != 3 {
		t.Fatalf("the transcript was cut before the backend accepted: %v", conv.Messages)
	}
	if len(a.notifications) == 0 {
		t.Fatal("the failure was not surfaced to the user")
	}

	a.applyUserMessageDeletion(conv, 2, nil)
	if a.persistTail != nil {
		// The snapshot write runs in a worker; wait for it so the test's temp
		// directory is not cleaned while the write is still in flight.
		<-a.persistTail
	}
	if len(conv.Messages) != 2 || str(conv.Messages[1], "content") != "answer" {
		t.Fatalf("an accepted deletion did not cut the transcript: %v", conv.Messages)
	}
}

// 清空草稿后丢弃空白标签必须落盘显式清空，否则重开该会话会复活旧草稿。
func TestDiscardingAPersistedBlankTabPersistsTheClearedDraft(t *testing.T) {
	t.Setenv("ALLY_DATA_DIR", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	core := backend.NewApp()
	conv := &conversation{ID: "cleared-draft", Workspace: t.TempDir(), SnapshotQueued: true}
	a := &desktop{core: core, openSessions: map[string]*conversation{conv.ID: conv}}
	if err := core.SaveSession(backend.SessionSnapshot{ID: conv.ID, Title: "draft session", Workspace: conv.Workspace, Draft: "old draft", CreatedAt: time.Now().UnixMilli(), HasSnapshot: true}); err != nil {
		t.Fatal(err)
	}

	a.discardBlankTab(conv)
	if a.persistTail != nil {
		<-a.persistTail
	}
	loaded, err := core.LoadSession(conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Draft != "" {
		t.Fatalf("the cleared draft revived on reload: %q", loaded.Draft)
	}
}

package desktop

import (
	backend "ally-dev/internal/app"
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestSSHTestDraftUsesEditedConnection(t *testing.T) {
	draft := backend.SSHServerNode{ID: "saved", Host: "new-host", Username: "new-user", AuthType: "password", Password: "new-secret", Port: 22}
	got, ok := sshTestDraft(draft, 2222)
	if !ok || got.ID != "" || got.Host != draft.Host || got.Username != draft.Username || got.Password != draft.Password || got.Port != 2222 {
		t.Fatalf("wrong test connection: %+v", got)
	}
	for _, port := range []float64{0, 65536, 22.5, math.NaN()} {
		if _, ok := sshTestDraft(draft, port); ok {
			t.Fatalf("accepted invalid port %v", port)
		}
	}
}

func TestSSHAuthCanonicalID(t *testing.T) {
	a := &desktop{sshServers: []backend.SSHServerNode{{ID: "node-id", Alias: "dev", Host: "example.invalid", Username: "user", Port: 22}}}
	a.sshF.AuthOpen = true
	a.sshF.AuthReady = true
	a.sshF.AuthWorkspace = t.TempDir()
	// GetWorkspaceAllowedServers returns stable IDs, just as loadSSHAuth stores.
	a.sshF.AuthDraft = map[string]bool{"node-id": true}
	tt := ui.NewTester(func(c *ui.Context) { a.sshAuthDialog(c) }, 900, 700)
	if err := tt.Click("dev"); err != nil {
		t.Fatal(err)
	}
	if a.sshF.AuthDraft["node-id"] {
		t.Fatal("toggling an authorized node did not revoke its canonical ID grant")
	}
}

func TestSSHAuthLoadingCannotCommitOldDraft(t *testing.T) {
	a := &desktop{}
	a.sshF.AuthOpen = true
	a.sshF.AuthWorkspace = "new-workspace"
	a.sshF.AuthDraft = map[string]bool{"old-node": true}
	// No backend is needed: both calls must stop before queuing any write.
	a.commitSSHAuth("new-workspace")
	a.sshF.AuthReady = true
	a.commitSSHAuth("old-workspace")
	if a.sshAuthTail != nil || len(a.sshAllowed) != 0 {
		t.Fatal("an unready or mismatched draft changed grants")
	}
	a.sshF.AuthReady = false
	tt := ui.NewTester(func(c *ui.Context) { a.sshAuthDialog(c) }, 900, 700)
	if err := tt.Click("关闭 SSH 授权"); err != nil {
		t.Fatal(err)
	}
	if a.sshF.AuthOpen || a.sshAuthTail != nil {
		t.Fatal("closing a loading dialog submitted stale grants")
	}
}

func TestSSHAuthSavesInOrder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	a := &desktop{core: backend.NewApp()}
	if err := a.core.SaveSSHServer(backend.SSHServerNode{Alias: "dev", Host: "example.invalid", Username: "user", Description: "test"}); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	node, _ := a.core.GetSSHServer("dev")
	blocked := make(chan struct{})
	a.sshAuthTail = blocked
	ids := []string{node.ID}
	first := a.queueSSHAuthSave(workspace, ids)
	second := a.queueSSHAuthSave(workspace, nil)
	select {
	case <-first:
		t.Fatal("save skipped its predecessor")
	default:
	}
	close(blocked)
	for _, result := range []<-chan error{first, second} {
		select {
		case err := <-result:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("queued authorization save did not finish")
		}
	}
	if a.core.IsServerAuthorizedForWorkspace(workspace, node.ID) {
		t.Fatal("older grant overwrote the later revocation")
	}
}

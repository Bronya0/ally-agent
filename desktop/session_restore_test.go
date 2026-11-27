package desktop

import (
	"slices"
	"testing"

	backend "ally-dev/internal/app"
)

// 会话快照与恢复必须往返已批准的附加写入根，重启不能静默吊销用户的授权；
// 主动清空的语义同样要往返。
func TestSnapshotAndRestoreRoundTripExtraRoots(t *testing.T) {
	rootA, rootB := t.TempDir(), t.TempDir()
	a := &desktop{extraRoots: map[string][]string{"s1": {rootA, rootB}}}
	s := snapshot(&conversation{ID: "s1", Workspace: t.TempDir()}, a.extraRoots["s1"])
	if !slices.Equal(s.ExtraRoots, []string{rootA, rootB}) {
		t.Fatalf("snapshot lost the approved roots: %v", s.ExtraRoots)
	}

	target := &desktop{extraRoots: map[string][]string{}}
	restored := target.applyRestoredSession(backend.SessionSnapshot{ID: "s1", Workspace: s.Workspace, ExtraRoots: s.ExtraRoots})
	if restored.ID != "s1" || !slices.Equal(target.extraRoots["s1"], []string{rootA, rootB}) {
		t.Fatalf("restore lost the approved roots: %v", target.extraRoots["s1"])
	}

	// 从未批准附加根的会话恢复后不带任何根，也不带消息。
	blankDesk := &desktop{extraRoots: map[string][]string{}}
	blank := blankDesk.applyRestoredSession(backend.SessionSnapshot{ID: "s2", Workspace: s.Workspace})
	if _, ok := blankDesk.extraRoots["s2"]; ok || len(blank.Messages) != 0 {
		t.Fatal("a root-less snapshot grew roots or messages")
	}
}

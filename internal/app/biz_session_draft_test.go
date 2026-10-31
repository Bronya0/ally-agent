package app

import "testing"

func TestSessionDraftSurvivesSaveAndReload(t *testing.T) {
	redirectAppStateDir(t)
	a := newIsolatedToolTestApp(t)
	s := SessionSnapshot{ID: "draft-session", Workspace: a.config.Workspace, Draft: "未发送的草稿\nsecond line"}
	if err := a.SaveSession(s); err != nil {
		t.Fatal(err)
	}
	got, err := a.LoadSession(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Draft != s.Draft || len(got.Messages) != 0 {
		t.Fatalf("draft was lost or injected into history: %+v", got)
	}
	s.Draft = ""
	if err := a.SaveSession(s); err != nil {
		t.Fatal(err)
	}
	got, err = a.LoadSession(s.ID)
	if err != nil || got.Draft != "" {
		t.Fatalf("cleared draft came back: %+v, %v", got, err)
	}
}

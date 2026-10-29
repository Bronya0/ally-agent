package desktop

import (
	"fmt"
	"testing"
)

func TestOperationNotices(t *testing.T) {
	a := &desktop{}
	a.notify("")
	if len(a.notifications) != 0 {
		t.Fatal("empty notice queued")
	}
	a.notifySuccess("设置已保存")
	a.notifySuccess("设置已保存")
	a.notifyError("保存失败")
	if len(a.notifications) != 2 || a.notifications[0].Type != "success" || a.notifications[1].Type != "error" {
		t.Fatalf("wrong outcomes: %+v", a.notifications)
	}
	for i := 0; i < 20; i++ {
		a.queueNotice(string(rune('a'+i)), "info")
	}
	if len(a.notifications) != 8 {
		t.Fatal("notice queue not bounded")
	}
}

func TestPrefsSaveOutcomeSeverity(t *testing.T) {
	a := &desktop{}
	a.prefsSaveResult(nil, []string{"外观设置已保存"})
	if len(a.notifications) != 1 || a.notifications[0].Type != "success" {
		t.Fatalf("save success must not use error icon: %+v", a.notifications)
	}
	a.prefsSaveResult(fmt.Errorf("disk failure"), []string{"外观设置已保存"})
	if len(a.notifications) != 2 || a.notifications[1].Type != "error" {
		t.Fatal("save failure must use error notice")
	}
}

// The rail footer carries no operation status line, so a hint that used to be
// written there has to reach the user as a notice instead of disappearing.
func TestComposerHintsSurfaceAsNotices(t *testing.T) {
	a := &desktop{ready: true}
	if a.send(&conversation{}, "hi") {
		t.Fatal("send without a model must not start a run")
	}
	if len(a.notifications) != 1 || a.notifications[0].Title != "请先在模型页面配置并选择模型" {
		t.Fatalf("model setup hint lost: %+v", a.notifications)
	}
	a = &desktop{ready: true}
	if a.send(&conversation{Busy: true}, "hi") {
		t.Fatal("an injection into a run that has not started must be refused")
	}
	if len(a.notifications) != 1 || a.notifications[0].Type != "error" {
		t.Fatalf("injection hint lost: %+v", a.notifications)
	}
}

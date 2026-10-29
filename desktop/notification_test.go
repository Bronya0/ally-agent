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
	if a.status != "保存失败" {
		t.Fatal("status not updated")
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

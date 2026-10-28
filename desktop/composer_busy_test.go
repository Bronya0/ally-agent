// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	backend "ally-dev/internal/app"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// A running conversation keeps its composer editable: the input takes focus and
// Enter reaches the follow-up path instead of being swallowed by a busy lock.
func TestComposerStaysEditableWhileBusy(t *testing.T) {
	a := demoDesktop()
	tm := a.current()
	tm.Busy = true
	tm.Draft = "追加一个问题"
	tt := ui.NewTester(a.view, 1280, 850)
	tt.Frame()
	if err := tt.Click("消息输入框"); err != nil {
		t.Fatalf("busy composer should stay focusable: %v", err)
	}
	if !tt.Focused("消息输入框") {
		t.Fatal("busy composer should take focus")
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if !strings.Contains(a.status, "本轮正在启动") {
		t.Fatalf("Enter should reach the follow-up path while busy, status=%q", a.status)
	}
	if !tm.Busy {
		t.Fatal("a follow-up must not clear the running state")
	}
}

// A follow-up that cannot be delivered keeps the draft and explains why: no
// second run, no attachments on the injection channel.
func TestFollowUpRefusedWithoutLiveRun(t *testing.T) {
	a := demoDesktop()
	tm := a.current()
	tm.Busy = true
	tm.Draft = "追问"
	if a.send(tm, "追问") {
		t.Fatal("send must not start a second run while busy")
	}
	if tm.Draft != "追问" {
		t.Fatalf("refused follow-up must keep the draft, got %q", tm.Draft)
	}
	if !strings.Contains(a.status, "本轮正在启动") {
		t.Fatalf("status should explain the wait, got %q", a.status)
	}

	tm.RunID = "run-1"
	tm.PendingAttachments = []backend.AttachmentInput{{Name: "a.png"}}
	if a.send(tm, "再追问") {
		t.Fatal("attachments cannot ride the injection channel")
	}
	if !strings.Contains(a.status, "附件") {
		t.Fatalf("status should mention attachments, got %q", a.status)
	}
}

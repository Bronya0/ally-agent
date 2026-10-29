// SPDX-License-Identifier: GPL-3.0-only
package desktop

import "testing"

// Clearing a submitted draft and showing the running state must leave the
// real composer ready for typing, including another submission during a run.
func TestComposerKeepsFocusAfterDraftIsSent(t *testing.T) {
	conv := &conversation{ID: "keep-focus", Draft: "待发送消息"}
	tt := pasteInto(t, conv)
	for _, busy := range []bool{true, true, false} {
		conv.Draft = ""
		conv.Busy = busy
		tt.Frame()
		tt.Type("继续输入")
		if conv.Draft != "继续输入" {
			t.Fatalf("composer lost focus after send: busy=%v draft=%q", busy, conv.Draft)
		}
	}
}

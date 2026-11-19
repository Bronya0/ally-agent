// SPDX-License-Identifier: GPL-3.0-only
package desktop

import "testing"

func TestRemoteEditKeepsOperationLabelAndReportsState(t *testing.T) {
	for _, tc := range []struct{ status, label string }{
		{"running", "运行中"},
		{"success", "已完成"},
		{"error", "失败"},
		{"cancelled", "已取消"},
	} {
		t.Run(tc.status, func(t *testing.T) {
			msg := map[string]any{
				"name": "remote_edit", "toolCallId": "edit", "status": tc.status,
				"args": `{"alias":"dev","path":"/srv/main.go"}`,
			}
			if tc.status == "error" {
				msg["result"] = map[string]any{"error": "E_MULTI_MATCH"}
			}
			tt := compactToolView(&desktop{}, msg)
			for _, label := range []string{"SSH", "(/srv/main.go)", "dev", tc.label} {
				if _, ok := tt.Find(label); !ok {
					t.Fatalf("missing %q: %v", label, tt.Texts())
				}
			}
			if tc.status == "error" && !tt.HasText("E_MULTI_MATCH") {
				t.Fatal("failed edit lost the error detail")
			}
		})
	}
}

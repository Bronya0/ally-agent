package desktop

import "testing"

func TestHistoricalCancellationNormalizesBeforeDisplay(t *testing.T) {
	for _, text := range []string{"已取消", "用户已取消", "cancelled by user"} {
		row := map[string]any{"role": "assistant", "content": text, "error": true}
		if userCancelledMessage(row) {
			t.Fatal("renderer inferred cancellation from text")
		}
		normalizeLoadedCancellation(row)
		if str(row, "status") != "cancelled" || !userCancelledMessage(row) {
			t.Fatalf("historical cancellation not normalized: %+v", row)
		}
	}
	for _, row := range []map[string]any{
		{"role": "user", "content": "已取消"},
		{"role": "assistant", "content": "用户已取消"},
		{"role": "assistant", "status": "error", "content": "已取消", "error": true},
		{"role": "assistant", "content": "请求超时，后续任务已取消", "error": true},
	} {
		normalizeLoadedCancellation(row)
		if userCancelledMessage(row) {
			t.Fatalf("ordinary content marked cancelled: %+v", row)
		}
	}
	tool := map[string]any{"role": "tool_call", "result": map[string]any{"error": "canceled by user"}}
	normalizeLoadedCancellation(tool)
	if got := toolState(tool); got != "cancelled" {
		t.Fatalf("historical tool state: %s", got)
	}
}

func TestLiveCancellationUsesRunStatus(t *testing.T) {
	if got := toolState(map[string]any{"status": "error", "errorCode": "E_CANCELLED"}); got != "cancelled" {
		t.Fatalf("cancellation error code ignored: %s", got)
	}

	for _, status := range []string{"cancelled", "error"} {
		conv := &conversation{Messages: []map[string]any{{"role": "tool_call", "status": "running"}}}
		settleRunningToolMessages(conv, status, "已取消")
		if got := toolState(conv.Messages[0]); got != status {
			t.Fatalf("unfinished tool status lost: %s", got)
		}
		appendRunError(conv, "已取消", status)
		if len(conv.Messages) != 2 || userCancelledMessage(conv.Messages[1]) != (status == "cancelled") {
			t.Fatalf("run status lost: %+v", conv.Messages)
		}
	}
}

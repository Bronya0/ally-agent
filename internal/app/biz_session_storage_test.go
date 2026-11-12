// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestSessionStorageProjectsDiskWithoutChangingLiveHistoryOrReplay(t *testing.T) {
	a := newApiTestApp(t)
	block, err := aes.NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	a.modelHistoryCodec = &modelHistoryCodec{protector: oauthTestProtector{gcm}}
	const sid = "disk-projection"
	history := fastCompactFixture()
	history[1].ToolCalls[0].Function.Arguments = `{"purpose":"inspect","code":"OLD_PRIVATE_ARGUMENT"}`
	history[2].Content = "// read | path=\"old.go\"\n\nOLD_PRIVATE_BODY"
	cfg := ConfigState{Model: "m", APIFormat: apiFormatOpenAIResponses, responsesPromptCacheKey: openAIResponsesPromptCacheKey(sid)}
	key := reasoningReplayKey(cfg, "m")
	for _, index := range []int{1, 21} {
		ordinal := index / 2
		label := "recent"
		if index == 1 {
			label = "OLD_PRIVATE_REPLAY"
		}
		reasoning, _ := json.Marshal(map[string]any{"type": "reasoning", "id": "rs_" + label, "encrypted_content": label, "summary": []any{}})
		call, _ := json.Marshal(map[string]any{"type": "function_call", "id": "fc_" + label, "call_id": history[index].ToolCalls[0].ID, "name": "read", "arguments": history[index].ToolCalls[0].Function.Arguments})
		a.reasoningStash.appendResponses(key, responsesMessageKey(ordinal, history[index]), []json.RawMessage{reasoning, call})
	}
	a.saveHistory(sid, history)
	if !reflect.DeepEqual(a.loadSessionHistoryCopy(sid), history) || len(a.reasoningStash.get(key).responsesNative) != 2 {
		t.Fatal("disk save changed live context/replay")
	}
	disk := sanitizeHistoryMessagesForDisk(history)
	raw, err := json.Marshal(a.modelHistoryForStorage(sid, disk, history))
	if err != nil {
		t.Fatal(err)
	}
	var metadata []modelHistoryMetadata
	if err := json.Unmarshal(raw, &metadata); err != nil || len(metadata[0].ProtectedState) == 0 {
		t.Fatal("protected recovery payload missing")
	}
	plain, err := a.modelHistoryCodec.protector.Open(metadata[0].ProtectedState)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"OLD_PRIVATE_ARGUMENT", "OLD_PRIVATE_BODY", "OLD_PRIVATE_REPLAY"} {
		if strings.Contains(string(raw), secret) || strings.Contains(string(plain), secret) {
			t.Fatalf("old private payload survived: %s", secret)
		}
	}
	b := newApiTestApp(t)
	b.historiesDir, b.modelHistoryCodec = a.historiesDir, a.modelHistoryCodec
	restored := b.loadSessionHistoryCopy(sid)
	if len(restored) != 5 || !isFastCompactHistory(restored[0]) || restored[2].ToolCalls[0].ID != "call-5" {
		t.Fatal("restart lost current task pairing")
	}
	payload := b.reasoningStash.get(key)
	if len(payload.responsesNative) != 1 || len(payload.nativeResponses(0, restored[2])) != 2 {
		t.Fatal("restart resurrected old replay or lost reindexed current replay")
	}
}

func TestSessionSnapshotStorageOnlyTrimsOldToolRows(t *testing.T) {
	a := newApiTestApp(t)
	a.modelHistoryCodec = nil
	a.saveHistory("snapshot", fastCompactFixture())
	snapshot := SessionSnapshot{ID: "snapshot", Draft: "unsent draft", Messages: []map[string]any{
		{"role": "user", "content": "requirements"},
		{"role": "assistant", "content": "complete answer"},
		{"role": "tool_call", "toolCallId": "call-0", "name": "read", "status": "error", "args": "old args", "result": map[string]any{"ok": false, "exitCode": 1, "output": "old output"}},
		{"role": "tool_call", "toolCallId": "call-5", "name": "read", "status": "success", "args": "recent args", "result": map[string]any{"output": "recent output"}},
	}}
	before, _ := json.Marshal(snapshot)
	stored := a.sessionSnapshotForStorage(snapshot)
	after, _ := json.Marshal(snapshot)
	if string(before) != string(after) || stored.Draft != snapshot.Draft || !reflect.DeepEqual(stored.Messages[3], snapshot.Messages[3]) {
		t.Fatal("storage projection changed its source, draft or current tool")
	}
	old := stored.Messages[2]
	if old["args"] != nil || old["fastCompacted"] != true || old["status"] != "error" || old["result"].(map[string]any)["output"] != nil || old["result"].(map[string]any)["ok"] != false {
		t.Fatal("old tool body survived or execution outcome changed")
	}
	if err := a.SaveSession(snapshot); err != nil {
		t.Fatal(err)
	}
	restored, err := a.LoadSession("snapshot")
	if err != nil {
		t.Fatal(err)
	}
	if restored.Messages[2]["args"] != nil || restored.Messages[3]["args"] != "recent args" || restored.Draft != snapshot.Draft {
		t.Fatal("snapshot save did not use the shared projection")
	}
}

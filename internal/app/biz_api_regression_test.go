// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestAPIRegisteredRoutesRequireAuthentication(t *testing.T) {
	a := newIsolatedChatTestApp(t)
	h := apiAuthWithToken(a.apiMux(), func() string { return "secret" })
	for _, route := range APIEndpoints() {
		path := strings.NewReplacer("{id}", "missing", "{name}", "missing", "{index}", "0").Replace(route[1])
		rec, _ := apiRequest(t, h, route[0], path, "", "{}")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s: %d", route[0], path, rec.Code)
		}
	}
	copy := APIEndpoints()
	copy[0][1] = "/changed"
	if APIEndpoints()[0][1] == "/changed" {
		t.Fatal("route metadata was shared")
	}
}

func TestAPIContextSettingsPreserveChoicesAndValidateInput(t *testing.T) {
	a := newIsolatedChatTestApp(t)
	h := a.apiMux()
	cfg := a.config.Clone()
	a.contextSettingsForSession("old", cfg, true)
	for _, body := range []string{"null", "[]", "{}", `{"disabledContext":null}`, `{"disabledTools":null}`, `{"customPrompt":null}`, `{"disabledContext":["unknown"]}`, `{"disabledTools":["unknown"]}`, `{"unrelated":true}`} {
		rec, _ := apiRequest(t, h, "PUT", "/api/v1/context/settings", "", body)
		if rec.Code != 400 {
			t.Fatalf("accepted %s: %d", body, rec.Code)
		}
	}
	rec, payload := apiRequest(t, h, "PUT", "/api/v1/context/settings", "", `{"disabledContext":["skills"],"disabledTools":["grep","read"],"customPrompt":"custom"}`)
	apiRequireOK(t, rec, payload)
	latest, _ := a.getConfig()
	if latest.ContextEnabled("skills") || latest.CustomPrompt != "custom" || len(latest.DisabledTools) != 1 || latest.DisabledTools[0] != "grep" {
		t.Fatal("context settings not applied or core tool disabled")
	}
	old := a.contextSettingsForSession("old", latest, false)
	if !old.ContextEnabled("skills") || len(old.DisabledTools) != 0 {
		t.Fatal("existing session choices changed")
	}
	rec, payload = apiRequest(t, h, "PUT", "/api/v1/context/settings", "", `{"disabledContext":[],"disabledTools":[]}`)
	apiRequireOK(t, rec, payload)
	latest, _ = a.getConfig()
	if !latest.ContextEnabled("skills") || len(latest.DisabledTools) != 0 || latest.CustomPrompt != "custom" {
		t.Fatal("clearing lists changed omitted prompt")
	}
	a.stopping = true
	rec, _ = apiRequest(t, h, "PUT", "/api/v1/context/settings", "", `{"customPrompt":"late"}`)
	if rec.Code != 503 {
		t.Fatal("shutdown accepted config mutation")
	}
}

func TestAPIHeadlessSkillsUseRuntimeWorkspaceAndRefresh(t *testing.T) {
	redirectAppStateDir(t)
	a := newIsolatedChatTestApp(t)
	stored, override := t.TempDir(), t.TempDir()
	a.config.Workspace, a.runtime.workspace = stored, override
	write := func(root, name, body string) {
		t.Helper()
		dir := filepath.Join(root, ".agents", "skills", name)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: test\n---\n"+body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(stored, "stored-only", "stored-body")
	write(override, "runtime-only", "runtime-body")
	h := a.apiMux()
	rec, payload := apiRequest(t, h, "GET", "/api/v1/skills/runtime-only", "", "")
	data := apiRequireOK(t, rec, payload)
	if !strings.Contains(data["content"].(string), "runtime-body") {
		t.Fatal("wrong skill source")
	}
	rec, _ = apiRequest(t, h, "GET", "/api/v1/skills/stored-only", "", "")
	if rec.Code != 404 {
		t.Fatal("stored workspace leaked into headless skill inventory")
	}
	write(override, "new-skill", "new-body")
	rec, payload = apiRequest(t, h, "POST", "/api/v1/skills/refresh", "", "")
	apiRequireOK(t, rec, payload)
	rec, payload = apiRequest(t, h, "GET", "/api/v1/skills/new-skill", "", "")
	apiRequireOK(t, rec, payload)
}

func TestAPIResultAndMessagesUnpackFastCompaction(t *testing.T) {
	a := newIsolatedChatTestApp(t)
	const id = "packed"
	if err := a.SaveSessionIndex(SessionIndexEntry{ID: id, Workspace: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	a.saveHistory(id, []openai.ChatCompletionMessage{
		encodeFastCompactHistory([]fastCompactEntry{{Role: "user", Content: "question"}, {Role: "assistant", Content: "retained answer"}}),
		{Role: "user", Content: "unanswered followup"},
	})
	h := a.apiMux()
	rec, payload := apiRequest(t, h, "GET", "/api/v1/sessions/packed/result", "", "")
	data := apiRequireOK(t, rec, payload)
	if data["result"] != "retained answer" {
		t.Fatalf("answer lost: %v", data)
	}
	rec, payload = apiRequest(t, h, "GET", "/api/v1/sessions/packed/messages", "", "")
	data = apiRequireOK(t, rec, payload)
	if len(data["messages"].([]any)) != 3 {
		t.Fatal("packed speakers were not restored")
	}
}

func TestAPIResultKeepsConcurrentTurnAndHistoryTogether(t *testing.T) {
	a := newIsolatedChatTestApp(t)
	const id = "concurrent-result"
	if err := a.SaveSessionIndex(SessionIndexEntry{ID: id, Workspace: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	update := func(run string) {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.histories[id] = []openai.ChatCompletionMessage{{Role: "assistant", Content: run}}
		a.historyCacheStateLocked(id).revision++
		a.sessionRunOutcomes[id] = sessionRunOutcome{RunID: run, Status: "done"}
	}
	update("a")
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			update("a")
			runtime.Gosched()
			update("b")
			runtime.Gosched()
		}
	}()
	t.Cleanup(func() { cancel(); <-finished })
	h := a.apiMux()
	for i := 0; i < 100; i++ {
		rec, payload := apiRequest(t, h, "GET", "/api/v1/sessions/"+id+"/result", "", "")
		if rec.Code == 409 {
			continue
		}
		data := apiRequireOK(t, rec, payload)
		if data["runId"] != data["result"] {
			encoded, _ := json.Marshal(data)
			t.Fatalf("mixed turns: %s", encoded)
		}
	}
}

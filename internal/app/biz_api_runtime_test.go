// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
)

func runtimeAPIHandler(a *App) http.Handler { return a.apiMux() }

func TestRuntimeAPITaskCreateGet(t *testing.T) {
	a := newIsolatedChatTestApp(t)
	a.runtime.workspace = t.TempDir()
	a.config.Models = []ModelConfig{
		{ProviderName: "first", Model: "one", APIKey: "private-api-key", CustomHeaders: map[string]string{"Authorization": "private-header"}},
		{ProviderName: "second", Model: "two", APIKey: "other-private-key"},
	}
	identity := identityOfModel(a.config.Models[0])
	a.config.LastUsedModel = &identity
	if err := a.startScheduledTaskManager(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.stopScheduledTaskManager)
	h := runtimeAPIHandler(a)
	for _, tc := range []struct {
		body, workspace, model string
	}{
		{`{"name":"default","instruction":"summarize","schedule":"24h"}`, a.runtime.workspace, "one"},
		{`{"name":"selected","instruction":"summarize","schedule":"24h","modelIndex":1}`, a.runtime.workspace, "two"},
		{`{"name":"command","command":"echo task","schedule":"24h","workspace":` + runtimeAPIJSONString(t, t.TempDir()) + `}`, "", ""},
	} {
		rec, payload := apiRequest(t, h, "POST", "/api/v1/tasks", "", tc.body)
		data := apiRequireOK(t, rec, payload)
		var task ScheduledTask
		encoded, _ := json.Marshal(data)
		if err := json.Unmarshal(encoded, &task); err != nil {
			t.Fatal(err)
		}
		if tc.workspace != "" && !samePath(task.Workspace, tc.workspace) {
			t.Fatalf("wrong workspace: %+v", task)
		}
		if tc.workspace == "" {
			var body struct{ Workspace string }
			_ = json.Unmarshal([]byte(tc.body), &body)
			if !samePath(task.Workspace, body.Workspace) {
				t.Fatalf("explicit workspace lost: %+v", task)
			}
		}
		if (tc.model == "" && task.Model != nil) || (tc.model != "" && (task.Model == nil || task.Model.Model != tc.model)) {
			t.Fatalf("wrong model identity: %+v", task.Model)
		}
		got, gotPayload := apiRequest(t, h, "GET", "/api/v1/tasks/"+task.ID, "", "")
		fetched := apiRequireOK(t, got, gotPayload)
		if fetched["id"] != task.ID || fetched["workspace"] != data["workspace"] {
			t.Fatalf("GET lost task: %s", got.Body.String())
		}
		for _, response := range []string{rec.Body.String(), got.Body.String()} {
			if strings.Contains(response, "private-") || strings.Contains(response, "apiKey") || strings.Contains(response, "customHeaders") {
				t.Fatalf("credentials leaked: %s", response)
			}
		}
	}
	for _, body := range []string{
		`{`,
		`{"instruction":"x","schedule":"24h"}`,
		`{"name":"x","schedule":"24h"}`,
		`{"name":"x","instruction":"x","command":"echo x","schedule":"24h"}`,
		`{"name":"x","command":"echo x","schedule":"invalid"}`,
		`{"name":"x","instruction":"x","schedule":"24h","modelIndex":-1}`,
		`{"name":"x","instruction":"x","schedule":"24h","modelIndex":2}`,
	} {
		rec, _ := apiRequest(t, h, "POST", "/api/v1/tasks", "", body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %s: %d %s", body, rec.Code, rec.Body.String())
		}
	}
	if len(a.ListScheduledTasks()) != 3 {
		t.Fatal("invalid request created a task")
	}
	rec, _ := apiRequest(t, h, "GET", "/api/v1/tasks/missing", "", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing task: %d", rec.Code)
	}
}

func TestRuntimeAPITaskRequiresConfiguredModel(t *testing.T) {
	a := newIsolatedChatTestApp(t)
	if err := a.startScheduledTaskManager(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.stopScheduledTaskManager)
	rec, _ := apiRequest(t, a.apiMux(), "POST", "/api/v1/tasks", "", `{"name":"instruction","instruction":"check project","schedule":"24h"}`)
	if rec.Code != http.StatusBadRequest || len(a.ListScheduledTasks()) != 0 {
		t.Fatal("instruction task accepted without a configured model")
	}
}

func runtimeAPIJSONString(t *testing.T, value string) string {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRuntimeAPIUnavailable(t *testing.T) {
	a := newIsolatedChatTestApp(t)
	h := runtimeAPIHandler(a)
	for _, method := range []string{"POST", "GET"} {
		path := "/api/v1/tasks"
		if method == "GET" {
			path += "/missing"
		}
		rec, _ := apiRequest(t, h, method, path, "", `{"name":"x","command":"echo x","schedule":"24h"}`)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("uninitialized scheduler: %d", rec.Code)
		}
	}
	for _, canceled := range []bool{false, true} {
		a.stopping = !canceled
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if canceled {
			a.ctx = ctx
		}
		for _, path := range []string{"tasks", "services", "services/missing/restart"} {
			rec, _ := apiRequest(t, h, "POST", "/api/v1/"+path, "", `{}`)
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("shutdown %s: %d", path, rec.Code)
			}
		}
	}
}

func TestRuntimeAPIStoppedScheduler(t *testing.T) {
	a := newIsolatedChatTestApp(t)
	if err := a.startScheduledTaskManager(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.stopScheduledTaskManager)
	manager, err := a.scheduledTaskManager()
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.stopAndWait(time.Second); err != nil {
		t.Fatal(err)
	}
	rec, _ := apiRequest(t, runtimeAPIHandler(a), "POST", "/api/v1/tasks", "", `{"name":"x","command":"echo x","schedule":"24h"}`)
	if rec.Code != http.StatusServiceUnavailable || len(manager.list()) != 0 {
		t.Fatalf("stopped scheduler accepted creation: %d %s", rec.Code, rec.Body.String())
	}
}

func TestRuntimeAPIUsesApplicationContext(t *testing.T) {
	a := newIsolatedChatTestApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.ctx = context.WithValue(ctx, struct{}{}, "runtime context")
	got, ok := a.apiRuntimeContext(httptest.NewRecorder())
	if !ok || got != a.ctx {
		t.Fatal("runtime mutation lost application context and its policy values")
	}
}

func TestRuntimeAPIServiceLifecycle(t *testing.T) {
	a := newIsolatedChatTestApp(t)
	a.runtime.workspace = t.TempDir()
	a.ctx = context.Background()
	a.config.APIKey = "private-service-key"
	t.Cleanup(a.stopAllServices)
	h := runtimeAPIHandler(a)
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/api/v1/services/missing", "", 404},
		{"POST", "/api/v1/services/missing/restart", "", 404},
		{"POST", "/api/v1/services/missing/restart", `{`, 400},
		{"POST", "/api/v1/services", `{`, 400},
		{"POST", "/api/v1/services", `{"command":"echo x"}`, 400},
		{"POST", "/api/v1/services", `{"description":"test"}`, 400},
	} {
		rec, _ := apiRequest(t, h, tc.method, tc.path, "", tc.body)
		if rec.Code != tc.status {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
	command := "echo runtime-ready && sleep 30"
	if runtime.GOOS == "windows" {
		command = "echo runtime-ready && ping -n 30 127.0.0.1 >NUL"
	}
	body := `{"description":"API lifecycle probe","command":` + runtimeAPIJSONString(t, command) + `}`
	// Cancel before serving to prove the handler uses the app runtime context.
	requestCtx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest("POST", "/api/v1/services", strings.NewReader(body)).WithContext(requestCtx)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	started := apiRequireOK(t, rec, payload)
	id := started["id"].(string)
	if !samePath(started["workspace"].(string), a.runtime.workspace) || strings.Contains(rec.Body.String(), "private-service-key") {
		t.Fatalf("wrong runtime response: %s", rec.Body.String())
	}
	waitRuntimeServiceOutput(t, a, id)
	get, p := apiRequest(t, h, "GET", "/api/v1/services/"+id, "", "")
	if apiRequireOK(t, get, p)["status"] != "running" {
		t.Fatalf("service did not survive reply: %s", get.Body.String())
	}
	// A different current workspace must not replace the service's owner.
	a.runtime.workspace = t.TempDir()
	rec, p = apiRequest(t, h, "POST", "/api/v1/services/"+id+"/restart", "", `{"workspace":`+runtimeAPIJSONString(t, a.runtime.workspace)+`}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("foreign workspace restart: %d %s", rec.Code, rec.Body.String())
	}
	a.stopping = true
	rec, _ = apiRequest(t, h, "POST", "/api/v1/services/"+id+"/restart", "", "")
	if rec.Code != http.StatusServiceUnavailable || a.ListServices().Services[0].Status != "running" {
		t.Fatal("shutdown restart stopped the original service")
	}
	a.stopping = false
	rec, p = apiRequest(t, h, "POST", "/api/v1/services/"+id+"/restart", "", "")
	restarted := apiRequireOK(t, rec, p)
	newID := restarted["id"].(string)
	if newID == id || restarted["workspace"] != started["workspace"] {
		t.Fatalf("restart lost identity/workspace: %s", rec.Body.String())
	}
	waitRuntimeServiceOutput(t, a, newID)
	old, p := apiRequest(t, h, "GET", "/api/v1/services/"+id, "", "")
	if apiRequireOK(t, old, p)["status"] == "running" {
		t.Fatal("restart left original service running")
	}
	// Explicit workspace takes precedence over the headless default on start.
	explicitWorkspace := t.TempDir()
	rec, p = apiRequest(t, h, "POST", "/api/v1/services", "",
		`{"description":"explicit workspace probe","command":"echo runtime-ready","workspace":`+runtimeAPIJSONString(t, explicitWorkspace)+`}`)
	explicit := apiRequireOK(t, rec, p)
	if !samePath(explicit["workspace"].(string), explicitWorkspace) {
		t.Fatalf("explicit service workspace lost: %s", rec.Body.String())
	}
	waitRuntimeServiceOutput(t, a, explicit["id"].(string))
}

func waitRuntimeServiceOutput(t *testing.T, a *App, id string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		output, err := a.GetServiceOutput(id)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(output.Output, "runtime-ready") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("service did not produce startup output")
}

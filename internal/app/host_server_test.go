//go:build server

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHeadlessAPIConcurrentRunsShutdownAndPersistence(t *testing.T) {
	a := newApiTestApp(t)
	t.Setenv("ALLY_DATA_DIR", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{}, 3)
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"reply\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"working\"},\"finish_reason\":null}]}\n\n")
		w.(http.Flusher).Flush()
		entered <- struct{}{}
		<-r.Context().Done()
	}))
	t.Cleanup(model.Close)
	a.config.Models = []ModelConfig{{ProviderName: "test", Model: "default", APIFormat: "openai_chat", BaseURL: model.URL + "/v1", APIKey: "test-key", ReasoningEffort: "off"}}
	allow := true
	a.config.AllowPrivateNetwork = &allow
	a.config.LastUsedModel = &ModelIdentity{ProviderName: "test", Model: "default"}
	sink, _ := newNetworkEventSink("127.0.0.1:0", "secret", 10)
	sink.done = ctx.Done()
	a.events = sink
	if err := a.startRuntime(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.stopRuntime(5 * time.Second) })
	handler := a.headlessHandler("secret", sink)
	rec, payload := apiRequest(t, handler, "GET", "/api/v1/health", "", "")
	if rec.Code != 401 {
		t.Fatal(rec.Code)
	}
	for _, path := range []string{"/wails/runtime", "/"} {
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 404 {
			t.Fatalf("exposed %s", path)
		}
	}
	sessions := make([]string, 0, 2)
	for i := 0; i < 2; i++ {
		rec, payload = apiRequest(t, handler, "POST", "/api/v1/sessions", "secret", "{}")
		id := apiRequireOK(t, rec, payload)["id"].(string)
		sessions = append(sessions, id)
		rec, payload = apiRequest(t, handler, "POST", "/api/v1/sessions/"+id+"/messages", "secret", `{"message":"persist this task","modelIndex":0}`)
		apiRequireOK(t, rec, payload)
	}
	manager, err := a.scheduledTaskManager()
	if err != nil {
		t.Fatal(err)
	}
	taskCfg := a.config
	taskCfg.ProviderName = "test"
	taskCfg.Model = "default"
	scheduled, err := manager.create(taskCfg, ScheduledTaskToolRequest{Action: "create", Name: "shutdown worker", Instruction: "persist scheduled outcome", Schedule: "1m"})
	if err != nil {
		t.Fatal(err)
	}
	manager.trigger(scheduled.ID)
	for i := 0; i < 3; i++ {
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			t.Fatal("sessions did not run concurrently")
		}
	}
	a.mu.Lock()
	count := len(a.runs)
	a.mu.Unlock()
	if count != 2 {
		t.Fatalf("active runs=%d", count)
	}
	rec, payload = apiRequest(t, handler, "GET", "/api/v1/events/poll", "secret", "")
	if rec.Code != 200 || payload["instance"] == "" {
		t.Fatalf("event poll: %s", rec.Body.String())
	}
	if len(sink.ring.buf) == 0 {
		t.Fatal("missing event history")
	}
	if err := a.stopRuntime(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	count = len(a.runs)
	a.mu.Unlock()
	if count != 0 {
		t.Fatal("shutdown returned before checkpoint cleanup")
	}
	manager.mu.Lock()
	task := cloneScheduledTask(manager.tasks[scheduled.ID])
	manager.mu.Unlock()
	if task.Running || task.RunCount != 1 {
		t.Fatalf("scheduled worker not finished: %+v", task)
	}

	for _, id := range sessions {
		// Evict the memory copy to prove the history actually reached disk.
		a.mu.Lock()
		delete(a.histories, id)
		delete(a.historyCacheStates, id)
		a.mu.Unlock()
		rec, payload = apiRequest(t, handler, "GET", "/api/v1/sessions/"+id+"/messages", "secret", "")
		if rec.Code != 503 {
			t.Fatalf("server accepted work after shutdown: %d", rec.Code)
		}
		messages, restore := a.readHistoryFromDisk(id)
		if restore != nil {
			restore()
		}
		raw, _ := json.Marshal(messages)
		if !strings.Contains(string(raw), "persist this task") {
			t.Fatalf("checkpoint not restored: %s", raw)
		}
	}
}

func TestHeadlessEventsShutdownAndReplay(t *testing.T) {
	t.Parallel()
	a := newApiTestApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink, _ := newNetworkEventSink("127.0.0.1:0", "secret", 2)
	sink.done = ctx.Done()
	for i := 0; i < 3; i++ {
		sink.Emit("run:stream", map[string]any{"sessionId": "s", "text": "x"})
	}
	srv := httptest.NewServer(a.headlessHandler("secret", sink))
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL+"/api/v1/events/poll?since=0", nil)
	req.Header.Set("Authorization", "Bearer secret")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var poll struct {
		Resync   bool   `json:"resyncRequired"`
		Instance string `json:"instance"`
	}
	if err = json.NewDecoder(res.Body).Decode(&poll); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if !poll.Resync || poll.Instance == "" {
		t.Fatalf("%+v", poll)
	}
	req, _ = http.NewRequest("GET", srv.URL+"/api/v1/events", nil)
	req.Header.Set("Authorization", "Bearer secret")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	cancel()
	done := make(chan struct{})
	go func() {
		var b [4096]byte
		for {
			if _, err := res.Body.Read(b[:]); err != nil {
				close(done)
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SSE prevented shutdown")
	}
}

func TestHeadlessWorkspaceOverrideDoesNotPersist(t *testing.T) {
	t.Parallel()
	a := newApiTestApp(t)
	stored := a.config.Workspace
	override := t.TempDir()
	a.runtime.workspace = override
	sink, _ := newNetworkEventSink("127.0.0.1:0", "secret", 2)
	handler := a.headlessHandler("secret", sink)
	rec, payload := apiRequest(t, handler, "POST", "/api/v1/models", "secret", `{"model":{"providerName":"test","model":"test","apiFormat":"openai_chat","apiKey":"fake"}}`)
	apiRequireOK(t, rec, payload)
	raw, err := os.ReadFile(a.configPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg ConfigState
	if err = json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Workspace != stored || a.config.Workspace != stored {
		t.Fatal("runtime workspace leaked into persisted configuration")
	}
	rec, payload = apiRequest(t, handler, "POST", "/api/v1/sessions", "secret", "{}")
	apiRequireOK(t, rec, payload)
	id := payload["data"].(map[string]any)["id"].(string)
	snapshot, err := a.LoadSession(id)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Workspace != override {
		t.Fatalf("runtime default ignored: %s", snapshot.Workspace)
	}
	if a.effectiveConfig(ConfigState{}).Workspace != override {
		t.Fatal("runtime overlay ignored")
	}
}

func TestHeadlessAuthenticatedShutdown(t *testing.T) {
	t.Parallel()
	a := newApiTestApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.runtime.requestShutdown = cancel
	sink, _ := newNetworkEventSink("127.0.0.1:0", "secret", 2)
	handler := a.headlessHandler("secret", sink)
	rec, _ := apiRequest(t, handler, "POST", "/api/v1/shutdown", "wrong", "")
	if rec.Code != 401 {
		t.Fatal(rec.Code)
	}
	select {
	case <-ctx.Done():
		t.Fatal("unauthenticated shutdown")
	default:
	}
	rec, payload := apiRequest(t, handler, "POST", "/api/v1/shutdown", "secret", "")
	apiRequireOK(t, rec, payload)
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("shutdown was not requested")
	}
}

func TestMcpFallbackUsesEmptyTemporaryDirectory(t *testing.T) {
	a := newApiTestApp(t)
	dir := t.TempDir()
	for _, key := range []string{"TMP", "TEMP", "TMPDIR"} {
		t.Setenv(key, dir)
	}
	root, err := a.mcpWorkspaceRoot(ConfigState{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.stopRuntime(time.Second) })
	rel, err := filepath.Rel(dir, root)
	if err != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("MCP directory escaped test sandbox: %s", root)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("MCP fallback must be empty")
	}
	if root == appDataDir() {
		t.Fatal("MCP cwd exposes config directory")
	}
}

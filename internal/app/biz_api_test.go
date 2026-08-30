// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// newApiTestApp 构造一个隔离的 App：HOME/USERPROFILE 指向临时目录，
// initialized 置 true 跳过真实用户目录的 ensureInitialized，会话/历史/配置
// 全部落进临时目录。
func newApiTestApp(t *testing.T) *App {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	app := &App{
		initialized:  true,
		configPath:   filepath.Join(home, "config.json"),
		historiesDir: filepath.Join(home, "histories"),
		sessionsDir:  filepath.Join(home, "sessions"),
		config:       defaultConfigState(),
		runs:         map[string]context.CancelFunc{},
		runSessions:  map[string]string{},
		runInputs:    map[string]chan string{},
	}
	if err := os.MkdirAll(app.sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(app.historiesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopApiListener() })
	return app
}

// newApiTestHandler 返回带鉴权中间件的完整 API handler，并把包级单例的
// token 设为测试值（与真实 listener 启动时的行为一致）。
func newApiTestHandler(t *testing.T) (http.Handler, *App, string) {
	t.Helper()
	app := newApiTestApp(t)
	token := "test-token-123"
	apiSvc.mu.Lock()
	apiSvc.token = token
	apiSvc.mu.Unlock()
	t.Cleanup(func() {
		apiSvc.mu.Lock()
		apiSvc.token = ""
		apiSvc.mu.Unlock()
	})
	return app.apiAuthMiddleware(app.apiMux()), app, token
}

func apiRequest(t *testing.T, handler http.Handler, method, path, token string, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	payload := map[string]any{}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("response is not JSON: %v (%s)", err, rec.Body.String())
	}
	return rec, payload
}

func apiRequireOK(t *testing.T, rec *httptest.ResponseRecorder, payload map[string]any) map[string]any {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if ok, _ := payload["ok"].(bool); !ok {
		t.Fatalf("expected ok=true: %s", rec.Body.String())
	}
	data, _ := payload["data"].(map[string]any)
	if data == nil {
		t.Fatalf("expected data object: %s", rec.Body.String())
	}
	return data
}

func TestApiAuthRequiresBearerToken(t *testing.T) {
	handler, _, token := newApiTestHandler(t)

	rec, payload := apiRequest(t, handler, "GET", "/api/v1/health", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: expected 401, got %d", rec.Code)
	}
	if payload["ok"] != false {
		t.Fatalf("expected ok=false error envelope: %s", rec.Body.String())
	}

	rec, _ = apiRequest(t, handler, "GET", "/api/v1/health", "wrong-token", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: expected 401, got %d", rec.Code)
	}

	rec, payload = apiRequest(t, handler, "GET", "/api/v1/health", token, "")
	data := apiRequireOK(t, rec, payload)
	if data["service"] != "ally-api" {
		t.Fatalf("unexpected health payload: %s", rec.Body.String())
	}
}

func TestApiCreateAndListSessions(t *testing.T) {
	handler, app, token := newApiTestHandler(t)

	rec, payload := apiRequest(t, handler, "POST", "/api/v1/sessions", token,
		`{"title":"task A","workspace":"F:/tmp/ws"}`)
	data := apiRequireOK(t, rec, payload)
	sessionID, _ := data["id"].(string)
	if sessionID == "" {
		t.Fatalf("expected session id: %s", rec.Body.String())
	}

	rec, payload = apiRequest(t, handler, "GET", "/api/v1/sessions", token, "")
	data = apiRequireOK(t, rec, payload)
	if int(data["count"].(float64)) != 1 {
		t.Fatalf("expected 1 session, got: %s", rec.Body.String())
	}
	sessions := data["sessions"].([]any)
	first := sessions[0].(map[string]any)
	if first["title"] != "task A" || first["running"] != false {
		t.Fatalf("unexpected session entry: %v", first)
	}

	// 空请求体创建：标题回退工作区目录名。
	rec, payload = apiRequest(t, handler, "POST", "/api/v1/sessions", token, "")
	data = apiRequireOK(t, rec, payload)
	if data["title"] == "" {
		t.Fatalf("expected default title, got: %s", rec.Body.String())
	}

	// workspace 过滤。
	rec, payload = apiRequest(t, handler, "GET", "/api/v1/sessions?workspace=other", token, "")
	data = apiRequireOK(t, rec, payload)
	if int(data["count"].(float64)) != 0 {
		t.Fatalf("expected workspace filter to exclude all, got: %s", rec.Body.String())
	}

	// 状态查询：空闲 + 404。
	rec, payload = apiRequest(t, handler, "GET", "/api/v1/sessions/"+sessionID, token, "")
	data = apiRequireOK(t, rec, payload)
	if data["running"] != false || data["id"] != sessionID {
		t.Fatalf("unexpected status payload: %s", rec.Body.String())
	}
	rec, _ = apiRequest(t, handler, "GET", "/api/v1/sessions/does-not-exist", token, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing session: expected 404, got %d", rec.Code)
	}

	// 任务结果：无历史时 status=done、result 为空。
	rec, payload = apiRequest(t, handler, "GET", "/api/v1/sessions/"+sessionID+"/result", token, "")
	data = apiRequireOK(t, rec, payload)
	if data["status"] != "done" || data["result"] != "" {
		t.Fatalf("unexpected result payload: %s", rec.Body.String())
	}

	_ = app
}

// registerApiRun 手工注册一个活跃 run（模拟 StartChat 的注册状态）。
func registerApiRun(t *testing.T, app *App, runID, sessionID string) chan string {
	t.Helper()
	ch := make(chan string, runInputBufferSize)
	app.mu.Lock()
	app.runs[runID] = func() {}
	app.runSessions[runID] = sessionID
	app.runInputs[runID] = ch
	app.mu.Unlock()
	return ch
}

func TestApiSendMessageQueuesIntoRunningSession(t *testing.T) {
	handler, app, token := newApiTestHandler(t)

	rec, payload := apiRequest(t, handler, "POST", "/api/v1/sessions", token, `{"title":"s"}`)
	sessionID := apiRequireOK(t, rec, payload)["id"].(string)

	ch := registerApiRun(t, app, "run-1", sessionID)

	// 运行中发送 → 排队追加（与界面的 InjectRunMessage 路径一致）。
	rec, payload = apiRequest(t, handler, "POST", "/api/v1/sessions/"+sessionID+"/messages", token,
		`{"message":"继续"}`)
	data := apiRequireOK(t, rec, payload)
	if data["queued"] != true || data["runId"] != "run-1" {
		t.Fatalf("expected queued injection, got: %s", rec.Body.String())
	}
	if len(ch) != 1 {
		t.Fatalf("expected 1 queued message, got %d", len(ch))
	}

	// 空消息在运行中的会话上直接 400。
	rec, _ = apiRequest(t, handler, "POST", "/api/v1/sessions/"+sessionID+"/messages", token, `{"message":"  "}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty message while running: expected 400, got %d", rec.Code)
	}

	// 状态显示运行中 + 排队数。
	rec, payload = apiRequest(t, handler, "GET", "/api/v1/sessions/"+sessionID, token, "")
	data = apiRequireOK(t, rec, payload)
	if data["running"] != true || int(data["queuedMessages"].(float64)) != 1 {
		t.Fatalf("unexpected running status: %s", rec.Body.String())
	}

	// 终止会话（复用 ESC 的 CancelRun 路径）。
	rec, payload = apiRequest(t, handler, "POST", "/api/v1/sessions/"+sessionID+"/cancel", token, "")
	data = apiRequireOK(t, rec, payload)
	if data["wasRunning"] != true {
		t.Fatalf("expected wasRunning=true: %s", rec.Body.String())
	}

	// 空闲会话的 cancel 是幂等 no-op。
	rec, payload = apiRequest(t, handler, "POST", "/api/v1/sessions/other-session/cancel", token, "")
	data = apiRequireOK(t, rec, payload)
	if data["wasRunning"] != false {
		t.Fatalf("expected wasRunning=false: %s", rec.Body.String())
	}
}

func TestApiSendMessageStartChatValidationSurfacesAs400(t *testing.T) {
	handler, app, token := newApiTestHandler(t)

	app.config.Model = "" // 让 StartChat 在启动 goroutine 前校验失败

	rec, payload := apiRequest(t, handler, "POST", "/api/v1/sessions", token, `{"title":"s"}`)
	sessionID := apiRequireOK(t, rec, payload)["id"].(string)

	rec, payload = apiRequest(t, handler, "POST", "/api/v1/sessions/"+sessionID+"/messages", token,
		`{"message":"hello"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid chat config, got %d: %s", rec.Code, rec.Body.String())
	}
	if payload["error"] != "model is required" {
		t.Fatalf("unexpected error message: %s", rec.Body.String())
	}
}

func TestApiModelsRedactApiKeys(t *testing.T) {
	handler, app, token := newApiTestHandler(t)
	app.config.Models = []ModelConfig{{
		ProviderName: "prov",
		Model:        "m-1",
		APIKey:       "sk-secret-value",
		APIKeys:      []string{"sk-secret-value"},
	}}

	rec, payload := apiRequest(t, handler, "GET", "/api/v1/models", token, "")
	data := apiRequireOK(t, rec, payload)
	if strings.Contains(rec.Body.String(), "sk-secret-value") {
		t.Fatalf("api key leaked in models response: %s", rec.Body.String())
	}
	models := data["models"].([]any)
	entry := models[0].(map[string]any)
	if entry["hasApiKey"] != true || int(entry["apiKeyCount"].(float64)) != 1 {
		t.Fatalf("unexpected key flags: %v", entry)
	}
	active := data["active"].(map[string]any)
	if active["model"] != app.config.Model {
		t.Fatalf("unexpected active model: %v", active)
	}
}

func TestApiCreateAndUpdateModelConfig(t *testing.T) {
	handler, app, token := newApiTestHandler(t)

	rec, payload := apiRequest(t, handler, "POST", "/api/v1/models", token,
		`{"model":{"providerName":"p","apiFormat":"chat","baseUrl":"http://x/v1","apiKey":"k1","model":"gpt-x"}}`)
	data := apiRequireOK(t, rec, payload)
	if int(data["index"].(float64)) != 0 {
		t.Fatalf("expected new model at index 0: %s", rec.Body.String())
	}

	// 按下标更新整个条目。
	rec, payload = apiRequest(t, handler, "POST", "/api/v1/models", token,
		`{"index":0,"model":{"providerName":"p","apiFormat":"anthropic","model":"claude-x","apiKey":"k2"}}`)
	apiRequireOK(t, rec, payload)

	app.mu.Lock()
	models := app.config.Models
	app.mu.Unlock()
	if len(models) != 1 || models[0].Model != "claude-x" || models[0].APIFormat != "anthropic_messages" {
		t.Fatalf("unexpected persisted models: %+v", models)
	}
	if _, err := os.Stat(app.configPath); err != nil {
		t.Fatalf("config file not persisted: %v", err)
	}

	rec, _ = apiRequest(t, handler, "POST", "/api/v1/models", token,
		`{"index":9,"model":{"model":"nope"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("out-of-range index: expected 400, got %d", rec.Code)
	}
	rec, _ = apiRequest(t, handler, "POST", "/api/v1/models", token, `{"model":{"model":"  "}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty model name: expected 400, got %d", rec.Code)
	}
}

func TestApiSkillsListAndNotFound(t *testing.T) {
	handler, _, token := newApiTestHandler(t)

	rec, payload := apiRequest(t, handler, "GET", "/api/v1/skills", token, "")
	data := apiRequireOK(t, rec, payload)
	skills, _ := data["skills"].([]any)
	if skills == nil {
		t.Fatalf("expected skills array: %s", rec.Body.String())
	}
	for _, raw := range skills {
		entry := raw.(map[string]any)
		if entry["name"] == "" {
			t.Fatalf("skill entry missing name: %v", entry)
		}
		if _, hasEnabled := entry["enabled"]; !hasEnabled {
			t.Fatalf("skill entry missing enabled flag: %v", entry)
		}
	}

	rec, _ = apiRequest(t, handler, "POST", "/api/v1/skills/no-such-skill-xyz/enable", token, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown skill enable: expected 404, got %d", rec.Code)
	}
	rec, _ = apiRequest(t, handler, "POST", "/api/v1/skills/no-such-skill-xyz/disable", token, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown skill disable: expected 404, got %d", rec.Code)
	}
}

func TestApiMcpEndpoints(t *testing.T) {
	handler, _, token := newApiTestHandler(t)

	rec, payload := apiRequest(t, handler, "GET", "/api/v1/mcp", token, "")
	data := apiRequireOK(t, rec, payload)
	if _, ok := data["servers"].([]any); !ok {
		t.Fatalf("expected servers array: %s", rec.Body.String())
	}
	if _, ok := data["config"].(string); !ok {
		t.Fatalf("expected raw config string: %s", rec.Body.String())
	}

	rec, payload = apiRequest(t, handler, "PUT", "/api/v1/mcp/config", token, `{"config":"not-json"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid mcp json: expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(payload["error"].(string), "invalid MCP JSON") {
		t.Fatalf("unexpected error: %s", rec.Body.String())
	}
}

func TestApiSessionMessagesTodosAndDelete(t *testing.T) {
	handler, app, token := newApiTestHandler(t)

	rec, payload := apiRequest(t, handler, "POST", "/api/v1/sessions", token, `{"title":"s"}`)
	sessionID := apiRequireOK(t, rec, payload)["id"].(string)

	// 完整消息快照：索引会话（无历史文件）返回空消息数组。
	rec, payload = apiRequest(t, handler, "GET", "/api/v1/sessions/"+sessionID+"/messages", token, "")
	data := apiRequireOK(t, rec, payload)
	if _, ok := data["messages"].([]any); !ok {
		t.Fatalf("expected messages array: %s", rec.Body.String())
	}

	// 待办列表：空数组。
	rec, payload = apiRequest(t, handler, "GET", "/api/v1/sessions/"+sessionID+"/todos", token, "")
	data = apiRequireOK(t, rec, payload)
	if _, ok := data["todos"].([]any); !ok {
		t.Fatalf("expected todos array: %s", rec.Body.String())
	}

	// 运行中的会话不允许删除（与 releaseSession 的活跃 run 检查一致）。
	registerApiRun(t, app, "run-1", sessionID)
	rec, payload = apiRequest(t, handler, "DELETE", "/api/v1/sessions/"+sessionID, token, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("deleting running session: expected 409, got %d: %s", rec.Code, rec.Body.String())
	}

	// run 结束后可以删除；列表里消失；重复删除幂等成功。
	app.mu.Lock()
	delete(app.runs, "run-1")
	delete(app.runSessions, "run-1")
	delete(app.runInputs, "run-1")
	app.mu.Unlock()
	rec, payload = apiRequest(t, handler, "DELETE", "/api/v1/sessions/"+sessionID, token, "")
	data = apiRequireOK(t, rec, payload)
	if data["deleted"] != true {
		t.Fatalf("expected deleted=true: %s", rec.Body.String())
	}
	rec, payload = apiRequest(t, handler, "GET", "/api/v1/sessions", token, "")
	data = apiRequireOK(t, rec, payload)
	if int(data["count"].(float64)) != 0 {
		t.Fatalf("expected empty list after delete: %s", rec.Body.String())
	}
	rec, _ = apiRequest(t, handler, "DELETE", "/api/v1/sessions/"+sessionID, token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("repeated delete should stay idempotent, got %d", rec.Code)
	}
}

func TestApiToolsSubagentsWorkspace(t *testing.T) {
	handler, _, token := newApiTestHandler(t)

	rec, payload := apiRequest(t, handler, "GET", "/api/v1/tools", token, "")
	data := apiRequireOK(t, rec, payload)
	tools := data["tools"].([]any)
	if len(tools) == 0 {
		t.Fatalf("expected built-in tools, got none: %s", rec.Body.String())
	}
	first := tools[0].(map[string]any)
	if first["name"] == "" || first["source"] == "" {
		t.Fatalf("unexpected tool summary: %v", first)
	}

	rec, payload = apiRequest(t, handler, "GET", "/api/v1/subagents", token, "")
	data = apiRequireOK(t, rec, payload)
	if _, ok := data["subagents"].([]any); !ok {
		t.Fatalf("expected subagents array: %s", rec.Body.String())
	}

	rec, payload = apiRequest(t, handler, "GET", "/api/v1/workspace", token, "")
	data = apiRequireOK(t, rec, payload)
	if _, ok := data["workspace"].(string); !ok {
		t.Fatalf("expected workspace string: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "apiKey") {
		t.Fatalf("workspace view must not contain key fields: %s", rec.Body.String())
	}
}

func TestApiServiceAndTaskEndpoints(t *testing.T) {
	handler, _, token := newApiTestHandler(t)

	rec, payload := apiRequest(t, handler, "GET", "/api/v1/services", token, "")
	data := apiRequireOK(t, rec, payload)
	if _, ok := data["services"].([]any); !ok {
		t.Fatalf("expected services array: %s", rec.Body.String())
	}

	rec, payload = apiRequest(t, handler, "GET", "/api/v1/services/no-such/output", token, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown service output: expected 404, got %d", rec.Code)
	}

	rec, payload = apiRequest(t, handler, "POST", "/api/v1/services/no-such/stop", token, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown service stop: expected 404, got %d: %s", rec.Code, rec.Body.String())
	}

	rec, payload = apiRequest(t, handler, "GET", "/api/v1/tasks", token, "")
	data = apiRequireOK(t, rec, payload)
	if _, ok := data["tasks"].([]any); !ok {
		t.Fatalf("expected tasks array: %s", rec.Body.String())
	}

	rec, _ = apiRequest(t, handler, "DELETE", "/api/v1/tasks/no-such", token, "")
	if rec.Code == http.StatusNotImplemented {
		t.Fatalf("delete task should not be a placeholder, got %d", rec.Code)
	}
}

func TestApiSettingsPersistenceAndListenerLifecycle(t *testing.T) {
	app := newApiTestApp(t)

	// 空 token 自动生成；端口 0 回落默认。
	state, err := app.SaveApiSettings(ApiSettingsRequest{Port: 0, Token: ""})
	if err != nil {
		t.Fatal(err)
	}
	if state.Port != apiDefaultPort {
		t.Fatalf("expected default port, got %d", state.Port)
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(state.Token) {
		t.Fatalf("expected generated 32-hex token, got %q", state.Token)
	}
	if state.Enabled {
		t.Fatal("service must start disabled")
	}
	reloaded, err := app.GetApiServiceState()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Token != state.Token {
		t.Fatalf("token not persisted: %q vs %q", reloaded.Token, state.Token)
	}

	// 非法端口回落默认；自定义 token 保留。
	state, err = app.SaveApiSettings(ApiSettingsRequest{Port: 80, Token: "custom-tok"})
	if err != nil {
		t.Fatal(err)
	}
	if state.Port != apiDefaultPort || state.Token != "custom-tok" {
		t.Fatalf("unexpected normalized settings: %+v", state)
	}

	// 真实 listener 生命周期：先借 127.0.0.1:0 拿一个空闲高位端口。
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot probe a free port: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()
	if port < 1024 {
		t.Skipf("probed port %d is privileged", port)
	}

	if _, err := app.SaveApiSettings(ApiSettingsRequest{Port: port, Token: "tok-lifecycle"}); err != nil {
		t.Fatal(err)
	}
	state, err = app.SetApiServiceEnabled(true)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Enabled {
		t.Fatalf("expected enabled after start: %+v", state)
	}

	req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/api/v1/health", port), nil)
	req.Header.Set("Authorization", "Bearer tok-lifecycle")
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("real listener request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from live listener, got %d", resp.StatusCode)
	}

	// 错误 token 被拒。
	badReq, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/api/v1/health", port), nil)
	badResp, err := (&http.Client{Timeout: 5 * time.Second}).Do(badReq)
	if err != nil {
		t.Fatal(err)
	}
	badResp.Body.Close()
	if badResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", badResp.StatusCode)
	}

	state, err = app.SetApiServiceEnabled(false)
	if err != nil {
		t.Fatal(err)
	}
	if state.Enabled {
		t.Fatal("expected disabled after stop")
	}
	_, err = (&http.Client{Timeout: 2 * time.Second}).Get(fmt.Sprintf("http://127.0.0.1:%d/api/v1/health", port))
	if err == nil {
		t.Fatal("listener still accepting after stop")
	}
}

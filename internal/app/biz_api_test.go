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

	openai "github.com/sashabaranov/go-openai"
)

// newApiTestApp 构造一个隔离的 App：HOME/USERPROFILE 指向临时目录，
// initialized 置 true 跳过真实用户目录的 ensureInitialized，会话/历史/配置
// 全部落进临时目录。
func newApiTestApp(t *testing.T) *App {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("ALLY_DATA_DIR", filepath.Join(home, ".ally_agent"))
	// Runtime maintenance enumerates the OS temp root, so isolate it too.
	tempRoot := t.TempDir()
	for _, key := range []string{"TMP", "TEMP", "TMPDIR"} {
		t.Setenv(key, tempRoot)
	}
	previousApp := aGlobalApp
	app := NewApp()
	t.Cleanup(func() { aGlobalApp = previousApp })
	app.initialized = true
	app.configPath = filepath.Join(home, "config.json")
	app.historiesDir = filepath.Join(home, "histories")
	app.sessionsDir = filepath.Join(home, "sessions")
	app.config = defaultConfigState()
	app.config.Workspace = t.TempDir()
	if err := os.MkdirAll(app.sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(app.historiesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopApiListenerLocked() })
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

	// 不存在的会话明确返回 404。
	rec, payload = apiRequest(t, handler, "POST", "/api/v1/sessions/other-session/cancel", token, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404: %s", rec.Body.String())
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
	app.config.LastUsedModel = &ModelIdentity{ProviderName: "prov", Model: "m-1"}

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
	// active 是配置里“最近使用模型”的展开结果（会话没有 Tab 快照）。
	active := data["active"].(map[string]any)
	if active["model"] != "m-1" || active["providerName"] != "prov" {
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

// TestApiUpdateInUseModelKeepsIdentityInSync: 整体替换条目会改掉 model id，若改的
// 正是“最近使用”那一条，身份必须跟着新条目走——否则它立即变成悬空身份被 converge
// 清掉，HTTP API 会话与计划任务下次直接跑不了模型。
func TestApiUpdateInUseModelKeepsIdentityInSync(t *testing.T) {
	handler, app, token := newApiTestHandler(t)

	rec, payload := apiRequest(t, handler, "POST", "/api/v1/models", token,
		`{"model":{"providerName":"p","apiFormat":"chat","baseUrl":"http://x/v1","apiKey":"k1","model":"gpt-x"}}`)
	apiRequireOK(t, rec, payload)
	rec, payload = apiRequest(t, handler, "POST", "/api/v1/models/activate", token, `{"index":0}`)
	apiRequireOK(t, rec, payload)
	if app.config.LastUsedModel == nil || app.config.LastUsedModel.Model != "gpt-x" {
		t.Fatalf("activate must record the identity, got %#v", app.config.LastUsedModel)
	}

	// 改的正是当前使用的那一条：身份跟着新 id 走。
	rec, payload = apiRequest(t, handler, "POST", "/api/v1/models", token,
		`{"index":0,"model":{"providerName":"p","apiFormat":"chat","baseUrl":"http://x/v1","apiKey":"k1","model":"gpt-x-2"}}`)
	apiRequireOK(t, rec, payload)
	if app.config.LastUsedModel == nil || app.config.LastUsedModel.Model != "gpt-x-2" {
		t.Fatalf("editing the in-use entry must move the identity with it, got %#v", app.config.LastUsedModel)
	}
	if got := expandLastUsedModel(app.config).Model; got != "gpt-x-2" {
		t.Fatalf("the effective config must use the renamed entry, got %q", got)
	}

	// 改别人那一条不影响身份。
	rec, payload = apiRequest(t, handler, "POST", "/api/v1/models", token,
		`{"model":{"providerName":"p","apiFormat":"chat","baseUrl":"http://y/v1","apiKey":"k2","model":"other"}}`)
	apiRequireOK(t, rec, payload)
	rec, payload = apiRequest(t, handler, "POST", "/api/v1/models", token,
		`{"index":1,"model":{"providerName":"p","apiFormat":"chat","baseUrl":"http://y/v1","apiKey":"k2","model":"other-2"}}`)
	apiRequireOK(t, rec, payload)
	if app.config.LastUsedModel == nil || app.config.LastUsedModel.Model != "gpt-x-2" {
		t.Fatalf("editing another entry must not touch the identity, got %#v", app.config.LastUsedModel)
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

// TestApiMcpConfigRejectsEmptyPayload: a missing or empty config must not be read
// as "clear every server". SaveMcpConfig normalizes an empty string to
// {"mcpServers":{}}, so PUTting back a partial object (or an empty body) used to
// overwrite mcp.json and disconnect every live server with no backup.
func TestApiMcpConfigRejectsEmptyPayload(t *testing.T) {
	handler, _, token := newApiTestHandler(t)
	path := mcpUserConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	original := "{\n  \"mcpServers\": {\n    \"keep\": {\n      \"command\": \"echo\"\n    }\n  }\n}"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, body := range []string{`{}`, `{"config":""}`, `{"config":"   "}`} {
		rec, payload := apiRequest(t, handler, "PUT", "/api/v1/mcp/config", token, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("empty mcp config %s: expected 400, got %d: %s", body, rec.Code, rec.Body.String())
		}
		if !strings.Contains(payload["error"].(string), "config is required") {
			t.Fatalf("empty mcp config %s: unexpected error %s", body, rec.Body.String())
		}
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Fatalf("mcp.json must survive an empty PUT, got %q", got)
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
	if !loadApiSettings().Enabled {
		t.Fatal("enabled choice must persist")
	}
	// Simulate process shutdown without changing the persisted preference.
	stopApiListenerLocked()
	app.restoreApiService()
	state, _ = app.GetApiServiceState()
	if !state.Enabled || state.Token != "tok-lifecycle" || state.Port != port {
		t.Fatalf("startup did not restore listener: %+v", state)
	}
	// A failed port change must preserve the previous listener and settings.
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	if _, err := app.SaveApiSettings(ApiSettingsRequest{Port: occupied.Addr().(*net.TCPAddr).Port, Token: "new-token"}); err == nil {
		t.Fatal("expected occupied-port error")
	}
	if saved := loadApiSettings(); saved.Port != port || saved.Token != "tok-lifecycle" || !saved.Enabled {
		t.Fatalf("failed update replaced settings: %+v", saved)
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
	if loadApiSettings().Enabled {
		t.Fatal("disabled choice must persist")
	}
	app.restoreApiService()
	_, err = (&http.Client{Timeout: 2 * time.Second}).Get(fmt.Sprintf("http://127.0.0.1:%d/api/v1/health", port))
	if err == nil {
		t.Fatal("listener still accepting after stop")
	}
}

func TestApiValidationAndMissingSessions(t *testing.T) {
	handler, _, token := newApiTestHandler(t)
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"POST", "/models/activate", `{}`, 400},
		{"POST", "/models", `{"index":-1,"model":{"model":"x"}}`, 400},
		{"POST", "/sessions", `{} {}`, 400},
		{"POST", "/ai/complete", `{}`, 400},
		{"POST", "/ai/complete", `{"prompt":"hi","modelIndex":99}`, 400},
		{"POST", "/sessions/missing/messages", `{"message":"hi"}`, 404},
		{"POST", "/sessions/missing/compact", `{}`, 404},
		{"GET", "/sessions/missing/todos", ``, 404},
		{"GET", "/sessions/missing/questions", ``, 404},
	} {
		rec, _ := apiRequest(t, handler, tc.method, "/api/v1"+tc.path, token, tc.body)
		if rec.Code != tc.status {
			t.Fatalf("%s %s: got %d, want %d: %s", tc.method, tc.path, rec.Code, tc.status, rec.Body.String())
		}
	}
}

func TestApiBusySessionAndQuestions(t *testing.T) {
	handler, app, token := newApiTestHandler(t)
	rec, payload := apiRequest(t, handler, "POST", "/api/v1/sessions", token, `{}`)
	sid := apiRequireOK(t, rec, payload)["id"].(string)
	registerApiRun(t, app, "run", sid)
	for _, body := range []string{`{"message":"hi","attachments":[{"name":"a"}]}`, `{"message":"hi","modelIndex":0}`} {
		rec, _ := apiRequest(t, handler, "POST", "/api/v1/sessions/"+sid+"/messages", token, body)
		if rec.Code != 409 {
			t.Fatalf("expected 409: %s", rec.Body.String())
		}
	}
	app.pendingAsks["ask"] = &pendingAsk{sessionID: sid, answers: make(chan AskResult, 1), request: AskRequest{
		Questions: []AskQuestion{{ID: "q1", Question: "Proceed?", Options: []AskOption{{ID: "yes", Label: "Yes"}}}},
	}}
	rec, payload = apiRequest(t, handler, "GET", "/api/v1/sessions/"+sid+"/questions", token, "")
	if data := apiRequireOK(t, rec, payload); len(data["questions"].([]any)) != 1 {
		t.Fatal(rec.Body.String())
	}
	rec, payload = apiRequest(t, handler, "POST", "/api/v1/sessions/"+sid+"/answers", token,
		`{"askId":"ask","answers":[{"questionId":"q1","selectedOptionIds":["yes"]}]}`)
	apiRequireOK(t, rec, payload)
	if len(app.pendingAsks["ask"].answers) != 1 {
		t.Fatal("answer not delivered")
	}
	app.finishRun("run")
	app.compactingSessions[sid] = struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app.compactingCancels[sid] = cancel
	for _, method := range []string{"DELETE", "POST"} {
		path := "/api/v1/sessions/" + sid
		if method == "POST" {
			path += "/compact"
		}
		// compactSession validates the model before the busy guard.
		app.config.Model, app.config.APIKey = "test", "test-key"
		rec, _ := apiRequest(t, handler, method, path, token, "")
		if rec.Code != 409 {
			t.Fatalf("compacting session: %s", rec.Body.String())
		}
	}
	rec, payload = apiRequest(t, handler, "POST", "/api/v1/sessions/"+sid+"/cancel", token, "")
	apiRequireOK(t, rec, payload)
	if ctx.Err() == nil {
		t.Fatal("compaction was not cancelled")
	}
	// A rejected start must not alter the compaction's frozen model or roots.
	app.sessionWorkspaces[sid] = "frozen"
	_, err := app.StartChat(ChatRequest{SessionID: sid, Message: "hi"})
	if err != errSessionCompacting || app.sessionWorkspaces[sid] != "frozen" {
		t.Fatalf("compacting context changed: err=%v workspace=%q", err, app.sessionWorkspaces[sid])
	}
}

func TestApiReadsBackendHistoryInsteadOfStaleSnapshot(t *testing.T) {
	handler, app, token := newApiTestHandler(t)
	const sid = "stale-ui"
	if err := app.SaveSession(SessionSnapshot{ID: sid, Workspace: app.config.Workspace,
		Messages: []map[string]any{{"role": "assistant", "content": "old answer"}}}); err != nil {
		t.Fatal(err)
	}
	app.saveHistory(sid, []openai.ChatCompletionMessage{
		{Role: "user", Content: "new question"}, {Role: "assistant", Content: "new answer"},
	})
	rec, payload := apiRequest(t, handler, "GET", "/api/v1/sessions/"+sid+"/result", token, "")
	if data := apiRequireOK(t, rec, payload); data["result"] != "new answer" {
		t.Fatalf("stale result: %s", rec.Body.String())
	}
	rec, payload = apiRequest(t, handler, "GET", "/api/v1/sessions/"+sid+"/messages", token, "")
	if data := apiRequireOK(t, rec, payload); len(data["messages"].([]any)) != 2 {
		t.Fatalf("stale messages: %s", rec.Body.String())
	}
}

func TestApiAIAndSessionRoundTrips(t *testing.T) {
	handler, app, token := newApiTestHandler(t)
	requests := make(chan map[string]any, 10)
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode model request: %v", err)
			w.WriteHeader(400)
			return
		}
		body["auth"] = r.Header.Get("Authorization")
		body["custom"] = r.Header.Get("X-Model")
		requests <- body
		if body["model"] == "broken" {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":{"message":"invalid test model","type":"invalid_request_error"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"reply\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"test reply\"},\"finish_reason\":null}]}\n\n")
		fmt.Fprint(w, "data: {\"id\":\"reply\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":3,\"total_tokens\":15}}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(modelServer.Close)
	app.config.Models = []ModelConfig{
		{ProviderName: "test", Model: "default", APIFormat: "openai_chat", BaseURL: modelServer.URL + "/v1", APIKey: "default-key", CustomHeaders: map[string]string{"X-Model": "default"}},
		{ProviderName: "test", Model: "selected", APIFormat: "openai_chat", BaseURL: modelServer.URL + "/v1", APIKey: "selected-key", ReasoningEffort: "off"},
		{ProviderName: "test", Model: "broken", APIFormat: "openai_chat", BaseURL: modelServer.URL + "/v1", APIKey: "broken-key"},
	}
	identity := identityOfModel(app.config.Models[0])
	app.config.LastUsedModel = &identity
	rec, payload := apiRequest(t, handler, "POST", "/api/v1/ai/complete", token, `{"modelIndex":1,"system":"test system","prompt":"hello"}`)
	data := apiRequireOK(t, rec, payload)
	if data["reply"] != "test reply" || data["totalTokens"] != float64(15) {
		t.Fatalf("unexpected AI response: %s", rec.Body.String())
	}
	request := <-requests
	if request["model"] != "selected" || request["auth"] != "Bearer selected-key" || request["custom"] != "" || request["tools"] != nil {
		t.Fatalf("model isolation failed: %v", request)
	}
	if app.config.LastUsedModel.Model != "default" {
		t.Fatal("per-request selection changed global model")
	}
	workspace := t.TempDir()
	create, _ := json.Marshal(map[string]any{"title": "API session", "workspace": workspace})
	rec, payload = apiRequest(t, handler, "POST", "/api/v1/sessions", token, string(create))
	sid := apiRequireOK(t, rec, payload)["id"].(string)
	for _, tc := range []struct {
		index  int
		status string
	}{{1, "done"}, {2, "error"}} {
		rec, payload = apiRequest(t, handler, "POST", "/api/v1/sessions/"+sid+"/messages", token,
			fmt.Sprintf(`{"message":"hello","modelIndex":%d}`, tc.index))
		runID := apiRequireOK(t, rec, payload)["runId"]
		deadline := time.Now().Add(10 * time.Second)
		for {
			rec, payload = apiRequest(t, handler, "GET", "/api/v1/sessions/"+sid+"/result", token, "")
			data = apiRequireOK(t, rec, payload)
			if data["status"] != "running" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("API session did not finish")
			}
			time.Sleep(10 * time.Millisecond)
		}
		if data["status"] != tc.status || data["runId"] != runID {
			t.Fatalf("unexpected outcome: %s", rec.Body.String())
		}
		if tc.status == "done" && data["result"] != "test reply" {
			t.Fatalf("missing result: %s", rec.Body.String())
		}
		if tc.status == "error" && data["error"] == "" {
			t.Fatalf("model failure was hidden: %s", rec.Body.String())
		}
		<-requests
	}
	app.mu.Lock()
	gotWorkspace := app.sessionWorkspaces[sid]
	app.mu.Unlock()
	if gotWorkspace != workspace || app.config.Workspace == workspace {
		t.Fatalf("session workspace routing failed: %q", gotWorkspace)
	}
	entries, err := app.ListSessions()
	if err != nil || len(entries) != 1 || entries[0].MessageCount == 0 || entries[0].FirstPrompt != "hello" {
		t.Fatalf("API runs did not update the session list: %+v, %v", entries, err)
	}
}

func TestApiPluginHTTPProxy(t *testing.T) {
	handler, app, token := newApiTestHandler(t)
	server := httptest.NewServer(handler)
	defer server.Close()
	source := t.TempDir()
	manifest := `{"id":"http-client","name":"API client","version":"1.0.0","entry":"index.js","menu":[{"key":"api","title":"API"}],"permissions":{"http":["127.0.0.1"]}}`
	for name, content := range map[string]string{"plugin.json": manifest, "index.js": "export function mount() {}"} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.ImportPluginFromDir(source); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		token  string
		status int
	}{{token, 200}, {"wrong", 401}} {
		result, err := app.PluginHTTPRequest(PluginHTTPRequest{
			PluginID: "http-client", URL: server.URL + "/api/v1/health", Headers: map[string]string{"Authorization": "Bearer " + tc.token},
		})
		if err != nil || result.Status != tc.status {
			t.Fatalf("plugin HTTP API request: status=%d error=%v", result.Status, err)
		}
		if payload, ok := result.JSON.(map[string]any); !ok || payload["ok"] != (tc.status == 200) {
			t.Fatalf("missing API envelope: %+v", result)
		}
	}
}

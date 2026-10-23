// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

// biz_api.go 实现对外本地 HTTP API 服务（v1）：让其它工具以固定 token 鉴权、
// 调用 Ally 的会话 / 模型 / MCP / skill 能力。整个模块自包含在本文件：
// 设置持久化（~/.ally_agent/api.json）、listener 生命周期、鉴权中间件、
// 路由与全部 handler；宿主启动时恢复已启用的服务。
//
// 设计边界：
//   - 仅绑定 127.0.0.1，不暴露局域网；所有 /api/v1/* 请求都要求
//     Authorization: Bearer <token>（常量时间比较）。
//   - 端口、token 与启用状态持久化；首次安装默认关闭。
//   - handler 只做薄封装：复用既有绑定方法（StartChat / CancelRun /
//     SaveMcpConfig / SwitchModel ...），不重复实现任何业务规则。
//   - App 关闭时 a.ctx 取消，watcher 协程自动停掉 listener；进程退出本身
//     也会释放端口，双保险。

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

// apiDefaultPort 是高位冷门默认端口：仅绑定回环地址 + token 鉴权双重边界。
const apiDefaultPort = 47821

// apiSettings 是 api.json 的持久化内容；旧文件缺 enabled 时默认关闭。
type apiSettings struct {
	Enabled bool   `json:"enabled"`
	Port    int    `json:"port"`
	Token   string `json:"token"`
}

// ApiServiceState 是设置页与 API 使用方看到的服务状态。
type ApiServiceState struct {
	Enabled bool   `json:"enabled"`
	Port    int    `json:"port"`
	Token   string `json:"token"`
	Addr    string `json:"addr"`
	BaseURL string `json:"baseUrl"`
}

// ApiSettingsRequest 保存设置：port=0 回落默认端口；token 留空则自动生成。
type ApiSettingsRequest struct {
	Port  int    `json:"port"`
	Token string `json:"token"`
}

// apiServer 持有当前 listener。一个进程只有一个 App 实例，包级单例避免
// 改动 App 结构体；handler 在 Start 时捕获具体 *App。
type apiServer struct {
	opMu     sync.Mutex // 串行化设置与 listener 生命周期，避免并发启停泄漏端口。
	mu       sync.Mutex
	server   *http.Server
	listener net.Listener
	token    string
	port     int
	running  bool
	done     chan struct{}
}

var apiSvc = &apiServer{}

// ── 设置持久化 ──

func apiSettingsPath() string {
	return filepath.Join(appDataDir(), "api.json")
}

func loadApiSettings() apiSettings {
	settings := apiSettings{Port: apiDefaultPort}
	data, err := os.ReadFile(apiSettingsPath())
	if err != nil {
		return settings
	}
	var saved apiSettings
	if json.Unmarshal(data, &saved) == nil {
		if saved.Port != 0 {
			settings.Port = saved.Port
		}
		settings.Token = strings.TrimSpace(saved.Token)
		settings.Enabled = saved.Enabled
	}
	return settings
}

func saveApiSettings(settings apiSettings) error {
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	path := apiSettingsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeAtomicBytes(path, data, 0o600)
}

// normalizeApiPort 把端口收敛到合法区间；0/非法值回落默认端口。
func normalizeApiPort(port int) int {
	if port <= 0 {
		return apiDefaultPort
	}
	if port < 1024 || port > 65535 {
		return apiDefaultPort
	}
	return port
}

// generateApiToken 生成 32 位十六进制随机 token。
func generateApiToken() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

// ── 服务生命周期（Wails 绑定） ──

// GetApiServiceState 返回当前服务状态与设置，供设置页渲染。
func (a *App) GetApiServiceState() (ApiServiceState, error) {
	settings := loadApiSettings()
	state := ApiServiceState{
		Port:  normalizeApiPort(settings.Port),
		Token: settings.Token,
	}
	apiSvc.mu.Lock()
	state.Enabled = apiSvc.running
	if apiSvc.running {
		state.Port = apiSvc.port
		state.Token = apiSvc.token
	}
	apiSvc.mu.Unlock()
	state.Addr = fmt.Sprintf("127.0.0.1:%d", state.Port)
	state.BaseURL = fmt.Sprintf("http://127.0.0.1:%d", state.Port)
	return state, nil
}

// SaveApiSettings 持久化端口与 token；token 留空自动生成并落盘。服务若在
// 运行中则用新设置重启 listener，返回重启后的状态。
func (a *App) SaveApiSettings(req ApiSettingsRequest) (ApiServiceState, error) {
	apiSvc.opMu.Lock()
	defer apiSvc.opMu.Unlock()
	settings := loadApiSettings()
	previous := settings
	settings.Port = normalizeApiPort(req.Port)
	settings.Token = strings.TrimSpace(req.Token)
	if settings.Token == "" {
		settings.Token = generateApiToken()
	}
	if err := saveApiSettings(settings); err != nil {
		return ApiServiceState{}, err
	}
	apiSvc.mu.Lock()
	wasRunning := apiSvc.running
	apiSvc.mu.Unlock()
	if wasRunning {
		if err := a.startApiListenerLocked(settings); err != nil {
			if restoreErr := saveApiSettings(previous); restoreErr != nil {
				return ApiServiceState{}, errors.Join(err, restoreErr)
			}
			return ApiServiceState{}, err
		}
	}
	return a.GetApiServiceState()
}

// SetApiServiceEnabled 开启或停止监听，并保存下次启动时的选择。
func (a *App) SetApiServiceEnabled(enabled bool) (ApiServiceState, error) {
	apiSvc.opMu.Lock()
	defer apiSvc.opMu.Unlock()
	settings := loadApiSettings()
	settings.Enabled = enabled
	if !enabled {
		if err := saveApiSettings(settings); err != nil {
			return ApiServiceState{}, err
		}
		stopApiListenerLocked()
		return a.GetApiServiceState()
	}
	if err := a.startApiListenerLocked(settings); err != nil {
		return ApiServiceState{}, err
	}
	// startApiListenerLocked 可能生成 token，必须保存那一份。
	settings.Token = a.apiToken()
	if err := saveApiSettings(settings); err != nil {
		stopApiListenerLocked()
		return ApiServiceState{}, err
	}
	return a.GetApiServiceState()
}

// restoreApiService 由宿主生命周期调用；监听失败只记日志，不阻断桌面启动。
func (a *App) restoreApiService() {
	apiSvc.opMu.Lock()
	defer apiSvc.opMu.Unlock()
	settings := loadApiSettings()
	if settings.Enabled {
		if err := a.startApiListenerLocked(settings); err != nil {
			log.Printf("[api] restore: %v", err)
		}
	}
}

func (a *App) apiToken() string {
	apiSvc.mu.Lock()
	defer apiSvc.mu.Unlock()
	return apiSvc.token
}

func (a *App) startApiListenerLocked(settings apiSettings) error {
	if err := a.ensureInitialized(); err != nil {
		return err
	}
	if strings.TrimSpace(settings.Token) == "" {
		settings.Token = generateApiToken()
		if err := saveApiSettings(settings); err != nil {
			return err
		}
	}
	port := normalizeApiPort(settings.Port)

	apiSvc.mu.Lock()
	if apiSvc.running && apiSvc.port == port {
		apiSvc.token = settings.Token
		apiSvc.mu.Unlock()
		return nil
	}
	apiSvc.mu.Unlock()

	handler := a.apiAuthMiddleware(a.apiMux())
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		// 读与空闲也必须有上限，否则本地进程可以慢速占着连接不放。
		// WriteTimeout 故意不设：HandleCompactSession 是同步长调用，
		// 设了会在压缩大会话时提前奶断响应。
		ReadTimeout: 60 * time.Second,
		IdleTimeout: 120 * time.Second,
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("api listen %s: %w", addr, err)
	}
	// 先取得新端口；失败时旧 listener 与 token 继续可用。
	stopApiListenerLocked()
	done := make(chan struct{})

	apiSvc.mu.Lock()
	apiSvc.server = server
	apiSvc.listener = listener
	apiSvc.token = settings.Token
	apiSvc.port = port
	apiSvc.running = true
	apiSvc.done = done
	apiSvc.mu.Unlock()

	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("[api] serve: %v", err)
			apiSvc.opMu.Lock()
			apiSvc.mu.Lock()
			current := apiSvc.server == server
			apiSvc.mu.Unlock()
			if current {
				stopApiListenerLocked()
			}
			apiSvc.opMu.Unlock()
		}
	}()

	// a.ctx 在应用关闭时取消，watcher 自动停掉 listener；正常退出时进程
	// 结束本身也会释放端口。
	if a.ctx != nil {
		parent := a.ctx
		go func() {
			select {
			case <-done:
				return
			case <-parent.Done():
			}
			apiSvc.opMu.Lock()
			defer apiSvc.opMu.Unlock()
			apiSvc.mu.Lock()
			current := apiSvc.server == server
			apiSvc.mu.Unlock()
			if current {
				stopApiListenerLocked()
			}
		}()
	}
	return nil
}

func stopApiListenerLocked() {
	apiSvc.mu.Lock()
	server := apiSvc.server
	apiSvc.server = nil
	apiSvc.listener = nil
	apiSvc.token = ""
	apiSvc.port = 0
	apiSvc.running = false
	if apiSvc.done != nil {
		close(apiSvc.done)
		apiSvc.done = nil
	}
	apiSvc.mu.Unlock()
	if server == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		_ = server.Close()
	}
}

// ── 鉴权 ──

func (a *App) apiAuthMiddleware(next http.Handler) http.Handler {
	return apiAuthWithToken(next, func() string {
		apiSvc.mu.Lock()
		defer apiSvc.mu.Unlock()
		return apiSvc.token
	})
}

func apiAuthWithToken(next http.Handler, getToken func() string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := getToken()
		if token == "" {
			apiWriteError(w, http.StatusServiceUnavailable, "api token is not initialized")
			return
		}
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(auth, prefix) ||
			subtle.ConstantTimeCompare([]byte(strings.TrimSpace(strings.TrimPrefix(auth, prefix))), []byte(token)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="ally-api"`)
			apiWriteError(w, http.StatusUnauthorized, "invalid or missing bearer token")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
		next.ServeHTTP(w, r)
	})
}

// ── 路由 ──

func (a *App) apiMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", a.apiHandleHealth)
	mux.HandleFunc("POST /api/v1/ai/complete", a.apiHandleAIComplete)

	mux.HandleFunc("GET /api/v1/sessions", a.apiHandleListSessions)
	mux.HandleFunc("POST /api/v1/sessions", a.apiHandleCreateSession)
	mux.HandleFunc("GET /api/v1/sessions/{id}", a.apiHandleSessionStatus)
	mux.HandleFunc("GET /api/v1/sessions/{id}/result", a.apiHandleSessionResult)
	mux.HandleFunc("GET /api/v1/sessions/{id}/messages", a.apiHandleSessionMessages)
	mux.HandleFunc("GET /api/v1/sessions/{id}/todos", a.apiHandleSessionPlan)
	mux.HandleFunc("GET /api/v1/sessions/{id}/questions", a.apiHandleSessionQuestions)
	mux.HandleFunc("POST /api/v1/sessions/{id}/answers", a.apiHandleSessionAnswers)
	mux.HandleFunc("POST /api/v1/sessions/{id}/messages", a.apiHandleSendMessage)
	mux.HandleFunc("POST /api/v1/sessions/{id}/cancel", a.apiHandleCancelSession)
	mux.HandleFunc("POST /api/v1/sessions/{id}/compact", a.apiHandleCompactSession)
	mux.HandleFunc("DELETE /api/v1/sessions/{id}", a.apiHandleDeleteSession)

	mux.HandleFunc("GET /api/v1/models", a.apiHandleListModels)
	mux.HandleFunc("POST /api/v1/models", a.apiHandleSaveModel)
	mux.HandleFunc("POST /api/v1/models/activate", a.apiHandleActivateModel)

	mux.HandleFunc("GET /api/v1/mcp", a.apiHandleGetMcp)
	mux.HandleFunc("PUT /api/v1/mcp/config", a.apiHandlePutMcpConfig)

	mux.HandleFunc("GET /api/v1/skills", a.apiHandleListSkills)
	mux.HandleFunc("GET /api/v1/skills/{name}", a.apiHandleGetSkill)
	mux.HandleFunc("POST /api/v1/skills/{name}/enable", a.apiHandleEnableSkill)
	mux.HandleFunc("POST /api/v1/skills/{name}/disable", a.apiHandleDisableSkill)

	mux.HandleFunc("GET /api/v1/tools", a.apiHandleListTools)
	mux.HandleFunc("GET /api/v1/subagents", a.apiHandleListSubagents)
	mux.HandleFunc("GET /api/v1/workspace", a.apiHandleGetWorkspace)

	mux.HandleFunc("GET /api/v1/services", a.apiHandleListServices)
	mux.HandleFunc("GET /api/v1/services/{id}/output", a.apiHandleServiceOutput)
	mux.HandleFunc("POST /api/v1/services/{id}/stop", a.apiHandleStopService)

	mux.HandleFunc("GET /api/v1/tasks", a.apiHandleListTasks)
	mux.HandleFunc("DELETE /api/v1/tasks/{id}", a.apiHandleDeleteTask)

	return mux
}

// ── JSON 信封 ──

func apiWriteJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func apiWriteOK(w http.ResponseWriter, data any) {
	apiWriteJSON(w, http.StatusOK, map[string]any{"ok": true, "data": data})
}

func apiWriteError(w http.ResponseWriter, status int, message string) {
	apiWriteJSON(w, status, map[string]any{"ok": false, "error": message})
}

func apiDecodeBody(r *http.Request, target any) error {
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("invalid JSON body: expected exactly one JSON value")
	}
	return nil
}

// apiDecodeOptionalBody 解码可选请求体：空请求体（EOF）保持 target 零值，
// 其余解码错误照常上报。
func apiDecodeOptionalBody(r *http.Request, target any) error {
	if err := apiDecodeBody(r, target); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// ── Health ──

func (a *App) apiHandleHealth(w http.ResponseWriter, r *http.Request) {
	apiWriteOK(w, map[string]any{"service": "ally-api", "status": "ok"})
}

// apiModelConfig resolves only configured models; credentials never cross the API.
func (a *App) apiModelConfig(index *int) (ConfigState, error) {
	cfg, err := a.getConfig()
	if err != nil {
		return ConfigState{}, err
	}
	if index == nil {
		return expandLastUsedModel(cfg), nil
	}
	if *index < 0 || *index >= len(cfg.Models) {
		return ConfigState{}, fmt.Errorf("model index out of range: %d", *index)
	}
	stripModelFields(&cfg)
	applyModelEntry(&cfg, cfg.Models[*index])
	return cfg, nil
}

// apiHandleAIComplete is a tool-free, stateless call through the existing model
// adapters. Its cancellation is scoped to this HTTP request, not the games UI.
func (a *App) apiHandleAIComplete(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ModelIndex *int   `json:"modelIndex"`
		System     string `json:"system"`
		Prompt     string `json:"prompt"`
	}
	if err := apiDecodeBody(r, &body); err != nil {
		apiWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(body.Prompt) == "" {
		apiWriteError(w, http.StatusBadRequest, "prompt is required")
		return
	}
	if len(body.Prompt) > 64*1024 || len(body.System) > 64*1024 {
		apiWriteError(w, http.StatusBadRequest, "prompt or system exceeds 64 KiB")
		return
	}
	cfg, err := a.apiModelConfig(body.ModelIndex)
	if err != nil {
		apiWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(cfg.Model) == "" || !hasModelCredential(cfg) {
		apiWriteError(w, http.StatusBadRequest, "model and API key are required")
		return
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = defaultMaxTokensForAPIFormat(cfg.APIFormat)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	if a.ctx != nil {
		stop := context.AfterFunc(a.ctx, cancel)
		defer stop()
	}
	messages := make([]openai.ChatCompletionMessage, 0, 2)
	if body.System != "" {
		messages = append(messages, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleSystem, Content: body.System})
	}
	messages = append(messages, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: body.Prompt})
	reply, usage, err := a.completeModelTextWithUsage(ctx, cfg, cfg.Model, messages, cfg.MaxTokens)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			status = http.StatusGatewayTimeout
		}
		apiWriteError(w, status, err.Error())
		return
	}
	result := map[string]any{"reply": reply, "promptTokens": 0, "completionTokens": 0, "totalTokens": 0}
	if usage != nil {
		result["promptTokens"] = usage.PromptTokens
		result["completionTokens"] = usage.CompletionTokens
		result["totalTokens"] = usage.PromptTokens + usage.CompletionTokens
	}
	apiWriteOK(w, result)
}

// ── 会话 ──

// apiLoadSession keeps metadata from the UI snapshot while reading conversation
// data from backend history. API-only runs do not write UI snapshots.
func (a *App) apiLoadSession(w http.ResponseWriter, sessionID string) (SessionSnapshot, bool) {
	snapshot, err := a.LoadSession(sessionID)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, os.ErrNotExist) {
			status = http.StatusNotFound
		}
		apiWriteError(w, status, err.Error())
		return SessionSnapshot{}, false
	}
	history := a.loadSessionHistoryCopy(sessionID)
	if len(history) > 0 {
		snapshot.Messages = make([]map[string]any, 0, len(history))
		for _, message := range history {
			if message.Role == openai.ChatMessageRoleSystem {
				continue
			}
			content := message.Content
			if content == "" && len(message.MultiContent) > 0 {
				content = textFromMultiContent(message.MultiContent)
			}
			item := map[string]any{"role": message.Role, "content": content, "done": true}
			if message.ToolCallID != "" {
				item["toolCallId"] = message.ToolCallID
			}
			if len(message.ToolCalls) > 0 {
				calls := make([]map[string]any, 0, len(message.ToolCalls))
				for _, call := range message.ToolCalls {
					calls = append(calls, map[string]any{"id": call.ID, "name": call.Function.Name, "arguments": call.Function.Arguments})
				}
				item["toolCalls"] = calls
			}
			snapshot.Messages = append(snapshot.Messages, item)
		}
		snapshot.ContextTokens = estimateTokensFromMessages(history)
		for _, path := range a.historyDiskPaths(sessionID) {
			if info, err := os.Stat(path); err == nil && info.ModTime().UnixMilli() > snapshot.UpdatedAt {
				snapshot.UpdatedAt = info.ModTime().UnixMilli()
			}
		}
	}
	return snapshot, true
}

func (a *App) apiSessionOutcome(sessionID string) (sessionRunOutcome, bool) {
	_, _, _, outcome, compacting := a.apiSessionState(sessionID)
	return outcome, compacting
}

// apiSessionRuntime 反查会话的活跃 run（与 StartChat 的 runSessions 注册对
// 应）。返回 runID、是否运行中、排队中的注入消息数。
func (a *App) apiSessionRuntime(sessionID string) (string, bool, int) {
	runID, running, queued, _, _ := a.apiSessionState(sessionID)
	return runID, running, queued
}

// Read status and outcome under the same lock: a polling client must not see
// an idle run paired with another run's newly registered outcome.
func (a *App) apiSessionState(sessionID string) (string, bool, int, sessionRunOutcome, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	runID := a.activeRunForSession(sessionID)
	queued := 0
	if ch := a.runInputs[runID]; ch != nil {
		queued = len(ch)
	}
	_, compacting := a.compactingSessions[sessionID]
	return runID, runID != "", queued, a.sessionRunOutcomes[sessionID], compacting
}

func (a *App) apiHandleListSessions(w http.ResponseWriter, r *http.Request) {
	entries, err := a.ListSessions()
	if err != nil {
		apiWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	wsFilter := strings.TrimSpace(r.URL.Query().Get("workspace"))
	out := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		if wsFilter != "" && entry.Workspace != wsFilter {
			continue
		}
		_, running, _ := a.apiSessionRuntime(entry.ID)
		out = append(out, map[string]any{
			"id":            entry.ID,
			"title":         entry.Title,
			"firstPrompt":   entry.FirstPrompt,
			"workspace":     entry.Workspace,
			"createdAt":     entry.CreatedAt,
			"updatedAt":     entry.UpdatedAt,
			"messageCount":  entry.MessageCount,
			"contextTokens": entry.ContextTokens,
			"hasSnapshot":   entry.HasSnapshot,
			"running":       running,
		})
	}
	apiWriteOK(w, map[string]any{"sessions": out, "count": len(out)})
}

func (a *App) apiHandleCreateSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title     string `json:"title"`
		Workspace string `json:"workspace"`
	}
	// 空请求体也允许：全部字段走默认值。
	if err := apiDecodeOptionalBody(r, &body); err != nil {
		apiWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(body.Workspace) == "" {
		cfg, err := a.getConfig()
		if err != nil {
			apiWriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		body.Workspace = strings.TrimSpace(a.runtimeConfig(cfg).Workspace)
	}
	if strings.TrimSpace(body.Title) == "" {
		body.Title = workspaceBaseLabel(body.Workspace)
	}
	now := time.Now().UnixMilli()
	entry := SessionIndexEntry{
		ID:        newID(),
		Title:     body.Title,
		Workspace: strings.TrimSpace(body.Workspace),
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := a.SaveSessionIndex(entry); err != nil {
		apiWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	apiWriteOK(w, map[string]any{
		"id":        entry.ID,
		"title":     entry.Title,
		"workspace": entry.Workspace,
		"createdAt": entry.CreatedAt,
	})
}

// workspaceBaseLabel 用工作区目录名做默认会话标题；空工作区给通用名。
func workspaceBaseLabel(workspace string) string {
	workspace = strings.TrimRight(strings.TrimSpace(workspace), `/\`)
	if workspace == "" {
		return "Session"
	}
	if index := strings.LastIndexAny(workspace, `/\`); index >= 0 {
		return workspace[index+1:]
	}
	return workspace
}

func (a *App) apiHandleSessionStatus(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	runID, running, queued, outcome, compacting := a.apiSessionState(sessionID)
	snapshot, ok := a.apiLoadSession(w, sessionID)
	if !ok {
		return
	}
	// API 会话不带 Tab 模型 overlay，下一回合用的就是配置里展开出来的
	// "最近使用模型"（见 ConfigState.LastUsedModel）。
	cfg := a.effectiveConfigSafe()
	if running {
		cfg = a.sessionModelConfigFor(sessionID).apply(cfg)
	}
	apiWriteOK(w, map[string]any{
		"id":             snapshot.ID,
		"title":          snapshot.Title,
		"workspace":      snapshot.Workspace,
		"createdAt":      snapshot.CreatedAt,
		"updatedAt":      snapshot.UpdatedAt,
		"messageCount":   len(snapshot.Messages),
		"contextTokens":  snapshot.ContextTokens,
		"running":        running,
		"runId":          runID,
		"compacting":     compacting,
		"lastRunStatus":  outcome.Status,
		"lastRunError":   outcome.Error,
		"queuedMessages": queued,
		"model":          cfg.Model,
		"modelProvider":  cfg.ProviderName,
	})
}

func (a *App) apiHandleSendMessage(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	var body struct {
		Message     string            `json:"message"`
		Attachments []AttachmentInput `json:"attachments"`
		ModelIndex  *int              `json:"modelIndex"`
	}
	if err := apiDecodeBody(r, &body); err != nil {
		apiWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	message := strings.TrimSpace(body.Message)
	snapshot, ok := a.apiLoadSession(w, sessionID)
	if !ok {
		return
	}
	// 与界面一致的自动判断：运行中 → InjectRunMessage 排队追加（纯文本）；
	// 空闲 → StartChat 新回合（首次发送与继续都是同一条持久化历史路径）。
	if runID, running, _ := a.apiSessionRuntime(sessionID); running {
		if len(body.Attachments) > 0 || body.ModelIndex != nil {
			apiWriteError(w, http.StatusConflict, "running sessions only accept text; attachments and modelIndex require an idle session")
			return
		}
		if message == "" {
			apiWriteError(w, http.StatusBadRequest, "message is required")
			return
		}
		if err := a.InjectRunMessage(runID, message); err != nil {
			apiWriteError(w, http.StatusConflict, err.Error())
			return
		}
		apiWriteOK(w, map[string]any{"runId": runID, "queued": true})
		return
	}
	if message == "" && len(body.Attachments) == 0 {
		apiWriteError(w, http.StatusBadRequest, "message is required")
		return
	}
	cfg, err := a.apiModelConfig(body.ModelIndex)
	if err != nil {
		apiWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg.Workspace = snapshot.Workspace
	cfg.ExtraRoots = cloneStringSlice(snapshot.ExtraRoots)
	runID, err := a.startChatWithConfig(ChatRequest{
		SessionID:        sessionID,
		Message:          message,
		Attachments:      body.Attachments,
		syncSessionIndex: true,
	}, cfg)
	if err != nil {
		if errors.Is(err, errAppStopping) {
			apiWriteError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		if errors.Is(err, errSessionBusy) || errors.Is(err, errSessionCompacting) {
			apiWriteError(w, http.StatusConflict, err.Error())
			return
		}
		apiWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	apiWriteOK(w, map[string]any{"runId": runID, "queued": false})
}

func (a *App) apiHandleCancelSession(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if _, ok := a.apiLoadSession(w, sessionID); !ok {
		return
	}
	_, compacting := a.apiSessionOutcome(sessionID)
	if compacting {
		a.CancelCompaction(sessionID)
		apiWriteOK(w, map[string]any{"wasRunning": true, "compacting": true})
		return
	}
	runID, running, _ := a.apiSessionRuntime(sessionID)
	if !running {
		apiWriteOK(w, map[string]any{"wasRunning": false})
		return
	}
	if err := a.CancelRun(runID); err != nil {
		apiWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	apiWriteOK(w, map[string]any{"wasRunning": true, "runId": runID})
}

// apiHandleSessionResult reads the backend checkpoint, never a stale UI snapshot.
func (a *App) apiHandleSessionResult(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	// Observe idle before loading history: finishRun removes the live run only
	// after its final history/index writes, so done cannot carry an older read.
	runID, running, _, outcome, compacting := a.apiSessionState(sessionID)
	snapshot, ok := a.apiLoadSession(w, sessionID)
	if !ok {
		return
	}
	if outcome.UpdatedAt > snapshot.UpdatedAt {
		snapshot.UpdatedAt = outcome.UpdatedAt
	}
	status := outcome.Status
	if status == "" {
		status = "done"
	}
	if running {
		status = "running"
	} else if compacting {
		status = "compacting"
	} else {
		runID = outcome.RunID
	}
	result := ""
	for i := len(snapshot.Messages) - 1; i >= 0; i-- {
		message := snapshot.Messages[i]
		if strings.TrimSpace(stringValue(message["role"])) != openai.ChatMessageRoleAssistant {
			continue
		}
		if calls, _ := message["toolCalls"].([]map[string]any); len(calls) > 0 {
			continue
		}
		if done, exists := message["done"].(bool); exists && !done {
			continue
		}
		if content := strings.TrimSpace(stringValue(message["content"])); content != "" {
			result = content
			break
		}
	}
	apiWriteOK(w, map[string]any{
		"sessionId": snapshot.ID,
		"status":    status,
		"runId":     runID,
		"error":     outcome.Error,
		"result":    result,
		"updatedAt": snapshot.UpdatedAt,
	})
}

// ── 模型 ──

// apiModelSummary 是模型条目的脱敏视图：绝不回传 apiKey / apiKeys 明文。
func apiModelSummary(index int, model ModelConfig) map[string]any {
	return map[string]any{
		"index":           index,
		"providerName":    model.ProviderName,
		"apiFormat":       model.APIFormat,
		"baseUrl":         model.BaseURL,
		"model":           model.Model,
		"maxTokens":       model.MaxTokens,
		"contextWindow":   model.ContextWindow,
		"reasoningTag":    model.ReasoningTag,
		"reasoningEffort": model.ReasoningEffort,
		"tokenParam":      model.TokenParam,
		// 自定义头可能携带网关凭据：只回传键名，永不回传值。
		"customHeaderNames": customHeaderNames(model.CustomHeaders),
		"hasApiKey":         model.APIKey != "" || len(model.APIKeys) > 0,
		"apiKeyCount":       len(resolveKeyPool(ConfigState{APIKey: model.APIKey, APIKeys: model.APIKeys})),
	}
}

func (a *App) apiHandleListModels(w http.ResponseWriter, r *http.Request) {
	cfg, err := a.getConfig()
	if err != nil {
		apiWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	models := make([]map[string]any, 0, len(cfg.Models))
	for index, model := range cfg.Models {
		models = append(models, apiModelSummary(index, model))
	}
	// active 是"下一回合将使用的模型"：API 会话没有 Tab 模型 overlay，就用配置里
	// 的最近使用模型展开出来的结果（界面上的每个 Tab 各自有自己的模型，与它无关）。
	active := expandLastUsedModel(cfg)
	apiWriteOK(w, map[string]any{
		"active": map[string]any{
			"providerName":      active.ProviderName,
			"apiFormat":         active.APIFormat,
			"baseUrl":           active.BaseURL,
			"model":             active.Model,
			"reasoningTag":      active.ReasoningTag,
			"reasoningEffort":   active.ReasoningEffort,
			"customHeaderNames": customHeaderNames(active.CustomHeaders),
		},
		"models": models,
		"count":  len(models),
	})
}

func (a *App) apiHandleSaveModel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Index *int        `json:"index"` // nil = 新建；提供 = 按下标更新
		Model ModelConfig `json:"model"`
	}
	if err := apiDecodeBody(r, &body); err != nil {
		apiWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	body.Model.Model = strings.TrimSpace(body.Model.Model)
	if body.Model.Model == "" {
		apiWriteError(w, http.StatusBadRequest, "model is required")
		return
	}
	body.Model.ProviderName = strings.TrimSpace(body.Model.ProviderName)
	body.Model.BaseURL = strings.TrimSpace(body.Model.BaseURL)
	body.Model.APIFormat = normalizeAPIFormat(body.Model.APIFormat)
	body.Model.CustomHeaders = normalizeCustomHeaders(body.Model.CustomHeaders)

	index := -1
	invalidIndex := false
	err := a.commitConfig(func(cfg *ConfigState) error {
		models := cfg.Models
		if body.Index != nil {
			index = *body.Index
			if index < 0 || index >= len(models) {
				invalidIndex = true
				return fmt.Errorf("model index out of range: %d", index)
			}
			inUse := cfg.LastUsedModel != nil && modelIndexByIdentity(models, *cfg.LastUsedModel) == index
			models[index] = body.Model
			if inUse {
				identity := identityOfModel(body.Model)
				cfg.LastUsedModel = &identity
			}
		} else {
			index = len(models)
			models = append(models, body.Model)
		}
		cfg.Models = models
		return nil
	})
	if err != nil {
		status := http.StatusInternalServerError
		if invalidIndex {
			status = http.StatusBadRequest
		}
		apiWriteError(w, status, err.Error())
		return
	}

	apiWriteOK(w, map[string]any{"index": index})
}

func (a *App) apiHandleActivateModel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Index *int `json:"index"`
	}
	if err := apiDecodeBody(r, &body); err != nil {
		apiWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Index == nil {
		apiWriteError(w, http.StatusBadRequest, "index is required")
		return
	}
	if err := a.SwitchModel(*body.Index); err != nil {
		apiWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	apiWriteOK(w, map[string]any{"activated": *body.Index})
}

// ── MCP ──

func (a *App) apiHandleGetMcp(w http.ResponseWriter, r *http.Request) {
	servers := a.GetMcpServers()
	if servers == nil {
		servers = []map[string]any{}
	}
	config, err := a.GetMcpConfig()
	if err != nil {
		apiWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	apiWriteOK(w, map[string]any{"servers": servers, "config": config})
}

// apiHandlePutMcpConfig 用原始 mcp.json 文本整体替换并应用（保存 +
// ReconcileMcpServers，只重连增删改的服务器，与设置页行为一致）。
func (a *App) apiHandlePutMcpConfig(w http.ResponseWriter, r *http.Request) {
	// config 是文档（docs/api.md）里的必填字段：空 body 不能被读成“清空所有
	// 服务器”。SaveMcpConfig 会把空串归一成 {"mcpServers":{}}，那会整份覆盖
	// mcp.json 并断开所有 live 连接——客户端拿 GET 的响应原样 PUT、或发一个
	// 缺字段的对象，就会静默丢掉全部配置。
	var body struct {
		Config *string `json:"config"`
	}
	if err := apiDecodeBody(r, &body); err != nil {
		apiWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Config == nil || strings.TrimSpace(*body.Config) == "" {
		apiWriteError(w, http.StatusBadRequest, "config is required: send the complete mcp.json text, an empty body would wipe every configured server")
		return
	}
	if err := a.SaveMcpConfig(*body.Config); err != nil {
		apiWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.ReconcileMcpServers(); err != nil {
		apiWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	apiWriteOK(w, map[string]any{"applied": true})
}

// ── Skills ──

func (a *App) apiHandleListSkills(w http.ResponseWriter, r *http.Request) {
	skills, err := a.ListSkills()
	if err != nil {
		apiWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	enabled := make(map[string]struct{})
	for _, name := range a.GetActiveSkills() {
		enabled[name] = struct{}{}
	}
	out := make([]map[string]any, 0, len(skills))
	for _, skill := range skills {
		_, isEnabled := enabled[skill.Name]
		out = append(out, map[string]any{
			"name":        skill.Name,
			"description": skill.Description,
			"source":      skill.Source,
			"type":        skill.Type,
			"whenToUse":   skill.WhenToUse,
			"enabled":     isEnabled,
		})
	}
	apiWriteOK(w, map[string]any{"skills": out, "count": len(out)})
}

// apiFindSkill 按名（大小写不敏感）查 skill；不存在返回 404。
func (a *App) apiFindSkill(w http.ResponseWriter, name string) (SkillDefinition, bool) {
	skills, err := a.ListSkills()
	if err != nil {
		apiWriteError(w, http.StatusInternalServerError, err.Error())
		return SkillDefinition{}, false
	}
	for _, skill := range skills {
		if strings.EqualFold(skill.Name, name) {
			return skill, true
		}
	}
	apiWriteError(w, http.StatusNotFound, fmt.Sprintf("skill not found: %s", name))
	return SkillDefinition{}, false
}

func (a *App) apiHandleEnableSkill(w http.ResponseWriter, r *http.Request) {
	skill, ok := a.apiFindSkill(w, r.PathValue("name"))
	if !ok {
		return
	}
	if err := a.enableSkill(skill.Name); err != nil {
		apiWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	apiWriteOK(w, map[string]any{"name": skill.Name, "enabled": true})
}

func (a *App) apiHandleDisableSkill(w http.ResponseWriter, r *http.Request) {
	skill, ok := a.apiFindSkill(w, r.PathValue("name"))
	if !ok {
		return
	}
	if err := a.DeactivateSkill(skill.Name); err != nil {
		apiWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	apiWriteOK(w, map[string]any{"name": skill.Name, "enabled": false})
}

func (a *App) apiHandleGetSkill(w http.ResponseWriter, r *http.Request) {
	skill, ok := a.apiFindSkill(w, r.PathValue("name"))
	if !ok {
		return
	}
	content, err := a.GetSkill(skill.Name)
	if err != nil {
		apiWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	apiWriteOK(w, map[string]any{
		"name":        skill.Name,
		"description": skill.Description,
		"source":      skill.Source,
		"content":     content,
	})
}

// ── 会话扩展：完整消息、待办、压缩、删除 ──

// apiHandleSessionMessages 返回会话完整 UI 快照（含工具卡等展示字段），
// 供外部工具取全量上下文。
func (a *App) apiHandleSessionMessages(w http.ResponseWriter, r *http.Request) {
	snapshot, ok := a.apiLoadSession(w, r.PathValue("id"))
	if !ok {
		return
	}
	apiWriteOK(w, map[string]any{
		"id":            snapshot.ID,
		"title":         snapshot.Title,
		"workspace":     snapshot.Workspace,
		"createdAt":     snapshot.CreatedAt,
		"updatedAt":     snapshot.UpdatedAt,
		"contextTokens": snapshot.ContextTokens,
		"messages":      snapshot.Messages,
	})
}

func (a *App) apiHandleSessionPlan(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.apiLoadSession(w, r.PathValue("id")); !ok {
		return
	}
	apiWriteOK(w, map[string]any{"todos": a.GetPlan(r.PathValue("id"))})
}

func (a *App) apiHandleSessionQuestions(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if _, ok := a.apiLoadSession(w, sessionID); !ok {
		return
	}
	questions := make([]map[string]any, 0)
	a.askMu.Lock()
	for id, pending := range a.pendingAsks {
		if pending != nil && pending.sessionID == sessionID {
			questions = append(questions, map[string]any{"askId": id, "request": pending.request})
		}
	}
	a.askMu.Unlock()
	apiWriteOK(w, map[string]any{"questions": questions})
}

func (a *App) apiHandleSessionAnswers(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if _, ok := a.apiLoadSession(w, sessionID); !ok {
		return
	}
	var body AskSubmitRequest
	if err := apiDecodeBody(r, &body); err != nil {
		apiWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.askMu.Lock()
	pending := a.pendingAsks[strings.TrimSpace(body.AskID)]
	belongs := pending != nil && pending.sessionID == sessionID
	a.askMu.Unlock()
	if !belongs {
		apiWriteError(w, http.StatusNotFound, "pending question not found in this session")
		return
	}
	body.SessionID = sessionID
	if err := a.SubmitAskResponse(body); err != nil {
		apiWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	apiWriteOK(w, map[string]any{"submitted": true})
}

// apiHandleCompactSession 压缩会话历史（同步调用：等 LLM 总结完成才返回；
// 客户端断开连接即取消总结调用）。
func (a *App) apiHandleCompactSession(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.apiLoadSession(w, r.PathValue("id")); !ok {
		return
	}
	var body struct {
		Instruction string `json:"instruction"`
	}
	if err := apiDecodeOptionalBody(r, &body); err != nil {
		apiWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	// 本地 HTTP API 没有 Tab 上下文，overlay 传空：模型选择回落会话冻结记录 /
	// 持久化配置（GUI 侧走 App.CompactSession 并带上当前 Tab 的模型）。
	result, err := a.compactSession(r.Context(), r.PathValue("id"), body.Instruction, ConfigState{})
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errSessionRunning) || errors.Is(err, errSessionCompacting) {
			status = http.StatusConflict
		}
		apiWriteError(w, status, err.Error())
		return
	}
	apiWriteOK(w, result)
}

func (a *App) apiHandleDeleteSession(w http.ResponseWriter, r *http.Request) {
	if err := a.DeleteSession(r.PathValue("id")); err != nil {
		if errors.Is(err, errSessionRunning) || errors.Is(err, errSessionCompacting) {
			apiWriteError(w, http.StatusConflict, err.Error())
			return
		}
		apiWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	apiWriteOK(w, map[string]any{"deleted": true})
}

// ── 工具清单 / 子代理 / 工作区 ──

func (a *App) apiHandleListTools(w http.ResponseWriter, r *http.Request) {
	tools := a.ListTools()
	if tools == nil {
		tools = []ToolDefinitionSummary{}
	}
	apiWriteOK(w, map[string]any{"tools": tools, "count": len(tools)})
}

func (a *App) apiHandleListSubagents(w http.ResponseWriter, r *http.Request) {
	runs := a.GetSubagents()
	if runs == nil {
		runs = []*SubagentRun{}
	}
	apiWriteOK(w, map[string]any{"subagents": runs, "count": len(runs)})
}

// apiHandleGetWorkspace 返回当前工作区配置（脱敏：不含任何 key）。
func (a *App) apiHandleGetWorkspace(w http.ResponseWriter, r *http.Request) {
	cfg, err := a.getConfig()
	if err != nil {
		apiWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cfg = a.runtimeConfig(cfg)
	apiWriteOK(w, map[string]any{
		"workspace":  cfg.Workspace,
		"extraRoots": cfg.ExtraRoots,
	})
}

// ── 后台服务 / 计划任务 ──

func (a *App) apiHandleListServices(w http.ResponseWriter, r *http.Request) {
	apiWriteOK(w, a.ListServices())
}

func (a *App) apiHandleServiceOutput(w http.ResponseWriter, r *http.Request) {
	output, err := a.GetServiceOutput(r.PathValue("id"))
	if err != nil {
		if errors.Is(err, errServiceNotFound) {
			apiWriteError(w, http.StatusNotFound, err.Error())
			return
		}
		apiWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	apiWriteOK(w, output)
}

func (a *App) apiHandleStopService(w http.ResponseWriter, r *http.Request) {
	var body struct {
		GraceSeconds int `json:"graceSeconds"`
	}
	if err := apiDecodeOptionalBody(r, &body); err != nil {
		apiWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	info, err := a.StopService(StopServiceRequest{
		ID:           r.PathValue("id"),
		GraceSeconds: body.GraceSeconds,
	})
	if err != nil {
		if errors.Is(err, errServiceNotFound) {
			apiWriteError(w, http.StatusNotFound, err.Error())
			return
		}
		apiWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	apiWriteOK(w, info)
}

func (a *App) apiHandleListTasks(w http.ResponseWriter, r *http.Request) {
	tasks := a.ListScheduledTasks()
	if tasks == nil {
		tasks = []ScheduledTask{}
	}
	apiWriteOK(w, map[string]any{"tasks": tasks, "count": len(tasks)})
}

func (a *App) apiHandleDeleteTask(w http.ResponseWriter, r *http.Request) {
	if err := a.DeleteScheduledTask(r.PathValue("id")); err != nil {
		apiWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	apiWriteOK(w, map[string]any{"deleted": true})
}

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
// 路由与全部 handler，均不改动既有 App 字段或生命周期代码。
//
// 设计边界：
//   - 仅绑定 127.0.0.1，不暴露局域网；所有 /api/v1/* 请求都要求
//     Authorization: Bearer <token>（常量时间比较）。
//   - 服务状态是运行时的：每次启动默认不监听，需在设置页手动开启
//     （端口与 token 持久化，enabled 不持久化）。
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

// apiSettings 是 api.json 的持久化内容。enabled 故意不持久化：服务每次
// 启动默认关闭，用户在设置页手动开启。
type apiSettings struct {
	Port  int    `json:"port"`
	Token string `json:"token"`
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
	mu       sync.Mutex
	server   *http.Server
	listener net.Listener
	token    string
	port     int
	running  bool
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
	return os.WriteFile(path, data, 0o600)
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
	apiSvc.mu.Unlock()
	state.Addr = fmt.Sprintf("127.0.0.1:%d", state.Port)
	state.BaseURL = fmt.Sprintf("http://127.0.0.1:%d", state.Port)
	return state, nil
}

// SaveApiSettings 持久化端口与 token；token 留空自动生成并落盘。服务若在
// 运行中则用新设置重启 listener，返回重启后的状态。
func (a *App) SaveApiSettings(req ApiSettingsRequest) (ApiServiceState, error) {
	settings := loadApiSettings()
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
		if err := a.startApiListener(); err != nil {
			return ApiServiceState{}, err
		}
	}
	return a.GetApiServiceState()
}

// SetApiServiceEnabled 开启或停止监听。开启语义是“按当前设置确保运行”：
// 已在运行则先停再启（让改端口/重新生成 token 立即生效）。
func (a *App) SetApiServiceEnabled(enabled bool) (ApiServiceState, error) {
	if !enabled {
		stopApiListener()
		return a.GetApiServiceState()
	}
	if err := a.startApiListener(); err != nil {
		return ApiServiceState{}, err
	}
	return a.GetApiServiceState()
}

// startApiListener 确保 API 服务按当前设置运行（幂等：先停旧实例）。
func (a *App) startApiListener() error {
	if err := a.ensureInitialized(); err != nil {
		return err
	}
	settings := loadApiSettings()
	if strings.TrimSpace(settings.Token) == "" {
		settings.Token = generateApiToken()
		if err := saveApiSettings(settings); err != nil {
			return err
		}
	}
	port := normalizeApiPort(settings.Port)

	stopApiListener()

	handler := a.apiAuthMiddleware(a.apiMux())
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("api listen %s: %w", addr, err)
	}

	apiSvc.mu.Lock()
	apiSvc.server = server
	apiSvc.listener = listener
	apiSvc.token = settings.Token
	apiSvc.port = port
	apiSvc.running = true
	apiSvc.mu.Unlock()

	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("[api] serve: %v", err)
		}
	}()

	// a.ctx 在应用关闭时取消，watcher 自动停掉 listener；正常退出时进程
	// 结束本身也会释放端口。
	if a.ctx != nil {
		go func() {
			<-a.ctx.Done()
			stopApiListener()
		}()
	}
	return nil
}

// stopApiListener 停止当前 listener（幂等）。带短超时优雅关闭。
func stopApiListener() {
	apiSvc.mu.Lock()
	server := apiSvc.server
	apiSvc.server = nil
	apiSvc.listener = nil
	apiSvc.token = ""
	apiSvc.port = 0
	apiSvc.running = false
	apiSvc.mu.Unlock()
	if server == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}

// ── 鉴权 ──

func (a *App) apiAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiSvc.mu.Lock()
		token := apiSvc.token
		apiSvc.mu.Unlock()
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

	mux.HandleFunc("GET /api/v1/sessions", a.apiHandleListSessions)
	mux.HandleFunc("POST /api/v1/sessions", a.apiHandleCreateSession)
	mux.HandleFunc("GET /api/v1/sessions/{id}", a.apiHandleSessionStatus)
	mux.HandleFunc("GET /api/v1/sessions/{id}/result", a.apiHandleSessionResult)
	mux.HandleFunc("GET /api/v1/sessions/{id}/messages", a.apiHandleSessionMessages)
	mux.HandleFunc("GET /api/v1/sessions/{id}/todos", a.apiHandleSessionTodos)
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

// ── 会话 ──

// apiSessionRuntime 反查会话的活跃 run（与 StartChat 的 runSessions 注册对
// 应）。返回 runID、是否运行中、排队中的注入消息数。
func (a *App) apiSessionRuntime(sessionID string) (string, bool, int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for runID, activeSessionID := range a.runSessions {
		if activeSessionID != sessionID {
			continue
		}
		queued := 0
		if ch := a.runInputs[runID]; ch != nil {
			queued = len(ch)
		}
		return runID, true, queued
	}
	return "", false, 0
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
		body.Workspace = strings.TrimSpace(cfg.Workspace)
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
	snapshot, err := a.LoadSession(sessionID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			apiWriteError(w, http.StatusNotFound, "session not found")
			return
		}
		apiWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_, running, queued := a.apiSessionRuntime(sessionID)
	cfg, _ := a.getConfig()
	apiWriteOK(w, map[string]any{
		"id":             snapshot.ID,
		"title":          snapshot.Title,
		"workspace":      snapshot.Workspace,
		"createdAt":      snapshot.CreatedAt,
		"updatedAt":      snapshot.UpdatedAt,
		"messageCount":   len(snapshot.Messages),
		"contextTokens":  snapshot.ContextTokens,
		"running":        running,
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
	}
	if err := apiDecodeBody(r, &body); err != nil {
		apiWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	message := strings.TrimSpace(body.Message)
	// 与界面一致的自动判断：运行中 → InjectRunMessage 排队追加（纯文本）；
	// 空闲 → StartChat 新回合（首次发送与继续都是同一条持久化历史路径）。
	if runID, running, _ := a.apiSessionRuntime(sessionID); running {
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
	runID, err := a.StartChat(ChatRequest{
		SessionID:   sessionID,
		Message:     message,
		Attachments: body.Attachments,
	})
	if err != nil {
		if errors.Is(err, errSessionBusy) {
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

// apiHandleSessionResult 返回会话最近一条完成的 assistant 消息。会话仍在
// 运行时 status=running，result 是上一回合的结果（可能为空）。
func (a *App) apiHandleSessionResult(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	snapshot, err := a.LoadSession(sessionID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			apiWriteError(w, http.StatusNotFound, "session not found")
			return
		}
		apiWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_, running, _ := a.apiSessionRuntime(sessionID)
	result := ""
	for i := len(snapshot.Messages) - 1; i >= 0; i-- {
		message := snapshot.Messages[i]
		if strings.TrimSpace(stringValue(message["role"])) != openai.ChatMessageRoleAssistant {
			continue
		}
		if content := strings.TrimSpace(stringValue(message["content"])); content != "" {
			result = content
			break
		}
	}
	apiWriteOK(w, map[string]any{
		"sessionId": snapshot.ID,
		"status":    map[bool]string{true: "running", false: "done"}[running],
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
		"hasApiKey":       model.APIKey != "" || len(model.APIKeys) > 0,
		"apiKeyCount":     len(model.APIKeys),
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
	apiWriteOK(w, map[string]any{
		// 会话没有独立的模型状态：每次请求都随全局配置走（与界面一致），
		// active 就是所有会话下一回合将使用的模型。
		"active": map[string]any{
			"providerName":    cfg.ProviderName,
			"apiFormat":       cfg.APIFormat,
			"baseUrl":         cfg.BaseURL,
			"model":           cfg.Model,
			"reasoningTag":    cfg.ReasoningTag,
			"reasoningEffort": cfg.ReasoningEffort,
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

	a.mu.Lock()
	cfg := a.config
	models := make([]ModelConfig, len(cfg.Models))
	copy(models, cfg.Models)
	for i := range models {
		models[i].APIKeys = cloneStringSlice(models[i].APIKeys)
	}
	index := -1
	if body.Index != nil {
		index = *body.Index
	}
	if index >= 0 {
		if index >= len(models) {
			a.mu.Unlock()
			apiWriteError(w, http.StatusBadRequest, fmt.Sprintf("model index out of range: %d", index))
			return
		}
		models[index] = body.Model
	} else {
		index = len(models)
		models = append(models, body.Model)
	}
	cfg.Models = models
	a.mu.Unlock()

	if err := a.saveConfig(cfg); err != nil {
		apiWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	apiWriteOK(w, map[string]any{"index": index})
}

func (a *App) apiHandleActivateModel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Index int `json:"index"`
	}
	if err := apiDecodeBody(r, &body); err != nil {
		apiWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.SwitchModel(body.Index); err != nil {
		apiWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	apiWriteOK(w, map[string]any{"activated": body.Index})
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
	var body struct {
		Config string `json:"config"`
	}
	if err := apiDecodeBody(r, &body); err != nil {
		apiWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.SaveMcpConfig(body.Config); err != nil {
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
	snapshot, err := a.LoadSession(r.PathValue("id"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			apiWriteError(w, http.StatusNotFound, "session not found")
			return
		}
		apiWriteError(w, http.StatusInternalServerError, err.Error())
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

func (a *App) apiHandleSessionTodos(w http.ResponseWriter, r *http.Request) {
	apiWriteOK(w, map[string]any{"todos": a.GetTodos(r.PathValue("id"))})
}

// apiHandleCompactSession 压缩会话历史（同步调用：等 LLM 总结完成才返回；
// 客户端断开连接即取消总结调用）。
func (a *App) apiHandleCompactSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Instruction string `json:"instruction"`
	}
	if err := apiDecodeOptionalBody(r, &body); err != nil {
		apiWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := a.compactSession(r.Context(), r.PathValue("id"), body.Instruction)
	if err != nil {
		apiWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	apiWriteOK(w, result)
}

func (a *App) apiHandleDeleteSession(w http.ResponseWriter, r *http.Request) {
	if err := a.DeleteSession(r.PathValue("id")); err != nil {
		if errors.Is(err, errSessionRunning) {
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

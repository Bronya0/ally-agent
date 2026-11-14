// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// Runtime mutations belong to the application, not the HTTP request lifetime.
func (a *App) apiRuntimeContext(w http.ResponseWriter) (context.Context, bool) {
	a.mu.Lock()
	ctx, stopping := a.ctx, a.stopping
	a.mu.Unlock()
	if stopping || (ctx != nil && ctx.Err() != nil) {
		apiWriteError(w, http.StatusServiceUnavailable, "application is shutting down")
		return nil, false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return ctx, true
}

func (a *App) apiWriteRuntimeError(w http.ResponseWriter, err error) {
	if _, ok := a.apiRuntimeContext(w); !ok {
		return
	}
	status := http.StatusBadRequest
	if errors.Is(err, errServiceNotFound) {
		status = http.StatusNotFound
	}
	apiWriteError(w, status, err.Error())
}

func (a *App) apiHandleCreateTask(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.apiRuntimeContext(w); !ok {
		return
	}
	var body struct {
		Name        string `json:"name"`
		Instruction string `json:"instruction"`
		Command     string `json:"command"`
		Schedule    string `json:"schedule"`
		Workspace   string `json:"workspace"`
		ModelIndex  *int   `json:"modelIndex"`
	}
	if err := apiDecodeBody(r, &body); err != nil {
		apiWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	manager, err := a.scheduledTaskManager()
	if err != nil {
		apiWriteError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	cfg, err := a.apiModelConfig(r, body.ModelIndex)
	if err != nil {
		status := http.StatusBadRequest
		if toolErrorCode(err) == "E_MODEL_IDENTITY" {
			status = http.StatusConflict
		}
		apiWriteError(w, status, err.Error())
		return
	}
	cfg = a.runtimeConfig(cfg)
	if workspace := strings.TrimSpace(body.Workspace); workspace != "" {
		cfg.Workspace = workspace
	}
	if strings.TrimSpace(body.Instruction) != "" && (strings.TrimSpace(cfg.Model) == "" || !hasModelCredential(cfg)) {
		apiWriteError(w, http.StatusBadRequest, "a configured model and credentials are required for instruction tasks")
		return
	}
	task, err := manager.create(cfg, ScheduledTaskToolRequest{
		Action: "create", Name: body.Name, Instruction: body.Instruction,
		Command: body.Command, Schedule: body.Schedule,
	})
	if err != nil {
		manager.mu.Lock()
		stopped := manager.stopped
		manager.mu.Unlock()
		if stopped {
			apiWriteError(w, http.StatusServiceUnavailable, "scheduled task manager is stopped")
			return
		}
		a.apiWriteRuntimeError(w, err)
		return
	}
	apiWriteOK(w, task)
}

func (a *App) apiHandleGetTask(w http.ResponseWriter, r *http.Request) {
	manager, err := a.scheduledTaskManager()
	if err != nil {
		apiWriteError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	for _, task := range manager.list() {
		if task.ID == r.PathValue("id") {
			apiWriteOK(w, task)
			return
		}
	}
	apiWriteError(w, http.StatusNotFound, "scheduled task not found")
}

func (a *App) apiHandleStartService(w http.ResponseWriter, r *http.Request) {
	ctx, ok := a.apiRuntimeContext(w)
	if !ok {
		return
	}
	var body struct {
		StartServiceRequest
		Workspace string `json:"workspace"`
	}
	if err := apiDecodeBody(r, &body); err != nil {
		apiWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg, err := a.getConfig()
	if err != nil {
		apiWriteError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	cfg = a.runtimeConfig(cfg)
	if workspace := strings.TrimSpace(body.Workspace); workspace != "" {
		cfg.Workspace = workspace
	}
	info, err := a.startServiceWithConfig(ctx, cfg, body.StartServiceRequest)
	if err != nil {
		a.apiWriteRuntimeError(w, err)
		return
	}
	apiWriteOK(w, info)
}

func (a *App) apiFindService(w http.ResponseWriter, id string) (ServiceInfo, bool) {
	for _, info := range a.ListServices().Services {
		if info.ID == id {
			return info, true
		}
	}
	apiWriteError(w, http.StatusNotFound, "service not found")
	return ServiceInfo{}, false
}

func (a *App) apiHandleGetService(w http.ResponseWriter, r *http.Request) {
	if info, ok := a.apiFindService(w, r.PathValue("id")); ok {
		apiWriteOK(w, info)
	}
}

func (a *App) apiHandleRestartService(w http.ResponseWriter, r *http.Request) {
	ctx, ok := a.apiRuntimeContext(w)
	if !ok {
		return
	}
	var body struct {
		Workspace string `json:"workspace"`
	}
	if err := apiDecodeOptionalBody(r, &body); err != nil {
		apiWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	original, ok := a.apiFindService(w, r.PathValue("id"))
	if !ok {
		return
	}
	cfg, err := a.getConfig()
	if err != nil {
		apiWriteError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	cfg = a.runtimeConfig(cfg)
	cfg.Workspace = strings.TrimSpace(body.Workspace)
	if cfg.Workspace == "" {
		cfg.Workspace = original.Workspace
	}
	info, err := a.restartServiceWithConfig(ctx, cfg, original.ID)
	if err != nil {
		a.apiWriteRuntimeError(w, err)
		return
	}
	apiWriteOK(w, info)
}

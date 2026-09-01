// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

// Section 1 + Section 8: Service re-exports + Services (was services.go)
// App-owned background-service orchestration that binds internal/tools/service
// rolling buffer to process tree control, the servicesMu guard, and the
// service:update event sink. Re-exports let app/ hold service types without
// referencing the tool package at call sites.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"ally-dev/internal/tools/service"
)

// ───────────────────────── Section 1: Re-exports ─────────────────────────

// rollingBuffer is the alias re-exported from the service tool package so
// app-side managedService can hold a reference without importing the tool
// package at call sites.
type rollingBuffer = service.RollingBuffer

func newRollingBuffer(limit int) *rollingBuffer {
	return service.NewRollingBuffer(limit)
}

func tailString(s string, limit int) string {
	return service.TailString(s, limit)
}

func normalizeServiceCommand(command string) string {
	return service.NormalizeCommand(command)
}

// looksLikeLongRunningService only blocks an explicit whitelist of known
// dev-server commands. Anything else continues to the normal command
// safety checks/timeouts.
func looksLikeLongRunningService(command string) bool {
	return service.LooksLikeLongRunningService(command)
}

func longRunningCommandError(command string) error {
	return service.LongRunningCommandError(command)
}

// ───────────────────────── Section 8: Services ─────────────────────────

const (
	serviceOutputLimit   = service.OutputLimit
	serviceOutputPreview = service.OutputPreview
	maxActiveServices    = service.MaxActive

	// Tool-facing read defaults. The model can request up to
	// maxServiceReadTailBytes of recent output per call; larger reads are
	// clamped so a single service read cannot dominate the model
	// context window.
	defaultServiceReadTailBytes = service.DefaultReadTail
	maxServiceReadTailBytes     = service.MaxReadTail

	// Unified three-platform stop semantics: best-effort graceful
	// termination, a bounded grace wait, then a force kill of the whole
	// process tree with a short confirm wait.
	defaultServiceStopGraceSeconds = 3
	maxServiceStopGraceSeconds     = 30
	serviceForceKillConfirmWait    = 2 * time.Second

	// Finished-service retention. A service that exited or was stopped stays
	// readable (list + read) so the model can diagnose why it died — the
	// crash reason is almost always in the last few KiB of output. Memory is
	// bounded: each record keeps its existing rolling buffer (512 KiB cap)
	// and only the most recent maxFinishedServices records survive.
	maxFinishedServices = 10
)

type managedService struct {
	mu       sync.Mutex
	info     ServiceInfo
	cmd      *exec.Cmd
	output   *rollingBuffer
	cancel   context.CancelFunc
	waitDone chan struct{}
	waitErr  error
}

func (a *App) StartService(req StartServiceRequest) (ServiceInfo, error) {
	return a.startServiceWithConfig(a.effectiveConfig(ConfigState{}), req)
}

func (a *App) StopService(req StopServiceRequest) (ServiceInfo, error) {
	return a.stopService(req)
}

func (a *App) ListServices() ServiceListResult {
	return a.listServices()
}

func (a *App) GetServiceOutput(id string) (ServiceOutputResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ServiceOutputResult{}, errors.New("id is required")
	}
	a.servicesMu.Lock()
	service := a.services[id]
	a.servicesMu.Unlock()
	if service == nil {
		return ServiceOutputResult{}, fmt.Errorf("service not found: %s", id)
	}
	output, total, truncated := service.outputSnapshot()
	return ServiceOutputResult{ID: id, Output: output, Bytes: total, Truncated: truncated}, nil
}

func (a *App) startServiceWithConfig(cfg ConfigState, req StartServiceRequest) (ServiceInfo, error) {
	if strings.TrimSpace(req.Command) == "" {
		return ServiceInfo{}, codedToolError("E_BAD_COMMAND", errors.New("command is required"))
	}
	roots, err := workspaceRoots(cfg)
	if err != nil {
		return ServiceInfo{}, err
	}
	root := roots[0]
	cwd := root
	if strings.TrimSpace(req.Cwd) != "" {
		cwd, err = resolveCommandCwd(roots, req.Cwd)
		if err != nil {
			return ServiceInfo{}, err
		}
	}
	if err := checkCommandSafetyAtCwd(CommandRequest{Command: req.Command, Cwd: req.Cwd}, roots, cwd); err != nil {
		return ServiceInfo{}, err
	}
	a.servicesMu.Lock()
	activeCount := 0
	for _, service := range a.services {
		service.mu.Lock()
		active := service.info.Status == "starting" || service.info.Status == "running"
		service.mu.Unlock()
		if active {
			activeCount++
		}
	}
	a.servicesMu.Unlock()
	if activeCount >= maxActiveServices {
		return ServiceInfo{}, codedToolError("E_SERVICE_LIMIT", fmt.Errorf("active service limit reached (%d)", maxActiveServices))
	}

	id := "svc_" + newID()
	ctx, cancel := context.WithCancel(context.Background())
	shell := commandShell(req.Command, cfg.GitBashPath)
	cmd := exec.CommandContext(ctx, shell.path, shell.args...)
	cmd.Dir = cwd
	cmd.Env = commandEnvironment(cfg)
	job := prepareServiceCommand(cmd)

	buf := newRollingBuffer(serviceOutputLimit)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return ServiceInfo{}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return ServiceInfo{}, err
	}
	if err := cmd.Start(); err != nil {
		discardProcessJob(job)
		cancel()
		return ServiceInfo{}, err
	}
	// Job Object 注册失败时忽略，停止服务时回退到 taskkill /T。
	_ = registerProcessJob(cmd.Process.Pid, job)

	service := &managedService{
		info: ServiceInfo{
			ID:        id,
			Name:      strings.TrimSpace(req.Name),
			Command:   req.Command,
			Cwd:       filepath.ToSlash(cwd),
			PID:       cmd.Process.Pid,
			Status:    "running",
			StartedAt: time.Now().Unix(),
		},
		cmd:      cmd,
		output:   buf,
		cancel:   cancel,
		waitDone: make(chan struct{}),
	}

	a.servicesMu.Lock()
	a.services[id] = service
	a.servicesMu.Unlock()
	a.emitServiceUpdate(service.snapshot())

	var copyWG sync.WaitGroup
	copyWG.Add(2)
	go copyServiceOutput(&copyWG, buf, stdout)
	go copyServiceOutput(&copyWG, buf, stderr)
	go func() {
		waitErr := cmd.Wait()
		copyWG.Wait()
		// 进程退出后关闭 job handle（KILL_ON_JOB_CLOSE 兜底清理残留）。
		unregisterProcessJob(cmd.Process.Pid)
		service.mu.Lock()
		service.waitErr = waitErr
		service.updateOutputInfoLocked()
		if service.info.Status != "stopped" {
			service.info.StoppedAt = time.Now().Unix()
			service.info.Status = "exited"
			if waitErr != nil {
				var exitErr *exec.ExitError
				if errors.As(waitErr, &exitErr) {
					service.info.ExitCode = exitErr.ExitCode()
				} else {
					service.info.Error = waitErr.Error()
				}
			}
		}
		service.mu.Unlock()
		cancel()
		close(service.waitDone)
		a.finalizeService(id, service)
	}()

	// Return immediately. The process runs in the background; the model can
	// poll status and output through service.list / read instead
	// of blocking the agent loop on a readiness wait.
	return service.snapshot(), nil
}

func (a *App) finalizeService(id string, service *managedService) {
	info := service.snapshot()
	// Do NOT drop the record: retain it as finished so the model can still
	// list it and read its final output (the crash reason is almost always
	// in the last few KiB of output). Only the most recent
	// maxFinishedServices finished records survive; evicted ids are removed.
	pruned := a.retainFinishedService(id)
	for _, prunedID := range pruned {
		if old := a.dropService(prunedID); old != nil {
			a.emitServiceUpdate(old.snapshot())
		}
	}
	a.emitServiceUpdate(info)
}

func copyServiceOutput(wg *sync.WaitGroup, dst io.Writer, src io.Reader) {
	defer wg.Done()
	_, _ = io.Copy(dst, src)
}

// retainFinishedService marks the service as finished and keeps it in the
// registry for post-mortem diagnosis. It returns the ids evicted beyond
// maxFinishedServices (oldest first); the caller must remove them from the
// registry. Idempotent: a stop-path call racing the waiter goroutine's
// finalizeService for the same id does not double-queue.
func (a *App) retainFinishedService(id string) []string {
	a.servicesMu.Lock()
	defer a.servicesMu.Unlock()
	for _, existing := range a.finishedQueue {
		if existing == id {
			return nil
		}
	}
	a.finishedQueue = append(a.finishedQueue, id)
	var pruned []string
	for len(a.finishedQueue) > maxFinishedServices {
		pruned = append(pruned, a.finishedQueue[0])
		a.finishedQueue = a.finishedQueue[1:]
	}
	return pruned
}

// dropService removes a service from the registry and the finished queue and
// returns the removed record (nil when absent).
func (a *App) dropService(id string) *managedService {
	a.servicesMu.Lock()
	service := a.services[id]
	if service != nil {
		delete(a.services, id)
	}
	filtered := a.finishedQueue[:0]
	for _, existing := range a.finishedQueue {
		if existing != id {
			filtered = append(filtered, existing)
		}
	}
	a.finishedQueue = filtered
	a.servicesMu.Unlock()

	// Keep cleanup idempotent for installations upgraded from the old
	// completed-service retention behavior.
	dir := a.serviceHistoryDir()
	if dir != "" && service != nil {
		_ = os.Remove(filepath.Join(dir, id+".json"))
		_ = os.Remove(filepath.Join(dir, id+".log"))
	}
	return service
}

func (a *App) stopService(req StopServiceRequest) (ServiceInfo, error) {
	id := strings.TrimSpace(req.ID)
	if id == "" {
		return ServiceInfo{}, errors.New("id is required")
	}
	a.servicesMu.Lock()
	service := a.services[id]
	a.servicesMu.Unlock()
	if service == nil {
		return ServiceInfo{}, fmt.Errorf("service not found: %s", id)
	}

	service.mu.Lock()
	pid := service.info.PID
	alreadyDone := service.info.Status == "stopped" || service.info.Status == "exited"
	service.mu.Unlock()
	if !alreadyDone {
		graceSeconds := req.GraceSeconds
		if graceSeconds <= 0 {
			graceSeconds = defaultServiceStopGraceSeconds
		}
		if graceSeconds > maxServiceStopGraceSeconds {
			graceSeconds = maxServiceStopGraceSeconds
		}
		// 统一停止语义：先尽力优雅终止（POSIX=进程组 SIGTERM，Windows=对
		// 有窗口进程投递 WM_CLOSE），挂 waitDone 等待；超时强杀进程树。
		// 优雅阶段信号投递失败不致命（进程可能已自行退出），照常等待。
		_ = gracefulStopProcessTree(pid)
		forced := false
		var forceErr error
		select {
		case <-service.waitDone:
		case <-time.After(time.Duration(graceSeconds) * time.Second):
			forced = true
			if err := stopProcessTree(pid); err != nil {
				forceErr = err
				service.cancel()
			}
			select {
			case <-service.waitDone:
			case <-time.After(serviceForceKillConfirmWait):
			}
		}
		service.mu.Lock()
		service.info.Status = "stopped"
		service.info.StoppedAt = time.Now().Unix()
		switch {
		case forceErr != nil:
			service.info.Error = fmt.Sprintf("force stop failed after %ds graceful wait: %v", graceSeconds, forceErr)
		case forced:
			service.info.Error = fmt.Sprintf("graceful stop timed out after %ds; process tree force killed", graceSeconds)
		}
		service.updateOutputInfoLocked()
		service.mu.Unlock()
		service.cancel()
	}
	info := service.snapshot()
	// Stopped services stay readable like exited ones: the model may need to
	// inspect the final output after an explicit stop.
	a.retainFinishedService(id)
	a.emitServiceUpdate(info)
	return info, nil
}

func (a *App) listServices() ServiceListResult {
	a.servicesMu.Lock()
	services := make([]*managedService, 0, len(a.services))
	for _, service := range a.services {
		services = append(services, service)
	}
	a.servicesMu.Unlock()
	infos := make([]ServiceInfo, 0, len(services))
	for _, service := range services {
		infos = append(infos, service.snapshot())
	}
	sort.Slice(infos, func(i, j int) bool {
		iActive := infos[i].Status == "starting" || infos[i].Status == "running"
		jActive := infos[j].Status == "starting" || infos[j].Status == "running"
		if iActive != jActive {
			return iActive
		}
		return infos[i].StartedAt > infos[j].StartedAt
	})
	return ServiceListResult{Services: infos}
}

// ServiceListToolResult is the model-facing list payload. It intentionally
// omits outputTail so listing 8 services cannot dominate the model context;
// the model must call service with action=read on a specific id to inspect
// output.
type ServiceListToolResult struct {
	ActiveCount int              `json:"activeCount"`
	MaxActive   int              `json:"maxActive"`
	Services    []ServiceSummary `json:"services"`
}

// ServiceSummary is the per-service metadata returned by the list action. It
// excludes the output tail; only byte accounting is included so the model can
// decide whether a read is worthwhile.
type ServiceSummary struct {
	ID              string `json:"id"`
	Name            string `json:"name,omitempty"`
	Command         string `json:"command"`
	Cwd             string `json:"cwd,omitempty"`
	PID             int    `json:"pid,omitempty"`
	Status          string `json:"status"`
	StartedAt       int64  `json:"startedAt"`
	StoppedAt       int64  `json:"stoppedAt,omitempty"`
	ExitCode        int    `json:"exitCode,omitempty"`
	OutputBytes     int64  `json:"outputBytes,omitempty"`
	OutputTruncated bool   `json:"outputTruncated,omitempty"`
	Error           string `json:"error,omitempty"`
}

func (a *App) listServicesForTool() ServiceListToolResult {
	listed := a.listServices()
	summaries := make([]ServiceSummary, 0, len(listed.Services))
	activeCount := 0
	for _, info := range listed.Services {
		if info.Status == "starting" || info.Status == "running" {
			activeCount++
		}
		summaries = append(summaries, ServiceSummary{
			ID:              info.ID,
			Name:            info.Name,
			Command:         info.Command,
			Cwd:             info.Cwd,
			PID:             info.PID,
			Status:          info.Status,
			StartedAt:       info.StartedAt,
			StoppedAt:       info.StoppedAt,
			ExitCode:        info.ExitCode,
			OutputBytes:     info.OutputBytes,
			OutputTruncated: info.OutputTruncated,
			Error:           info.Error,
		})
	}
	return ServiceListToolResult{
		ActiveCount: activeCount,
		MaxActive:   maxActiveServices,
		Services:    summaries,
	}
}

// ServiceReadResult is the model-facing read payload. Output is bounded by
// maxServiceReadTailBytes so a single read cannot overload the model context.
type ServiceReadResult struct {
	ID            string `json:"id"`
	Output        string `json:"output"`
	ReturnedBytes int    `json:"returnedBytes"`
	BufferBytes   int64  `json:"bufferBytes"`
	TotalBytes    int64  `json:"totalBytes"`
	Truncated     bool   `json:"truncated"`
	Status        string `json:"status"`
	FromByte      int    `json:"fromByte"`
}

func (a *App) readServiceOutput(req ServiceReadRequest) (ServiceReadResult, error) {
	id := strings.TrimSpace(req.ID)
	if id == "" {
		return ServiceReadResult{}, codedToolError("E_BAD_SERVICE_ID", errors.New("id is required"))
	}
	a.servicesMu.Lock()
	service := a.services[id]
	a.servicesMu.Unlock()
	if service == nil {
		return ServiceReadResult{}, codedToolError("E_SERVICE_NOT_FOUND", fmt.Errorf("service not found: %s", id))
	}

	tailBytes := req.TailBytes
	if tailBytes <= 0 {
		tailBytes = defaultServiceReadTailBytes
	}
	if tailBytes > maxServiceReadTailBytes {
		tailBytes = maxServiceReadTailBytes
	}

	service.mu.Lock()
	status := service.info.Status
	service.mu.Unlock()
	output, total, truncated := service.outputSnapshot()
	// Command output is decoded at the consumption boundary (the same
	// contract as the command tool): native Windows tools and locale-encoded
	// runtimes emit GBK through Git Bash pipes on stock codepage-936 zh-CN
	// systems, so repair non-UTF-8 tails as GB18030 instead of returning
	// mojibake to the model. The whole buffer is a single encoding in
	// practice (rolling buffer keeps only the recent tail).
	output = decodeConsoleOutput(output)
	// The rolling buffer drops early bits once full. fromByte reflects where
	// the returned slice starts within the *current* buffer; the model can
	// infer how much older output was already discarded by comparing
	// totalBytes (process-lifetime output) and bufferBytes (current retained).
	bufferBytes := int64(len(output))
	fromByte := 0
	if bufferBytes > int64(tailBytes) {
		fromByte = int(bufferBytes) - tailBytes
	}
	returned := output
	if fromByte > 0 {
		returned = output[fromByte:]
	}
	return ServiceReadResult{
		ID:            id,
		Output:        returned,
		ReturnedBytes: len(returned),
		BufferBytes:   bufferBytes,
		TotalBytes:    total,
		Truncated:     truncated,
		Status:        status,
		FromByte:      fromByte,
	}, nil
}

// stopAllServices 并发停止所有服务：每个服务最坏需要 grace+confirm 约 5s，
// 串行会在应用退出时把这个时长乘以服务数。只停活跃服务；已结束记录留在内存里
// 随进程退出一起消亡（它们无进程可停）。
func (a *App) stopAllServices() {
	active := make([]string, 0, len(a.services))
	a.servicesMu.Lock()
	for id, service := range a.services {
		service.mu.Lock()
		status := service.info.Status
		service.mu.Unlock()
		if status == "starting" || status == "running" {
			active = append(active, id)
		}
	}
	a.servicesMu.Unlock()
	var wg sync.WaitGroup
	for _, id := range active {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, _ = a.stopService(StopServiceRequest{ID: id})
		}(id)
	}
	wg.Wait()
}

func (s *managedService) snapshot() ServiceInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	info := s.info
	s.updateOutputInfoLocked()
	info = s.info
	return info
}

func (s *managedService) updateOutputInfoLocked() {
	if s.output == nil {
		return
	}
	output, total, truncated := s.output.Snapshot()
	s.info.OutputTail = tailString(output, serviceOutputPreview)
	s.info.OutputBytes = total
	s.info.OutputTruncated = truncated
}

func (s *managedService) outputSnapshot() (string, int64, bool) {
	if s == nil || s.output == nil {
		return "", 0, false
	}
	return s.output.Snapshot()
}

func (a *App) emitServiceUpdate(info ServiceInfo) {
	if a.ctx != nil && a.ctx.Err() == nil {
		a.emit("service:update", map[string]any{"service": info})
	}
}

func (a *App) serviceHistoryDir() string {
	a.mu.Lock()
	configPath := a.configPath
	a.mu.Unlock()
	if strings.TrimSpace(configPath) == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(configPath), "service_history")
}

func (a *App) loadServiceHistory() error {
	dir := a.serviceHistoryDir()
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	// Completed services are no longer retained. Remove records written by
	// older versions so they do not reappear after upgrading.
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		if strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".log") {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
	return nil
}

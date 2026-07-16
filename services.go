package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	serviceOutputLimit       = 512 * 1024
	serviceOutputPreview     = 8 * 1024
	maxCompletedServices     = 20
	defaultServiceReadyLimit = 15
	maxServiceReadyLimit     = 120
	maxActiveServices        = 8
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
	root, err := workspaceRoot(cfg)
	if err != nil {
		return ServiceInfo{}, err
	}
	if err := checkCommandSafety(CommandRequest{Command: req.Command, Cwd: req.Cwd}, root); err != nil {
		return ServiceInfo{}, err
	}
	cwd := root
	if strings.TrimSpace(req.Cwd) != "" {
		cwd, err = resolveCommandCwd(root, req.Cwd)
		if err != nil {
			return ServiceInfo{}, err
		}
	}
	if req.Port < 0 || req.Port > 65535 {
		return ServiceInfo{}, codedToolError("E_BAD_SERVICE_PORT", fmt.Errorf("invalid port: %d", req.Port))
	}
	if req.Port > 0 && isLocalPortListening(req.Port) {
		return ServiceInfo{}, codedToolError("E_PORT_IN_USE", fmt.Errorf("port %d is already listening; refusing to start another service", req.Port))
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
	cmd.Env = proxyEnvironment(cfg, os.Environ())
	prepareServiceCommand(cmd)

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
		cancel()
		return ServiceInfo{}, err
	}

	service := &managedService{
		info: ServiceInfo{
			ID:        id,
			Name:      strings.TrimSpace(req.Name),
			Command:   req.Command,
			Cwd:       filepath.ToSlash(cwd),
			PID:       cmd.Process.Pid,
			Port:      req.Port,
			Status:    "starting",
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

	info := a.waitForServiceReady(service, req)
	return info, nil
}

func (a *App) finalizeService(id string, service *managedService) {
	_ = a.persistService(service)
	a.pruneCompletedServices()
	a.emitServiceUpdate(service.snapshot())
}

func copyServiceOutput(wg *sync.WaitGroup, dst io.Writer, src io.Reader) {
	defer wg.Done()
	_, _ = io.Copy(dst, src)
}

func (a *App) waitForServiceReady(service *managedService, req StartServiceRequest) ServiceInfo {
	limit := req.TimeoutSeconds
	if limit <= 0 {
		limit = defaultServiceReadyLimit
	}
	if limit > maxServiceReadyLimit {
		limit = maxServiceReadyLimit
	}
	deadline := time.Now().Add(time.Duration(limit) * time.Second)
	var readyRE *regexp.Regexp
	if strings.TrimSpace(req.ReadyPattern) != "" {
		if re, err := regexp.Compile(req.ReadyPattern); err == nil {
			readyRE = re
		}
	}
	for {
		select {
		case <-service.waitDone:
			return service.snapshot()
		default:
		}
		out := service.output.String()
		ready := false
		if readyRE != nil && readyRE.MatchString(out) {
			ready = true
		}
		if !ready && req.Port > 0 && isLocalPortListening(req.Port) {
			ready = true
		}
		if !ready && readyRE == nil && req.Port == 0 && time.Since(time.Unix(service.info.StartedAt, 0)) >= 500*time.Millisecond {
			ready = true
		}
		if ready || time.Now().After(deadline) {
			service.mu.Lock()
			if service.info.Status == "starting" {
				if ready {
					service.info.Status = "running"
				} else {
					service.info.Status = "running"
					service.info.Error = fmt.Sprintf("service did not report readiness within %ds; it may still be starting", limit)
				}
			}
			service.updateOutputInfoLocked()
			info := service.info
			service.mu.Unlock()
			a.emitServiceUpdate(info)
			return info
		}
		time.Sleep(100 * time.Millisecond)
	}
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
		if err := stopProcessTree(pid); err != nil {
			service.cancel()
			service.mu.Lock()
			service.info.Error = err.Error()
			service.mu.Unlock()
		}
		select {
		case <-service.waitDone:
		case <-time.After(5 * time.Second):
			service.cancel()
		}
		service.mu.Lock()
		service.info.Status = "stopped"
		service.info.StoppedAt = time.Now().Unix()
		service.updateOutputInfoLocked()
		service.mu.Unlock()
	}
	info := service.snapshot()
	_ = a.persistService(service)
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

func (a *App) stopAllServices() {
	for _, service := range a.listServices().Services {
		_, _ = a.stopService(StopServiceRequest{ID: service.ID})
	}
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

func (a *App) persistService(service *managedService) error {
	dir := a.serviceHistoryDir()
	if dir == "" || service == nil {
		return nil
	}
	info := service.snapshot()
	output, total, truncated := service.outputSnapshot()
	info.OutputTail = tailString(output, serviceOutputPreview)
	info.OutputBytes = total
	info.OutputTruncated = truncated
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	metadata, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicWriteFile(filepath.Join(dir, info.ID+".json"), metadata, 0o600); err != nil {
		return err
	}
	return atomicWriteFile(filepath.Join(dir, info.ID+".log"), []byte(output), 0o600)
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
	loaded := make([]*managedService, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			continue
		}
		raw, readErr := os.ReadFile(filepath.Join(dir, entry.Name()))
		if readErr != nil {
			continue
		}
		var info ServiceInfo
		if json.Unmarshal(raw, &info) != nil || strings.TrimSpace(info.ID) == "" {
			continue
		}
		if info.Status == "starting" || info.Status == "running" {
			info.Status = "interrupted"
			info.StoppedAt = time.Now().Unix()
			info.Error = "Ally exited before the service reported a terminal state"
		}
		output, _ := os.ReadFile(filepath.Join(dir, info.ID+".log"))
		buf := newRollingBuffer(serviceOutputLimit)
		buf.Restore(output, info.OutputBytes, info.OutputTruncated)
		service := &managedService{info: info, output: buf, waitDone: closedServiceWaitDone()}
		loaded = append(loaded, service)
	}
	sort.Slice(loaded, func(i, j int) bool { return loaded[i].info.StartedAt > loaded[j].info.StartedAt })
	a.servicesMu.Lock()
	for _, service := range loaded {
		a.services[service.info.ID] = service
	}
	a.servicesMu.Unlock()
	a.pruneCompletedServices()
	return nil
}

func closedServiceWaitDone() chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}

func (a *App) pruneCompletedServices() {
	a.servicesMu.Lock()
	type completedEntry struct {
		id      string
		started int64
	}
	completed := []completedEntry{}
	for id, service := range a.services {
		service.mu.Lock()
		status := service.info.Status
		started := service.info.StartedAt
		service.mu.Unlock()
		if status != "starting" && status != "running" {
			completed = append(completed, completedEntry{id: id, started: started})
		}
	}
	sort.Slice(completed, func(i, j int) bool { return completed[i].started > completed[j].started })
	removeIDs := []string{}
	if len(completed) > maxCompletedServices {
		for _, entry := range completed[maxCompletedServices:] {
			delete(a.services, entry.id)
			removeIDs = append(removeIDs, entry.id)
		}
	}
	a.servicesMu.Unlock()
	dir := a.serviceHistoryDir()
	for _, id := range removeIDs {
		_ = os.Remove(filepath.Join(dir, id+".json"))
		_ = os.Remove(filepath.Join(dir, id+".log"))
	}
}

type rollingBuffer struct {
	mu        sync.Mutex
	buf       []byte
	limit     int
	total     int64
	truncated bool
}

func newRollingBuffer(limit int) *rollingBuffer {
	return &rollingBuffer{limit: limit}
}

func (b *rollingBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.total += int64(len(p))
	if b.limit <= 0 {
		return len(p), nil
	}
	if len(p) >= b.limit {
		b.buf = append(b.buf[:0], p[len(p)-b.limit:]...)
		b.truncated = true
		return len(p), nil
	}
	if overflow := len(b.buf) + len(p) - b.limit; overflow > 0 {
		copy(b.buf, b.buf[overflow:])
		b.buf = b.buf[:len(b.buf)-overflow]
		b.truncated = true
	}
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *rollingBuffer) String() string {
	output, _, _ := b.Snapshot()
	return output
}

func (b *rollingBuffer) Snapshot() (string, int64, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(bytes.Clone(b.buf)), b.total, b.truncated
}

func (b *rollingBuffer) Restore(output []byte, total int64, truncated bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(output) > b.limit {
		output = output[len(output)-b.limit:]
		truncated = true
	}
	b.buf = append(b.buf[:0], output...)
	b.total = total
	if b.total < int64(len(output)) {
		b.total = int64(len(output))
	}
	b.truncated = truncated
}

func isLocalPortListening(port int) bool {
	if port <= 0 || port > 65535 {
		return false
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func tailString(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	return s[len(s)-limit:]
}

func normalizeServiceCommand(command string) string {
	return strings.ToLower(strings.Join(strings.Fields(command), " "))
}

func looksLikeLongRunningService(command string) bool {
	cmd := normalizeServiceCommand(command)
	patterns := []string{
		"manage.py runserver",
		"flask run",
		"uvicorn ",
		"hypercorn ",
		"fastapi dev",
		"npm run dev",
		"pnpm dev",
		"yarn dev",
		"bun dev",
		"vite",
		"next dev",
		"nuxt dev",
		"wails dev",
	}
	for _, pattern := range patterns {
		if strings.Contains(cmd, pattern) {
			return true
		}
	}
	return false
}

func longRunningCommandError(command string) error {
	return codedToolError("E_LONG_RUNNING_COMMAND", fmt.Errorf("this command looks like a long-running process; use background_process with action=start so it can run without blocking the agent.\n被拦截的命令: %s", command))
}

package main

import (
	"bytes"
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
)

const (
	serviceOutputLimit   = 512 * 1024
	serviceOutputPreview = 8 * 1024
	maxActiveServices    = 8

	// Tool-facing read defaults. The model can request up to
	// maxServiceReadTailBytes of recent output per call; larger reads are
	// clamped so a single background_process.read cannot dominate the model
	// context window.
	defaultServiceReadTailBytes = 8 * 1024
	maxServiceReadTailBytes     = 32 * 1024
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
	// poll status and output through background_process.list / read instead
	// of blocking the agent loop on a readiness wait.
	return service.snapshot(), nil
}

func (a *App) finalizeService(id string, service *managedService) {
	info := service.snapshot()
	a.removeService(id, service)
	a.emitServiceUpdate(info)
}

func copyServiceOutput(wg *sync.WaitGroup, dst io.Writer, src io.Reader) {
	defer wg.Done()
	_, _ = io.Copy(dst, src)
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
	a.removeService(id, service)
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
// the model must call background_process.read on a specific id to inspect
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
	// The rolling buffer drops early bytes once full. fromByte reflects where
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

func (a *App) removeService(id string, service *managedService) {
	a.servicesMu.Lock()
	if current := a.services[id]; current == service {
		delete(a.services, id)
	}
	a.servicesMu.Unlock()

	// Keep cleanup idempotent for installations upgraded from the old
	// completed-service retention behavior.
	dir := a.serviceHistoryDir()
	if dir != "" {
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

func tailString(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	return s[len(s)-limit:]
}

func normalizeServiceCommand(command string) string {
	return strings.ToLower(strings.Join(strings.Fields(command), " "))
}

// looksLikeLongRunningService only blocks an explicit whitelist of known dev-server
// commands. Anything else continues to the normal run_command safety checks/timeouts.
func looksLikeLongRunningService(command string) bool {
	cmd := normalizeServiceCommand(command)
	if cmd == "" {
		return false
	}
	// Whitelist only: miss => allow run_command.
	patterns := []string{
		"manage.py runserver",
		"flask run",
		"uvicorn ",
		"hypercorn ",
		"fastapi dev",
		"npm run dev",
		"pnpm run dev",
		"pnpm dev",
		"yarn run dev",
		"yarn dev",
		"bun run dev",
		"bun dev",
		"next dev",
		"nuxt dev",
		"wails dev",
		"vite preview",
		"vite dev",
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

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	serviceOutputLimit       = 32 * 1024
	defaultServiceReadyLimit = 15
	maxServiceReadyLimit     = 120
	maxActiveServices        = 8
)

type managedService struct {
	mu       sync.Mutex
	info     ServiceInfo
	cmd      *exec.Cmd
	output   *limitedBuffer
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
	activeCount := len(a.services)
	a.servicesMu.Unlock()
	if activeCount >= maxActiveServices {
		return ServiceInfo{}, codedToolError("E_SERVICE_LIMIT", fmt.Errorf("active service limit reached (%d)", maxActiveServices))
	}

	id := "svc_" + newID()
	ctx, cancel := context.WithCancel(context.Background())
	shell := commandShell(req.Command, cfg.GitBashPath)
	cmd := exec.CommandContext(ctx, shell.path, shell.args...)
	cmd.Dir = cwd
	cmd.Env = os.Environ()
	prepareServiceCommand(cmd)

	buf := &limitedBuffer{limit: serviceOutputLimit}
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

	var copyWG sync.WaitGroup
	copyWG.Add(2)
	go copyServiceOutput(&copyWG, buf, stdout)
	go copyServiceOutput(&copyWG, buf, stderr)
	go func() {
		waitErr := cmd.Wait()
		copyWG.Wait()
		service.mu.Lock()
		service.waitErr = waitErr
		service.info.OutputTail = tailString(buf.String(), maxToolOutput)
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
		a.removeService(id, service)
	}()

	info := a.waitForServiceReady(service, req)
	return info, nil
}

func (a *App) removeService(id string, service *managedService) {
	a.servicesMu.Lock()
	if a.services[id] == service {
		delete(a.services, id)
	}
	a.servicesMu.Unlock()
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
			service.info.OutputTail = tailString(out, maxToolOutput)
			info := service.info
			service.mu.Unlock()
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
		service.info.OutputTail = tailString(service.output.String(), maxToolOutput)
		service.mu.Unlock()
	}
	return service.snapshot(), nil
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
	if s.output != nil {
		info.OutputTail = tailString(s.output.String(), maxToolOutput)
	}
	return info
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

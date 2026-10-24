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
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type mcpWaitEventSink struct {
	mu      sync.Mutex
	events  []map[string]any
	waiting chan struct{}
}

func (s *mcpWaitEventSink) Emit(name string, payload any) {
	if name != "run:mcp-wait" {
		return
	}
	event := payload.(map[string]any)
	s.mu.Lock()
	s.events = append(s.events, event)
	s.mu.Unlock()
	if event["state"] == "waiting" && s.waiting != nil {
		select {
		case s.waiting <- struct{}{}:
		default:
		}
	}
}

func (s *mcpWaitEventSink) snapshot() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.events...)
}

func TestMcpFirstSessionWaitsBeforeFreezingAndReportsProgress(t *testing.T) {
	t.Parallel()
	a := newApiTestApp(t)
	m := NewMcpManager(a.config.Workspace, nil)
	defer m.Shutdown()
	a.mcpManager = m
	m.configSnapshot = map[string]McpServerConfig{"slow": {}, "fast": {}}
	m.clients["fast"] = &McpClientHandle{ServerName: "fast", Status: "connected"}
	sink := &mcpWaitEventSink{waiting: make(chan struct{}, 4)}
	a.events = sink
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		if err := a.waitForSessionMcp(ctx, "run", "session"); err != nil {
			done <- err
			return
		}
		a.buildToolsForSession("session", a.config)
		done <- nil
	}()
	select {
	case <-sink.waiting:
	case <-ctx.Done():
		t.Fatal("no wait status emitted")
	}
	a.mu.Lock()
	_, frozen := a.sessionToolsets["session"]
	a.mu.Unlock()
	if frozen {
		t.Fatal("toolset froze before discovery")
	}
	events := sink.snapshot()
	if events[0]["runId"] != "run" || events[0]["sessionId"] != "session" || events[0]["servers"].([]string)[0] != "slow" {
		t.Fatalf("progress is not session-scoped or names the wrong server: %+v", events)
	}
	m.mu.Lock()
	m.clients["slow"] = &McpClientHandle{ServerName: "slow", Status: "connected", ToolDefs: []McpDiscoveredTool{{ServerName: "slow", Name: "found", FunctionName: mcpToolFunctionName("slow", "found")}}}
	m.initialLoading = false
	m.mu.Unlock()
	m.notifyChange()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("completion did not wake waiter")
	}
	tools := a.buildToolsForSession("session", a.config)
	found := false
	for _, tool := range tools {
		if tool.Function.Name == mcpToolFunctionName("slow", "found") {
			found = true
		}
	}
	if !found {
		t.Fatal("discovered tool missing from frozen session")
	}
	events = sink.snapshot()
	if events[len(events)-1]["state"] != "ready" {
		t.Fatalf("wait label was not cleared: %+v", events)
	}
	// A subsequent run keeps its frozen set even while another connection starts.
	m.mu.Lock()
	m.clients["slow"].Status = "connecting"
	m.mu.Unlock()
	if err := a.waitForSessionMcp(ctx, "next", "session"); err != nil {
		t.Fatal(err)
	}
	if len(sink.snapshot()) != len(events) {
		t.Fatal("frozen session waited again")
	}
}

func TestMcpDiscoveryWaitIsCancellableAndBounded(t *testing.T) {
	t.Parallel()
	for _, timeout := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "deadline"}[timeout], func(t *testing.T) {
			a := newApiTestApp(t)
			m := NewMcpManager(a.config.Workspace, nil)
			defer m.Shutdown()
			a.mcpManager = m
			sink := &mcpWaitEventSink{waiting: make(chan struct{}, 1)}
			a.events = sink
			ctx, cancel := context.WithCancel(t.Context())
			if timeout {
				ctx, cancel = context.WithTimeout(t.Context(), 30*time.Millisecond)
			}
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- a.waitForSessionMcp(ctx, "run", "session") }()
			select {
			case <-sink.waiting:
			case <-time.After(time.Second):
				t.Fatal("missing wait event")
			}
			if !timeout {
				cancel()
			}
			select {
			case err := <-done:
				if !errors.Is(err, ctx.Err()) {
					t.Fatalf("wrong cancellation: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("wait stuck after cancellation")
			}
			if len(a.sessionToolsets) != 0 {
				t.Fatal("cancelled wait froze tools")
			}
			events := sink.snapshot()
			if events[len(events)-1]["state"] != "cancelled" {
				t.Fatal("cancel did not clear wait phase")
			}
		})
	}
	// The discovery primitive itself must obey its budget even when no server
	// ever answers. A ready/failed server never consumes that budget.
	m := NewMcpManager(t.TempDir(), nil)
	defer m.Shutdown()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := m.waitForDiscovery(ctx, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wrong timeout: %v", err)
	}
	m.mu.Lock()
	m.initialLoading = false
	m.clients["failed"] = &McpClientHandle{Status: "failed"}
	m.mu.Unlock()
	if err := m.waitForDiscovery(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestMcpDiscoveryProgressShrinksAndShutdownWakesWaiter(t *testing.T) {
	m := NewMcpManager(t.TempDir(), nil)
	m.initialLoading = false
	m.configSnapshot = map[string]McpServerConfig{"one": {}, "two": {}}
	defer m.Shutdown()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	updates := make(chan []string, 2)
	done := make(chan error, 1)
	go func() { done <- m.waitForDiscovery(ctx, func(names []string) { updates <- names }) }()
	select {
	case names := <-updates:
		if strings.Join(names, ",") != "one,two" {
			t.Fatalf("wrong pending servers: %v", names)
		}
	case <-ctx.Done():
		t.Fatal("missing initial progress")
	}
	m.mu.Lock()
	m.clients["one"] = &McpClientHandle{Status: "failed"}
	m.mu.Unlock()
	m.notifyChange()
	select {
	case names := <-updates:
		if strings.Join(names, ",") != "two" {
			t.Fatalf("finished server still pending: %v", names)
		}
	case <-ctx.Done():
		t.Fatal("missing updated progress")
	}
	m.Shutdown()
	select {
	case err := <-done:
		if err == nil || errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("shutdown did not wake waiter: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("shutdown stranded waiter")
	}
}

func TestMcpReloadFailureRetainsLiveServersAndSnapshot(t *testing.T) {
	t.Parallel()
	a := newApiTestApp(t)
	m := NewMcpManager(a.config.Workspace, nil)
	defer m.Shutdown()
	config := filepath.Join(t.TempDir(), "mcp.json")
	m.configPaths = []string{config}
	if err := os.WriteFile(config, []byte(`{"mcpServers":{"kept":{"enabled":false,"command":"unused"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.StartAll(t.Context()); err != nil {
		t.Fatal(err)
	}
	handle := m.clients["kept"]
	for _, unreadable := range []bool{false, true} {
		if unreadable {
			m.configPaths = []string{t.TempDir()}
		} else if err := os.WriteFile(config, []byte(`{broken`), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := m.ReconcileConfigs(t.Context()); err == nil {
			t.Fatal("invalid load must fail")
		}
		if m.clients["kept"] != handle || len(m.configSnapshot) != 1 {
			t.Fatal("reload failure altered live state")
		}
	}
	m.configPaths = []string{filepath.Join(t.TempDir(), "missing.json")}
	if _, err := m.ReconcileConfigs(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(m.clients) != 0 {
		t.Fatal("missing file must still mean empty configuration")
	}
}

func TestMcpSSEStartupDeadlineCoversResponseHeaders(t *testing.T) {
	t.Parallel()
	a := newApiTestApp(t)
	arrived := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { arrived <- struct{}{}; <-r.Context().Done() }))
	defer server.Close()
	m := NewMcpManager(a.config.Workspace, nil)
	defer m.Shutdown()
	m.SetNetworkConfigProvider(func() ConfigState { return ConfigState{AllowPrivateNetwork: boolPtr(true), ProxyMode: "off"} })
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	started := time.Now()
	_, err := m.initializeMcpClient(ctx, "slow", McpServerConfig{Transport: "sse", URL: server.URL, StartupTimeoutSec: 0.05})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wrong startup failure: %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("startup ignored its own timeout")
	}
	select {
	case <-arrived:
	default:
		t.Fatal("request never reached local server")
	}
}

func TestMcpReconnectedStdioOutlivesCallingContext(t *testing.T) {
	t.Parallel()
	a := newApiTestApp(t)
	script := filepath.Join(t.TempDir(), "server.py")
	if err := os.WriteFile(script, []byte(mcpStdioTestServerScript), 0600); err != nil {
		t.Fatal(err)
	}
	m := NewMcpManager(a.config.Workspace, nil)
	defer m.Shutdown()
	m.clients["echo"] = &McpClientHandle{ServerName: "echo", Status: "failed", Config: McpServerConfig{Command: mcpTestPython(t), Args: []string{script}}}
	ctx, cancel := context.WithCancel(t.Context())
	first, err := m.CallTool(ctx, "echo", "where", nil)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.CallTool(t.Context(), "echo", "where", nil)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || !strings.Contains(second, "pid=") {
		t.Fatalf("call cancellation replaced live process: %q / %q", first, second)
	}
}

func TestMcpSSEConnectionOutlivesStartupBudgetAndCaller(t *testing.T) {
	t.Parallel()
	a := newApiTestApp(t)
	protocol := server.NewMCPServer("lifetime-test", "1.0", server.WithToolCapabilities(true))
	protocol.AddTool(mcp.NewTool("echo"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("still connected"), nil
	})
	endpoint := httptest.NewServer(server.NewSSEServer(protocol))
	defer endpoint.Close()
	m := NewMcpManager(a.config.Workspace, nil)
	defer m.Shutdown()
	m.SetNetworkConfigProvider(func() ConfigState { return ConfigState{AllowPrivateNetwork: boolPtr(true), ProxyMode: "off"} })
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	established, err := m.initializeMcpClient(ctx, "echo", McpServerConfig{Transport: "sse", URL: endpoint.URL + "/sse", StartupTimeoutSec: 0.05})
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	defer established.client.Close()
	defer established.token.stop()
	// Exceed the now-detached startup budget; neither that timer nor canceling
	// the establishing caller may tear down the live SSE stream. The wait only has
	// to clear the budget, so keep it just above it instead of burning a quarter
	// second per run.
	time.Sleep(80 * time.Millisecond)
	callCtx, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	response, err := established.client.CallTool(callCtx, mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "echo"}})
	if err != nil || response.IsError || mcpToolResultText(response) != "still connected" {
		t.Fatalf("startup context leaked into transport lifetime: %v / %+v", err, response)
	}
}

func TestMcpToolDisableReadConcurrentWithUpdates(t *testing.T) {
	m := NewMcpManager(t.TempDir(), nil)
	defer m.Shutdown()
	m.clients["server"] = &McpClientHandle{Status: "connected"}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			m.IsToolDisabled("server", "tool")
		}
	}()
	for i := 0; i < 1000; i++ {
		m.mu.Lock()
		m.clients["server"].Config.DisabledTools = []string{"tool"}
		m.mu.Unlock()
	}
	wg.Wait()
}

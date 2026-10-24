// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestServiceToolWorkspaceIsolation(t *testing.T) {
	root := redirectAppStateDir(t)
	workspaceA, workspaceB := filepath.Join(root, "a"), filepath.Join(root, "b")
	a := NewApp()
	for _, info := range []ServiceInfo{
		{ID: "own", Workspace: workspaceA, Cwd: workspaceB, Status: "exited"},
		{ID: "foreign", Workspace: workspaceB, Cwd: workspaceA, Status: "exited"},
		{ID: "legacy", Cwd: workspaceA, Status: "exited"},
		{ID: "nested", Workspace: filepath.Join(workspaceA, "nested"), Status: "exited"},
	} {
		buffer := newRollingBuffer(serviceOutputLimit)
		_, _ = buffer.Write([]byte(info.ID + "-private-output"))
		a.services[info.ID] = &managedService{info: info, output: buffer}
	}
	cfg := ConfigState{Workspace: workspaceA, ExtraRoots: []string{workspaceB}}
	for _, session := range []string{"first-session", "second-session", "subagent"} {
		listed := a.executeTool(context.Background(), cfg, session, "service", []byte(`{"action":"list","includeFinished":true}`))
		if !listed.OK {
			t.Fatal(listed.Error)
		}
		result := listed.Data.(ServiceListToolResult)
		if len(result.Services) != 1 || result.Services[0].ID != "own" || result.ActiveCount != 0 {
			t.Fatalf("session %s leaked foreign or unowned services: %+v", session, result)
		}
	}
	for _, id := range []string{"foreign", "legacy", "nested", "missing"} {
		for _, action := range []string{"read", "stop"} {
			args, _ := json.Marshal(BackgroundProcessRequest{Action: action, ID: id})
			r := a.executeTool(context.Background(), cfg, "s", "service", args)
			if r.OK || r.ErrorCode != "E_SERVICE_NOT_FOUND" || strings.Contains(r.Error, "private-output") {
				t.Fatalf("%s %s bypassed workspace ownership: %+v", action, id, r)
			}
		}
	}
	if a.services["foreign"].snapshot().Status != "exited" {
		t.Fatal("cross-workspace stop changed foreign service")
	}
	read := a.executeTool(context.Background(), cfg, "s", "service", []byte(`{"action":"read","id":"own"}`))
	if !read.OK || !strings.Contains(read.Data.(ServiceReadResult).Output, "own-private-output") {
		t.Fatalf("own workspace read failed: %+v", read)
	}
	stop := a.executeTool(context.Background(), cfg, "s", "service", []byte(`{"action":"stop","id":"own"}`))
	if !stop.OK {
		t.Fatalf("own workspace stop failed: %+v", stop)
	}
	if len(a.ListServices().Services) != 4 {
		t.Fatal("global management list lost services")
	}
	if output, err := a.GetServiceOutput("foreign"); err != nil || !strings.Contains(output.Output, "foreign-private-output") {
		t.Fatalf("global user management cannot inspect foreign service: %+v, %v", output, err)
	}
	if _, err := a.listServicesForTool(ConfigState{}, true); err == nil {
		t.Fatal("missing workspace must not grant global model access")
	}
	if runtime.GOOS == "windows" {
		aliasCfg := ConfigState{Workspace: strings.ToUpper(filepath.ToSlash(workspaceA)) + "/"}
		if err := a.checkServiceWorkspace(aliasCfg, "own"); err != nil {
			t.Fatalf("Windows spelling split one workspace: %v", err)
		}
	}
}

func TestServiceStartAndCommandPromotionKeepWorkspaceOwner(t *testing.T) {
	t.Parallel()
	root := validationSandbox(t)
	extra := t.TempDir()
	cfg := ConfigState{Workspace: root, ExtraRoots: []string{extra}}
	// Isolated App: the service and its output spill must not land in a shared data dir.
	a := newIsolatedToolTestApp(t)
	t.Cleanup(a.stopAllServices)
	args, _ := json.Marshal(BackgroundProcessRequest{Action: "start", Description: " 校验服务归属 ", Command: "printf 'owner-probe\\n'", Cwd: extra})
	started := a.executeTool(context.Background(), cfg, "s", "service", args)
	if !started.OK {
		t.Fatal(started.Error)
	}
	info := started.Data.(ServiceInfo)
	// cwd 经执行路径规范化；macOS 的 /var 与 /private/var 是同一个目录。
	expectedCwd, err := filepath.EvalSymlinks(extra)
	if err != nil {
		t.Fatal(err)
	}
	if info.Description != "校验服务归属" {
		t.Fatalf("description lost during start: %+v", info)
	}
	if !samePath(info.Workspace, root) || !samePath(info.Cwd, expectedCwd) {
		t.Fatalf("cwd replaced workspace ownership: %+v", info)
	}
	a.servicesMu.Lock()
	done := a.services[info.ID].waitDone
	a.servicesMu.Unlock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("short-lived service did not finish")
	}
	listed, err := a.listServicesForTool(cfg, true)
	if err != nil || len(listed.Services) != 1 || listed.Services[0].Description != info.Description {
		t.Fatalf("list lost service description: %+v, %v", listed, err)
	}
	if !strings.Contains(renderServiceInfoResultForModel(info), info.Description) || !strings.Contains(renderServiceListResultForModel(listed), info.Description) {
		t.Fatal("model-facing results dropped service description")
	}
	result, err := a.runCommandWithConfig(context.Background(), cfg, CommandRequest{Command: "printf 'promoted-owner\\n'; sleep 30", Cwd: extra, Timeout: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !result.PromotedToService {
		t.Fatalf("expected promotion: %+v", result)
	}
	services := a.ListServices().Services
	found := false
	for _, svc := range services {
		if svc.Promoted {
			found = true
			if svc.Description != "" || svc.TimeoutSeconds != 1 {
				t.Fatalf("promotion must retain structured metadata, not localized prose: %+v", svc)
			}
			list, err := a.listServicesForTool(cfg, true)
			if err != nil {
				t.Fatal(err)
			}
			structured := renderServiceInfoResultForModel(svc)
			if !strings.Contains(structured, `promoted="true" timeout-seconds="1"`) || !strings.Contains(renderServiceListResultForModel(list), "promoted=true timeout-seconds=1") {
				t.Fatal("model results lost structured promotion metadata")
			}
			if !samePath(svc.Workspace, root) || !samePath(svc.Cwd, expectedCwd) {
				t.Fatalf("promoted process lost workspace: %+v", svc)
			}
			if _, err := a.StopService(StopServiceRequest{ID: svc.ID, GraceSeconds: 1}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !found {
		t.Fatal("promoted service missing from global list")
	}
}

func TestServiceListSeparatesWorkspaceAndGlobalCapacity(t *testing.T) {
	root := redirectAppStateDir(t)
	a := NewApp()
	for _, info := range []ServiceInfo{
		{ID: "own", Workspace: root, Status: "starting"},
		{ID: "foreign", Workspace: filepath.Join(root, "other"), Status: "running"},
		{ID: "unowned", Status: "running"},
		{ID: "finished", Workspace: root, Status: "exited"},
	} {
		a.services[info.ID] = &managedService{info: info}
	}
	for _, includeFinished := range []bool{false, true} {
		result, err := a.listServicesForTool(ConfigState{Workspace: root}, includeFinished)
		if err != nil {
			t.Fatal(err)
		}
		if result.ActiveCount != 1 || result.GlobalActiveCount != 3 || result.MaxActive != maxActiveServices {
			t.Fatalf("capacity mixed workspace and global counts: %+v", result)
		}
		model := renderServiceListResultForModel(result)
		if !strings.Contains(model, `workspace-active="1" global-active="3"`) || strings.Contains(model, "foreign") || strings.Contains(model, "unowned") {
			t.Fatalf("scope or capacity ambiguous in model result: %s", model)
		}
	}
}

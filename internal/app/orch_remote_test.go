// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// pickRemoteHelperPython 选择本地 python3/python 解释器；都没有则跳过。
// 远程 helper 是 Python 字符串，无法直接跑远端，这里用本地解释器做镜像测试。
func pickRemoteHelperPython(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"python3", "python"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	t.Skip("python3/python not found; skipping remote helper tests")
	return ""
}

// TestRemoteScriptTransportInvariants 锁定 payload 传输与删除拦截的关键不变量：
// payload 走 stdin 占位符（不用 argv）、删除判定收敛到 Go 侧（Python 无正则）。
func TestRemoteScriptTransportInvariants(t *testing.T) {
	if strings.Contains(remotePythonScript, "sys.argv") {
		t.Error("remote python script must not use sys.argv; payload is injected via __PAYLOAD_B64__ placeholder")
	}
	if !strings.Contains(remotePythonScript, "__PAYLOAD_B64__") {
		t.Error("remote python script lost __PAYLOAD_B64__ placeholder")
	}
	if strings.Contains(remotePythonScript, "DELETE_RE") {
		t.Error("remote python script still contains DELETE_RE regex; deletion should be Go-side only")
	}
	if !strings.Contains(remotePythonScript, "__DELETE_EXACT_ONLY__") {
		t.Error("remote python script lost __DELETE_EXACT_ONLY__ placeholder")
	}
	if !strings.Contains(remotePythonScript, "__DELETE_PROTECTED_TREES__") {
		t.Error("remote python script lost __DELETE_PROTECTED_TREES__ placeholder")
	}
}

// TestBuildRemoteScriptInjectsProtectionAndPayload 验证 buildRemoteScript 把删除
// 清单与 payload 占位符全部替换，且产物能用真实解释器跑通。
func TestBuildRemoteScriptInjectsProtectionAndPayload(t *testing.T) {
	py := pickRemoteHelperPython(t)
	root := t.TempDir()
	script, err := buildRemoteScript(map[string]any{
		"op":            "_check_write_targets",
		"workspaceRoot": root,
		"cwd":           ".",
		"targets":       []string{},
	})
	if err != nil {
		t.Fatalf("buildRemoteScript: %v", err)
	}
	for _, placeholder := range []string{"__PAYLOAD_B64__", "__DELETE_EXACT_ONLY__", "__DELETE_PROTECTED_TREES__"} {
		if strings.Contains(script, placeholder) {
			t.Errorf("buildRemoteScript left placeholder %q unreplaced", placeholder)
		}
	}
	cmd := exec.Command(py, "-")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("built script failed to run: %v; output: %s", err, out)
	}
	if !strings.Contains(string(out), remotePythonMarker) {
		t.Fatalf("built script produced no result marker: %q", string(out))
	}
}

// runRemoteHelperScript 用本地解释器执行注入 payload 后的 helper 脚本，
// 返回 marker 后的 JSON 解码结果。远程 helper 无法直接跑远端，这里镜像
// 执行以锁定判定契约（只触碰 t.TempDir()，不做任何真实系统路径删除）。
func runRemoteHelperScript(t *testing.T, py, script string) (remotePythonResponse, error) {
	t.Helper()
	cmd := exec.Command(py, "-")
	cmd.Stdin = strings.NewReader(script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return remotePythonResponse{}, fmt.Errorf("helper failed: %v; stderr: %s", err, stderr.String())
	}
	out := stdout.String()
	idx := strings.LastIndex(out, remotePythonMarker)
	if idx < 0 {
		return remotePythonResponse{}, fmt.Errorf("no result marker; output: %q", out)
	}
	var resp remotePythonResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(out[idx+len(remotePythonMarker):])), &resp); err != nil {
		return remotePythonResponse{}, fmt.Errorf("bad result JSON: %w; raw: %q", err, out[idx:])
	}
	return resp, nil
}

// TestValidateRemoteWorkspacePathRebase 锁定远端路径校验的重定基契约：
// root 内的绝对路径（含 root 本身）等价于其相对拼写，root 外绝对路径拒绝。
// 动机：cwd="/tmp" 且 target root 也是 /tmp 时，旧实现按“绝对路径一律拒绝”
// 处理，同义拼写被误伤；语义边界只应是 root 内外，而非拼与不拼。
func TestValidateRemoteWorkspacePathRebase(t *testing.T) {
	cases := []struct {
		name      string
		p         string
		root      string
		allowRoot bool
		want      string
		wantErr   string
	}{
		{"root itself absolute", "/srv/app", "/srv/app", true, ".", ""},
		{"root itself absolute no allowRoot", "/srv/app", "/srv/app", false, "", "path is required"},
		{"inside root rebased", "/srv/app/src/main.go", "/srv/app", false, "src/main.go", ""},
		{"empty means root", "", "/srv/app", true, ".", ""},
		{"relative passthrough", "src/main.go", "/srv/app", false, "src/main.go", ""},
		{"outside root rejected", "/etc/passwd", "/srv/app", true, "", "outside the remote workspaceRoot"},
		{"sibling prefix not root", "/srv/appdata/x", "/srv/app", true, "", "outside the remote workspaceRoot"},
		{"trailing slash root normalized", "/srv/app/", "/srv/app", true, ".", ""},
		{"dot-dot still rejected", "/srv/app/../etc", "/srv/app", true, "", "must not contain"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := validateRemoteWorkspacePath(tc.p, tc.root, tc.allowRoot)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("path %q root %q: expected error containing %q, got %q (clean=%q)", tc.p, tc.root, tc.wantErr, err, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("path %q root %q: unexpected error: %v", tc.p, tc.root, err)
			}
			if got != tc.want {
				t.Fatalf("path %q root %q: expected %q, got %q", tc.p, tc.root, tc.want, got)
			}
		})
	}
}

// TestRemoteHelperProtectedDeleteClassification 锁定删除保护的分类契约：
// 系统树（/etc、/usr 等）整体拒绝；主目录类父根（/root、/home）只拦目录
// 本身，其子树内的普通工作区文件必须放行（/root 曾被误放进树清单，导致
// root 用户远程工作区所有删除被拒）。
func TestRemoteHelperProtectedDeleteClassification(t *testing.T) {
	py := pickRemoteHelperPython(t)
	root := t.TempDir()
	cases := []struct {
		name      string
		path      string
		protected bool
	}{
		{"filesystem root", "/", true},
		{"root home itself", "/root", true},
		{"etc tree dir", "/etc", true},
		{"etc file", "/etc/passwd", true},
		{"usr subtree", "/usr/bin/ls", true},
		{"workspace file under /root", "/root/ally-remote-test/app.py", false},
		{"project file under /root", "/root/projects/tooltest/file.txt", false},
		{"project file under /home", "/home/alice/project/file.txt", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			script, err := buildRemoteScript(map[string]any{
				"op":            "_check_protected",
				"workspaceRoot": root,
				"path":          tc.path,
			})
			if err != nil {
				t.Fatalf("buildRemoteScript: %v", err)
			}
			resp, err := runRemoteHelperScript(t, py, script)
			if err != nil {
				t.Fatal(err)
			}
			if !resp.OK {
				t.Fatalf("helper failed: %s", resp.Error)
			}
			var data struct {
				Protected bool `json:"protected"`
			}
			if err := json.Unmarshal(resp.Data, &data); err != nil {
				t.Fatalf("decode data: %v", err)
			}
			if data.Protected != tc.protected {
				t.Fatalf("path %s: expected protected=%v, got %v", tc.path, tc.protected, data.Protected)
			}
		})
	}
}

// TestRemoteHelperDeleteOpTouchesOnlyWorkspace 用真实 helper 脚本跑完整
// delete op（只操作 t.TempDir() 内的文件）：普通文件删、目录需 recursive、
// 递归删目录、工作区根本身拒绝、逃逸路径拒绝。
func TestRemoteHelperDeleteOpTouchesOnlyWorkspace(t *testing.T) {
	py := pickRemoteHelperPython(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.py"), []byte("print('x')\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "nested.txt"), []byte("n"), 0o600); err != nil {
		t.Fatal(err)
	}

	run := func(payload map[string]any) remotePythonResponse {
		t.Helper()
		script, err := buildRemoteScript(payload)
		if err != nil {
			t.Fatalf("buildRemoteScript: %v", err)
		}
		resp, err := runRemoteHelperScript(t, py, script)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	// 1) 普通文件删除成功
	resp := run(map[string]any{"op": "delete", "workspaceRoot": root, "path": "app.py", "recursive": false})
	if !resp.OK {
		t.Fatalf("delete plain file failed: %s", resp.Error)
	}
	if _, err := os.Stat(filepath.Join(root, "app.py")); !os.IsNotExist(err) {
		t.Fatalf("app.py should be deleted, stat err: %v", err)
	}

	// 2) 目录未给 recursive 报错
	resp = run(map[string]any{"op": "delete", "workspaceRoot": root, "path": "sub", "recursive": false})
	if resp.OK {
		t.Fatal("delete dir without recursive should fail")
	}
	if !strings.Contains(resp.Error, "recursive") {
		t.Fatalf("unexpected error: %s", resp.Error)
	}

	// 3) recursive 删除子目录成功
	resp = run(map[string]any{"op": "delete", "workspaceRoot": root, "path": "sub", "recursive": true})
	if !resp.OK {
		t.Fatalf("recursive delete failed: %s", resp.Error)
	}

	// 4) 工作区根本身拒绝
	resp = run(map[string]any{"op": "delete", "workspaceRoot": root, "path": ".", "recursive": false})
	if resp.OK || !strings.Contains(resp.Error, "refusing to delete remote workspace root") {
		t.Fatalf("workspace root delete should be refused, got ok=%v error=%s", resp.OK, resp.Error)
	}

	// 5) 逃逸路径拒绝
	resp = run(map[string]any{"op": "delete", "workspaceRoot": root, "path": "../outside.txt", "recursive": false})
	if resp.OK || !strings.Contains(resp.Error, "..") {
		t.Fatalf("escape path should be refused, got ok=%v error=%s", resp.OK, resp.Error)
	}
}

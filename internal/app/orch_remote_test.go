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
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// pickRemoteHelperPython 选择镜像测试解释器。可用 ALLY_TEST_PYTHON 指定版本，
// 例如 Python 2.7 环境设为 python2；未指定时优先 Python 3，再尝试 Python 2。
func pickRemoteHelperPython(t *testing.T) string {
	t.Helper()
	names := []string{}
	if name := os.Getenv("ALLY_TEST_PYTHON"); name != "" {
		names = append(names, name)
	} else {
		names = []string{"python3", "python", "python2"}
	}
	for _, name := range names {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	t.Skip("Python interpreter not found; set ALLY_TEST_PYTHON to the desired interpreter")
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
	if strings.Contains(remotePythonScript, "pathlib") {
		t.Error("remote python script must not use pathlib (unsupported in Python 2.7)")
	}
	if strings.Contains(remotePythonScript, "commonpath") {
		t.Error("remote python script must not use os.path.commonpath (unsupported in Python 2.7)")
	}
	if strings.Contains(remotePythonScript, "datetime") {
		t.Error("remote python script must not use datetime.timezone (unsupported in Python 2.7)")
	}
	if !strings.Contains(remotePythonScript, "from __future__ import print_function") {
		t.Error("remote python script should import print_function for Python 2 compatibility")
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
		{"tempdir child path", filepath.Join(os.TempDir(), "project", "file.txt"), false},
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

// TestRemoteHelperWriteDefaultPerm0644 锁定新建文件权限契约：mkstemp 的
// 0600 会让远程新建文件对其他账号不可读；现在必须对齐本地
// SafeWriteFile 的 0644 默认值，覆盖时保留原文件权限位。
func TestRemoteHelperWriteDefaultPerm0644(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not enforced on Windows; the helper mirror test cannot verify fchmod locally (real remotes are POSIX)")
	}
	py := pickRemoteHelperPython(t)
	root := t.TempDir()
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

	// 新建文件 → 0644
	resp := run(map[string]any{"op": "write", "workspaceRoot": root, "path": "new.txt", "dataBase64": base64.StdEncoding.EncodeToString([]byte("hello")), "overwrite": false, "mkdirs": true})
	if !resp.OK {
		t.Fatalf("write new file failed: %s", resp.Error)
	}
	info, err := os.Stat(filepath.Join(root, "new.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Fatalf("new file perm = %o, want 0644 (aligned with local SafeWriteFile)", perm)
	}

	// 覆盖已有 0600 文件 → 保留 0600
	if err := os.WriteFile(filepath.Join(root, "secret.txt"), []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	resp = run(map[string]any{"op": "write", "workspaceRoot": root, "path": "secret.txt", "dataBase64": base64.StdEncoding.EncodeToString([]byte("s2")), "overwrite": true, "mkdirs": false})
	if !resp.OK {
		t.Fatalf("overwrite failed: %s", resp.Error)
	}
	info, err = os.Stat(filepath.Join(root, "secret.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("overwritten file perm = %o, want preserved 0600", perm)
	}
}

// TestRemoteHelperReadBatchOp 锁定 read_batch 契约：逐文件错误隔离、结
// 果槽顺序与请求一致、预算耗尽时剩余路径进 remaining 交给下一轮会话。
func TestRemoteHelperReadBatchOp(t *testing.T) {
	py := pickRemoteHelperPython(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("aaa"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("bbb"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	// budget: totalBytes=5 → a(3) 进末尾，b(3) 溢出进 remaining（不产生占位槽，
	// Go 侧由 remaining 下一轮会话回填）。
	payload := map[string]any{
		"op":            "read_batch",
		"workspaceRoot": root,
		"paths":         []string{"a.txt", "missing.txt", "sub", "b.txt"},
		"maxBytes":      1024,
		"totalBytes":    5,
	}
	script, err := buildRemoteScript(payload)
	if err != nil {
		t.Fatalf("buildRemoteScript: %v", err)
	}
	resp, err := runRemoteHelperScript(t, py, script)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("read_batch failed: %s", resp.Error)
	}
	var data struct {
		Files []struct {
			Path  string `json:"path"`
			OK    bool   `json:"ok"`
			Error string `json:"error"`
			Data  struct {
				DataBase64 string `json:"dataBase64"`
			} `json:"data"`
		} `json:"files"`
		Remaining []string `json:"remaining"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("decode read_batch data: %v", err)
	}
	if len(data.Files) != 3 {
		t.Fatalf("expected 3 filled slots (deferred file leaves no placeholder), got %d: %+v", len(data.Files), data.Files)
	}
	if !data.Files[0].OK || data.Files[0].Data.DataBase64 == "" {
		t.Fatalf("a.txt should read ok, got %+v", data.Files[0])
	}
	if data.Files[1].OK || data.Files[1].Error == "" {
		t.Fatalf("missing.txt should carry per-file error, got %+v", data.Files[1])
	}
	if data.Files[2].OK || data.Files[2].Error == "" {
		t.Fatalf("directory target should carry per-file error, got %+v", data.Files[2])
	}
	if len(data.Remaining) != 1 || data.Remaining[0] != "b.txt" {
		t.Fatalf("remaining = %v, want [b.txt]", data.Remaining)
	}
}

// TestRemoteScriptNonAsciiTextBoundary 锁定 payload 文本的 OS 边界规则：
// Python 2 的 str(unicode) 一律按 ASCII 编码，命令或路径里只要有非 ASCII 字符
// 就可能抛编码错误；Linux 文件系统使用字节路径，因此统一转成 UTF-8 字节串。
func TestRemoteScriptNonAsciiTextBoundary(t *testing.T) {
	for _, forbidden := range []string{
		`str(payload`,
		`str(rel)`,
		`str(path)`,
		`str(root)`,
		`str(cwd)`,
		`str(t_str)`,
		`str(b64)`,
	} {
		if strings.Contains(remotePythonScript, forbidden) {
			t.Errorf("remote python script must not coerce payload text with %s (Python 2 encodes str(unicode) as ASCII): use to_os_text()", forbidden)
		}
	}
	for _, required := range []string{
		"def to_os_text(s):",
		"payload = payload_os_text(payload)",
		`command = to_os_text(payload.get("command")`,
	} {
		if !strings.Contains(remotePythonScript, required) {
			t.Errorf("remote python script lost %q; Python 2 remotes would fail on non-ASCII commands/paths", required)
		}
	}
}

// TestRemoteHelperNonAsciiTextRoundTrip 用真实 helper 脚本跑非 ASCII 路径：
// 非 ASCII 目录名 / 文件名 / 文件内容必须原样往返（只碰 t.TempDir()）。
func TestRemoteHelperNonAsciiTextRoundTrip(t *testing.T) {
	py := pickRemoteHelperPython(t)
	root := t.TempDir()
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
		if !resp.OK {
			t.Fatalf("helper failed: %s", resp.Error)
		}
		return resp
	}

	relPath := "中文目录/说明.txt"
	content := "你好，世界\nhello\n"
	resp := run(map[string]any{
		"op":            "write",
		"workspaceRoot": root,
		"path":          relPath,
		"dataBase64":    base64.StdEncoding.EncodeToString([]byte(content)),
		"overwrite":     true,
		"mkdirs":        true,
	})
	var written struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(resp.Data, &written); err != nil {
		t.Fatalf("decode write data: %v", err)
	}
	if written.Path != relPath {
		t.Fatalf("write returned path %q, want %q", written.Path, relPath)
	}
	if onDisk, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relPath))); err != nil || string(onDisk) != content {
		t.Fatalf("file on disk = %q (err %v), want %q", onDisk, err, content)
	}

	resp = run(map[string]any{
		"op":            "read_batch",
		"workspaceRoot": root,
		"paths":         []string{relPath},
		"maxBytes":      1 << 20,
		"totalBytes":    1 << 20,
	})
	var batch struct {
		Files []struct {
			Path  string `json:"path"`
			OK    bool   `json:"ok"`
			Error string `json:"error"`
			Data  struct {
				DataBase64 string `json:"dataBase64"`
			} `json:"data"`
		} `json:"files"`
	}
	if err := json.Unmarshal(resp.Data, &batch); err != nil {
		t.Fatalf("decode read_batch data: %v", err)
	}
	if len(batch.Files) != 1 || !batch.Files[0].OK {
		t.Fatalf("read_batch should read %s, got %+v", relPath, batch.Files)
	}
	if batch.Files[0].Path != relPath {
		t.Fatalf("read_batch path = %q, want %q (the path is echoed back through JSON)", batch.Files[0].Path, relPath)
	}
	raw, err := base64.StdEncoding.DecodeString(batch.Files[0].Data.DataBase64)
	if err != nil {
		t.Fatalf("decode read data: %v", err)
	}
	if string(raw) != content {
		t.Fatalf("read content = %q, want %q", raw, content)
	}

	// 命令字面写入目标里的非 ASCII 路径同样要能过边界（targets 由 Go 侧从
	// 命令文本里解析出来，与 run op 走同一条 to_os_text 通道）。
	resp = run(map[string]any{
		"op":            "_check_write_targets",
		"workspaceRoot": root,
		"cwd":           "中文目录",
		"targets":       []string{"新建输出.txt"},
	})
	var checked struct {
		Checked bool `json:"checked"`
	}
	if err := json.Unmarshal(resp.Data, &checked); err != nil {
		t.Fatalf("decode check data: %v", err)
	}
	if !checked.Checked {
		t.Fatalf("write-target check for a non-ASCII in-workspace path must pass, data=%s", resp.Data)
	}
}

// TestRemoteHelperWriteFchmodFallback 锁定 os.fchmod 缺失时的降级路径（Windows
// 直到 Python 3.13 才提供 os.fchmod）：前置钩子把 os.fchmod 拿掉后写文件，不得
// 因 AttributeError 把整个 write 打挂，且 POSIX 上权限仍须是契约里的 0644
// （Windows 只校验降级分支不报错，权限位不被强制）。
func TestRemoteHelperWriteFchmodFallback(t *testing.T) {
	py := pickRemoteHelperPython(t)
	root := t.TempDir()
	script, err := buildRemoteScript(map[string]any{
		"op":            "write",
		"workspaceRoot": root,
		"path":          "new.txt",
		"dataBase64":    base64.StdEncoding.EncodeToString([]byte("hello")),
		"overwrite":     true,
		"mkdirs":        true,
	})
	if err != nil {
		t.Fatalf("buildRemoteScript: %v", err)
	}
	// 注入位置必须在 `from __future__ import print_function` 之后（future 导入
	// 要求位于文件最前），且在同一进程内拿掉 os.fchmod——脚本随后 import os
	// 拿到的是同一个模块对象，属性依旧缺失。
	hook := "import os as _ally_os\nif hasattr(_ally_os, \"fchmod\"):\n    del _ally_os.fchmod"
	patched := strings.Replace(script, "from __future__ import print_function",
		"from __future__ import print_function\n"+hook, 1)
	if patched == script {
		t.Fatal("remote python script lost its `from __future__ import print_function` line")
	}
	resp, err := runRemoteHelperScript(t, py, patched)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("write without os.fchmod failed: %s", resp.Error)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(root, "new.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o644 {
			t.Fatalf("fallback perm = %o, want 0644", perm)
		}
	}
}

// TestRemoteHelperRunCommandShells 锁定 op_run 的两件事：
//  1. payload 不带 shell 时 helper 必须自己挑一个能用的（posix: bash/sh，
//     Windows: COMSPEC）。这一步同时验证信号表探测——Windows 没有 SIGHUP，
//     旧实现取信号时就把整个 run 打挂。
//  2. 命令与 cwd 含非 ASCII 时必须原样执行并按 UTF-8 回传（POSIX 用默认 shell；
//     Windows 的 cmd.exe 会按控制台代码页转中文，故显式给 bash，没有则跳过）。
func TestRemoteHelperRunCommandShells(t *testing.T) {
	py := pickRemoteHelperPython(t)
	root := t.TempDir()
	type runData struct {
		Output   string `json:"output"`
		ExitCode int    `json:"exitCode"`
		Shell    string `json:"shellPath"`
	}
	execCommand := func(payload map[string]any) runData {
		t.Helper()
		script, err := buildRemoteScript(payload)
		if err != nil {
			t.Fatalf("buildRemoteScript: %v", err)
		}
		resp, err := runRemoteHelperScript(t, py, script)
		if err != nil {
			t.Fatal(err)
		}
		if !resp.OK {
			t.Fatalf("run helper failed: %s", resp.Error)
		}
		var data runData
		if err := json.Unmarshal(resp.Data, &data); err != nil {
			t.Fatalf("decode run data: %v", err)
		}
		return data
	}

	got := execCommand(map[string]any{
		"op":             "run",
		"workspaceRoot":  root,
		"cwd":            ".",
		"command":        "echo ally-run-ok",
		"timeoutSeconds": 30,
		"maxOutput":      1 << 16,
		"shell":          "",
		"targets":        []string{},
	})
	if got.Shell == "" {
		t.Fatalf("helper must report the shell it picked: %+v", got)
	}
	if got.ExitCode != 0 || !strings.Contains(got.Output, "ally-run-ok") {
		t.Fatalf("default shell (%s) run failed: exit=%d output=%q", got.Shell, got.ExitCode, got.Output)
	}

	if err := os.MkdirAll(filepath.Join(root, "中文目录"), 0o755); err != nil {
		t.Fatal(err)
	}
	shell := ""
	if runtime.GOOS == "windows" {
		// Windows 上 Popen(shell=True) 会给 executable 拼 "/c"（cmd 语义），bash 会把
		// /c 当脚本路径；走默认 cmd.exe 时非 ASCII 输出又会被控制台代码页转码。
		// 非 ASCII 命令这半只在 posix 上镜像执行（Windows 的默认 shell 通道见第 1 段）。
		t.Skip("non-ascii command text needs a POSIX shell: shell=True on Windows always means cmd.exe semantics")
	}
	got = execCommand(map[string]any{
		"op":             "run",
		"workspaceRoot":  root,
		"cwd":            "中文目录",
		"command":        `printf '%s\n' "你好，世界"`,
		"timeoutSeconds": 30,
		"maxOutput":      1 << 16,
		"shell":          shell,
		"targets":        []string{},
	})
	if got.ExitCode != 0 {
		t.Fatalf("non-ascii run exit=%d output=%q", got.ExitCode, got.Output)
	}
	if !strings.Contains(got.Output, "你好，世界") {
		t.Fatalf("non-ascii run output = %q, want the non-ASCII command output", got.Output)
	}
}

// TestRemoteScriptVersionGuards 锁定“能力探测”契约：远端解释器可能是
// Python 2.6/2.7，也可能是 3.x，可选 API 必须探测后降级，不得写死平台/版本
// 假设——写死就是 AttributeError 把整个 op 打挂。语法上还要保持 2.6 能解析。
func TestRemoteScriptVersionGuards(t *testing.T) {
	for _, required := range []string{
		`hasattr(os, "fchmod")`,                // Windows 直到 Python 3.13 才有
		`os.environ.get("COMSPEC")`,            // Windows 没有 /bin/sh
		`getattr(subprocess, "DEVNULL", None)`, // 3.3 以下没有
		`hasattr(os, "replace")`,               // 3.3 以下没有
		`hasattr(proc.stdout, "read1")`,        // Python 2 的 file 对象没有
		`if sys.version_info >= (3, 2):`,       // start_new_session 要 3.2+
		`sys.version_info >= (3, 3)`,           // Popen.wait(timeout=) 要 3.3+
		`if sys.version_info[0] < 3:`,          // py2 专属分支
	} {
		if !strings.Contains(remotePythonScript, required) {
			t.Errorf("remote python script lost version guard %q", required)
		}
	}
	// 2.6 解析不了 / 语义不同的写法（本地镜像测试跑的是 py3，写错了本地测不出来）。
	if regexp.MustCompile(`\bf["']`).MatchString(remotePythonScript) {
		t.Error("remote python script uses an f-string, which Python 2.6 cannot parse")
	}
	if regexp.MustCompile(`[\w)\]]\s*:=[^=]`).MatchString(remotePythonScript) {
		t.Error("remote python script uses the walrus operator, which Python 2.6 cannot parse")
	}
	if regexp.MustCompile(`\\u[0-9a-fA-F]{4}`).MatchString(remotePythonScript) {
		t.Error("remote python script contains a \\uXXXX escape: Python 2 byte-string literals do not expand it")
	}
	// 2.6/3.0 底线上不存在的标准库入口。
	for _, forbidden := range []string{"subprocess.run(", "os.scandir(", "shutil.which(", "exist_ok"} {
		if strings.Contains(remotePythonScript, forbidden) {
			t.Errorf("remote python script uses %s, unavailable on the Python 2.6/3.0 floor", forbidden)
		}
	}
}

// TestRemoteScriptHasCancelKill 锁定取消连带击杀存在：op_run 必须注册
// SIGTERM/SIGHUP/SIGINT 处理器，Go 侧取消杀掉 ssh 后远端孤儿进程被同
// 步回收。
func TestRemoteScriptHasCancelKill(t *testing.T) {
	if !strings.Contains(remotePythonScript, "_terminate_command_group") {
		t.Error("remote python script lost _terminate_command_group signal handler; cancelling a run would leak remote orphan processes")
	}
	if !strings.Contains(remotePythonScript, `for _sig_name in ("SIGTERM", "SIGHUP", "SIGINT")`) {
		t.Error("remote python script must register SIGTERM/SIGHUP/SIGINT handlers")
	}
	if !strings.Contains(remotePythonScript, `getattr(signal, _sig_name, None)`) {
		t.Error("remote python script must resolve signal names via getattr: Windows has no SIGHUP")
	}
}

// TestRemoteReadFileDuplicatePathSlots 锁定重复路径契约：同批同路径（不
// 同行区间）的每个槽位都必须各自渲染，不得只填首个槽位留零值——旧实
// 现逐槽位独立填充，批量会话重构后曾退化为只认首个槽位。通过注入假
// 会话函数避免真实 ssh，专测 Go 侧分派/回填逻辑。
func TestRemoteReadFileDuplicatePathSlots(t *testing.T) {
	a := NewApp()
	a.remoteReadBatchFn = func(ctx context.Context, rt remoteTarget, paths []string) ([]remoteReadBatchItem, []string, error) {
		if len(paths) != 1 || paths[0] != "a.txt" {
			t.Fatalf("expected deduped pending [a.txt], got %v", paths)
		}
		var it remoteReadBatchItem
		it.Path = "a.txt"
		it.OK = true
		it.Data.Path = "a.txt"
		it.Data.DataBase64 = base64.StdEncoding.EncodeToString([]byte("l1\nl2\nl3\n"))
		it.Data.Size = 12
		return []remoteReadBatchItem{it}, nil, nil
	}
	result, err := a.remoteReadFile(context.Background(), RemoteReadFileRequest{
		Target: "user@host:/srv/app",
		Files: []BatchReadFileRequest{
			{Path: "a.txt"},
			{Path: "a.txt", StartLine: 2, EndLine: 2},
			{Path: "../escape"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 3 {
		t.Fatalf("expected 3 slots, got %d", len(result.Files))
	}
	if result.Files[0].Content == "" || result.Files[1].Content == "" {
		t.Fatalf("both duplicate-path slots must have content, got %q / %q", result.Files[0].Content, result.Files[1].Content)
	}
	if !strings.Contains(result.Files[1].Content, "l2") || strings.Contains(result.Files[1].Content, "l3") {
		t.Fatalf("slot 2 should render only line range 2-2, got %q", result.Files[1].Content)
	}
	if result.Files[2].Error == "" {
		t.Fatal("escape path slot must carry per-slot validation error")
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

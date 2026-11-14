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
	"time"
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
			if runtime.GOOS == "windows" && (strings.HasSuffix(strings.ToLower(p), ".cmd") || strings.HasSuffix(strings.ToLower(p), ".bat")) {
				continue
			}
			return p
		}
	}
	// Fall back to any found interpreter
	for _, name := range names {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	t.Skip("Python interpreter not found; set ALLY_TEST_PYTHON to the desired interpreter")
	return ""
}

func TestRemoteHelperLegacyReplaceFailurePreservesOriginal(t *testing.T) {
	py := pickRemoteHelperPython(t)
	root := t.TempDir()
	target := filepath.Join(root, "file.txt")
	if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	script, err := buildRemoteScript(map[string]any{
		"op": "write", "path": remoteFixturePath(root, "file.txt"),
		"dataBase64": base64.StdEncoding.EncodeToString([]byte("replacement")), "overwrite": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Emulate legacy Windows rename semantics without source-text injection:
	// rename cannot replace an existing file, but succeeds after it is deleted.
	prelude := `import base64, errno, hashlib, json, os, stat, sys, tempfile, time
original_rename = os.rename
def legacy_rename(source, target):
    if os.path.lexists(target):
        raise OSError(errno.EACCES, 'cannot replace an existing file')
    return original_rename(source, target)
if hasattr(os, 'replace'):
    del os.replace
os.rename = legacy_rename
os.name = 'nt'
`
	wrapped := prelude + "exec(compile(base64.b64decode('" + base64.StdEncoding.EncodeToString([]byte(script)) + "'), '<ally-test>', 'exec'))\n"
	resp, err := runRemoteHelperScript(t, py, wrapped)
	if err != nil {
		t.Fatal(err)
	}
	if resp.OK {
		t.Fatal("unsafe legacy replacement must fail")
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "original" {
		t.Fatalf("failed replacement damaged original: %q, %v", got, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("unexpected temporary files: %v, %v", entries, err)
	}
}

// TestRemoteScriptTransportInvariants 锁定 payload 传输与删除拦截的关键不变量：
// 普通模式 payload 走 stdin 占位符，传输模式才读 argv；删除判定 Python 无正则。
func TestRemoteScriptTransportInvariants(t *testing.T) {
	if !strings.Contains(remotePythonScript, "TRANSFER_MODE = False") ||
		!strings.Contains(strings.ReplaceAll(remotePythonScript, "\r\n", "\n"), "encoded = PAYLOAD_B64\n    if TRANSFER_MODE:") {
		t.Error("ordinary helper mode must use the injected payload; only transfer mode may read argv")
	}
	if !strings.Contains(remotePythonScript, "__PAYLOAD_B64__") {
		t.Error("remote python script lost __PAYLOAD_B64__ placeholder")
	}
	if strings.Contains(remotePythonScript, "DELETE_RE") {
		t.Error("remote python script still contains DELETE_RE regex; deletion should be Go-side only")
	}
	if !strings.Contains(remotePythonScript, "__DELETE_SENSITIVE_TARGETS__") {
		t.Error("remote python script lost __DELETE_SENSITIVE_TARGETS__ placeholder")
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
	t.Parallel()
	py := pickRemoteHelperPython(t)
	root := t.TempDir()
	script, err := buildRemoteScript(map[string]any{
		"op": "stat", "path": remoteFixturePath(root, "."),
	})
	if err != nil {
		t.Fatalf("buildRemoteScript: %v", err)
	}
	for _, placeholder := range []string{"__PAYLOAD_B64__", "__DELETE_SENSITIVE_TARGETS__"} {
		if strings.Contains(script, placeholder) {
			t.Errorf("buildRemoteScript left placeholder %q unreplaced", placeholder)
		}
	}
	cmd := exec.Command(py, "-")
	home := t.TempDir()

	cmd.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home)
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
func runRemoteHelperScript(t *testing.T, py, script string, homes ...string) (remotePythonResponse, error) {
	t.Helper()
	cmd := exec.Command(py, "-")
	home := t.TempDir()
	if len(homes) > 0 {
		home = homes[0]
	}
	cmd.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home)
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

func TestValidateRemotePath(t *testing.T) {
	for _, tc := range []struct {
		p, want string
		root    bool
	}{{"/srv/app/main.go", "/srv/app/main.go", false}, {"/etc/nginx/nginx.conf", "/etc/nginx/nginx.conf", false}, {"/srv/app/../other/file", "/srv/other/file", false}, {"/", "/", true}} {
		got, err := validateRemotePath(tc.p, tc.root)
		if err != nil || got != tc.want {
			t.Fatalf("%s: %s, %v", tc.p, got, err)
		}
	}
	for _, p := range []string{"", ".", "relative/file", "../other/file", "/", "/tmp/bad\x00"} {
		if got, err := validateRemotePath(p, false); err == nil {
			t.Fatalf("accepted %q as %q", p, got)
		}
	}
}

// TestRemoteHelperProtectedDeleteClassification 锁定删除保护的分类与理由契约：
// 系统树（/etc、/usr 等）整体拒绝；主目录类父根（/root、/home）只拦目录
// 本身，其子树内的普通工作区文件必须放行（/root 曾被误放进树清单，导致
// root 用户远程工作区所有删除被拒）。每条拒绝还要报出真正触发的那条机制：
// 一句笼统的「系统敏感路径」会让模型归因错规则、换个路径反复重试。
func TestRemoteHelperProtectedDeleteClassification(t *testing.T) {
	t.Parallel()
	py := pickRemoteHelperPython(t)
	root := t.TempDir()
	cases := []struct {
		name      string
		path      string
		protected bool
		// reasons 是可接受的原因片段：模型侧看到的文案必须点名真正触发的那条规则。
		// 同一条路径在不同平台上可能撞上不同机制（depth 2 的名单目标在文件不存在的
		// 平台上会先撞层级规则），两者都在契约之内，所以按「命中其中之一」断言。
		reasons []string
	}{
		{"filesystem root", "/", true, []string{"filesystem root"}},
		{"root home itself", "/root", true, []string{"top-level filesystem directory"}},
		{"etc tree dir", "/etc", true, []string{"top-level filesystem directory"}},
		{"etc file", "/etc/passwd", true, []string{"protected system target", "second-level filesystem directory"}},
		{"etc direct file", "/etc/hosts", true, []string{"protected system target", "second-level filesystem directory"}},
		{"usr subtree", "/usr/bin/ls", true, []string{"protected system target"}},
		{"usr share tree", "/usr/share/doc/foo", true, []string{"protected system target"}},
		{"usr local lib tree", "/usr/local/lib/node_modules/pkg/index.js", true, []string{"protected system target"}},
		{"var local libs", "/var/local/libs", true, []string{"protected system target"}},
		{"second-level directory", "/var/log", true, []string{"second-level filesystem directory"}},
		{"temporary root itself", os.TempDir(), true, []string{"temporary directory root"}},
		{"workspace file under /root", "/root/ally-remote-test/app.py", false, nil},
		{"project file under /root", "/root/projects/tooltest/file.txt", false, nil},
		{"project file under /home", "/home/alice/project/file.txt", false, nil},
		{"deep config file in etc", "/etc/nginx/conf.d/test.conf", false, nil},
		{"tempdir child path", filepath.Join(os.TempDir(), "project", "file.txt"), false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script, err := buildRemoteScript(map[string]any{
				"op":   "_check_protected",
				"path": remoteFixturePath(root, tc.path),
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
				Protected bool   `json:"protected"`
				Reason    string `json:"reason"`
			}
			if err := json.Unmarshal(resp.Data, &data); err != nil {
				t.Fatalf("decode data: %v", err)
			}
			if data.Protected != tc.protected {
				t.Fatalf("path %s: expected protected=%v, got %v", tc.path, tc.protected, data.Protected)
			}
			if !tc.protected {
				if data.Reason != "" {
					t.Fatalf("path %s: an allowed path must carry no reason, got %q", tc.path, data.Reason)
				}
				return
			}
			if len(tc.reasons) == 0 {
				t.Fatalf("path %s: a protected case must declare the rule it expects", tc.path)
			}
			matched := false
			for _, want := range tc.reasons {
				if strings.Contains(data.Reason, want) {
					matched = true
					break
				}
			}
			if !matched {
				t.Fatalf("path %s: expected one of %q, got reason %q", tc.path, tc.reasons, data.Reason)
			}
		})
	}
}

// TestRemoteHelperStatOp 锁定新的 stat 条目：read_raw_file 对超限文件与目录
// 直接报错，因此存在性不能从失败的读取里推断，而覆盖审批与 created 报告都
// 依赖这里的元数据。
func TestRemoteHelperStatOp(t *testing.T) {
	t.Parallel()
	py := pickRemoteHelperPython(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "small.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		path   string
		exists bool
		isDir  bool
		size   int64
	}{
		{"existing file", "small.txt", true, false, 5},
		{"directory", "nested", true, true, 0},
		{"missing path", "gone.txt", false, false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script, err := buildRemoteScript(map[string]any{
				"op":   "stat",
				"path": remoteFixturePath(root, tc.path),
			})
			if err != nil {
				t.Fatalf("buildRemoteScript: %v", err)
			}
			resp, err := runRemoteHelperScript(t, py, script)
			if err != nil {
				t.Fatal(err)
			}
			if !resp.OK {
				t.Fatalf("stat op failed: %s", resp.Error)
			}
			var data struct {
				Exists bool  `json:"exists"`
				IsDir  bool  `json:"isDir"`
				Size   int64 `json:"size"`
			}
			if err := json.Unmarshal(resp.Data, &data); err != nil {
				t.Fatalf("decode data: %v", err)
			}
			if data.Exists != tc.exists || data.IsDir != tc.isDir {
				t.Fatalf("path %s: expected exists=%v isDir=%v, got exists=%v isDir=%v", tc.path, tc.exists, tc.isDir, data.Exists, data.IsDir)
			}
			if tc.exists && !tc.isDir && data.Size != tc.size {
				t.Fatalf("path %s: expected size %d, got %d", tc.path, tc.size, data.Size)
			}
		})
	}
}

// Stat must distinguish absence from permission, link-loop and storage failures.
func TestRemoteHelperStatErrors(t *testing.T) {
	t.Parallel()
	py := pickRemoteHelperPython(t)
	root := t.TempDir()
	for _, tc := range []struct {
		errno   string
		missing bool
	}{
		{"ENOENT", true}, {"ENOTDIR", true},
		{"EACCES", false}, {"EIO", false}, {"ELOOP", false},
	} {
		t.Run(tc.errno, func(t *testing.T) {
			t.Parallel()
			script, err := buildRemoteScript(map[string]any{"op": "stat", "path": remoteFixturePath(root, "probe")})
			if err != nil {
				t.Fatal(err)
			}
			// Simulate OS failures deterministically, including under administrator/root.
			hook := "import os, errno\n_original_stat = os.stat\ndef _probe_stat(path, *args, **kwargs):\n    if path.endswith('probe'):\n        raise OSError(errno." + tc.errno + ", 'simulated " + tc.errno + "')\n    return _original_stat(path, *args, **kwargs)\nos.stat = _probe_stat\n"
			script = strings.Replace(script, "from __future__ import print_function", "from __future__ import print_function\n"+hook, 1)
			resp, err := runRemoteHelperScript(t, py, script)
			if err != nil {
				t.Fatal(err)
			}
			if resp.OK != tc.missing {
				t.Fatalf("%s: ok=%v error=%q", tc.errno, resp.OK, resp.Error)
			}
			if tc.missing {
				var data struct {
					Exists bool `json:"exists"`
				}
				if err := json.Unmarshal(resp.Data, &data); err != nil || data.Exists {
					t.Fatalf("expected missing result, data=%s err=%v", resp.Data, err)
				}
			} else if !strings.Contains(resp.Error, "simulated "+tc.errno) {
				t.Fatalf("lost OS error: %q", resp.Error)
			}
		})
	}
}

// TestRemoteHelperReadBatchOp 锁定 read_batch 契约：逐文件错误隔离、结
// 果槽顺序与请求一致、预算耗尽时剩余路径进 remaining 交给下一轮会话。
func TestRemoteHelperReadBatchOp(t *testing.T) {
	t.Parallel()
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
		"op":         "read_batch",
		"paths":      remoteFixturePaths(root, []string{"a.txt", "missing.txt", "sub", "b.txt"}),
		"maxBytes":   1024,
		"totalBytes": 5,
	}
	script, err := buildRemoteScript(payload)
	if err != nil {
		t.Fatalf("buildRemoteScript: %v", err)
	}
	// Deferred files must not be opened in this round (nor encoded and discarded).
	hook := "_original_open = open\ndef open(path, *args, **kwargs):\n    if path.endswith('b.txt'):\n        raise IOError('deferred file must not be opened')\n    return _original_open(path, *args, **kwargs)\n"
	script = strings.Replace(script, "from __future__ import print_function", "from __future__ import print_function\n"+hook, 1)
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
	if len(data.Remaining) != 1 || data.Remaining[0] != remoteFixturePath(root, "b.txt") {
		t.Fatalf("remaining = %v, want [b.txt]", data.Remaining)
	}
}

func TestRemoteHelperReadBatchErrorDoesNotConsumeBudget(t *testing.T) {
	t.Parallel()
	py := pickRemoteHelperPython(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "large.txt"), []byte("large content"), 0o600); err != nil {
		t.Fatal(err)
	}
	script, err := buildRemoteScript(map[string]any{
		"op":    "read_batch",
		"paths": remoteFixturePaths(root, []string{"missing.txt", "large.txt"}), "maxBytes": 1024, "totalBytes": 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := runRemoteHelperScript(t, py, script)
	if err != nil || !resp.OK {
		t.Fatalf("read batch: %v, %s", err, resp.Error)
	}
	var data struct {
		Files     []struct{ OK bool } `json:"files"`
		Remaining []string            `json:"remaining"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Files) != 2 || !data.Files[1].OK || len(data.Remaining) != 0 {
		t.Fatalf("first readable file should use this round even after an error: %s", resp.Data)
	}
}

func TestRemoteHelperReadRefusesFIFO(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("FIFO is a POSIX file type")
	}
	py := pickRemoteHelperPython(t)
	root := t.TempDir()
	// 用系统 mkfifo 建真实 FIFO：原来注入脚本内部逻辑的写法依赖一个早已不存在
	// 的锚点，实际只测了“路径不存在”，永远测不到 S_ISREG 那一关。
	pipe := filepath.Join(root, "pipe")
	if out, err := exec.Command("mkfifo", pipe).CombinedOutput(); err != nil {
		t.Skipf("mkfifo unavailable: %v %s", err, out)
	}
	script, err := buildRemoteScript(map[string]any{
		"op": "read", "path": remoteFixturePath(root, "pipe"),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, py, "-")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "E_NOT_REGULAR_FILE") {
		t.Fatalf("FIFO read should fail immediately: %v: %s", err, out)
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

// TestRemoteHelperWriteFchmodFallback 锁定 os.fchmod 缺失时的降级路径（Windows
// 直到 Python 3.13 才提供 os.fchmod）：前置钩子把 os.fchmod 拿掉后写文件，不得
// 因 AttributeError 把整个 write 打挂，且 POSIX 上权限仍须是契约里的 0644
// （Windows 只校验降级分支不报错，权限位不被强制）。
func TestRemoteHelperWriteFchmodFallback(t *testing.T) {
	t.Parallel()
	py := pickRemoteHelperPython(t)
	root := t.TempDir()
	script, err := buildRemoteScript(map[string]any{
		"op":         "write",
		"path":       remoteFixturePath(root, "new.txt"),
		"dataBase64": base64.StdEncoding.EncodeToString([]byte("hello")),
		"overwrite":  true,
		"mkdirs":     true,
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
	t.Parallel()
	a := newIsolatedToolTestApp(t)
	a.remoteReadBatchFn = func(ctx context.Context, rt remoteTarget, paths []string) ([]remoteReadBatchItem, []string, error) {
		if len(paths) != 1 || paths[0] != "/srv/app/a.txt" {
			t.Fatalf("expected deduped pending [a.txt], got %v", paths)
		}
		var it remoteReadBatchItem
		it.Path = "/srv/app/a.txt"
		it.OK = true
		it.Data.Path = "/srv/app/a.txt"
		it.Data.DataBase64 = base64.StdEncoding.EncodeToString([]byte("l1\nl2\nl3\n"))
		it.Data.Size = 12
		return []remoteReadBatchItem{it}, nil, nil
	}
	result, err := a.remoteReadFile(context.Background(), RemoteReadFileRequest{
		Alias: "user@host",
		Files: []BatchReadFileRequest{
			{Path: "/srv/app/a.txt"},
			{Path: "/srv/app/a.txt", StartLine: 2, EndLine: 2},
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

// TestRemoteReadReusesSessionCache 锁定远端读也走会话级读缓存：同一会话里第二
// 次读同一台机器上同一区间、内容未变的文件时，正文换成 reused 说明、不再重复
// 投递；内容变了或区间不同则必须重新投递。用注入的假批量读会话避免真实 ssh
// （与 TestRemoteReadFileDuplicatePathSlots 同法）；用两个文件是为了走批量读
// 路径（单文件走 remoteReadRawOne，没有注入点），而缓存比对在包装层，两条
// 返回路径共用同一份实现。
func TestRemoteReadReusesSessionCache(t *testing.T) {
	t.Parallel()
	a := newIsolatedToolTestApp(t)
	payload := []byte("l1\nl2\nl3\n")
	a.remoteReadBatchFn = func(ctx context.Context, rt remoteTarget, paths []string) ([]remoteReadBatchItem, []string, error) {
		items := make([]remoteReadBatchItem, 0, len(paths))
		for _, p := range paths {
			var it remoteReadBatchItem
			it.Path = p
			it.OK = true
			it.Data.Path = p
			it.Data.DataBase64 = base64.StdEncoding.EncodeToString(payload)
			it.Data.Size = int64(len(payload))
			items = append(items, it)
		}
		return items, nil, nil
	}
	ctx := context.WithValue(context.Background(), sessionReadCacheContextKey{}, newSessionReadCache())
	req := RemoteReadFileRequest{
		Alias: "user@host",
		Files: []BatchReadFileRequest{{Path: "/srv/app/a.txt"}, {Path: "/srv/app/b.txt"}},
	}

	first, err := a.remoteReadFileCached(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Files) != 2 {
		t.Fatalf("expected 2 file slots, got %d", len(first.Files))
	}
	for _, f := range first.Files {
		if f.Reused || f.Content == "" {
			t.Fatalf("first remote read must return content, got %#v", f)
		}
	}

	second, err := a.remoteReadFileCached(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range second.Files {
		if !f.Reused || f.Content != "" || f.Version == "" {
			t.Fatalf("second remote read must reuse the payload and keep the version, got %#v", f)
		}
	}

	// 远端文件被改动：命中判定靠载荷哈希而不是靠键，所以必须重新投递。
	payload = []byte("l1\nl2\nl3\nl4\n")
	third, err := a.remoteReadFileCached(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range third.Files {
		if f.Reused || f.Content == "" {
			t.Fatalf("changed remote content must be sent again, got %#v", f)
		}
	}

	// 区间不同 = 键不同：先重投，再读同一区间才命中。
	rangeReq := RemoteReadFileRequest{
		Alias: "user@host",
		Files: []BatchReadFileRequest{{Path: "/srv/app/a.txt", StartLine: 2, EndLine: 2}, {Path: "/srv/app/b.txt", StartLine: 2, EndLine: 2}},
	}
	fourth, err := a.remoteReadFileCached(ctx, rangeReq)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fourth.Files {
		if f.Reused || f.Content == "" {
			t.Fatalf("a different range must be sent on its own, got %#v", f)
		}
	}
	fifth, err := a.remoteReadFileCached(ctx, rangeReq)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fifth.Files {
		if !f.Reused || f.Content != "" {
			t.Fatalf("re-reading the same remote range must reuse, got %#v", f)
		}
	}
}

// TestRemoteReadResultItemCarriesRangeForCacheKey 锁定远端条目带上行区间：读缓
// 存的键是「路径 + 区间」，条目丢了 Req 就会让同一文件的所有区间共用一把键互相
// 覆盖，跨区间去重静默失效。
func TestRemoteReadResultItemCarriesRangeForCacheKey(t *testing.T) {
	data := []byte("l1\nl2\nl3\n")
	item, err := buildRemoteReadResultItem(
		remoteRawFile{Path: "a.txt", Data: data, Size: int64(len(data))},
		BatchReadFileRequest{Path: "/srv/app/a.txt", StartLine: 2, EndLine: 2},
	)
	if err != nil {
		t.Fatal(err)
	}
	if item.Req.StartLine != 2 || item.Req.EndLine != 2 {
		t.Fatalf("remote read item must carry its range for the cache key, got %#v", item.Req)
	}
	tail, err := buildRemoteReadResultItem(
		remoteRawFile{Path: "a.txt", Data: data, Size: int64(len(data))},
		BatchReadFileRequest{Path: "a.txt", TailLines: 2},
	)
	if err != nil {
		t.Fatal(err)
	}
	if tail.Req.StartLine != -2 {
		t.Fatalf("tailLines must fold into a negative startLine for the key, got %d", tail.Req.StartLine)
	}
}

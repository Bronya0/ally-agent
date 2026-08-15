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
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func encodeUTF16LE(s string) []byte {
	units := utf16.Encode([]rune(s))
	out := make([]byte, 0, len(units)*2)
	for _, u := range units {
		out = append(out, byte(u), byte(u>>8))
	}
	return out
}

func encodeUTF16BE(s string) []byte {
	units := utf16.Encode([]rune(s))
	out := make([]byte, 0, len(units)*2)
	for _, u := range units {
		out = append(out, byte(u>>8), byte(u))
	}
	return out
}

func remoteRawPayload(data []byte) struct {
	Path       string `json:"path"`
	DataBase64 string `json:"dataBase64"`
	Size       int64  `json:"size"`
	Mode       int    `json:"mode"`
	ModTime    string `json:"modTime"`
} {
	return struct {
		Path       string `json:"path"`
		DataBase64 string `json:"dataBase64"`
		Size       int64  `json:"size"`
		Mode       int    `json:"mode"`
		ModTime    string `json:"modTime"`
	}{
		Path:       "notes.txt",
		DataBase64: base64.StdEncoding.EncodeToString(data),
		Size:       int64(len(data)),
		Mode:       0o644,
		ModTime:    "2026-01-01T00:00:00Z",
	}
}

func TestDecodeRemoteRawFileTranscodesUTF16(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"utf8 passthrough", []byte("plain utf8\n"), "plain utf8\n"},
		{"utf16le bom", append([]byte{0xFF, 0xFE}, encodeUTF16LE("hello\nworld\n")...), "hello\nworld\n"},
		{"utf16le no bom", encodeUTF16LE("line1\nline2\n"), "line1\nline2\n"},
		{"utf16le cjk", append([]byte{0xFF, 0xFE}, encodeUTF16LE("你好 world\n")...), "你好 world\n"},
		{"utf16be bom", append([]byte{0xFE, 0xFF}, encodeUTF16BE("hello\nworld\n")...), "hello\nworld\n"},
		{"utf16be no bom", encodeUTF16BE("line1\nline2\n"), "line1\nline2\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			file, err := decodeRemoteRawFile(remoteRawPayload(c.data))
			if err != nil {
				t.Fatalf("decodeRemoteRawFile(%s) error: %v", c.name, err)
			}
			if string(file.Data) != c.want {
				t.Fatalf("decodeRemoteRawFile(%s) = %q, want %q", c.name, file.Data, c.want)
			}
		})
	}
}

func TestDecodeRemoteRawFileRejectsBinary(t *testing.T) {
	cases := []struct {
		name    string
		data    []byte
		wantErr string
	}{
		{"nul bytes", []byte("ab\x00cd"), "E_BINARY_FILE"},
		{"invalid utf8", []byte{0xC3, 0x28}, "E_NOT_UTF8"},
		{"utf32le bom", []byte{0xFF, 0xFE, 0x00, 0x00, 'a', 0, 0, 0}, "E_BINARY_FILE"},
		{"utf32be bom", []byte{0x00, 0x00, 0xFE, 0xFF, 0, 0, 0, 'a'}, "E_BINARY_FILE"},
		{"odd utf16 length", []byte{0xFF, 0xFE, 0x61, 0x00, 0x62}, "E_BINARY_FILE"},
		{"probe odd utf16 length", encodeUTF16LE("abc")[:5], "E_BINARY_FILE"},
		{"decoded nul char", []byte{0xFF, 0xFE, 0x41, 0x00, 0x00, 0x00}, "E_BINARY_FILE"},
		{"binary control pairs", []byte{0x01, 0x00, 0x02, 0x00, 0x03, 0x00}, "E_BINARY_FILE"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := decodeRemoteRawFile(remoteRawPayload(c.data))
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("decodeRemoteRawFile(%s) error = %v, want containing %q", c.name, err, c.wantErr)
			}
		})
	}
}

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

// runRemoteHelperOp 把 payload 编码进脚本占位符，经 stdin 喂给本地解释器，
// 验证远端 Python 逻辑（安全判定、写目标检查等）。返回解析后的 data 或错误文本。
func runRemoteHelperOp(t *testing.T, py string, payload map[string]any) (map[string]any, string) {
	t.Helper()
	if !strings.Contains(remotePythonScript, "__PAYLOAD_B64__") {
		t.Fatal("remotePythonScript lost __PAYLOAD_B64__ placeholder")
	}
	script, err := buildRemoteScript(payload)
	if err != nil {
		t.Fatalf("build remote script: %v", err)
	}
	cmd := exec.Command(py, "-")
	cmd.Stdin = strings.NewReader(script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("run remote helper: %v; stderr: %s", err, stderr.String())
	}
	out := stdout.String()
	idx := strings.LastIndex(out, remotePythonMarker)
	if idx < 0 {
		t.Fatalf("no marker in helper output: %q", out)
	}
	var resp remotePythonResponse
	if err := json.Unmarshal([]byte(strings.TrimRight(out[idx+len(remotePythonMarker):], "\r\n")), &resp); err != nil {
		t.Fatalf("decode helper response: %v; output: %q", err, out)
	}
	if !resp.OK {
		return nil, resp.Error
	}
	var data map[string]any
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("decode helper data: %v", err)
	}
	return data, ""
}

// TestRemoteHelperProtectedDeletePaths 验证 Python 侧删除保护清单与本地
// isDangerousDeletePath 的 Linux/macOS 分支一致。
func TestRemoteHelperProtectedDeletePaths(t *testing.T) {
	py := pickRemoteHelperPython(t)
	// 保护清单直接派生自 Go 侧单一来源，锁死 Python 注入的清单与本地一致。
	protected := []string{"/"}
	protected = append(protected, remoteDeleteProtectedExactOnly()...)
	protected = append(protected, remoteDeleteProtectedTrees()...)
	// 特殊分支：home 根、/home/* 与 /Users/* 父目录、树内路径
	protected = append(protected, "/home/alice", "/Users/alice", "/var/www", "/srv/app")
	notProtected := []string{
		"/home/alice/project", "/Users/alice/project", "/tmp", "/tmp/foo", "/srv-other",
	}
	for _, p := range protected {
		data, errStr := runRemoteHelperOp(t, py, map[string]any{
			"op": "_check_protected", "workspaceRoot": t.TempDir(), "path": p,
		})
		if errStr != "" {
			t.Fatalf("check %q: helper error: %s", p, errStr)
		}
		if data["protected"] != true {
			t.Errorf("is_protected_delete_path(%q) = false, want true", p)
		}
	}
	for _, p := range notProtected {
		data, errStr := runRemoteHelperOp(t, py, map[string]any{
			"op": "_check_protected", "workspaceRoot": t.TempDir(), "path": p,
		})
		if errStr != "" {
			t.Fatalf("check %q: helper error: %s", p, errStr)
		}
		if data["protected"] == true {
			t.Errorf("is_protected_delete_path(%q) = true, want false", p)
		}
	}
}

// TestRemoteHelperWriteTargetsOutsideRoot 验证 Python 侧写目标越界检查
// 与本地 command 的 E_PATH_OUTSIDE 策略一致：已存在的越界目标拦截，
// 不存在的越界新路径、工作区内目标、/dev/null 与动态目标放行。
func TestRemoteHelperWriteTargetsOutsideRoot(t *testing.T) {
	py := pickRemoteHelperPython(t)
	root := t.TempDir()
	outsideDir := filepath.Join(filepath.Dir(root), "outside-"+filepath.Base(root))
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(outsideDir)
	outsideExisting := filepath.Join(outsideDir, "victim.txt")
	if err := os.WriteFile(outsideExisting, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	inRoot := filepath.Join(root, "existing.txt")
	if err := os.WriteFile(inRoot, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		cwd     string
		targets []string
		wantErr bool
	}{
		{"inside existing", ".", []string{inRoot}, false},
		{"inside missing", ".", []string{filepath.Join(root, "new.txt")}, false},
		{"outside existing", ".", []string{outsideExisting}, true},
		{"outside missing", ".", []string{filepath.Join(outsideDir, "new.txt")}, false},
		{"devnull", ".", []string{"/dev/null"}, false},
		{"dynamic skipped", ".", []string{"$HOME/x"}, false},
		{"relative inside", "sub", []string{"f.txt"}, false},
		{"relative escape existing", "sub", []string{filepath.Join("..", "..", "outside-"+filepath.Base(root), "victim.txt")}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, errStr := runRemoteHelperOp(t, py, map[string]any{
				"op": "_check_write_targets", "workspaceRoot": root, "cwd": c.cwd, "targets": c.targets,
			})
			if c.wantErr && !strings.Contains(errStr, "E_PATH_OUTSIDE") {
				t.Fatalf("want E_PATH_OUTSIDE, got error: %q", errStr)
			}
			if !c.wantErr && errStr != "" {
				t.Fatalf("unexpected error: %q", errStr)
			}
		})
	}
	// 符号链接父目录逃逸（新文件）：词法在工作区内、realpath 逃到工作区外，
	// 即使目标文件不存在也必须拦截（对齐本地 inspectCommandMutationTarget）。
	t.Run("symlink parent new file escape", func(t *testing.T) {
		link := filepath.Join(root, "link")
		if err := os.Symlink(outsideDir, link); err != nil {
			t.Skipf("cannot create symlink on this platform: %v", err)
		}
		_, errStr := runRemoteHelperOp(t, py, map[string]any{
			"op": "_check_write_targets", "workspaceRoot": root, "cwd": ".",
			"targets": []string{"link/new.txt"},
		})
		if !strings.Contains(errStr, "E_PATH_OUTSIDE") {
			t.Fatalf("want E_PATH_OUTSIDE for symlink escape, got error: %q", errStr)
		}
	})
}

// TestRemoteHelperWriteReportsCreatedDirs 验证 op_write 在 mkdirs 时返回
// 实际缺失的父目录链（相对 root、外层→内层），父目录已存在时为空。
func TestRemoteHelperWriteReportsCreatedDirs(t *testing.T) {
	py := pickRemoteHelperPython(t)
	root := t.TempDir()
	payload := func(path string) map[string]any {
		return map[string]any{
			"op": "write", "workspaceRoot": root, "path": path,
			"dataBase64": "", "overwrite": false, "mkdirs": true,
		}
	}
	assertDirs := func(path string, want []string) {
		t.Helper()
		data, errStr := runRemoteHelperOp(t, py, payload(path))
		if errStr != "" {
			t.Fatalf("write %s: helper error: %s", path, errStr)
		}
		got, _ := data["createdDirs"].([]any)
		if len(got) != len(want) {
			t.Fatalf("write %s: createdDirs=%v, want %v", path, got, want)
		}
		for i, w := range want {
			if got[i] != w {
				t.Fatalf("write %s: createdDirs=%v, want %v", path, got, want)
			}
		}
	}
	// 父目录全部缺失：报告完整链，外层→内层。
	assertDirs("a/b/c.txt", []string{"a", "a/b"})
	// 父目录已存在：链为空。
	assertDirs("a/b/d.txt", []string{})
	assertDirs("a/e.txt", []string{})
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

// TestBuildRemoteScriptInjectsListsAndPayload 验证 buildRemoteScript 把删除
// 清单与 payload 占位符全部替换，且产物能用真实解释器跑通。
func TestBuildRemoteScriptInjectsListsAndPayload(t *testing.T) {
	py := pickRemoteHelperPython(t)
	script, err := buildRemoteScript(map[string]any{
		"op":            "list",
		"workspaceRoot": t.TempDir(),
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

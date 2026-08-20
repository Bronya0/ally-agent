// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"os/exec"
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

// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWorkspaceMapFoldingAndSize 验证：目录直接子项超过预算后折叠为
// "+N more files"，文件显示大小，目录节点保留。
func TestWorkspaceMapFoldingAndSize(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 60; i++ {
		name := "f" + string(rune('a'+i/26)) + string(rune('a'+i%26))
		if err := os.WriteFile(filepath.Join(sub, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx := buildWorkspaceMapContext(root)
	if !strings.Contains(ctx, "+10 more files") {
		t.Fatalf("expected fold placeholder, got:\n%s", ctx)
	}
	if !strings.Contains(ctx, "sub/") {
		t.Fatalf("expected sub dir node, got:\n%s", ctx)
	}
	if !strings.Contains(ctx, "1 B") {
		t.Fatalf("expected file size display, got:\n%s", ctx)
	}
}

// TestWorkspaceMapRootPlaceholderIndent 验证根目录直接子项超预算时，
// 折叠占位以 0 级缩进显示（不缩进、不跑到错误层级）。
func TestWorkspaceMapRootPlaceholderIndent(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 60; i++ {
		name := "file" + string(rune('a'+i/26)) + string(rune('a'+i%26))
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx := buildWorkspaceMapContext(root)
	if !strings.Contains(ctx, "\n- +10 more files\n") {
		t.Fatalf("expected root fold placeholder line, got:\n%s", ctx)
	}
}

// TestWorkspaceMapExcludesSensitiveEnv 验证 .env/.env.* 敏感文件被排除，
// .env.example/.sample/.template 模板保留（与原 isWorkspaceMapSensitiveFile
// 语义一致，rg 路径和 walkdir 回退都应满足）。
func TestWorkspaceMapExcludesSensitiveEnv(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{".env", ".env.local", ".env.production", ".env.example", ".env.sample", ".env.template"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("SECRET=1"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx := buildWorkspaceMapContext(root)
	for _, secret := range []string{".env.local", ".env.production"} {
		if strings.Contains(ctx, secret) {
			t.Fatalf("sensitive %s must be excluded, got:\n%s", secret, ctx)
		}
	}
	if strings.Contains(ctx, "\n- .env ") {
		t.Fatalf("sensitive .env must be excluded, got:\n%s", ctx)
	}
	for _, keep := range []string{".env.example", ".env.sample", ".env.template"} {
		if !strings.Contains(ctx, keep) {
			t.Fatalf("template %s must be kept, got:\n%s", keep, ctx)
		}
	}
}

// TestWorkspaceMapZeroByteFileShowsSize 验证 0 字节文件也显示大小（"0 B"）。
func TestWorkspaceMapZeroByteFileShowsSize(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "empty.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := buildWorkspaceMapContext(root)
	if !strings.Contains(ctx, "empty.txt  0 B") {
		t.Fatalf("expected 0 B size display, got:\n%s", ctx)
	}
}

// TestWorkspaceMapDeepDirsPruned 验证超过 maxDepth 的目录节点不插入
// （深层目录链不会吃光全局配额），但 maxDepth 内的目录节点保留。
func TestWorkspaceMapDeepDirsPruned(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b", "c", "d", "e") // depth 5 > maxDepth 3
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := buildWorkspaceMapContext(root)
	for _, want := range []string{"a/", "b/", "c/"} {
		if !strings.Contains(ctx, want) {
			t.Fatalf("expected dir node %s, got:\n%s", want, ctx)
		}
	}
	// 只检查树条目行（头部说明文本含 "ignored/heavy" 等，不能全文匹配）。
	for _, line := range strings.Split(ctx, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- d/") || strings.HasPrefix(trimmed, "- e/") {
			t.Fatalf("deep dir node must not appear, got line %q", line)
		}
	}
}

// TestWorkspaceMapGlobalLimitStillHolds 验证全局 320 硬停仍然生效：
// 多个大目录叠加后条目超限，map 标记 truncated=true（折叠递增不得绕过硬停）。
func TestWorkspaceMapGlobalLimitStillHolds(t *testing.T) {
	root := t.TempDir()
	for d := 0; d < 7; d++ {
		dir := filepath.Join(root, "dir"+string(rune('0'+d)))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 60; i++ {
			name := "f" + string(rune('a'+i/26)) + string(rune('a'+i%26))
			if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	ctx := buildWorkspaceMapContext(root)
	if !strings.Contains(ctx, "truncated=true") {
		t.Fatalf("expected truncated=true under global limit, got:\n%s", ctx)
	}
}

// TestFormatMapFileSize 验证大小格式：B/KB/MB 边界。
func TestFormatMapFileSize(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{500, "500 B"},
		{2048, "2 KB"},
		{3 * 1024 * 1024, "3.0 MB"},
	}
	for _, c := range cases {
		if got := formatMapFileSize(c.in); got != c.want {
			t.Errorf("formatMapFileSize(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

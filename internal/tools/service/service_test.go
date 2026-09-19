// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package service

import (
	"testing"
	"unicode/utf8"
)

// TestPromotedServiceName 锁定晋升服务命名行为：取命令前两个 token，
// 大小写归一，空白折叠；过短命令不越界。
func TestPromotedServiceName(t *testing.T) {
	cases := []struct {
		command string
		want    string
	}{
		{"npm run dev -- --host", "npm run"},
		{"python manage.py runserver 0.0.0.0:8000", "python manage.py"},
		{"  Vite  dev  ", "vite dev"},
		{"go run .", "go run"},
		{"sleep", "sleep"},
		{"", ""},
		{"   ", ""},
	}
	for _, c := range cases {
		if got := PromotedServiceName(c.command); got != c.want {
			t.Errorf("PromotedServiceName(%q) = %q, want %q", c.command, got, c.want)
		}
	}
}

// TestTailStringKeepsRuneBoundaries 锁定截尾的字符边界契约：按字节切会让尾巴以
// 半个多字节字符开头，进请求 JSON 时变成 U+FFFD（中文日志 3 字节/字，约 2/3 的
// 切割点都落在字符中间）。非 UTF-8 输入（GBK 日志、二进制）必须原样返回字节。
func TestTailStringKeepsRuneBoundaries(t *testing.T) {
	// "abc" + 4 个汉字 = 15 字节；截 10 字节正好落在第 2 个字中间。
	zh := "abc中文中文"
	tail := TailString(zh, 10)
	if !utf8.ValidString(tail) {
		t.Fatalf("tail must stay valid UTF-8, got %q", tail)
	}
	if tail != "文中文" {
		t.Fatalf("expected the partial rune to be dropped, got %q", tail)
	}
	if len(tail) > 10 {
		t.Fatalf("tail must not exceed the limit, got %d bytes", len(tail))
	}

	// 不需要回退的情况保持原有的按字节语义。
	if got := TailString("abcdef", 3); got != "def" {
		t.Fatalf("ascii tail changed: %q", got)
	}
	if got := TailString("abc", 3); got != "abc" {
		t.Fatalf("short input must pass through: %q", got)
	}
	if got := TailString("abc", 0); got != "abc" {
		t.Fatalf("non-positive limit must pass through: %q", got)
	}
	if got := TailString("中文中文", 6); got != "中文" {
		t.Fatalf("an aligned cut must be untouched: %q", got)
	}

	// 非 UTF-8（GBK 的「中文」= D6 D0 CE C4）：原样返回，字节不丢。
	gbk := string([]byte{0xD6, 0xD0, 0xCE, 0xC4, 0xD6, 0xD0})
	if got, want := TailString(gbk, 4), string([]byte{0xCE, 0xC4, 0xD6, 0xD0}); got != want {
		t.Fatalf("non-UTF-8 tail must keep its bytes: got % x want % x", got, want)
	}
}

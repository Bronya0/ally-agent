// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package service

import "testing"

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

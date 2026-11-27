// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package grep

import (
	"path/filepath"
	"testing"
)

func TestPointIntoExcludedDir(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "proj")
	outside := filepath.Join(t.TempDir(), "other")
	distNamed := filepath.Join(t.TempDir(), "dist", "proj")

	cases := []struct {
		name   string
		root   string
		search string
		want   bool
	}{
		{name: "工作区根本身", root: workspace, search: workspace},
		{name: "普通父目录", root: workspace, search: filepath.Join(workspace, "frontend")},
		{name: "子目录里的普通目录", root: workspace, search: filepath.Join(workspace, "src", "internal")},
		{name: "依赖目录本身", root: workspace, search: filepath.Join(workspace, "node_modules"), want: true},
		{name: "依赖目录之下", root: workspace, search: filepath.Join(workspace, "frontend", "node_modules", "pkg"), want: true},
		{name: "venv 之下", root: workspace, search: filepath.Join(workspace, "svc", ".venv", "lib"), want: true},
		{name: "组件名大小写不一致", root: workspace, search: filepath.Join(workspace, "Node_Modules"), want: true},
		{name: "工作区自己叫 dist", root: distNamed, search: filepath.Join(distNamed, "src")},
		{name: "工作区外只认叶子名", root: workspace, search: filepath.Join(outside, "node_modules", "pkg")},
		{name: "工作区外的依赖目录本身", root: workspace, search: filepath.Join(outside, "node_modules"), want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PointIntoExcludedDir(tc.root, tc.search); got != tc.want {
				t.Fatalf("PointIntoExcludedDir(%q, %q) = %v, want %v", tc.root, tc.search, got, tc.want)
			}
		})
	}
}

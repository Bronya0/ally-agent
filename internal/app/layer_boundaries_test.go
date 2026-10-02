// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

// 分层边界只有测试守得住：AGENTS.md 那几条规则编译器不管，而包内的 biz_/orch_/
// prov_/host_/infra_ 只是命名约定（同一个包，谁都能调谁，这也正是它们需要文档约束的
// 原因）。真正跨目录的两条边界破坏起来没有任何症状，所以钉在这里：
//
//  1. internal/ 下除 internal/app 外的所有包（tools、sandbox、builtin_skills …）不许
//     import internal/app：纯算法层要能脱离 App 状态单测，反向依赖一出现这条前提就没了。
//  2. internal/app 里除 host_*.go 外的实现文件不许 import Wails runtime：宿主中立，
//     只有 host_*.go 与根目录 main.go 能碰窗口/事件/原生 API。

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestLayerBoundaries(t *testing.T) {
	const (
		appImport    = "ally-dev/internal/app"
		wailsRuntime = "github.com/wailsapp/wails/v3/"
	)
	root := filepath.Join("..", "..")

	var violations []string
	walkErr := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		relative = filepath.ToSlash(relative)
		imports, parseErr := importPaths(path)
		if parseErr != nil {
			return parseErr
		}
		inApp := strings.HasPrefix(relative, "internal/app/")
		for _, imported := range imports {
			switch {
			case !inApp && imported == appImport:
				violations = append(violations, fmt.Sprintf("%s import 了 %s（纯算法层不许反向依赖 app）", relative, imported))
			case inApp && !strings.HasPrefix(filepath.Base(relative), "host_") && strings.HasPrefix(imported, wailsRuntime):
				violations = append(violations, fmt.Sprintf("%s import 了 %s（宿主中立：只有 host_*.go 能碰 Wails）", relative, imported))
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("遍历 internal/ 失败：%v", walkErr)
	}
	if len(violations) > 0 {
		t.Fatalf("分层边界被打破：\n  %s", strings.Join(violations, "\n  "))
	}
}

// importPaths 只解析 import 段（ImportsOnly），比整份解析快得多。
func importPaths(path string) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		return nil, fmt.Errorf("解析 %s 失败：%w", path, err)
	}
	imports := make([]string, 0, len(file.Imports))
	for _, spec := range file.Imports {
		if unquoted, unquoteErr := strconv.Unquote(spec.Path.Value); unquoteErr == nil {
			imports = append(imports, unquoted)
		}
	}
	return imports, nil
}

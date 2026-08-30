// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

// Knowledge-base run policy: when a run's workspace resolves to the
// user-configured KB root (ConfigState.KBRoot), the sources/ subtree holds
// original documents that the model must treat as read-only. This file owns
// the mode detection, the per-run deny-root propagation (context key), and
// the deny checks consumed by the mutation tool cases in executeTool.

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"ally-dev/internal/tools/command"
	"ally-dev/internal/tools/pathutil"
)

// kbSourcesDirName is the read-only original-materials subdirectory inside a
// knowledge-base root.
const kbSourcesDirName = "sources"

// kbDenyRootsContextKey carries the per-run model-write deny roots (absolute
// paths). runChat attaches it for knowledge-base runs so every executeTool
// call site (main loop, sub-agents) inherits the same policy through the
// context without signature changes.
type kbDenyRootsContextKey struct{}

func withKBDenyRoots(ctx context.Context, roots []string) context.Context {
	if len(roots) == 0 {
		return ctx
	}
	return context.WithValue(ctx, kbDenyRootsContextKey{}, roots)
}

func kbDenyRoots(ctx context.Context) []string {
	if roots, ok := ctx.Value(kbDenyRootsContextKey{}).([]string); ok {
		return roots
	}
	return nil
}

// isKnowledgeBaseWorkspace reports whether a run's workspace resolves to the
// configured knowledge-base root. This is the single KB-mode definition shared
// by the prompt builder and the deny policy; the frontend uses the same rule
// when it points a KB session's config.workspace at the configured root.
func isKnowledgeBaseWorkspace(workspaceRoot, kbRoot string) bool {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	kbRoot = strings.TrimSpace(kbRoot)
	if workspaceRoot == "" || kbRoot == "" {
		return false
	}
	return pathutil.SamePath(workspaceRoot, kbRoot)
}

// kbDenyRootsForConfig returns the deny roots for a knowledge-base run — the
// sources/ subtree in both lexical and symlink-resolved form — or nil when
// cfg is not a KB workspace.
func kbDenyRootsForConfig(cfg ConfigState) []string {
	if !isKnowledgeBaseWorkspace(cfg.Workspace, cfg.KBRoot) {
		return nil
	}
	workspaceAbs, err := filepath.Abs(cfg.Workspace)
	if err != nil {
		return nil
	}
	lexical := filepath.Join(workspaceAbs, kbSourcesDirName)
	roots := []string{lexical}
	resolvedBase := workspaceAbs
	if resolved, err := filepath.EvalSymlinks(workspaceAbs); err == nil {
		resolvedBase = resolved
	}
	resolvedRoot := filepath.Join(resolvedBase, kbSourcesDirName)
	if resolved, err := filepath.EvalSymlinks(lexical); err == nil {
		resolvedRoot = resolved
	}
	if !pathutil.SamePath(resolvedRoot, lexical) {
		roots = append(roots, resolvedRoot)
	}
	return roots
}

// kbPathDenied reports whether target (absolute path) falls inside a deny
// root. Existing targets are symlink-resolved first so a link pointing into
// or out of sources/ is judged by its real location.
func kbPathDenied(denyRoots []string, target string) bool {
	if len(denyRoots) == 0 || strings.TrimSpace(target) == "" {
		return false
	}
	cleaned := filepath.Clean(target)
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		cleaned = resolved
	}
	return pathutil.InsideAnyRoot(denyRoots, cleaned)
}

// kbDenyError is the coded model-facing rejection for a denied KB write.
func kbDenyError(path string) error {
	return codedToolError("E_KB_SOURCES_READONLY", fmt.Errorf("安全围栏已拦截：知识库原始资料目录 sources/ 对模型只读。\n被拒绝的目标：%s\n原因：sources/ 保存原始文档，模型只能读取和引用，不能创建、修改或删除其中的任何内容。\n处理方式：把提炼后的内容写成知识库条目（sources/ 之外的分类目录），并在条目 frontmatter 的 source 字段里引用原始文档路径。", filepath.ToSlash(path)))
}

// kbDenyCheckPaths is the executeTool pre-check for path-taking mutation
// tools (edit/create/delete, http_request saveTo). Relative paths resolve
// against the run's workspace roots exactly like the executors resolve them;
// paths that fail workspace-boundary resolution are left to the executors to
// reject with their own coded errors.
func kbDenyCheckPaths(ctx context.Context, cfg ConfigState, paths ...string) error {
	denyRoots := kbDenyRoots(ctx)
	if len(denyRoots) == 0 {
		return nil
	}
	roots, err := workspaceRoots(cfg)
	if err != nil {
		return nil
	}
	for _, p := range paths {
		if strings.TrimSpace(p) == "" {
			continue
		}
		resolved, err := pathutil.SafeJoin(pathRuntime, roots, p)
		if err != nil {
			continue
		}
		if kbPathDenied(denyRoots, resolved) {
			return kbDenyError(resolved)
		}
	}
	return nil
}

// checkKBDenyTargets blocks literal shell write targets inside the deny
// roots. It mirrors firstExistingOutsideMutationTarget's iteration over
// command.LiteralWriteTargets; dynamically resolved targets (variables,
// globs, command substitutions) stay permitted, consistent with the existing
// permissive policy for unresolvable targets.
func checkKBDenyTargets(commandLine string, workingDir string, denyRoots []string) error {
	if len(denyRoots) == 0 || strings.TrimSpace(workingDir) == "" {
		return nil
	}
	for _, target := range command.LiteralWriteTargets(commandLine) {
		path, ok := command.ResolveCommandLiteralPath(target.Path, workingDir)
		if !ok {
			continue
		}
		if kbPathDenied(denyRoots, path) {
			return kbDenyError(path)
		}
	}
	return nil
}

// kbDenyCheckCommand is the shell-content deny check for tools that execute a
// command line (command / service start). It resolves the default working
// directory the same way runCommandWithConfig does.
func kbDenyCheckCommand(ctx context.Context, cfg ConfigState, commandLine string) error {
	denyRoots := kbDenyRoots(ctx)
	if len(denyRoots) == 0 {
		return nil
	}
	roots, err := workspaceRoots(cfg)
	if err != nil {
		return nil
	}
	workingDir := ""
	if len(roots) > 0 {
		workingDir = roots[0]
	}
	return checkKBDenyTargets(commandLine, workingDir, denyRoots)
}

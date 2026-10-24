// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

// Section 5: Memory (was memory.go)
// App-owned thin wrapper that injects *App as the memory.Runtime and re-exports
// the memory tool's DTOs so existing app-level callers (executeTool,
// prompt_builder) keep referencing the same types. The orchestration logic
// (list/read/write, path resolution, index cache) lives in
// internal/tools/memory.

import (
	"ally-dev/internal/tools/memory"
)

// Re-export the memory tool's DTOs still used by app-level code
// (prompt_builder cache lookup) without importing the tool package at every
// call site. The memory_read/memory_write tools are removed; only the index
// listing types remain.
type (
	MemoryIndexEntry = memory.IndexEntry
	MemoryListResult = memory.ListResult
)

// memoryIndexCache is the app-level handle for the memory tool's process-wide
// index cache. It delegates to memory.IndexCache so the system prompt builder
// shares one cache without the tool package depending on App.
var memoryIndexCache = memory.IndexCache

// memoriesRuntime returns *App as a memory.Runtime. The interface is satisfied
// structurally by MemoriesDir(); emit is not needed by the memory tool itself.
func (a *App) memoriesRuntime() memory.Runtime { return a }

// listMemories delegates to the memory tool with this App as the Runtime. The
// package-level aGlobalApp singleton it used to read is gone: prompt assembly
// and the memory tool both carry their own App, so parallel tests cannot see
// each other's memory directory.
func (a *App) listMemories() (MemoryListResult, error) {
	return memory.List(a.memoriesRuntime())
}

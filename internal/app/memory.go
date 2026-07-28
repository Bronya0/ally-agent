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

// Re-export the memory tool's DTOs so app-level code (executeTool dispatch,
// prompt_builder cache lookup, frontend Wails bindings) keeps referencing the
// same types without importing the tool package at every call site.
type (
	MemoryIndexEntry   = memory.IndexEntry
	MemoryListResult   = memory.ListResult
	MemoryReadRequest  = memory.ReadRequest
	MemoryReadResult   = memory.ReadResult
	MemoryWriteRequest = memory.WriteRequest
	MemoryWriteResult  = memory.WriteResult
)

// memoryIndexCache is the app-level handle for the memory tool's process-wide
// index cache. It delegates to memory.IndexCache so the system prompt builder
// and memoryWrite share one cache without the tool package depending on App.
var memoryIndexCache = memory.IndexCache

// memoriesRuntime returns *App as a memory.Runtime. The interface is satisfied
// structurally by MemoriesDir(); emit is not needed by the memory tool itself.
func (a *App) memoriesRuntime() memory.Runtime { return a }

// listMemories delegates to the memory tool with App as the Runtime.
func listMemories() (MemoryListResult, error) {
	return memory.List(aGlobalApp.memoriesRuntime())
}

// aGlobalApp is set in NewApp so package-level helpers that predate the
// Runtime injection (listMemories, memoryIndexCache usage in prompt_builder)
// can reach the active App without a parameter change. It is nil before NewApp
// and after shutdown; callers must guard accordingly.
var aGlobalApp *App

// resolveMemoryPath delegates to the memory tool with the global App.
func resolveMemoryPath(p string) (string, error) {
	return memory.ResolvePath(aGlobalApp.memoriesRuntime(), p)
}

func defaultMemoryPath(description string) string {
	return memory.DefaultPath(description)
}

func (a *App) memoryRead(req MemoryReadRequest) (MemoryReadResult, error) {
	return memory.Read(a.memoriesRuntime(), req)
}

func (a *App) memoryWrite(req MemoryWriteRequest) (MemoryWriteResult, error) {
	return memory.Write(a.memoriesRuntime(), req)
}

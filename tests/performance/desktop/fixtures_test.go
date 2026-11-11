//go:build ally_performance

// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"encoding/json"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// These files are overlaid into desktop by tests/performance/run.py. They are
// deliberately excluded from normal package discovery and production builds.
type performanceConfig struct {
	Sizes              []int `json:"sizes"`
	StressRows         int   `json:"stress_rows"`
	SearchRows         int   `json:"search_rows"`
	Pages              int   `json:"pages"`
	PayloadCount       int   `json:"payload_count"`
	PayloadBytes       int   `json:"payload_bytes"`
	Cycles             int   `json:"cycles"`
	OwnershipBatches   int   `json:"ownership_batches"`
	CodeSizes          []int `json:"code_sizes"`
	SnapshotQueueRows  int   `json:"snapshot_queue_rows"`
	SnapshotQueueCount int   `json:"snapshot_queue_count"`
}

// The suite has one fixed workload, with no environment or command-line modes.
func performanceOptions(_ testing.TB) performanceConfig {
	return performanceConfig{
		Sizes: []int{1000, 10000, 100000}, StressRows: 30000, SearchRows: 100000,
		Pages: 160, PayloadCount: 128, PayloadBytes: 1 << 20, Cycles: 12,
		OwnershipBatches: 80, CodeSizes: []int{32 << 10, 256 << 10, 1 << 20},
		SnapshotQueueRows: 10000, SnapshotQueueCount: 8,
	}
}

func performanceTranscript(n, textBytes int) *conversation {
	conv := &conversation{ID: "performance-synthetic", Messages: make([]map[string]any, 0, n+2048)}
	body := strings.Repeat("plain text ", (textBytes+10)/11)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("call-%d", i)
		var m map[string]any
		switch i % 10 {
		case 0, 1:
			role := "user"
			if i%10 == 1 {
				role = "assistant"
			}
			text := fmt.Sprintf("record-%d %s", i, body)
			if i%1000 == 1 {
				text += " needle_marker"
			}
			m = map[string]any{"role": role, "content": text}
		case 8:
			m = map[string]any{"role": "tool_call", "name": "subagent", "toolCallId": id, "status": "success", "result": map[string]any{"agentId": id}}
		case 9:
			m = map[string]any{"role": "subagent", "subId": fmt.Sprintf("call-%d", i-1), "content": "child report"}
		default:
			m = map[string]any{"role": "tool_call", "name": "read", "toolCallId": id, "status": "success", "args": `{"path":"synthetic.go"}`, "result": map[string]any{"ok": true, "files": []any{map[string]any{"path": "synthetic.go", "startLine": 1, "endLine": 10}}}}
		}
		conv.Messages = append(conv.Messages, m)
	}
	return conv
}

// GC is a measurement boundary, never a proposed production scheduling policy.
func performanceHeap() runtime.MemStats {
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m
}

func performanceMetric(tb testing.TB, scenario string, values map[string]any) {
	tb.Helper()
	values["scenario"] = scenario
	encoded, err := json.Marshal(values)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Logf("PERF_METRIC %s", encoded)
}

func performanceChatTester(conv *conversation, pageSize int) *ui.Tester {
	a := &desktop{prefs: defaultPrefs(), page: "chat", tabs: []*conversation{conv}}
	a.prefs.HistoryPageSize = pageSize
	conv.uiState().Follow = false
	return ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		ui.Column(c).Fill().Children(func() { a.chatMessages(c, conv, 1000) })
	}, 1000, 700)
}

// Each call creates independent backing storage. Original backend evidence
// exists only during ingestion; the measured fixture retains just the UI copy.
func performanceIngestTool(a *desktop, conv *conversation, name string, i, size int) {
	body := strings.Repeat("中", size/3)
	data := map[string]any{"content": body, "output": body, "elapsedMs": 10, "files": []any{map[string]any{"path": "synthetic.txt", "content": body}}}
	payload := map[string]any{"name": name, "toolCallId": fmt.Sprintf("large-%d", i), "result": map[string]any{"ok": true, "data": data}}
	if name == "create" || name == "remote_create_file" {
		args, _ := json.Marshal(map[string]any{"path": "synthetic.txt", "content": body})
		payload["args"] = string(args)
		payload["result"] = map[string]any{"ok": true, "path": "synthetic.txt"}
	}
	projected := toolEventForDisplay("tool:result", payload).(map[string]any)
	a.applyToolEvent(conv, "tool:result", projected)
	if len(str(data, "content")) != len(body) {
		panic("display projection changed backend evidence")
	}
}

func performanceToolNames() []string {
	return []string{"mcp__synthetic__query", "read", "remote_read", "command", "create", "remote_create_file"}
}

func performanceToolIsCreate(name string) bool {
	return name == "create" || name == "remote_create_file"
}

func performanceSizes(tb testing.TB) []int { return slices.Clone(performanceOptions(tb).Sizes) }

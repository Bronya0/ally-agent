//go:build ally_performance

// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"encoding/json"
	"fmt"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

func performanceCheckOwnership(t *testing.T, conv *conversation) {
	t.Helper()
	full, got := newTranscriptRows(conv), conv.cachedTranscriptRows()
	if !reflect.DeepEqual(got.children, full.children) || !reflect.DeepEqual(got.nested, full.nested) {
		t.Fatalf("incremental ownership disagrees with a full rebuild at %d records", len(conv.Messages))
	}
	want := make([]int, 0)
	for i := range conv.Messages {
		if full.startsBlock(i) {
			want = append(want, i)
		}
	}
	want = append(want, len(conv.Messages))
	if !slices.Equal(conv.blockStarts(), want) {
		t.Fatalf("pagination disagrees with ownership at %d records", len(conv.Messages))
	}
}

func TestPerformanceHistoryStress(t *testing.T) {
	cfg := performanceOptions(t)
	conv := performanceTranscript(cfg.StressRows, 96)
	performanceCheckOwnership(t, conv)
	for batch := 0; batch < cfg.OwnershipBatches; batch++ {
		id := fmt.Sprintf("stress-parent-%d", batch)
		child := map[string]any{"role": "subagent", "subId": id, "content": "child"}
		parent := map[string]any{"role": "tool_call", "name": "subagent", "toolCallId": id, "result": map[string]any{"agentId": id}}
		if batch%3 == 0 {
			conv.Messages = append(conv.Messages, child)
			_ = conv.blockStarts()
			conv.Messages = append(conv.Messages, parent)
		} else {
			conv.Messages = append(conv.Messages, parent, child)
		}
		for i := 0; i < 8; i++ {
			conv.Messages = append(conv.Messages, map[string]any{"role": "tool_call", "name": "read", "toolCallId": fmt.Sprintf("extra-%d-%d", batch, i)})
		}
		if batch%10 == 0 {
			performanceCheckOwnership(t, conv)
		} else {
			_ = conv.cachedTranscriptRows()
		}
	}
	performanceCheckOwnership(t, conv)
	conv.Messages[8]["result"] = map[string]any{"agentId": "stress-parent-3"}
	conv.markHistoryAt(8)
	performanceCheckOwnership(t, conv)
	keep := cfg.StressRows / 2
	clear(conv.Messages[keep:])
	conv.Messages = conv.Messages[:keep]
	conv.resetTranscriptLookup()
	conv.markHistory()
	for i := 0; i < 100; i++ {
		conv.Messages = append(conv.Messages, map[string]any{"role": "assistant", "content": "after deletion"})
	}
	performanceCheckOwnership(t, conv)
	// Same-length replacement also has to invalidate ownership and pagination.
	conv.Messages[len(conv.Messages)-1] = map[string]any{"role": "tool_call", "name": "read", "toolCallId": "replaced"}
	conv.markHistory()
	performanceCheckOwnership(t, conv)
	replaceTranscript(conv, "compacted")
	performanceCheckOwnership(t, conv)
	performanceMetric(t, "history_stress", map[string]any{"initial_records": cfg.StressRows, "batches": cfg.OwnershipBatches})
}

func TestPerformanceSearchStress(t *testing.T) {
	cfg := performanceOptions(t)
	for _, n := range []int{1000, cfg.SearchRows} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			conv, a := performanceTranscript(n, 256), &desktop{}
			s := &conv.uiState().Search
			s.Query = "needle_marker"
			check := func() {
				t.Helper()
				s.lastCompute, s.frameKey = time.Time{}, ""
				got := a.chatSearchMatches(conv)
				want, truncated := chatSearchComputeBounded(conv, &chatSearch{Query: s.Query, Regexp: s.Regexp})
				if !slices.Equal(got, want) || s.scanTruncated != truncated {
					t.Fatalf("cached search differs: matches=%d/%d truncated=%t/%t", len(got), len(want), s.scanTruncated, truncated)
				}
			}
			check()
			for i := 0; i < 200; i++ {
				a.applyToolEvent(conv, "tool:start", map[string]any{"toolCallId": fmt.Sprintf("search-extra-%d", i), "name": "read"})
				if i%25 == 24 {
					check()
				}
			}
			conv.Messages = append(conv.Messages, map[string]any{"role": "assistant", "content": "tail needle_marker"})
			invalidateChatSearch(conv)
			check()
			conv.Messages[len(conv.Messages)-1]["content"] = "tail needle_marker" + strings.Repeat(" unicode 中 ", 30000)
			invalidateChatSearchTail(conv)
			check()
			conv.Messages[0]["content"] = "edited prefix needle_marker"
			invalidateChatSearch(conv)
			check()
			s.Query, s.Regexp = "needle_[a-z]+", true
			check()
			performanceMetric(t, "search_stress", map[string]any{"records": len(conv.Messages), "matches": len(s.cache), "scanned_bytes": s.scanBytes, "truncated": s.scanTruncated})
		})
	}
}

func TestPerformanceRenderPageCaches(t *testing.T) {
	cfg := performanceOptions(t)
	rows := slices.Max(cfg.Sizes)
	conv := performanceTranscript(rows, 96)
	tt := performanceChatTester(conv, 80)
	st := conv.uiState()
	st.Search.Open, st.Search.Query = true, "needle_marker"
	starts := slices.Clone(conv.blockStarts())
	seen := map[*ui.Handle]bool{}
	var warmed runtime.MemStats
	for page := 0; page < cfg.Pages; page++ {
		block := (page * 337) % (len(starts) - 81)
		st.VisibleStart, st.Follow = starts[block]+1, false
		conv.Scroll.Y = 0
		tt.Frame()
		tt.Frame()
		if len(conv.MD) > 80 || len(conv.MDCodeBlock) > 80 || len(st.visibleRows) > 80 {
			t.Fatalf("page %d kept off-page derived data", page)
		}
		for _, handles := range []map[int]*ui.Handle{st.PromptHandles, st.Search.handles} {
			for _, h := range handles {
				seen[h] = true
			}
		}
		for _, h := range st.transcriptHandlePool {
			seen[h] = true
		}
		if len(seen) > 80 {
			t.Fatalf("navigation accumulated %d distinct handles", len(seen))
		}
		for i := range conv.MD {
			if i < st.windowStart || i >= st.windowEnd {
				t.Fatal("off-page Markdown retained")
			}
		}
		for key := range st.Search.codeCache {
			if key.Message < st.windowStart || key.Message >= st.windowEnd {
				t.Fatal("off-page code search cache retained")
			}
		}
		if st.Search.frameDoc != nil {
			t.Fatal("parser document survived the frame")
		}
		if page == cfg.Pages/4-1 {
			warmed = performanceHeap()
		}
	}
	live := performanceHeap()
	performanceMetric(t, "page_churn", map[string]any{"records": rows, "pages": cfg.Pages, "page_size": 80, "unique_handles": len(seen), "markdown_options": len(conv.MD), "warm_heap_bytes": warmed.HeapAlloc, "live_heap_bytes": live.HeapAlloc, "growth_bytes": int64(live.HeapAlloc) - int64(warmed.HeapAlloc)})
	starts = nil
	replaceTranscript(conv, "small summary")
	tt.Frame()
	tt.Frame()
	if len(conv.Messages) != 1 || st.Stream != nil || len(st.CommandDetails) != 0 || len(conv.MD) > 1 || len(conv.messageLookup.tools) != 0 {
		t.Fatal("compaction kept retired display state")
	}
	compacted := performanceHeap()
	performanceMetric(t, "compact_release", map[string]any{"live_heap_bytes": live.HeapAlloc, "compacted_heap_bytes": compacted.HeapAlloc, "released_bytes": int64(live.HeapAlloc) - int64(compacted.HeapAlloc)})
	runtime.KeepAlive(tt)
	runtime.KeepAlive(conv)
}

func TestPerformanceToolsRetention(t *testing.T) {
	cfg := performanceOptions(t)
	for _, name := range performanceToolNames() {
		t.Run(name, func(t *testing.T) {
			// Warm format/encoding pools before the live-heap boundary.
			func() { performanceIngestTool(&desktop{}, &conversation{}, name, 0, 4096) }()
			before := performanceHeap()
			conv, a := &conversation{ID: "large-results"}, &desktop{}
			for i := 0; i < cfg.PayloadCount; i++ {
				performanceIngestTool(a, conv, name, i, cfg.PayloadBytes)
			}
			after := performanceHeap()
			encoded, err := json.Marshal(snapshot(conv))
			if err != nil {
				t.Fatal(err)
			}
			if len(encoded) > cfg.PayloadCount*(9<<10) {
				t.Fatalf("UI snapshot retained unnecessary large bodies: %d bytes", len(encoded))
			}
			if performanceToolIsCreate(name) {
				for _, m := range conv.Messages {
					var args map[string]any
					if err := json.Unmarshal([]byte(str(m, "args")), &args); err != nil {
						t.Fatal(err)
					}
					if s := str(args, "content"); len(s) > 8<<10 || !utf8.ValidString(s) {
						t.Fatal("create preview exceeded its UTF-8 byte budget")
					}
				}
			}
			retained := int64(after.HeapAlloc) - int64(before.HeapAlloc)
			performanceMetric(t, "tool_retention", map[string]any{"tool": name, "input_bytes": cfg.PayloadCount * (cfg.PayloadBytes / 3 * 3), "retained_heap_bytes": retained, "object_delta": int64(after.HeapObjects) - int64(before.HeapObjects), "snapshot_bytes": len(encoded), "allocated_bytes": after.TotalAlloc - before.TotalAlloc})
			// A generous safety guard catches retained backing strings, not normal
			// GC/pool noise. Exact heap/timing deltas remain diagnostic metrics.
			if retained > int64(8<<20)+int64(cfg.PayloadCount*(9<<10)) {
				t.Fatalf("tool UI retained %.2f MiB after GC", float64(retained)/(1<<20))
			}
			runtime.KeepAlive(conv)
		})
	}
}

func TestPerformanceToolsEvidenceAndTerminalStates(t *testing.T) {
	body := strings.Repeat("synthetic body\n", 4096)
	for _, result := range []map[string]any{
		{"ok": false, "error": "denied", "errorCode": "DENIED", "elapsedMs": 123, "content": body},
		{"reason": "user_cancelled", "content": body},
		{"ok": true, "durationMs": 456, "content": body},
	} {
		original := map[string]any{"name": "mcp__synthetic__query", "toolCallId": "state", "result": map[string]any{"data": result}}
		before, _ := json.Marshal(original)
		projected := toolEventForDisplay("tool:result", original).(map[string]any)
		conv := &conversation{}
		(&desktop{}).applyToolEvent(conv, "tool:result", projected)
		got := conv.Messages[0]
		want := map[string]any{"status": "success", "result": result}
		if toolState(got) != toolState(want) || toolDuration(got, toolResult(got)) != toolDuration(nil, result) {
			t.Fatal("projection changed terminal state or duration")
		}
		after, _ := json.Marshal(original)
		if !slices.Equal(before, after) {
			t.Fatal("projection mutated original backend evidence")
		}
	}
}

func TestPerformanceSnapshotIsolation(t *testing.T) {
	conv := performanceTranscript(performanceOptions(t).SnapshotQueueRows, 128)
	saved := snapshot(conv)
	if len(saved.Messages) != len(conv.Messages) {
		t.Fatal("snapshot lost history")
	}
	saved.Messages[0]["content"] = "changed snapshot"
	if str(conv.Messages[0], "content") == "changed snapshot" {
		t.Fatal("snapshot shares live message maps")
	}
	files := list(toolResult(saved.Messages[2]), "files")
	files[0].(map[string]any)["path"] = "changed snapshot"
	if str(list(toolResult(conv.Messages[2]), "files")[0].(map[string]any), "path") == "changed snapshot" {
		t.Fatal("snapshot shares nested live result maps")
	}
}

func TestPerformanceSnapshotQueue(t *testing.T) {
	cfg := performanceOptions(t)
	conv := performanceTranscript(cfg.SnapshotQueueRows, 256)
	_ = conv.blockStarts()
	base := performanceHeap()
	queued := make([]backend.SessionSnapshot, cfg.SnapshotQueueCount)
	for i := range queued {
		queued[i] = snapshot(conv)
	}
	live := performanceHeap()
	if len(queued[len(queued)-1].Messages) != len(conv.Messages) {
		t.Fatal("queued snapshot lost records")
	}
	performanceMetric(t, "snapshot_queue", map[string]any{"records": cfg.SnapshotQueueRows, "queued": cfg.SnapshotQueueCount, "extra_heap_bytes": int64(live.HeapAlloc) - int64(base.HeapAlloc)})
	runtime.KeepAlive(queued)
	queued = nil
	drained := performanceHeap()
	performanceMetric(t, "snapshot_queue_drained", map[string]any{"extra_heap_bytes": int64(drained.HeapAlloc) - int64(base.HeapAlloc)})
	runtime.KeepAlive(conv)
}

func performanceLifecycleCycle(n int) {
	conv := performanceTranscript(n, 128)
	_ = conv.blockStarts()
	conv.indexTranscriptTail()
	conv.uiState().Search.Query = "needle_marker"
	_ = (&desktop{}).chatSearchMatches(conv)
	_ = snapshot(conv)
	replaceTranscript(conv, "summary")
	_ = conv.blockStarts()
	runtime.KeepAlive(conv)
}

func TestPerformanceLifecycleRelease(t *testing.T) {
	cfg := performanceOptions(t)
	performanceLifecycleCycle(cfg.SnapshotQueueRows)
	before := performanceHeap()
	for i := 0; i < cfg.Cycles; i++ {
		performanceLifecycleCycle(cfg.SnapshotQueueRows)
	}
	after := performanceHeap()
	performanceMetric(t, "lifecycle_release", map[string]any{"cycles": cfg.Cycles, "records_per_cycle": cfg.SnapshotQueueRows, "retained_heap_bytes": int64(after.HeapAlloc) - int64(before.HeapAlloc), "object_delta": int64(after.HeapObjects) - int64(before.HeapObjects), "allocated_bytes": after.TotalAlloc - before.TotalAlloc})
}

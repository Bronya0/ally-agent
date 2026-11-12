//go:build ally_performance

// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

var performanceSnapshotSink backend.SessionSnapshot
var performanceIntSink int
var performanceStringSink string

func BenchmarkPerformanceHistoryCached(b *testing.B) {
	for _, n := range performanceSizes(b) {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			conv := performanceTranscript(n, 96)
			_ = conv.blockStarts()
			b.ReportAllocs()
			b.ResetTimer()
			// Batch tiny lookups for useful timer precision. ns/op refers to the
			// batch; ns/lookup is the single-call metric used by the report.
			const lookups = 10000
			for i := 0; i < b.N; i++ {
				for j := 0; j < lookups; j++ {
					performanceIntSink = len(conv.blockStarts())
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*lookups), "ns/lookup")
		})
	}
}

func BenchmarkPerformanceHistoryRebuild(b *testing.B) {
	for _, n := range performanceSizes(b) {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			conv := performanceTranscript(n, 96)
			_ = conv.blockStarts()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				conv.markHistory()
				performanceIntSink = len(conv.blockStarts())
			}
		})
	}
}

func BenchmarkPerformanceHistoryParentAppend(b *testing.B) {
	for _, n := range performanceSizes(b) {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			conv := performanceTranscript(n, 96)
			_ = conv.blockStarts()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				id := fmt.Sprintf("bench-parent-%d", i)
				conv.Messages = append(conv.Messages, map[string]any{"role": "tool_call", "name": "subagent", "toolCallId": id}, map[string]any{"role": "subagent", "parentToolCallId": id})
				performanceIntSink = len(conv.blockStarts())
			}
		})
	}
}

func BenchmarkPerformanceSearchToolAppend(b *testing.B) {
	for _, n := range performanceSizes(b) {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			conv, a := performanceTranscript(n, 96), &desktop{}
			s := &conv.uiState().Search
			s.Query = "needle_marker"
			_ = a.chatSearchMatches(conv)
			conv.indexTranscriptTail()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				a.applyToolEvent(conv, "tool:start", map[string]any{"name": "read", "toolCallId": fmt.Sprintf("bench-search-%d", i)})
				s.lastCompute, s.frameKey = time.Time{}, ""
				performanceIntSink = len(a.chatSearchMatches(conv))
			}
		})
	}
}

func BenchmarkPerformanceSearchCold(b *testing.B) {
	for _, n := range performanceSizes(b) {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			conv := performanceTranscript(n, 256)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				matches, _ := chatSearchComputeBounded(conv, &chatSearch{Query: "absent_needle_marker"})
				performanceIntSink = len(matches)
			}
		})
	}
}

func BenchmarkPerformanceSearchTruncatedAppend(b *testing.B) {
	n := performanceOptions(b).SearchRows
	// Ensure this fixture exceeds the search budget, not just the row count.
	textBytes := max(256, (2*chatSearchMaxBytes)/(n/5)+1)
	conv, a := performanceTranscript(n, textBytes), &desktop{}
	s := &conv.uiState().Search
	s.Query = "absent_needle_marker"
	_ = a.chatSearchMatches(conv)
	conv.indexTranscriptTail()
	if !s.scanTruncated {
		b.Fatal("fixture must exceed the search byte budget")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.applyToolEvent(conv, "tool:start", map[string]any{"name": "read", "toolCallId": fmt.Sprintf("truncated-append-%d", i)})
		s.lastCompute, s.frameKey = time.Time{}, ""
		performanceIntSink = len(a.chatSearchMatches(conv))
	}
}

func BenchmarkPerformanceSnapshotBuild(b *testing.B) {
	defer func() { performanceSnapshotSink = backend.SessionSnapshot{} }()
	for _, n := range performanceSizes(b) {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			conv := performanceTranscript(n, 256)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				performanceSnapshotSink = snapshot(conv)
			}
		})
	}
}

func BenchmarkPerformanceRenderStablePage(b *testing.B) {
	for _, n := range performanceSizes(b) {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			conv := performanceTranscript(n, 96)
			tt := performanceChatTester(conv)
			tt.Frame()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tt.Frame()
			}
		})
	}
}

func BenchmarkPerformanceRenderOpenCodeStream(b *testing.B) {
	for _, size := range performanceOptions(b).CodeSizes {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			var stream strings.Builder
			stream.Grow(size + 32*1024)
			stream.WriteString("```go\n" + strings.Repeat("var value = 123 // synthetic code\n", size/34))
			source := stream.String()
			a := &desktop{prefs: defaultPrefs()}
			options := a.readingMarkdownOptionsForSource(func() string { return source })
			a.configureReadingMarkdown(options)
			tt := ui.NewTester(func(c *ui.Context) {
				c.SetTheme(a.theme())
				c.DisableAnimations()
				ui.Scroll(c).Fill().Children(func() { markdown.View(c, source, options).FillWidth() })
			}, 1000, 700)
			tt.Frame()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				stream.WriteString("// appended\n")
				source = stream.String()
				tt.Frame()
			}
		})
	}
}

func BenchmarkPerformanceToolsMCPEvent(b *testing.B) {
	for _, size := range []int{64 << 10, performanceOptions(b).PayloadBytes} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			body := strings.Repeat("x", size)
			payload := map[string]any{"name": "mcp__synthetic__query", "toolCallId": "mcp-event", "result": map[string]any{"ok": true, "data": map[string]any{"content": body, "elapsedMs": 10}}}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				wire, err := json.Marshal(toolEventForDisplay("tool:result", payload))
				if err != nil {
					b.Fatal(err)
				}
				performanceIntSink = len(wire)
			}
		})
	}
}

func BenchmarkPerformanceToolsCreatePreview(b *testing.B) {
	for _, size := range []int{64 << 10, performanceOptions(b).PayloadBytes} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			body := strings.Repeat("中", size/3)
			args, _ := json.Marshal(map[string]any{"path": "synthetic.txt", "content": body})
			encoded := string(args)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m := map[string]any{"name": "create", "status": "success", "args": encoded}
				retainCreateDisplayPreview(m)
				performanceStringSink = str(m, "args")
			}
		})
	}
}

func BenchmarkPerformanceLifecycleCycle(b *testing.B) {
	for _, n := range performanceSizes(b) {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				performanceLifecycleCycle(n)
			}
		})
	}
}

func BenchmarkPerformanceToolsProgressEvent(b *testing.B) {
	for _, n := range performanceSizes(b) {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			conv, a := performanceTranscript(n, 96), &desktop{}
			a.applyToolEvent(conv, "tool:start", map[string]any{"name": "read", "toolCallId": "progress"})
			conv.indexTranscriptTail()
			payload := map[string]any{"name": "read", "toolCallId": "progress", "args": `{"path":"synthetic.go"}`}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				a.applyToolEvent(conv, "tool:update", payload)
			}
		})
	}
}

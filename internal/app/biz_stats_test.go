package app

import (
	"math"
	"testing"
	"time"
)

func TestGetTokenStatsAggregatesDimensions(t *testing.T) {
	app := NewApp()
	now := time.Now()
	previousDay := now.AddDate(0, 0, -1)

	records := []statsRecord{
		{
			Provider: "OpenAI", Model: "gpt-5", Workspace: "/workspace/a", SessionID: "session-a", Source: "main",
			Ts: now.UnixMilli(), InputTokens: 1000, OutputTokens: 200, CacheHitTokens: 600, CacheMissTokens: 400, Requests: 1,
		},
		{
			Provider: "OpenAI", Model: "gpt-5", Workspace: "/workspace/a", SessionID: "session-a", Source: "subagent",
			Ts: now.UnixMilli(), InputTokens: 500, OutputTokens: 100, CacheHitTokens: 200, CacheMissTokens: 300, Requests: 1,
		},
		{
			Provider: "Anthropic", Model: "claude", Workspace: "/workspace/b", SessionID: "scheduled:daily", Source: "scheduled",
			Ts: previousDay.UnixMilli(), InputTokens: 300, OutputTokens: 50, CacheHitTokens: 0, CacheMissTokens: 300, Requests: 1,
		},
	}
	for _, record := range records {
		app.stats.record(record)
	}

	result := app.GetTokenStats(7)
	if !result.OK {
		t.Fatalf("GetTokenStats() error = %q", result.Error)
	}
	if result.TotalInputTokens != 1800 || result.TotalOutputTokens != 350 || result.TotalRequests != 3 {
		t.Fatalf("totals = input %d output %d requests %d", result.TotalInputTokens, result.TotalOutputTokens, result.TotalRequests)
	}
	if result.UniqueSessions != 2 || result.ActiveDays != 2 {
		t.Fatalf("unique sessions/active days = %d/%d, want 2/2", result.UniqueSessions, result.ActiveDays)
	}
	wantCacheRate := float64(800) / float64(1800)
	if math.Abs(result.CacheHitRate-wantCacheRate) > 0.000001 {
		t.Fatalf("cache hit rate = %f, want %f", result.CacheHitRate, wantCacheRate)
	}
	if len(result.ByProvider) != 2 || result.ByProvider[0].Name != "OpenAI" {
		t.Fatalf("providers = %#v", result.ByProvider)
	}
	if len(result.ByModel) != 2 || result.ByModel[0].Name != "gpt-5" || result.ByModel[0].Requests != 2 {
		t.Fatalf("models = %#v", result.ByModel)
	}
	if len(result.ByWorkspace) != 2 || result.ByWorkspace[0].Name != "/workspace/a" {
		t.Fatalf("workspaces = %#v", result.ByWorkspace)
	}
	if len(result.BySource) != 3 {
		t.Fatalf("sources = %#v", result.BySource)
	}
	if len(result.ByDay) != 7 || len(result.ByHour) != 24 {
		t.Fatalf("bucket lengths = days %d hours %d", len(result.ByDay), len(result.ByHour))
	}

	dayRequests := 0
	for _, day := range result.ByDay {
		dayRequests += day.Requests
	}
	if dayRequests != 3 {
		t.Fatalf("daily requests = %d, want 3", dayRequests)
	}
	hourRequests := 0
	for _, hour := range result.ByHour {
		hourRequests += hour.Requests
	}
	if hourRequests != 3 {
		t.Fatalf("hourly requests = %d, want 3", hourRequests)
	}
}

func TestStatsRecorderQueueNeverBlocksWhenFull(t *testing.T) {
	recorder := newStatsRecorder()
	for i := 0; i < statsQueueSize; i++ {
		recorder.record(statsRecord{Model: "model", InputTokens: 1})
	}
	if got := len(recorder.queue); got != statsQueueSize {
		t.Fatalf("queue length = %d, want %d", got, statsQueueSize)
	}

	done := make(chan struct{})
	go func() {
		recorder.record(statsRecord{Model: "dropped", InputTokens: 1})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("record blocked while telemetry queue was full")
	}
	if got := len(recorder.queue); got != statsQueueSize {
		t.Fatalf("queue length after drop = %d, want %d", got, statsQueueSize)
	}
}

func TestRecordTokenStatsUsesFallbackAndClassifiesScheduled(t *testing.T) {
	app := NewApp()
	app.recordTokenStats("provider", "model", "/workspace", "scheduled:task-1", "subagent", nil, 120, 30)
	result := app.GetTokenStats(1)
	if result.TotalInputTokens != 120 || result.TotalOutputTokens != 30 {
		t.Fatalf("fallback totals = %d/%d, want 120/30", result.TotalInputTokens, result.TotalOutputTokens)
	}
	if len(result.BySource) != 1 || result.BySource[0].Name != "scheduled" {
		t.Fatalf("sources = %#v, want scheduled", result.BySource)
	}
}

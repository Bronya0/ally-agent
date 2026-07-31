package app

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
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

func TestStatsRecorderStopFlushesPendingRecords(t *testing.T) {
	dir := t.TempDir()
	recorder := newStatsRecorder()
	recorder.storageDir = dir
	recorder.start(context.Background())
	now := time.Now()
	recorder.record(statsRecord{Model: "model", Ts: now.UnixMilli(), InputTokens: 42, Requests: 1})
	if err := recorder.stop(2 * time.Second); err != nil {
		t.Fatalf("stop() error = %v", err)
	}

	date := statsDateKey(now.UnixMilli())
	records, err := readStatsDayFile(filepath.Join(dir, date+".json"), date)
	if err != nil {
		t.Fatalf("read flushed stats: %v", err)
	}
	if len(records) != 1 || records[0].InputTokens != 42 {
		t.Fatalf("flushed records = %#v", records)
	}
}

func TestReadStatsDayFileFiltersInvalidRecords(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	date := statsDateKey(now.UnixMilli())
	path := filepath.Join(dir, date+".json")
	records := []statsRecord{
		{Model: "valid", Ts: now.UnixMilli(), InputTokens: 5, Requests: 1},
		{Model: "negative", Ts: now.UnixMilli(), InputTokens: -1, Requests: 1},
		{Model: "oversized", Ts: now.UnixMilli(), InputTokens: statsMaxTokensPerRecord + 1, Requests: 1},
		{Model: "wrong-day", Ts: now.AddDate(0, 0, -1).UnixMilli(), InputTokens: 1, Requests: 1},
	}
	data, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := readStatsDayFile(path, date)
	if err != nil {
		t.Fatalf("readStatsDayFile() error = %v", err)
	}
	if len(loaded) != 1 || loaded[0].Model != "valid" {
		t.Fatalf("loaded records = %#v", loaded)
	}
}

func TestReadStatsDayFileRejectsOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversized.json")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(statsMaxFileBytes + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := readStatsDayFile(path, "2026-01-01"); err == nil {
		t.Fatal("expected oversized stats file to be rejected")
	}
}

func TestRecoverStatsBackupsRestoresMissingDayFile(t *testing.T) {
	dir := t.TempDir()
	backup := filepath.Join(dir, "2026-01-01.json.bak")
	if err := os.WriteFile(backup, []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}
	recoverStatsBackups(dir)
	if _, err := os.Stat(filepath.Join(dir, "2026-01-01.json")); err != nil {
		t.Fatalf("restored day file: %v", err)
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Fatalf("backup should be consumed, stat error = %v", err)
	}
}

func TestRecoverStatsBackupsReplacesCorruptDestination(t *testing.T) {
	dir := t.TempDir()
	date := "2026-01-01"
	dst := filepath.Join(dir, date+".json")
	backup := dst + ".bak"
	if err := os.WriteFile(dst, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backup, []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}
	recoverStatsBackups(dir)
	if _, err := readStatsDayFile(dst, date); err != nil {
		t.Fatalf("recovered destination: %v", err)
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Fatalf("backup should be consumed, stat error = %v", err)
	}
}

func TestStatsRecorderRejectsRecordsAfterStop(t *testing.T) {
	recorder := newStatsRecorder()
	if err := recorder.stop(time.Second); err != nil {
		t.Fatalf("stop() error = %v", err)
	}
	recorder.record(statsRecord{Model: "late", InputTokens: 1})
	if got := len(recorder.queue); got != 0 {
		t.Fatalf("queue length after stop = %d, want 0", got)
	}
}

func TestDropOldestStatsRecord(t *testing.T) {
	days := map[string][]statsRecord{
		"2026-01-01": {{Model: "old-1"}, {Model: "old-2"}},
		"2026-01-02": {{Model: "new"}},
	}
	if dropped := dropOldestStatsRecord(days); dropped != "2026-01-01" {
		t.Fatalf("dropped date = %q", dropped)
	}
	if len(days["2026-01-01"]) != 1 || days["2026-01-01"][0].Model != "old-2" {
		t.Fatalf("remaining oldest day = %#v", days["2026-01-01"])
	}
}

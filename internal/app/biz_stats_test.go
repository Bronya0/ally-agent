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

func TestGetTokenStatsAggregatesDashboard(t *testing.T) {
	app := NewApp()
	now := time.Now()

	records := []statsRecord{
		{
			Model: "gpt-5", Workspace: "/workspace/a",
			Ts: now.UnixMilli(), InputTokens: 1000, OutputTokens: 200, CacheHitTokens: 600, CacheMissTokens: 400, Requests: 1,
		},
		{
			Model: "gpt-5", Workspace: "/workspace/a",
			Ts: now.Add(-10 * time.Minute).UnixMilli(), InputTokens: 500, OutputTokens: 100, CacheHitTokens: 200, CacheMissTokens: 300, Requests: 1,
		},
		{
			Model: "claude", Workspace: "/workspace/b",
			Ts: now.Add(-20 * time.Minute).UnixMilli(), InputTokens: 300, OutputTokens: 50, CacheHitTokens: 0, CacheMissTokens: 300, Requests: 1,
		},
	}
	for _, record := range records {
		app.stats.record(record)
	}

	result := app.GetTokenStats()
	if !result.OK {
		t.Fatalf("GetTokenStats() error = %q", result.Error)
	}
	if result.SummaryToday.TotalTokens != 2150 || result.SummaryToday.Requests != 3 {
		t.Fatalf("today summary = %#v", result.SummaryToday)
	}
	if result.Summary7Days.TotalTokens != 2150 || result.SummaryMonth.TotalTokens != 2150 {
		t.Fatalf("week/month summary = %#v / %#v", result.Summary7Days, result.SummaryMonth)
	}
	if result.SummaryToday.InputTokens != 1800 || result.SummaryToday.OutputTokens != 350 {
		t.Fatalf("today input/output = %d/%d", result.SummaryToday.InputTokens, result.SummaryToday.OutputTokens)
	}
	wantCacheRate := float64(800) / float64(1800)
	if math.Abs(result.SummaryToday.CacheHitRate-wantCacheRate) > 0.000001 {
		t.Fatalf("today cache hit rate = %f, want %f", result.SummaryToday.CacheHitRate, wantCacheRate)
	}
	if result.SummaryToday.AvgPerRequest != 2150/3 {
		t.Fatalf("avg per request = %d, want %d", result.SummaryToday.AvgPerRequest, 2150/3)
	}
	if len(result.Daily) != statsDailyRangeDays {
		t.Fatalf("daily length = %d, want %d", len(result.Daily), statsDailyRangeDays)
	}
	last := result.Daily[len(result.Daily)-1]
	if last.Date != now.Format("2006-01-02") || last.InputTokens != 1800 || last.OutputTokens != 350 || last.Requests != 3 {
		t.Fatalf("last daily bucket = %#v", last)
	}
	// Workspace names are shortened to the last path segment.
	if len(result.WorkspaceWeek) != 2 || result.WorkspaceWeek[0].Name != "a" || result.WorkspaceWeek[0].FullName != "/workspace/a" {
		t.Fatalf("workspace week = %#v", result.WorkspaceWeek)
	}
	if result.WorkspaceWeek[0].InputTokens != 1500 || result.WorkspaceWeek[0].OutputTokens != 300 {
		t.Fatalf("workspace week totals = %#v", result.WorkspaceWeek[0])
	}
	if len(result.WorkspaceMonth) != 2 || result.WorkspaceMonth[1].Name != "b" {
		t.Fatalf("workspace month = %#v", result.WorkspaceMonth)
	}
	if len(result.ModelWeek) != 2 || result.ModelWeek[0].Name != "gpt-5" || result.ModelWeek[0].Requests != 2 {
		t.Fatalf("model week = %#v", result.ModelWeek)
	}
	if len(result.ModelMonth) != 2 || result.ModelMonth[1].Name != "claude" {
		t.Fatalf("model month = %#v", result.ModelMonth)
	}
}

func TestBuildStatsDailyBucketsAcrossDays(t *testing.T) {
	start := time.Date(2026, 1, 10, 0, 0, 0, 0, time.Local)
	records := []statsRecord{
		{Ts: start.AddDate(0, 0, 1).Add(3 * time.Hour).UnixMilli(), InputTokens: 100, OutputTokens: 10, Requests: 1},
		{Ts: start.AddDate(0, 0, 1).Add(4 * time.Hour).UnixMilli(), InputTokens: 50, OutputTokens: 5, Requests: 1},
		{Ts: start.AddDate(0, 0, 3).Add(2 * time.Hour).UnixMilli(), InputTokens: 20, OutputTokens: 2, Requests: 1},
	}
	daily := buildStatsDaily(records, start, 7)
	if len(daily) != 7 {
		t.Fatalf("daily length = %d, want 7", len(daily))
	}
	if daily[1].InputTokens != 150 || daily[1].OutputTokens != 15 || daily[1].Requests != 2 {
		t.Fatalf("day 1 bucket = %#v", daily[1])
	}
	if daily[3].InputTokens != 20 || daily[3].OutputTokens != 2 {
		t.Fatalf("day 3 bucket = %#v", daily[3])
	}
	if daily[0].Requests != 0 || daily[2].Requests != 0 {
		t.Fatalf("zero-filled buckets expected: %#v / %#v", daily[0], daily[2])
	}
}

func TestRecordTokenStatsUsesFallback(t *testing.T) {
	app := NewApp()
	app.recordTokenStats("model", "/workspace", nil, 120, 30)
	result := app.GetTokenStats()
	if result.SummaryToday.InputTokens != 120 || result.SummaryToday.OutputTokens != 30 {
		t.Fatalf("fallback totals = %d/%d, want 120/30", result.SummaryToday.InputTokens, result.SummaryToday.OutputTokens)
	}
	if len(result.ModelWeek) != 1 || result.ModelWeek[0].Name != "model" {
		t.Fatalf("model week = %#v, want model", result.ModelWeek)
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

// TestReadStatsDayFileAcceptsLegacyFields verifies that day files written by
// the previous schema (with Provider/SessionID/Source fields) still decode:
// encoding/json ignores unknown fields, and the simplified record keeps only
// the fields the dashboard needs.
func TestReadStatsDayFileAcceptsLegacyFields(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	date := statsDateKey(now.UnixMilli())
	path := filepath.Join(dir, date+".json")
	legacy := []map[string]any{
		{
			"provider": "OpenAI", "model": "gpt-5", "workspace": "/ws/a",
			"sessionId": "s-1", "source": "main",
			"ts": now.UnixMilli(), "inputTokens": 100, "outputTokens": 20,
			"cacheHitTokens": 60, "cacheMissTokens": 40, "requests": 1,
		},
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := readStatsDayFile(path, date)
	if err != nil {
		t.Fatalf("readStatsDayFile(legacy) error = %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("loaded legacy records = %#v", loaded)
	}
	record := loaded[0]
	if record.Model != "gpt-5" || record.Workspace != "/ws/a" ||
		record.InputTokens != 100 || record.OutputTokens != 20 ||
		record.CacheHitTokens != 60 || record.CacheMissTokens != 40 || record.Requests != 1 {
		t.Fatalf("legacy record fields = %#v", record)
	}
}

func TestGetTokenStatsEmptyFreshInstall(t *testing.T) {
	app := NewApp()
	result := app.GetTokenStats()
	if !result.OK {
		t.Fatalf("GetTokenStats() error = %q", result.Error)
	}
	if result.SummaryToday.TotalTokens != 0 || result.SummaryMonth.TotalTokens != 0 ||
		result.SummaryToday.Requests != 0 || len(result.Daily) != statsDailyRangeDays ||
		len(result.WorkspaceWeek) != 0 || len(result.ModelWeek) != 0 {
		t.Fatalf("empty result = %#v", result)
	}
}

// TestStatsWindowsMonthStartCoverage simulates querying on the 31st of a
// 31-day month: the monthly window starts Aug 1, earlier than the 30-day bar
// window (Aug 2), and the snapshot must cover Aug 1 so monthly summary and
// month pies include day 1.
func TestStatsWindowsMonthStartCoverage(t *testing.T) {
	query := time.Date(2026, 8, 31, 12, 0, 0, 0, time.Local)
	midnight, monthStart, _, dailyStart, snapshotStart := statsWindows(query)
	if midnight.Day() != 31 {
		t.Fatalf("midnight = %v", midnight)
	}
	if monthStart.Format("2006-01-02") != "2026-08-01" {
		t.Fatalf("monthStart = %v", monthStart)
	}
	if dailyStart.Format("2006-01-02") != "2026-08-02" {
		t.Fatalf("dailyStart = %v, want 2026-08-02", dailyStart)
	}
	if snapshotStart.Format("2006-01-02") != "2026-08-01" {
		t.Fatalf("snapshotStart = %v, want 2026-08-01 (must cover month start)", snapshotStart)
	}
}

// TestStatsWindowsMondayStart verifies the week window starts on Monday,
// matching the heatmap's Monday-first layout.
func TestStatsWindowsMondayStart(t *testing.T) {
	// 2026-08-17 is a Monday.
	monday := time.Date(2026, 8, 17, 10, 0, 0, 0, time.Local)
	_, _, weekStart, _, _ := statsWindows(monday)
	if weekStart.Format("2006-01-02") != "2026-08-17" {
		t.Fatalf("weekStart on Monday = %v, want 2026-08-17", weekStart)
	}
	// 2026-08-19 is a Wednesday: week start is still Monday the 17th.
	wednesday := time.Date(2026, 8, 19, 10, 0, 0, 0, time.Local)
	_, _, weekStart, _, _ = statsWindows(wednesday)
	if weekStart.Format("2006-01-02") != "2026-08-17" {
		t.Fatalf("weekStart on Wednesday = %v, want 2026-08-17", weekStart)
	}
	// 2026-08-23 is a Sunday: week start is the previous Monday (Aug 17).
	sunday := time.Date(2026, 8, 23, 10, 0, 0, 0, time.Local)
	_, _, weekStart, _, _ = statsWindows(sunday)
	if weekStart.Format("2006-01-02") != "2026-08-17" {
		t.Fatalf("weekStart on Sunday = %v, want 2026-08-17", weekStart)
	}
}

func TestWorkspaceDisplayNameEdges(t *testing.T) {
	cases := map[string]string{
		"":               statsUnknownName,
		"   ":            statsUnknownName,
		statsUnknownName: statsUnknownName,
		"/":              statsUnknownName,
		"C:\\":           "C:",
		"C:\\work\\proj": "proj",
		"//server/share": "share",
		"/home/u/proj/":  "proj",
		"/a/b/c":         "c",
		"proj":           "proj",
	}
	for input, want := range cases {
		if got := workspaceDisplayName(input); got != want {
			t.Errorf("workspaceDisplayName(%q) = %q, want %q", input, got, want)
		}
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

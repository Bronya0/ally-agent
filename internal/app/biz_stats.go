package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Token statistics recording.
//
// Design goals:
//   - Recording is fire-and-forget: recordTokenStats uses a bounded,
//     non-blocking queue and only a brief lifecycle lock on the chat hot path.
//   - Persistence is done by a single background goroutine (started in
//     Startup) that flushes dirty day files every few seconds and on exit.
//   - Aggregation for GetTokenStats is computed on demand from an in-memory
//     snapshot, so opening the stats modal performs no disk IO.
//   - Day files live in ~/.ally_agent/stats/<date>.json, are retained for
//     90 days, and are pruned at load time.

const (
	statsSubDir               = "stats"
	statsRetentionDays        = 90
	statsFlushInterval        = 5 * time.Second
	statsQueueSize            = 2048
	statsMaxRecordsPerDay     = 10000
	statsMaxTotalRecords      = 20000
	statsMaxDecodedPerDay     = 100000
	statsMaxFileBytes         = 64 << 20
	statsMaxTokensPerRecord   = 1_000_000_000
	statsMaxRequestsPerRecord = 1_000_000
	statsShutdownTimeout      = 10 * time.Second
	statsPieSliceLimit        = 10
	statsDailyRangeDays       = 30
	statsUnknownName          = "unknown"
)

func statsDir() string { return filepath.Join(appDataDir(), statsSubDir) }

func statsDateKey(ts int64) string { return time.UnixMilli(ts).Format("2006-01-02") }

// statsRecord is a single LLM usage event.
type statsRecord struct {
	Model           string `json:"model"`
	Workspace       string `json:"workspace"`
	Ts              int64  `json:"ts"`
	InputTokens     int    `json:"inputTokens"`
	OutputTokens    int    `json:"outputTokens"`
	CacheHitTokens  int    `json:"cacheHitTokens"`
	CacheMissTokens int    `json:"cacheMissTokens"`
	Requests        int    `json:"requests"`
}

// statsRecorder keeps usage records in memory and persists them as day files.
// The bounded queue is deliberately lossy under extreme pressure: dropping a
// telemetry sample is preferable to ever delaying a model response.
type statsRecorder struct {
	mu            sync.Mutex
	lifecycleMu   sync.Mutex
	lifecycleCond *sync.Cond
	accepting     bool
	active        int
	days          map[string][]statsRecord // date ("2006-01-02") -> records
	dirtyDays     map[string]bool
	queue         chan statsRecord
	startOnce     sync.Once
	stopOnce      sync.Once
	cancel        context.CancelFunc
	done          chan struct{}
	storageDir    string
}

func newStatsRecorder() *statsRecorder {
	s := &statsRecorder{
		days:       map[string][]statsRecord{},
		dirtyDays:  map[string]bool{},
		queue:      make(chan statsRecord, statsQueueSize),
		done:       make(chan struct{}),
		storageDir: statsDir(),
	}
	s.lifecycleCond = sync.NewCond(&s.lifecycleMu)
	s.accepting = true
	return s
}

// record enqueues one usage event without performing IO. A brief lifecycle
// lock prevents producers from racing the final shutdown drain. If telemetry
// falls behind and the bounded queue is full, the sample is dropped so normal
// chat processing never waits for the recorder.
func (s *statsRecorder) record(r statsRecord) {
	if r.Ts == 0 {
		r.Ts = time.Now().UnixMilli()
	}
	if r.Model == "" {
		r.Model = statsUnknownName
	}
	if r.Workspace == "" {
		r.Workspace = statsUnknownName
	}
	if r.Requests <= 0 {
		r.Requests = 1
	}
	s.lifecycleMu.Lock()
	if !s.accepting {
		s.lifecycleMu.Unlock()
		return
	}
	s.active++
	s.lifecycleMu.Unlock()
	defer func() {
		s.lifecycleMu.Lock()
		s.active--
		if s.active == 0 {
			s.lifecycleCond.Broadcast()
		}
		s.lifecycleMu.Unlock()
	}()
	select {
	case s.queue <- r:
	default:
	}
}

func (s *statsRecorder) appendRecord(r statsRecord) {
	key := statsDateKey(r.Ts)
	s.mu.Lock()
	if len(s.days[key]) >= statsMaxRecordsPerDay {
		// Keep the newest samples because the active dashboard ranges are
		// more useful than the oldest records from a saturated day.
		s.days[key] = append(s.days[key][1:], r)
	} else {
		if totalStatsRecords(s.days) >= statsMaxTotalRecords {
			if droppedDate := dropOldestStatsRecord(s.days); droppedDate != "" {
				s.dirtyDays[droppedDate] = true
			}
		}
		s.days[key] = append(s.days[key], r)
	}
	s.dirtyDays[key] = true
	s.mu.Unlock()
}

func totalStatsRecords(days map[string][]statsRecord) int {
	total := 0
	for _, records := range days {
		total += len(records)
	}
	return total
}

func dropOldestStatsRecord(days map[string][]statsRecord) string {
	oldest := ""
	for date, records := range days {
		if len(records) > 0 && (oldest == "" || date < oldest) {
			oldest = date
		}
	}
	if oldest == "" {
		return ""
	}
	if len(days[oldest]) == 1 {
		delete(days, oldest)
		return oldest
	}
	days[oldest] = days[oldest][1:]
	return oldest
}

// start loads persisted data before entering the single recorder loop. The
// lifecycle is owned here so shutdown can wait for the final queue drain and
// flush instead of relying on process teardown timing.
func (s *statsRecorder) start(parent context.Context) {
	s.startOnce.Do(func() {
		ctx, cancel := context.WithCancel(parent)
		s.cancel = cancel
		go func() {
			defer close(s.done)
			s.load()
			s.run(ctx)
		}()
	})
}

func (s *statsRecorder) stop(timeout time.Duration) error {
	started := false
	s.startOnce.Do(func() {
		// A recorder that was never started has nothing to flush.
		close(s.done)
	})
	s.lifecycleMu.Lock()
	s.accepting = false
	for s.active > 0 {
		s.lifecycleCond.Wait()
	}
	s.lifecycleMu.Unlock()
	if s.cancel != nil {
		started = true
		s.stopOnce.Do(s.cancel)
	}
	if !started {
		return nil
	}
	if timeout <= 0 {
		timeout = statsShutdownTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-s.done:
		return nil
	case <-timer.C:
		return errors.New("timed out flushing token statistics")
	}
}

// run owns queue consumption, flushes dirty state periodically, and drains
// pending records before the final shutdown flush.
func (s *statsRecorder) run(ctx context.Context) {
	ticker := time.NewTicker(statsFlushInterval)
	defer ticker.Stop()
	for {
		select {
		case r := <-s.queue:
			s.appendRecord(r)
		case <-ctx.Done():
			for {
				select {
				case r := <-s.queue:
					s.appendRecord(r)
				default:
					s.flush()
					return
				}
			}
		case <-ticker.C:
			s.flushIfDirty()
		}
	}
}

// drainQueue moves all currently queued telemetry into the in-memory index
// without waiting. It is used before an explicit dashboard query so the view
// includes the most recent completed model response.
func (s *statsRecorder) drainQueue() {
	for {
		select {
		case r := <-s.queue:
			s.appendRecord(r)
		default:
			return
		}
	}
}

func (s *statsRecorder) flushIfDirty() {
	s.mu.Lock()
	dirty := len(s.dirtyDays) > 0
	s.mu.Unlock()
	if dirty {
		s.flush()
	}
}

// flush writes all day files atomically (tmp + rename). Records added while
// flushing stay in memory and are written on the next tick.
func (s *statsRecorder) flush() {
	s.mu.Lock()
	if len(s.dirtyDays) == 0 {
		s.mu.Unlock()
		return
	}
	days := make(map[string][]statsRecord, len(s.dirtyDays))
	for date := range s.dirtyDays {
		records := s.days[date]
		if len(records) == 0 {
			// 整日记录已被逐出（dropOldestStatsRecord 删除了该日），
			// 没有可落盘的数据：跳过，避免把 null 写进日文件。
			continue
		}
		days[date] = append([]statsRecord(nil), records...)
	}
	s.dirtyDays = map[string]bool{}
	s.mu.Unlock()
	if len(days) == 0 {
		return
	}

	dir := s.storageDir
	if strings.TrimSpace(dir) == "" {
		dir = statsDir()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.markDirty(mapKeys(days)...)
		return
	}
	failedDates := make([]string, 0)
	for date, records := range days {
		data, err := json.Marshal(records)
		if err != nil {
			failedDates = append(failedDates, date)
			continue
		}
		path := filepath.Join(dir, date+".json")
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, data, 0o600); err != nil {
			failedDates = append(failedDates, date)
			continue
		}
		if err := replaceStatsFile(tmp, path); err != nil {
			failedDates = append(failedDates, date)
			_ = os.Remove(tmp)
		}
	}
	if len(failedDates) > 0 {
		s.markDirty(failedDates...)
	}
}

func (s *statsRecorder) markDirty(dates ...string) {
	s.mu.Lock()
	for _, date := range dates {
		if date != "" {
			s.dirtyDays[date] = true
		}
	}
	s.mu.Unlock()
}

func mapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}

// replaceStatsFile handles both POSIX atomic replacement and Windows, where
// os.Rename does not replace an existing destination.
func replaceStatsFile(tmp, dst string) error {
	if err := os.Rename(tmp, dst); err == nil {
		return nil
	}
	backup := dst + ".bak"
	_ = os.Remove(backup)
	if err := os.Rename(dst, backup); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Rename(backup, dst)
		return err
	}
	_ = os.Remove(backup)
	return nil
}

func recoverStatsBackups(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json.bak") {
			continue
		}
		dateStr := strings.TrimSuffix(entry.Name(), ".json.bak")
		if _, err := time.ParseInLocation("2006-01-02", dateStr, time.Local); err != nil {
			continue
		}
		backup := filepath.Join(dir, entry.Name())
		if _, err := readStatsDayFile(backup, dateStr); err != nil {
			continue
		}
		dst := filepath.Join(dir, dateStr+".json")
		if _, err := readStatsDayFile(dst, dateStr); err == nil {
			_ = os.Remove(backup)
			continue
		}
		_ = os.Remove(dst)
		_ = os.Rename(backup, dst)
	}
}

// load reads persisted day files within the retention window into memory.
// Each file and decoded record count is bounded before it can affect process
// memory. Called once at startup while memory is still empty.
func (s *statsRecorder) load() {
	dir := s.storageDir
	if strings.TrimSpace(dir) == "" {
		dir = statsDir()
	}
	recoverStatsBackups(dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	cutoff := midnight.AddDate(0, 0, -(statsRetentionDays - 1))
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		dateStr := strings.TrimSuffix(e.Name(), ".json")
		day, err := time.ParseInLocation("2006-01-02", dateStr, time.Local)
		if err != nil || day.After(midnight) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if day.Before(cutoff) {
			_ = os.Remove(path)
			continue
		}
		records, err := readStatsDayFile(path, dateStr)
		if err != nil {
			continue
		}
		s.mu.Lock()
		// Startup loading may overlap an early dashboard query. Preserve fresh
		// in-process samples while keeping both day and process caps intact.
		existing := s.days[dateStr]
		available := minInt(statsMaxRecordsPerDay-len(existing), statsMaxTotalRecords-totalStatsRecords(s.days))
		if available > 0 {
			if len(records) > available {
				records = records[len(records)-available:]
			}
			s.days[dateStr] = append(records, existing...)
		}
		s.mu.Unlock()
	}
}

func readStatsDayFile(path, dateStr string) ([]statsRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil {
		return nil, err
	} else if !info.Mode().IsRegular() || info.Size() > statsMaxFileBytes {
		return nil, fmt.Errorf("stats file exceeds %d bytes", statsMaxFileBytes)
	}

	decoder := json.NewDecoder(io.LimitReader(f, statsMaxFileBytes+1))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('[') {
		return nil, errors.New("stats file must contain a JSON array")
	}
	records := make([]statsRecord, 0, minInt(statsMaxRecordsPerDay, 1024))
	decoded := 0
	next := 0
	for decoder.More() {
		var record statsRecord
		if err := decoder.Decode(&record); err != nil {
			return nil, err
		}
		decoded++
		if decoded > statsMaxDecodedPerDay {
			return nil, fmt.Errorf("stats file exceeds %d decoded records", statsMaxDecodedPerDay)
		}
		if !validLoadedStatsRecord(record, dateStr) {
			continue
		}
		if len(records) < statsMaxRecordsPerDay {
			records = append(records, record)
			continue
		}
		records[next] = record
		next = (next + 1) % statsMaxRecordsPerDay
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return nil, errors.New("stats file contains trailing data")
	}
	if next > 0 {
		ordered := make([]statsRecord, 0, len(records))
		ordered = append(ordered, records[next:]...)
		ordered = append(ordered, records[:next]...)
		records = ordered
	}
	return records, nil
}

func validLoadedStatsRecord(record statsRecord, dateStr string) bool {
	return record.Ts > 0 && statsDateKey(record.Ts) == dateStr &&
		len(record.Model) <= 256 && len(record.Workspace) <= 1024 &&
		record.InputTokens >= 0 && record.InputTokens <= statsMaxTokensPerRecord &&
		record.OutputTokens >= 0 && record.OutputTokens <= statsMaxTokensPerRecord &&
		record.CacheHitTokens >= 0 && record.CacheHitTokens <= statsMaxTokensPerRecord &&
		record.CacheMissTokens >= 0 && record.CacheMissTokens <= statsMaxTokensPerRecord &&
		record.Requests > 0 && record.Requests <= statsMaxRequestsPerRecord
}

// recordTokenStats is a fire-and-forget hook called after each LLM step
// (main chat loop and sub-agents). It never blocks the caller.
func (a *App) recordTokenStats(model, workspace string, usage *modelUsage, fallbackInput, fallbackOutput int) {
	if a.stats == nil {
		return
	}
	input := fallbackInput
	output := fallbackOutput
	cacheHit := 0
	cacheMiss := 0
	if usage != nil {
		if usage.PromptTokens > 0 {
			input = usage.PromptTokens
		}
		if usage.CompletionTokens > 0 {
			output = usage.CompletionTokens
		}
		cacheHit = usage.CacheHitTokens
		cacheMiss = usage.CacheMissTokens
	}
	if input <= 0 && output <= 0 {
		return
	}
	a.stats.record(statsRecord{
		Model:           model,
		Workspace:       workspace,
		Ts:              time.Now().UnixMilli(),
		InputTokens:     input,
		OutputTokens:    output,
		CacheHitTokens:  cacheHit,
		CacheMissTokens: cacheMiss,
		Requests:        1,
	})
}

// ── Dashboard aggregation ──
//
// GetTokenStats returns everything the Token Usage dashboard needs in one
// call: a summary row (today / 7 days / month), a fixed 30-day daily bar
// chart, a monthly heatmap, and workspace / model breakdowns for the current
// week and month. Computed from a bounded in-memory snapshot, no disk IO.

// TokenStatsSummary aggregates usage over one fixed range.
type TokenStatsSummary struct {
	TotalTokens     int     `json:"totalTokens"`
	InputTokens     int     `json:"inputTokens"`
	OutputTokens    int     `json:"outputTokens"`
	CacheHitTokens  int     `json:"cacheHitTokens"`
	CacheMissTokens int     `json:"cacheMissTokens"`
	AvgPerRequest   int     `json:"avgPerRequest"`
	CacheHitRate    float64 `json:"cacheHitRate"` // 0-1 over hit+miss prompt tokens
	Requests        int     `json:"requests"`
}

// TokenDailyStat aggregates usage for one calendar day (zero-filled).
type TokenDailyStat struct {
	Date            string `json:"date"`
	InputTokens     int    `json:"inputTokens"`
	OutputTokens    int    `json:"outputTokens"`
	CacheHitTokens  int    `json:"cacheHitTokens"`
	CacheMissTokens int    `json:"cacheMissTokens"`
	Requests        int    `json:"requests"`
}

// TokenDimensionStat aggregates usage for one workspace or model.
type TokenDimensionStat struct {
	Name         string `json:"name"`               // display name (workspace basename or model)
	FullName     string `json:"fullName,omitempty"` // original workspace path when shortened
	InputTokens  int    `json:"inputTokens"`
	OutputTokens int    `json:"outputTokens"`
	Requests     int    `json:"requests"`
}

// TokenStatsResult is the aggregated response for GetTokenStats.
type TokenStatsResult struct {
	OK             bool                 `json:"ok"`
	Error          string               `json:"error,omitempty"`
	SummaryToday   TokenStatsSummary    `json:"summaryToday"`
	Summary7Days   TokenStatsSummary    `json:"summary7Days"`
	SummaryMonth   TokenStatsSummary    `json:"summaryMonth"`
	Daily          []TokenDailyStat     `json:"daily"` // fixed 30 days, oldest first
	WorkspaceWeek  []TokenDimensionStat `json:"workspaceWeek"`
	WorkspaceMonth []TokenDimensionStat `json:"workspaceMonth"`
	ModelWeek      []TokenDimensionStat `json:"modelWeek"`
	ModelMonth     []TokenDimensionStat `json:"modelMonth"`
}

// GetTokenStats aggregates recorded usage for the dashboard. It is computed
// from in-memory state, so it is safe to call while chats are running.
func (a *App) GetTokenStats() TokenStatsResult {
	s := a.stats
	if s == nil {
		return TokenStatsResult{OK: false, Error: "stats not initialized"}
	}
	// Include telemetry still waiting in the non-blocking queue. This work is
	// initiated by the dashboard request, never by the chat hot path.
	s.drainQueue()

	now := time.Now()
	midnight, monthStart, weekStart, dailyStart, snapshotStart := statsWindows(now)
	nowMs := now.UnixMilli()
	weekStartMs := weekStart.UnixMilli()
	monthStartMs := monthStart.UnixMilli()
	cutoffStr := snapshotStart.Format("2006-01-02")
	retentionCutoffStr := midnight.AddDate(0, 0, -(statsRetentionDays - 1)).Format("2006-01-02")

	s.mu.Lock()
	for date := range s.days {
		if date < retentionCutoffStr {
			delete(s.days, date)
			delete(s.dirtyDays, date)
		}
	}
	daysSnapshot := make(map[string][]statsRecord, len(s.days))
	for date, records := range s.days {
		if date < cutoffStr {
			continue
		}
		daysSnapshot[date] = append([]statsRecord(nil), records...)
	}
	s.mu.Unlock()

	all := make([]statsRecord, 0, 4096)
	for _, records := range daysSnapshot {
		all = append(all, records...)
	}

	result := TokenStatsResult{OK: true}
	result.SummaryToday = summarizeStatsRecords(statsRecordsInRange(all, midnight.UnixMilli(), nowMs))
	result.Summary7Days = summarizeStatsRecords(statsRecordsInRange(all, midnight.AddDate(0, 0, -6).UnixMilli(), nowMs))
	result.SummaryMonth = summarizeStatsRecords(statsRecordsInRange(all, monthStartMs, nowMs))
	result.Daily = buildStatsDaily(statsRecordsInRange(all, dailyStart.UnixMilli(), nowMs), dailyStart, statsDailyRangeDays)
	result.WorkspaceWeek = buildStatsDimension(statsRecordsInRange(all, weekStartMs, nowMs), workspaceKey, workspaceDisplayName)
	result.WorkspaceMonth = buildStatsDimension(statsRecordsInRange(all, monthStartMs, nowMs), workspaceKey, workspaceDisplayName)
	result.ModelWeek = buildStatsDimension(statsRecordsInRange(all, weekStartMs, nowMs), modelKey, nil)
	result.ModelMonth = buildStatsDimension(statsRecordsInRange(all, monthStartMs, nowMs), modelKey, nil)
	return result
}

func workspaceKey(r statsRecord) string { return r.Workspace }
func modelKey(r statsRecord) string     { return r.Model }

// statsWindows computes the dashboard time windows for a given "now".
// Extracted so month/week boundary behavior is unit-testable with fixed dates.
// The snapshot must cover the earlier of the daily bar window and the month
// window: on a 31-day month's last day, day 1 falls before today-29 and would
// otherwise be dropped from the monthly summary and month pies.
func statsWindows(now time.Time) (midnight, monthStart, weekStart, dailyStart, snapshotStart time.Time) {
	midnight = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	monthStart = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	// 周一起始（与热力图一致）：Monday = 0 .. Sunday = 6。
	weekStart = midnight.AddDate(0, 0, -int((midnight.Weekday()+6)%7))
	dailyStart = midnight.AddDate(0, 0, -(statsDailyRangeDays - 1))
	snapshotStart = dailyStart
	if monthStart.Before(snapshotStart) {
		snapshotStart = monthStart
	}
	return
}

// workspaceDisplayName keeps only the last path segment of a workspace path
// (works across Windows backslashes and POSIX slashes).
func workspaceDisplayName(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || path == statsUnknownName {
		return statsUnknownName
	}
	trimmed := strings.TrimRight(path, `/\`)
	if idx := strings.LastIndexAny(trimmed, `/\`); idx >= 0 && idx < len(trimmed)-1 {
		return trimmed[idx+1:]
	}
	if trimmed != "" {
		return trimmed
	}
	return statsUnknownName
}

func statsRecordsInRange(records []statsRecord, fromMs, toMs int64) []statsRecord {
	out := make([]statsRecord, 0, len(records))
	for _, r := range records {
		if r.Ts >= fromMs && r.Ts <= toMs {
			out = append(out, r)
		}
	}
	return out
}

func summarizeStatsRecords(records []statsRecord) TokenStatsSummary {
	var s TokenStatsSummary
	for _, r := range records {
		s.TotalTokens += r.InputTokens + r.OutputTokens
		s.InputTokens += r.InputTokens
		s.OutputTokens += r.OutputTokens
		s.CacheHitTokens += r.CacheHitTokens
		s.CacheMissTokens += r.CacheMissTokens
		s.Requests += r.Requests
	}
	if s.Requests > 0 {
		s.AvgPerRequest = s.TotalTokens / s.Requests
	}
	if hitMiss := s.CacheHitTokens + s.CacheMissTokens; hitMiss > 0 {
		s.CacheHitRate = float64(s.CacheHitTokens) / float64(hitMiss)
	}
	return s
}

// buildStatsDaily zero-fills a continuous day range so the bar chart has no
// gaps.
func buildStatsDaily(records []statsRecord, start time.Time, days int) []TokenDailyStat {
	daily := make([]TokenDailyStat, days)
	index := make(map[string]int, days)
	for i := 0; i < days; i++ {
		d := start.AddDate(0, 0, i)
		daily[i].Date = d.Format("2006-01-02")
		index[daily[i].Date] = i
	}
	for _, r := range records {
		i, ok := index[statsDateKey(r.Ts)]
		if !ok {
			continue
		}
		d := &daily[i]
		d.InputTokens += r.InputTokens
		d.OutputTokens += r.OutputTokens
		d.CacheHitTokens += r.CacheHitTokens
		d.CacheMissTokens += r.CacheMissTokens
		d.Requests += r.Requests
	}
	return daily
}

// buildStatsDimension aggregates records by a key (workspace path or model)
// and returns the top slices, sorted by total tokens descending. displayFn
// may be nil (identity) — used for models where the key is already the name.
func buildStatsDimension(records []statsRecord, keyFn func(statsRecord) string, displayFn func(string) string) []TokenDimensionStat {
	index := map[string]int{}
	items := make([]TokenDimensionStat, 0, 8)
	for _, r := range records {
		key := keyFn(r)
		if key == "" {
			key = statsUnknownName
		}
		name := key
		if displayFn != nil {
			name = displayFn(key)
		}
		i, ok := index[key]
		if !ok {
			index[key] = len(items)
			items = append(items, TokenDimensionStat{
				Name:         name,
				FullName:     key,
				InputTokens:  r.InputTokens,
				OutputTokens: r.OutputTokens,
				Requests:     r.Requests,
			})
			continue
		}
		it := &items[i]
		it.InputTokens += r.InputTokens
		it.OutputTokens += r.OutputTokens
		it.Requests += r.Requests
	}
	for i := range items {
		if items[i].FullName == items[i].Name {
			items[i].FullName = ""
		}
	}
	sort.Slice(items, func(i, j int) bool {
		return statsDimensionTotal(items[i]) > statsDimensionTotal(items[j])
	})
	if len(items) > statsPieSliceLimit {
		items = items[:statsPieSliceLimit]
	}
	return items
}

func statsDimensionTotal(s TokenDimensionStat) int {
	return s.InputTokens + s.OutputTokens
}

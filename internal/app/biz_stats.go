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
	statsMaxRangeDays         = 90
	statsQueueSize            = 2048
	statsMaxRecordsPerDay     = 10000
	statsMaxTotalRecords      = 20000
	statsMaxDecodedPerDay     = 100000
	statsMaxFileBytes         = 64 << 20
	statsMaxTokensPerRecord   = 1_000_000_000
	statsMaxRequestsPerRecord = 1_000_000
	statsShutdownTimeout      = 10 * time.Second
	statsMaxDaySeries         = 12
)

func statsDir() string { return filepath.Join(appDataDir(), statsSubDir) }

func statsDateKey(ts int64) string { return time.UnixMilli(ts).Format("2006-01-02") }

// statsRecord is a single LLM usage event.
type statsRecord struct {
	Provider        string `json:"provider,omitempty"`
	Model           string `json:"model"`
	Workspace       string `json:"workspace"`
	SessionID       string `json:"sessionId"`
	Source          string `json:"source,omitempty"`
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
	if r.Provider == "" {
		r.Provider = "unknown"
	}
	if r.Model == "" {
		r.Model = "unknown"
	}
	if r.Source == "" {
		r.Source = "main"
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
		days[date] = append([]statsRecord(nil), s.days[date]...)
	}
	s.dirtyDays = map[string]bool{}
	s.mu.Unlock()

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
		len(record.Provider) <= 128 && len(record.Model) <= 256 &&
		len(record.Workspace) <= 1024 && len(record.SessionID) <= 256 && len(record.Source) <= 64 &&
		record.InputTokens >= 0 && record.InputTokens <= statsMaxTokensPerRecord &&
		record.OutputTokens >= 0 && record.OutputTokens <= statsMaxTokensPerRecord &&
		record.CacheHitTokens >= 0 && record.CacheHitTokens <= statsMaxTokensPerRecord &&
		record.CacheMissTokens >= 0 && record.CacheMissTokens <= statsMaxTokensPerRecord &&
		record.Requests > 0 && record.Requests <= statsMaxRequestsPerRecord
}

// recordTokenStats is a fire-and-forget hook called after each LLM step
// (main chat loop and sub-agents). It never blocks the caller.
func (a *App) recordTokenStats(provider, model, workspace, sessionID, source string, usage *modelUsage, fallbackInput, fallbackOutput int) {
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
	if strings.HasPrefix(sessionID, "scheduled:") {
		source = "scheduled"
	}
	a.stats.record(statsRecord{
		Provider:        provider,
		Model:           model,
		Workspace:       workspace,
		SessionID:       sessionID,
		Source:          source,
		Ts:              time.Now().UnixMilli(),
		InputTokens:     input,
		OutputTokens:    output,
		CacheHitTokens:  cacheHit,
		CacheMissTokens: cacheMiss,
		Requests:        1,
	})
}

// TokenStatsModel aggregates usage for one model or workspace.
type TokenStatsModel struct {
	Name            string  `json:"name"`
	InputTokens     int     `json:"inputTokens"`
	OutputTokens    int     `json:"outputTokens"`
	CacheHitTokens  int     `json:"cacheHitTokens"`
	CacheMissTokens int     `json:"cacheMissTokens"`
	Requests        int     `json:"requests"`
	Share           float64 `json:"share"` // share of total tokens (0-1)
}

// TokenStatsDaySeries holds per-day token series for one named category entry
// (model, provider, source, or workspace), aligned by index with ByDay.
type TokenStatsDaySeries struct {
	Name         string `json:"name"`
	InputTokens  []int  `json:"inputTokens"`
	OutputTokens []int  `json:"outputTokens"`
}

// TokenStatsDay aggregates usage for one calendar day.
type TokenStatsDay struct {
	Date            string `json:"date"`
	InputTokens     int    `json:"inputTokens"`
	OutputTokens    int    `json:"outputTokens"`
	CacheHitTokens  int    `json:"cacheHitTokens"`
	CacheMissTokens int    `json:"cacheMissTokens"`
	Requests        int    `json:"requests"`
}

// TokenStatsHour aggregates usage for one hour of the day (0-23).
type TokenStatsHour struct {
	Hour            int `json:"hour"`
	InputTokens     int `json:"inputTokens"`
	OutputTokens    int `json:"outputTokens"`
	CacheHitTokens  int `json:"cacheHitTokens"`
	CacheMissTokens int `json:"cacheMissTokens"`
	Requests        int `json:"requests"`
}

// TokenStatsResult is the aggregated response for GetTokenStats.
type TokenStatsResult struct {
	OK                   bool                  `json:"ok"`
	Error                string                `json:"error,omitempty"`
	RangeDays            int                   `json:"rangeDays"`
	FromTs               int64                 `json:"fromTs"`
	ToTs                 int64                 `json:"toTs"`
	TotalInputTokens     int                   `json:"totalInputTokens"`
	TotalOutputTokens    int                   `json:"totalOutputTokens"`
	TotalCacheHitTokens  int                   `json:"totalCacheHitTokens"`
	TotalCacheMissTokens int                   `json:"totalCacheMissTokens"`
	TotalRequests        int                   `json:"totalRequests"`
	UniqueSessions       int                   `json:"uniqueSessions"`
	ActiveDays           int                   `json:"activeDays"`
	CacheHitRate         float64               `json:"cacheHitRate"` // 0-1 over prompt tokens
	ByProvider           []TokenStatsModel     `json:"byProvider"`
	ByModel              []TokenStatsModel     `json:"byModel"`
	ByWorkspace          []TokenStatsModel     `json:"byWorkspace"`
	BySource             []TokenStatsModel     `json:"bySource"`
	ByProviderDay        []TokenStatsDaySeries `json:"byProviderDay"`
	ByModelDay           []TokenStatsDaySeries `json:"byModelDay"`
	ByWorkspaceDay       []TokenStatsDaySeries `json:"byWorkspaceDay"`
	BySourceDay          []TokenStatsDaySeries `json:"bySourceDay"`
	ByDay                []TokenStatsDay       `json:"byDay"`
	ByHour               []TokenStatsHour      `json:"byHour"`
}

// GetTokenStats aggregates recorded usage for the last rangeDays (1-90,
// default 30). It is computed from in-memory state, so it is safe to call
// while chats are running and does not perform disk IO.
func (a *App) GetTokenStats(rangeDays int) TokenStatsResult {
	if rangeDays <= 0 || rangeDays > statsMaxRangeDays {
		rangeDays = 30
	}
	s := a.stats
	if s == nil {
		return TokenStatsResult{OK: false, Error: "stats not initialized"}
	}
	// Include telemetry still waiting in the non-blocking queue. This work is
	// initiated by the dashboard request, never by the chat hot path.
	s.drainQueue()
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	cutoff := midnight.AddDate(0, 0, -(rangeDays - 1))
	cutoffMs := cutoff.UnixMilli()
	cutoffStr := cutoff.Format("2006-01-02")

	result := TokenStatsResult{OK: true, RangeDays: rangeDays, FromTs: cutoffMs, ToTs: now.UnixMilli()}

	providerIdx := map[string]int{}
	modelIdx := map[string]int{}
	wsIdx := map[string]int{}
	sourceIdx := map[string]int{}
	providerDay := map[string]*statsDaySeriesAccum{}
	modelDay := map[string]*statsDaySeriesAccum{}
	workspaceDay := map[string]*statsDaySeriesAccum{}
	sourceDay := map[string]*statsDaySeriesAccum{}
	sessions := map[string]struct{}{}
	activeDates := map[string]struct{}{}
	dayIdx := map[string]int{}
	hours := make([]TokenStatsHour, 24)
	for i := range hours {
		hours[i].Hour = i
	}

	// Continuous day buckets (zero-filled) so the frontend can draw a clean
	// series without gaps.
	for i := 0; i < rangeDays; i++ {
		d := cutoff.AddDate(0, 0, i).Format("2006-01-02")
		dayIdx[d] = len(result.ByDay)
		result.ByDay = append(result.ByDay, TokenStatsDay{Date: d})
	}

	retentionCutoffStr := midnight.AddDate(0, 0, -(statsRetentionDays - 1)).Format("2006-01-02")
	s.mu.Lock()
	for date := range s.days {
		if date < retentionCutoffStr {
			delete(s.days, date)
			delete(s.dirtyDays, date)
		}
	}
	daysSnapshot := make(map[string][]statsRecord, rangeDays)
	for date, records := range s.days {
		if date < cutoffStr {
			continue
		}
		daysSnapshot[date] = append([]statsRecord(nil), records...)
	}
	s.mu.Unlock()
	for date, records := range daysSnapshot {
		if date < cutoffStr {
			continue
		}
		for _, r := range records {
			if r.Ts < cutoffMs {
				continue
			}
			result.TotalInputTokens += r.InputTokens
			result.TotalOutputTokens += r.OutputTokens
			result.TotalCacheHitTokens += r.CacheHitTokens
			result.TotalCacheMissTokens += r.CacheMissTokens
			result.TotalRequests += r.Requests
			if r.SessionID != "" {
				sessions[r.SessionID] = struct{}{}
			}
			activeDates[statsDateKey(r.Ts)] = struct{}{}

			provider := r.Provider
			if provider == "" {
				provider = "unknown"
			}
			accumulateStatsModel(&result.ByProvider, providerIdx, provider, r)
			accumulateStatsModel(&result.ByModel, modelIdx, r.Model, r)
			accumulateStatsModel(&result.ByWorkspace, wsIdx, r.Workspace, r)
			source := r.Source
			if source == "" {
				source = "main"
			}
			accumulateStatsModel(&result.BySource, sourceIdx, source, r)

			if i, ok := dayIdx[statsDateKey(r.Ts)]; ok {
				d := &result.ByDay[i]
				d.InputTokens += r.InputTokens
				d.OutputTokens += r.OutputTokens
				d.CacheHitTokens += r.CacheHitTokens
				d.CacheMissTokens += r.CacheMissTokens
				d.Requests += r.Requests
				accumulateDaySeries(providerDay, i, rangeDays, provider, r.InputTokens, r.OutputTokens)
				accumulateDaySeries(modelDay, i, rangeDays, r.Model, r.InputTokens, r.OutputTokens)
				accumulateDaySeries(workspaceDay, i, rangeDays, r.Workspace, r.InputTokens, r.OutputTokens)
				accumulateDaySeries(sourceDay, i, rangeDays, source, r.InputTokens, r.OutputTokens)
			}

			h := time.UnixMilli(r.Ts).Hour()
			hh := &hours[h]
			hh.InputTokens += r.InputTokens
			hh.OutputTokens += r.OutputTokens
			hh.CacheHitTokens += r.CacheHitTokens
			hh.CacheMissTokens += r.CacheMissTokens
			hh.Requests += r.Requests
		}
	}
	result.ByHour = hours
	result.ByProviderDay = buildStatsDaySeries(providerDay)
	result.ByModelDay = buildStatsDaySeries(modelDay)
	result.ByWorkspaceDay = buildStatsDaySeries(workspaceDay)
	result.BySourceDay = buildStatsDaySeries(sourceDay)
	result.UniqueSessions = len(sessions)
	result.ActiveDays = len(activeDates)

	totalTokens := result.TotalInputTokens + result.TotalOutputTokens
	if totalTokens > 0 {
		setStatsShares(result.ByProvider, totalTokens)
		setStatsShares(result.ByModel, totalTokens)
		setStatsShares(result.ByWorkspace, totalTokens)
		setStatsShares(result.BySource, totalTokens)
	}
	if hitMiss := result.TotalCacheHitTokens + result.TotalCacheMissTokens; hitMiss > 0 {
		result.CacheHitRate = float64(result.TotalCacheHitTokens) / float64(hitMiss)
	}

	sortStatsModels(result.ByProvider)
	sortStatsModels(result.ByModel)
	sortStatsModels(result.ByWorkspace)
	sortStatsModels(result.BySource)
	return result
}

// statsDaySeriesAccum accumulates per-day token counts for one named category
// entry (model, provider, source, or workspace), aligned by index with ByDay.
type statsDaySeriesAccum struct {
	name   string
	input  []int
	output []int
}

func accumulateDaySeries(series map[string]*statsDaySeriesAccum, dayIndex, rangeDays int, name string, input, output int) {
	if name == "" {
		name = "unknown"
	}
	acc, ok := series[name]
	if !ok {
		acc = &statsDaySeriesAccum{name: name, input: make([]int, rangeDays), output: make([]int, rangeDays)}
		series[name] = acc
	}
	acc.input[dayIndex] += input
	acc.output[dayIndex] += output
}

func buildStatsDaySeries(series map[string]*statsDaySeriesAccum) []TokenStatsDaySeries {
	list := make([]*statsDaySeriesAccum, 0, len(series))
	for _, acc := range series {
		list = append(list, acc)
	}
	sort.Slice(list, func(i, j int) bool {
		return daySeriesTotal(list[i]) > daySeriesTotal(list[j])
	})
	if len(list) > statsMaxDaySeries {
		list = list[:statsMaxDaySeries]
	}
	result := make([]TokenStatsDaySeries, 0, len(list))
	for _, acc := range list {
		result = append(result, TokenStatsDaySeries{Name: acc.name, InputTokens: acc.input, OutputTokens: acc.output})
	}
	return result
}

func daySeriesTotal(acc *statsDaySeriesAccum) int {
	total := 0
	for _, v := range acc.input {
		total += v
	}
	for _, v := range acc.output {
		total += v
	}
	return total
}

func accumulateStatsModel(items *[]TokenStatsModel, index map[string]int, name string, r statsRecord) {
	if name == "" {
		name = "unknown"
	}
	if i, ok := index[name]; ok {
		m := &(*items)[i]
		m.InputTokens += r.InputTokens
		m.OutputTokens += r.OutputTokens
		m.CacheHitTokens += r.CacheHitTokens
		m.CacheMissTokens += r.CacheMissTokens
		m.Requests += r.Requests
		return
	}
	index[name] = len(*items)
	*items = append(*items, TokenStatsModel{
		Name: name, InputTokens: r.InputTokens, OutputTokens: r.OutputTokens,
		CacheHitTokens: r.CacheHitTokens, CacheMissTokens: r.CacheMissTokens, Requests: r.Requests,
	})
}

func setStatsShares(items []TokenStatsModel, totalTokens int) {
	for i := range items {
		items[i].Share = float64(tokenTotal(items[i])) / float64(totalTokens)
	}
}

func sortStatsModels(items []TokenStatsModel) {
	sort.Slice(items, func(i, j int) bool {
		return tokenTotal(items[i]) > tokenTotal(items[j])
	})
}

func tokenTotal(m TokenStatsModel) int {
	return m.InputTokens + m.OutputTokens
}

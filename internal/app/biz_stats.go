package app

import (
	"context"
	"encoding/json"
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
//     non-blocking queue and never takes a stats lock on the chat hot path.
//   - Persistence is done by a single background goroutine (started in
//     Startup) that flushes dirty day files every few seconds and on exit.
//   - Aggregation for GetTokenStats is computed on demand from an in-memory
//     snapshot, so opening the stats modal performs no disk IO.
//   - Day files live in ~/.ally_agent/stats/<date>.json, are retained for
//     90 days, and are pruned at load time.

const (
	statsSubDir           = "stats"
	statsRetentionDays    = 90
	statsFlushInterval    = 5 * time.Second
	statsMaxRangeDays     = 90
	statsQueueSize        = 2048
	statsMaxRecordsPerDay = 100000
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
	mu        sync.Mutex
	days      map[string][]statsRecord // date ("2006-01-02") -> records
	dirtyDays map[string]bool
	queue     chan statsRecord
}

func newStatsRecorder() *statsRecorder {
	return &statsRecorder{
		days:      map[string][]statsRecord{},
		dirtyDays: map[string]bool{},
		queue:     make(chan statsRecord, statsQueueSize),
	}
}

// record enqueues one usage event without taking a lock or performing IO. If
// telemetry falls behind and the bounded queue is full, the sample is dropped
// so statistics can never block normal chat processing.
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
	select {
	case s.queue <- r:
	default:
	}
}

func (s *statsRecorder) appendRecord(r statsRecord) {
	s.mu.Lock()
	key := statsDateKey(r.Ts)
	if len(s.days[key]) >= statsMaxRecordsPerDay {
		s.mu.Unlock()
		return
	}
	s.days[key] = append(s.days[key], r)
	s.dirtyDays[key] = true
	s.mu.Unlock()
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

	dir := statsDir()
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

// load reads persisted day files within the retention window into memory.
// Called once at startup while memory is still empty.
func (s *statsRecorder) load() {
	entries, err := os.ReadDir(statsDir())
	if err != nil {
		return
	}
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	cutoff := midnight.AddDate(0, 0, -(statsRetentionDays - 1))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		dateStr := strings.TrimSuffix(e.Name(), ".json")
		day, err := time.ParseInLocation("2006-01-02", dateStr, time.Local)
		if err != nil {
			continue
		}
		if day.Before(cutoff) {
			_ = os.Remove(filepath.Join(statsDir(), e.Name()))
			continue
		}
		data, err := os.ReadFile(filepath.Join(statsDir(), e.Name()))
		if err != nil {
			continue
		}
		var records []statsRecord
		if err := json.Unmarshal(data, &records); err != nil {
			continue
		}
		s.mu.Lock()
		// Startup loading may overlap an early dashboard query. Preserve any
		// fresh in-process samples already present for the same day.
		existing := s.days[dateStr]
		s.days[dateStr] = append(records, existing...)
		s.mu.Unlock()
	}
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
	OK                   bool              `json:"ok"`
	Error                string            `json:"error,omitempty"`
	RangeDays            int               `json:"rangeDays"`
	FromTs               int64             `json:"fromTs"`
	ToTs                 int64             `json:"toTs"`
	TotalInputTokens     int               `json:"totalInputTokens"`
	TotalOutputTokens    int               `json:"totalOutputTokens"`
	TotalCacheHitTokens  int               `json:"totalCacheHitTokens"`
	TotalCacheMissTokens int               `json:"totalCacheMissTokens"`
	TotalRequests        int               `json:"totalRequests"`
	UniqueSessions       int               `json:"uniqueSessions"`
	ActiveDays           int               `json:"activeDays"`
	CacheHitRate         float64           `json:"cacheHitRate"` // 0-1 over prompt tokens
	ByProvider           []TokenStatsModel `json:"byProvider"`
	ByModel              []TokenStatsModel `json:"byModel"`
	ByWorkspace          []TokenStatsModel `json:"byWorkspace"`
	BySource             []TokenStatsModel `json:"bySource"`
	ByDay                []TokenStatsDay   `json:"byDay"`
	ByHour               []TokenStatsHour  `json:"byHour"`
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

	s.mu.Lock()
	daysSnapshot := make(map[string][]statsRecord, len(s.days))
	for date, records := range s.days {
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

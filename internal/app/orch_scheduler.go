// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

// Section 7: Scheduler (was scheduler.go)
// App-owned scheduled-task manager that binds internal/tools/scheduler cron
// parsing/validation to the cron library, App context, executeDelegate, and
// the scheduled:* event sink.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	openai "github.com/sashabaranov/go-openai"

	"ally-dev/internal/tools/scheduler"
)

// Constants re-exported from the scheduler tool package so existing call sites
// in app/ keep working without referencing the tool package directly. The
// source of truth lives in internal/tools/scheduler.
const (
	defaultScheduledTaskSteps   = scheduler.DefaultSteps
	maxScheduledTaskSteps       = scheduler.MaxSteps
	defaultScheduledTaskTimeout = scheduler.DefaultTimeout
	maxScheduledTaskTimeout     = scheduler.MaxTimeout
	minScheduledTaskInterval    = scheduler.MinInterval
	maxScheduledTasks           = scheduler.MaxTasks
	scheduledTaskSummaryLimit   = scheduler.SummaryLimit
)

type ScheduledTaskSchedule struct {
	Type  string `json:"type"`
	At    string `json:"at,omitempty"`
	Every string `json:"every,omitempty"`
	Cron  string `json:"cron,omitempty"`
}

type ScheduledTask struct {
	ID                  string                `json:"id"`
	Name                string                `json:"name"`
	Instruction         string                `json:"instruction"`
	Workspace           string                `json:"workspace"`
	Schedule            ScheduledTaskSchedule `json:"schedule"`
	PermissionMode      string                `json:"permissionMode"`
	MaxSteps            int                   `json:"maxSteps"`
	TimeoutSeconds      int                   `json:"timeoutSeconds"`
	CreatedAt           int64                 `json:"createdAt"`
	UpdatedAt           int64                 `json:"updatedAt"`
	NextRunAt           int64                 `json:"nextRunAt,omitempty"`
	LastRunAt           int64                 `json:"lastRunAt,omitempty"`
	LastStatus          string                `json:"lastStatus"`
	LastSummary         string                `json:"lastSummary,omitempty"`
	LastError           string                `json:"lastError,omitempty"`
	RunCount            int                   `json:"runCount"`
	ConsecutiveFailures int                   `json:"consecutiveFailures"`
	Running             bool                  `json:"running"`
}

type ScheduledTaskToolRequest struct {
	Action      string `json:"action"`
	ID          string `json:"id,omitempty"`
	Name        string `json:"name,omitempty"`
	Instruction string `json:"instruction,omitempty"`
	Schedule    string `json:"schedule,omitempty"`
}

type ScheduledTaskToolView struct {
	ID             string                `json:"id"`
	Name           string                `json:"name"`
	Workspace      string                `json:"workspace"`
	Schedule       ScheduledTaskSchedule `json:"schedule"`
	PermissionMode string                `json:"permissionMode"`
	MaxSteps       int                   `json:"maxSteps"`
	TimeoutSeconds int                   `json:"timeoutSeconds"`
	NextRunAt      int64                 `json:"nextRunAt,omitempty"`
	LastRunAt      int64                 `json:"lastRunAt,omitempty"`
	LastStatus     string                `json:"lastStatus"`
	RunCount       int                   `json:"runCount"`
	Running        bool                  `json:"running"`
}

type ScheduledTaskToolResult struct {
	Task      *ScheduledTaskToolView  `json:"task,omitempty"`
	Tasks     []ScheduledTaskToolView `json:"tasks,omitempty"`
	Count     int                     `json:"count,omitempty"`
	Truncated bool                    `json:"truncated,omitempty"`
	Deleted   string                  `json:"deleted,omitempty"`
}

type scheduledTaskManager struct {
	app       *App
	events    eventSink
	cron      *cron.Cron
	path      string
	mu        sync.Mutex
	tasks     map[string]*ScheduledTask
	entries   map[string]cron.EntryID
	timers    map[string]*time.Timer
	schedules map[string]cron.Schedule
	cancels   map[string]context.CancelFunc
	runSem    chan struct{}
	stopped   bool
}

func (a *App) startScheduledTaskManager() error {
	a.scheduledMu.Lock()
	defer a.scheduledMu.Unlock()
	if a.scheduled != nil {
		return nil
	}
	if strings.TrimSpace(a.configPath) == "" {
		return errors.New("config path is not initialized")
	}
	manager := &scheduledTaskManager{
		app:       a,
		events:    appEventSink{app: a},
		cron:      cron.New(cron.WithChain(cron.Recover(cron.DefaultLogger))),
		path:      filepath.Join(filepath.Dir(a.configPath), "scheduled_tasks.json"),
		tasks:     map[string]*ScheduledTask{},
		entries:   map[string]cron.EntryID{},
		timers:    map[string]*time.Timer{},
		schedules: map[string]cron.Schedule{},
		cancels:   map[string]context.CancelFunc{},
		runSem:    make(chan struct{}, 1),
	}
	loadErr := manager.load()
	manager.cron.Start()
	a.scheduled = manager
	return loadErr
}

func (a *App) stopScheduledTaskManager() {
	a.scheduledMu.Lock()
	manager := a.scheduled
	a.scheduled = nil
	a.scheduledMu.Unlock()
	if manager != nil {
		manager.stop()
	}
}

func (a *App) scheduledTaskManager() (*scheduledTaskManager, error) {
	a.scheduledMu.Lock()
	manager := a.scheduled
	a.scheduledMu.Unlock()
	if manager == nil {
		return nil, errors.New("scheduled task manager is not initialized")
	}
	return manager, nil
}

func (a *App) ListScheduledTasks() []ScheduledTask {
	manager, err := a.scheduledTaskManager()
	if err != nil {
		return []ScheduledTask{}
	}
	return manager.list()
}

func (a *App) DeleteScheduledTask(id string) error {
	manager, err := a.scheduledTaskManager()
	if err != nil {
		return err
	}
	return manager.delete(id)
}

func (a *App) executeScheduledTaskTool(cfg ConfigState, req ScheduledTaskToolRequest) (any, error) {
	manager, err := a.scheduledTaskManager()
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(strings.TrimSpace(req.Action)) {
	case "create":
		task, err := manager.create(cfg, req)
		if err != nil {
			return nil, err
		}
		view := scheduledTaskToolView(task)
		return ScheduledTaskToolResult{Task: &view}, nil
	case "list":
		tasks := manager.list()
		limit := len(tasks)
		if limit > 50 {
			limit = 50
		}
		views := make([]ScheduledTaskToolView, 0, limit)
		for i := 0; i < limit; i++ {
			views = append(views, scheduledTaskToolView(&tasks[i]))
		}
		return ScheduledTaskToolResult{Tasks: views, Count: len(tasks), Truncated: len(tasks) > limit}, nil
	case "delete":
		id := strings.TrimSpace(req.ID)
		if id == "" {
			return nil, codedToolError("E_SCHEDULED_TASK_ID", errors.New("id is required for delete"))
		}
		if err := manager.delete(id); err != nil {
			return nil, err
		}
		return ScheduledTaskToolResult{Deleted: id}, nil
	default:
		return nil, codedToolError("E_SCHEDULED_TASK_ACTION", errors.New("action must be create, list, or delete"))
	}
}

func (m *scheduledTaskManager) load() error {
	// Scheduled tasks are intentionally process-local. Remove the legacy file
	// on every startup so older persistent definitions cannot restart silently.
	if err := os.Remove(m.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (m *scheduledTaskManager) stop() {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return
	}
	m.stopped = true
	for _, timer := range m.timers {
		timer.Stop()
	}
	for _, cancel := range m.cancels {
		cancel()
	}
	m.mu.Unlock()
	ctx := m.cron.Stop()
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
	}
	_ = os.Remove(m.path)
}

func (m *scheduledTaskManager) create(cfg ConfigState, req ScheduledTaskToolRequest) (*ScheduledTask, error) {
	now := time.Now()
	schedule, err := parseScheduledTaskSchedule(req.Schedule)
	if err != nil {
		return nil, err
	}
	task := &ScheduledTask{
		ID:             "task_" + newID(),
		Name:           strings.TrimSpace(req.Name),
		Instruction:    strings.TrimSpace(req.Instruction),
		Workspace:      strings.TrimSpace(cfg.Workspace),
		Schedule:       schedule,
		PermissionMode: "workspace_write",
		MaxSteps:       defaultScheduledTaskSteps,
		TimeoutSeconds: defaultScheduledTaskTimeout,
		CreatedAt:      now.UnixMilli(),
		UpdatedAt:      now.UnixMilli(),
		LastStatus:     "scheduled",
	}
	if task.Name == "" {
		return nil, codedToolError("E_SCHEDULED_TASK_NAME", errors.New("name is required"))
	}
	if task.Instruction == "" {
		return nil, codedToolError("E_SCHEDULED_TASK_INSTRUCTION", errors.New("instruction is required"))
	}
	if task.Workspace == "" {
		return nil, codedToolError("E_SCHEDULED_TASK_WORKSPACE", errors.New("workspace is required"))
	}
	root, err := workspaceRoot(cfg)
	if err != nil {
		return nil, err
	}
	task.Workspace = root
	if err := normalizeScheduledTask(task, now); err != nil {
		return nil, err
	}
	if task.Schedule.Type == "once" {
		at, _ := time.Parse(time.RFC3339, task.Schedule.At)
		if !at.After(now) {
			return nil, codedToolError("E_SCHEDULED_TASK_AT", errors.New("one-time schedule must be in the future"))
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return nil, errors.New("scheduled task manager is stopped")
	}
	if len(m.tasks) >= maxScheduledTasks {
		return nil, codedToolError("E_SCHEDULED_TASK_LIMIT", fmt.Errorf("scheduled task limit reached (%d)", maxScheduledTasks))
	}
	m.tasks[task.ID] = task
	if err := m.registerLocked(task, now); err != nil {
		delete(m.tasks, task.ID)
		return nil, err
	}
	if err := m.persistLocked(); err != nil {
		m.unregisterLocked(task.ID)
		delete(m.tasks, task.ID)
		return nil, err
	}
	copyTask := cloneScheduledTask(task)
	go m.emit("scheduled:update", map[string]any{"task": copyTask})
	return &copyTask, nil
}

// parseScheduledTaskSchedule delegates to the pure scheduler.ParseSchedule
// and converts the tool-local Schedule back to the app-facing type.
func parseScheduledTaskSchedule(value string) (ScheduledTaskSchedule, error) {
	sched, err := scheduler.ParseSchedule(value)
	if err != nil {
		return ScheduledTaskSchedule{}, err
	}
	return ScheduledTaskSchedule{
		Type:  sched.Type,
		At:    sched.At,
		Every: sched.Every,
		Cron:  sched.Cron,
	}, nil
}

func (m *scheduledTaskManager) delete(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("id is required")
	}
	m.mu.Lock()
	task := m.tasks[id]
	if task == nil {
		m.mu.Unlock()
		return codedToolError("E_SCHEDULED_TASK_NOT_FOUND", fmt.Errorf("scheduled task not found: %s", id))
	}
	m.unregisterLocked(id)
	if cancel := m.cancels[id]; cancel != nil {
		cancel()
		delete(m.cancels, id)
	}
	delete(m.tasks, id)
	err := m.persistLocked()
	m.mu.Unlock()
	if err != nil {
		return err
	}
	m.emit("scheduled:update", map[string]any{"deleted": id})
	return nil
}

func (m *scheduledTaskManager) list() []ScheduledTask {
	m.mu.Lock()
	defer m.mu.Unlock()
	tasks := make([]ScheduledTask, 0, len(m.tasks))
	for _, task := range m.tasks {
		tasks = append(tasks, cloneScheduledTask(task))
	}
	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].Running != tasks[j].Running {
			return tasks[i].Running
		}
		if tasks[i].NextRunAt == 0 {
			return false
		}
		if tasks[j].NextRunAt == 0 {
			return true
		}
		return tasks[i].NextRunAt < tasks[j].NextRunAt
	})
	return tasks
}

func (m *scheduledTaskManager) registerLocked(task *ScheduledTask, now time.Time) error {
	m.unregisterLocked(task.ID)
	switch task.Schedule.Type {
	case "once":
		at, err := time.Parse(time.RFC3339, task.Schedule.At)
		if err != nil {
			return codedToolError("E_SCHEDULED_TASK_AT", fmt.Errorf("invalid RFC3339 time: %w", err))
		}
		if !at.After(now) {
			task.NextRunAt = 0
			if task.LastRunAt == 0 {
				task.LastStatus = "missed"
				task.LastError = "one-time schedule elapsed while Ally was not running"
			}
			return nil
		}
		task.NextRunAt = at.UnixMilli()
		m.timers[task.ID] = time.AfterFunc(time.Until(at), func() { m.safeTrigger(task.ID) })
		return nil
	case "interval":
		duration, err := time.ParseDuration(task.Schedule.Every)
		if err != nil {
			return codedToolError("E_SCHEDULED_TASK_INTERVAL", fmt.Errorf("invalid interval: %w", err))
		}
		schedule := scheduler.EveryDuration(duration)
		m.schedules[task.ID] = schedule
		m.entries[task.ID] = m.cron.Schedule(schedule, cron.FuncJob(func() { m.safeTrigger(task.ID) }))
		task.NextRunAt = schedule.Next(now).UnixMilli()
		return nil
	case "cron":
		schedule, err := scheduler.ParseCron(task.Schedule.Cron)
		if err != nil {
			return codedToolError("E_SCHEDULED_TASK_CRON", fmt.Errorf("invalid cron expression: %w", err))
		}
		m.schedules[task.ID] = schedule
		m.entries[task.ID] = m.cron.Schedule(schedule, cron.FuncJob(func() { m.safeTrigger(task.ID) }))
		task.NextRunAt = schedule.Next(now).UnixMilli()
		return nil
	default:
		return codedToolError("E_SCHEDULED_TASK_TYPE", errors.New("schedule.type must be once, interval, or cron"))
	}
}

func (m *scheduledTaskManager) unregisterLocked(id string) {
	if entryID, ok := m.entries[id]; ok {
		m.cron.Remove(entryID)
		delete(m.entries, id)
	}
	if timer := m.timers[id]; timer != nil {
		timer.Stop()
		delete(m.timers, id)
	}
	delete(m.schedules, id)
}

func (m *scheduledTaskManager) trigger(id string) {
	m.mu.Lock()
	task := m.tasks[id]
	if task == nil || m.stopped {
		m.mu.Unlock()
		return
	}
	if task.Running {
		m.advanceNextRunLocked(task, time.Now())
		task.LastStatus = "skipped"
		task.LastError = "previous execution is still running"
		task.UpdatedAt = time.Now().UnixMilli()
		_ = m.persistLocked()
		copyTask := cloneScheduledTask(task)
		m.mu.Unlock()
		m.emit("scheduled:update", map[string]any{"task": copyTask})
		return
	}
	select {
	case m.runSem <- struct{}{}:
	default:
		m.advanceNextRunLocked(task, time.Now())
		task.LastStatus = "skipped"
		task.LastError = "another scheduled task is running"
		task.UpdatedAt = time.Now().UnixMilli()
		_ = m.persistLocked()
		copyTask := cloneScheduledTask(task)
		m.mu.Unlock()
		m.emit("scheduled:update", map[string]any{"task": copyTask})
		return
	}
	now := time.Now()
	task.Running = true
	task.LastRunAt = now.UnixMilli()
	task.LastStatus = "running"
	task.LastError = ""
	task.UpdatedAt = now.UnixMilli()
	if schedule := m.schedules[id]; schedule != nil {
		task.NextRunAt = schedule.Next(now).UnixMilli()
	} else {
		task.NextRunAt = 0
	}
	_ = m.persistLocked()
	copyTask := cloneScheduledTask(task)
	m.mu.Unlock()
	m.emit("scheduled:run_start", map[string]any{"task": copyTask})
	go m.run(copyTask)
}

func (m *scheduledTaskManager) safeTrigger(id string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			m.finish(id, "failed", "", fmt.Sprintf("scheduler panic: %v", recovered))
		}
	}()
	m.trigger(id)
}

func (m *scheduledTaskManager) advanceNextRunLocked(task *ScheduledTask, now time.Time) {
	if schedule := m.schedules[task.ID]; schedule != nil {
		task.NextRunAt = schedule.Next(now).UnixMilli()
	} else {
		task.NextRunAt = 0
	}
}

func (m *scheduledTaskManager) run(task ScheduledTask) {
	defer func() { <-m.runSem }()
	finished := false
	defer func() {
		if recovered := recover(); recovered != nil && !finished {
			m.finish(task.ID, "failed", "", fmt.Sprintf("scheduled task panic: %v", recovered))
		}
	}()

	timeout := time.Duration(task.TimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(m.app.ctx, timeout)
	defer cancel()
	m.mu.Lock()
	if m.tasks[task.ID] == nil {
		m.mu.Unlock()
		return
	}
	m.cancels[task.ID] = cancel
	m.mu.Unlock()

	cfg := m.app.effectiveConfig(ConfigState{Workspace: task.Workspace})
	cfg.Workspace = task.Workspace

	if err := m.app.acquireSubagentSlot(ctx); err != nil {
		m.finish(task.ID, "failed", "", err.Error())
		finished = true
		return
	}
	result, runErr := m.app.executeDelegate(ctx, cfg, "scheduled:"+task.ID, AgentDelegateRequest{
		Task:         "You are executing a temporary scheduled task in isolated fresh context. It exists only for the current Ally process. Do not create, list, or delete scheduled tasks. Complete the instruction and finish with a concise report for the user.\n\n" + task.Instruction,
		Description:  "Scheduled: " + task.Name,
		CleanContext: false,
		MaxSteps:     task.MaxSteps,
		tools:        m.app.scheduledTaskTools(cfg),
	}, cancel)
	m.app.releaseSubagentSlot()
	if result != nil && result.AgentID != "" {
		m.app.subRunsMu.Lock()
		delete(m.app.subRuns, result.AgentID)
		m.app.subRunsMu.Unlock()
	}
	status := "completed"
	summary := ""
	errText := ""
	if result != nil {
		summary = tailString(strings.TrimSpace(result.Summary), scheduledTaskSummaryLimit)
		if result.Status != "" && result.Status != "completed" {
			status = result.Status
		}
		if result.Error != "" {
			errText = result.Error
		}
	}
	if runErr != nil {
		status = "failed"
		errText = runErr.Error()
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		status = "timed_out"
		errText = fmt.Sprintf("execution exceeded %ds", task.TimeoutSeconds)
	} else if errors.Is(ctx.Err(), context.Canceled) && runErr != nil {
		status = "cancelled"
	}
	m.finish(task.ID, status, summary, tailString(errText, 8*1024))
	finished = true
}

func (m *scheduledTaskManager) finish(id, status, summary, errText string) {
	m.mu.Lock()
	task := m.tasks[id]
	if task == nil {
		delete(m.cancels, id)
		m.mu.Unlock()
		return
	}
	delete(m.cancels, id)
	task.Running = false
	task.LastStatus = status
	task.LastSummary = summary
	task.LastError = errText
	task.RunCount++
	if status == "completed" {
		task.ConsecutiveFailures = 0
	} else {
		task.ConsecutiveFailures++
	}
	task.UpdatedAt = time.Now().UnixMilli()
	_ = m.persistLocked()
	copyTask := cloneScheduledTask(task)
	m.mu.Unlock()
	event := "scheduled:run_done"
	if status != "completed" {
		event = "scheduled:run_error"
	}
	m.emit(event, map[string]any{"task": copyTask})
}

func (m *scheduledTaskManager) emit(name string, payload map[string]any) {
	if m.events != nil {
		m.events.Emit(name, payload)
	}
}

func (m *scheduledTaskManager) persistLocked() error {
	return nil
}

func normalizeScheduledTask(task *ScheduledTask, now time.Time) error {
	task.ID = strings.TrimSpace(task.ID)
	task.Name = strings.TrimSpace(task.Name)
	task.Instruction = strings.TrimSpace(task.Instruction)
	task.Workspace = strings.TrimSpace(task.Workspace)
	if task.ID == "" {
		return errors.New("task id is required")
	}
	// Scheduled tasks intentionally run with the normal workspace tool set.
	// Force the persisted value so tasks created before this behavior migrate automatically.
	task.PermissionMode = "workspace_write"

	steps, err := scheduler.ValidateSteps(task.MaxSteps)
	if err != nil {
		return err
	}
	task.MaxSteps = steps

	timeout, err := scheduler.ValidateTimeout(task.TimeoutSeconds)
	if err != nil {
		return err
	}
	task.TimeoutSeconds = timeout

	// Normalize and validate the schedule via the pure scheduler package.
	sched := scheduler.Schedule{
		Type:  task.Schedule.Type,
		At:    task.Schedule.At,
		Every: task.Schedule.Every,
		Cron:  task.Schedule.Cron,
	}
	scheduler.NormalizeSchedule(&sched)
	if err := scheduler.ValidateSchedule(sched); err != nil {
		return err
	}
	// Write the normalized values back so persistence and downstream code
	// see the trimmed/lowercased form.
	task.Schedule.Type = sched.Type
	task.Schedule.At = sched.At
	task.Schedule.Every = sched.Every
	task.Schedule.Cron = sched.Cron

	if task.CreatedAt == 0 {
		task.CreatedAt = now.UnixMilli()
	}
	if task.UpdatedAt == 0 {
		task.UpdatedAt = task.CreatedAt
	}
	return nil
}

func scheduledTaskToolView(task *ScheduledTask) ScheduledTaskToolView {
	return ScheduledTaskToolView{
		ID: task.ID, Name: task.Name, Workspace: task.Workspace, Schedule: task.Schedule,
		PermissionMode: task.PermissionMode, MaxSteps: task.MaxSteps, TimeoutSeconds: task.TimeoutSeconds,
		NextRunAt: task.NextRunAt, LastRunAt: task.LastRunAt, LastStatus: task.LastStatus,
		RunCount: task.RunCount, Running: task.Running,
	}
}

func cloneScheduledTask(task *ScheduledTask) ScheduledTask {
	if task == nil {
		return ScheduledTask{}
	}
	return *task
}

func (a *App) scheduledTaskTools(cfg ConfigState) []openai.Tool {
	all := a.buildToolsForConfig(cfg)
	filtered := make([]openai.Tool, 0, len(all)-1)
	for _, tool := range all {
		if tool.Function != nil && (tool.Function.Name == "scheduled_task" || tool.Function.Name == "ask") {
			continue
		}
		filtered = append(filtered, tool)
	}
	return filtered
}

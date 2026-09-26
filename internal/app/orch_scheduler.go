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
	"encoding/json"
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
	ID   string `json:"id"`
	Name string `json:"name"`
	// 任务内容二选一：Instruction 走 LLM agent 委托，Command 走命令执行
	// （复用 command 工具的安全检查与工作区 cwd 边界）。
	Instruction    string                `json:"instruction,omitempty"`
	Command        string                `json:"command,omitempty"`
	Workspace      string                `json:"workspace"`
	Schedule       ScheduledTaskSchedule `json:"schedule"`
	MaxSteps       int                   `json:"maxSteps"`
	TimeoutSeconds int                   `json:"timeoutSeconds"`
	CreatedAt      int64                 `json:"createdAt"`
	UpdatedAt      int64                 `json:"updatedAt"`
	NextRunAt      int64                 `json:"nextRunAt,omitempty"`
	LastRunAt      int64                 `json:"lastRunAt,omitempty"`
	LastStatus     string                `json:"lastStatus"`
	LastSummary    string                `json:"lastSummary,omitempty"`
	LastError      string                `json:"lastError,omitempty"`
	RunCount       int                   `json:"runCount"`
	// Model 是创建任务时正在使用的模型身份（providerName + model id，只记身份）。
	// LLM 委托型任务按它在每个触发点展开模型，之后界面切模型不影响已建任务；
	// 命令型任务不用模型，留空。身份对应的预设被删掉后，任务会以 failed 收场
	// 并说明原因，而不是静默换一个模型跑（见 modelConfigForTask）。
	Model *ModelIdentity `json:"model,omitempty"`
	// ConsecutiveFailures 仅供任务中心展示；没有任何退避/停用策略消费它。
	ConsecutiveFailures int  `json:"consecutiveFailures"`
	Running             bool `json:"running"`
}

type ScheduledTaskToolRequest struct {
	Action      string `json:"action"`
	ID          string `json:"id,omitempty"`
	Name        string `json:"name,omitempty"`
	Instruction string `json:"instruction,omitempty"`
	Command     string `json:"command,omitempty"`
	Schedule    string `json:"schedule,omitempty"`
}

type ScheduledTaskToolView struct {
	ID             string                `json:"id"`
	Name           string                `json:"name"`
	Instruction    string                `json:"instruction,omitempty"`
	Command        string                `json:"command,omitempty"`
	Workspace      string                `json:"workspace"`
	Schedule       ScheduledTaskSchedule `json:"schedule"`
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
	// Scheduled tasks persist across restarts: recurring tasks (interval/cron)
	// resume from "now" without catching up missed fires, pending one-shot tasks
	// survive, fired one-shots are dropped, and past-due ones are reported as
	// missed instead of firing late.
	// 调用时机：startScheduledTaskManager 在把 manager 挂到 App 上之前调用它，此时
	// 没有任何其他 goroutine 拿得到 m，所以下面 registerLocked 的 Locked 后缀遵循
	// 的是「尚无并发」这一前提，而不是「调用方已持锁」。
	data, err := os.ReadFile(m.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var stored []ScheduledTask
	if err := json.Unmarshal(data, &stored); err != nil {
		// Corrupt store: keep it aside as .bak and start empty instead of
		// blocking startup.
		_ = os.Rename(m.path, m.path+".bak")
		return nil
	}
	now := time.Now()
	for i := range stored {
		task := &stored[i]
		// 落盘的运行标记（running / lastStatus=running）只说明上个进程死在执行中，
		// 那次执行本身已经不存在：清标记并把状态改判为「已中断」。否则任务卡片会
		// 一直显示一个没翻译的 running，还与「运行中 0」自相矛盾。
		task.Running = false
		if task.LastStatus == "running" {
			task.LastStatus = "interrupted"
			task.LastError = "Ally exited while this task was running"
		}
		if task.Schedule.Type == "once" {
			at, atErr := time.Parse(time.RFC3339, task.Schedule.At)
			if atErr != nil {
				continue
			}
			// 已执行过的一次性任务不再回到列表；到点时 Ally 没开着的（LastRunAt 仍为
			// 0）留给 registerLocked 标成 missed，用户能看到它错过了什么。
			if !at.After(now) && task.LastRunAt != 0 {
				continue
			}
		}
		if err := normalizeScheduledTask(task, now); err != nil {
			continue
		}
		// 工作区已不在的任务不再排期：临时工作区随退出即删、项目目录也可能被移走，
		// 留着只会在每次到点时白失败一次。保留在列表里标成 invalid 并把 NextRunAt
		// 归零（不再触发），用户能看到原因并自行删除。
		if !scheduledTaskWorkspaceUsable(task.Workspace) {
			task.NextRunAt = 0
			task.LastStatus = "invalid"
			task.LastError = "workspace is no longer available: " + task.Workspace
			m.app.logAppError("scheduled task workspace missing", "taskId", task.ID, "workspace", task.Workspace)
			m.tasks[task.ID] = task
			continue
		}
		if err := m.registerLocked(task, now); err != nil {
			continue
		}
		m.tasks[task.ID] = task
	}
	return nil
}

// scheduledTaskWorkspaceUsable 判断任务记录的工作区是否还是个目录。任务持久化
// 之后会跨进程存活，而工作区可能先一步消失（临时工作区退出即删、项目目录被移走），
// 这种任务每次到点都只会失败一次，没有重试价值。
func scheduledTaskWorkspaceUsable(workspace string) bool {
	trimmed := strings.TrimSpace(workspace)
	if trimmed == "" {
		return false
	}
	info, err := os.Stat(trimmed)
	return err == nil && info.IsDir()
}

func (m *scheduledTaskManager) stop() {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return
	}
	m.stopped = true
	// Persist a clean final state (running flags reset) so the next launch
	// does not restore tasks stuck in "running".
	for _, task := range m.tasks {
		task.Running = false
	}
	_ = m.persistLocked()
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
		Command:        strings.TrimSpace(req.Command),
		Workspace:      strings.TrimSpace(cfg.Workspace),
		Schedule:       schedule,
		MaxSteps:       defaultScheduledTaskSteps,
		TimeoutSeconds: defaultScheduledTaskTimeout,
		CreatedAt:      now.UnixMilli(),
		UpdatedAt:      now.UnixMilli(),
		LastStatus:     "scheduled",
	}
	if task.Name == "" {
		return nil, codedToolError("E_SCHEDULED_TASK_NAME", errors.New("name is required"))
	}
	// 任务内容二选一：instruction（LLM 委托）与 command（命令执行）恰好给一个。
	if (task.Instruction == "") == (task.Command == "") {
		return nil, codedToolError("E_SCHEDULED_TASK_CONTENT", errors.New("provide exactly one of instruction or command"))
	}
	// LLM 委托型任务记下创建时正在使用的模型：任务存盘后跨重启存活，而配置里的
	// "最近使用模型"会被用户切走，所以模型必须跟任务一起定下来（命令型任务不记）。
	if task.Instruction != "" && strings.TrimSpace(cfg.Model) != "" {
		identity := ModelIdentity{ProviderName: strings.TrimSpace(cfg.ProviderName), Model: strings.TrimSpace(cfg.Model)}
		task.Model = &identity
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
			// 到点时进程没开着（load 把这类没执行过的一次性任务送到这里）：标成
			// missed 且不再排期，而不是静默丢弃，用户能看到它错过了。
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
	// 命令型任务绝不能用带 deadline 的 ctx 传给 command 工具：外层 deadline
	// 与命令自身的超时 timer 同时到期时，三路竞态（timer.C 先赢 → 收编；
	// runCtx.Done 先赢 → Cancel 杀树报 cancelled；watchCtx 已读到旧 Cancel →
	// 杀掉刚收编的进程且构成对 cmd.Cancel 的无同步并发读写）。命令超时完全
	// 由 runCommandTask 内部的 CommandRequest.Timeout（600s 上限）承担，且
	// 超时后是收编为后台服务而不是杀进程；这里只保留取消能力（手动停任务
	// /应用退出）。LLM 委托型任务仍用 WithTimeout 约束总时长。
	var ctx context.Context
	var cancel context.CancelFunc
	if strings.TrimSpace(task.Command) != "" {
		ctx, cancel = context.WithCancel(m.app.ctx)
	} else {
		ctx, cancel = context.WithTimeout(m.app.ctx, timeout)
	}
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
	// LLM 委托型任务用创建时记下的模型身份；身份已失效（预设被删）就报错收场，
	// 不静默退回"最近使用模型"——那会拿别的模型跑用户的任务。
	if task.Instruction != "" {
		expanded, ok := modelConfigForTask(cfg, task.Model)
		if !ok {
			reason := "no model is configured; configure one and recreate the task"
			if label := modelIdentityModelID(task.Model); label != "" {
				reason = fmt.Sprintf("model %q is no longer configured; fix the model list and recreate the task", label)
			}
			m.finish(task.ID, "failed", "", reason)
			finished = true
			return
		}
		cfg = expanded
	}
	// KB sources/ 只读围栏挂在 run ctx 上，计划任务的 ctx 从 app.ctx 派生
	// 不会继承它；任务可能在 KB 会话中创建、稍后触发，因此按任务自身的
	// workspace 重新计算 deny roots（与 runChat 的 withKBDenyRoots 同源），
	// KB root 的计划任务同样无法写 sources/。
	ctx = withKBDenyRoots(ctx, kbDenyRootsForConfig(cfg))

	// 命令型任务：复用 command 工具的执行边界（AST 安全检查、工作区 cwd、
	// 有界输出），不占 LLM 委托槽、不产生任何模型步骤。
	if strings.TrimSpace(task.Command) != "" {
		m.runCommandTask(task, ctx, cfg)
		finished = true
		return
	}

	if err := m.app.acquireSubagentSlot(ctx); err != nil {
		m.finish(task.ID, "failed", "", err.Error())
		finished = true
		return
	}
	// 槽位必须 defer 归还：上面的 recover defer 会吞掉 executeDelegate 的
	// panic 并直接 return，手工写在调用之后的 release 在 panic 路径上永不
	// 执行（与 app.go:2660 的 subagent 分支同形）,漏满槽位后
	// acquireSubagentSlot 会把所有委派阻塞到重启。
	result, runErr := func() (*AgentDelegateResult, error) {
		defer m.app.releaseSubagentSlot()
		return m.app.executeDelegate(ctx, cfg, "scheduled:"+task.ID, AgentDelegateRequest{
			Task:         "You are executing a scheduled task in isolated fresh context. The task persists across Ally restarts. Do not create, list, or delete scheduled tasks. Complete the instruction and finish with a concise report for the user.\n\n" + task.Instruction,
			Description:  "Scheduled: " + task.Name,
			CleanContext: false,
			MaxSteps:     task.MaxSteps,
			tools:        m.app.scheduledTaskTools(cfg),
		})
	}()
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

// runCommandTask executes a command-mode scheduled task through the same
// boundary as the command tool (safety AST, workspace cwd, bounded output,
// process-tree kill on cancel) and maps the CommandResult onto the task's
// status/summary fields.
func (m *scheduledTaskManager) runCommandTask(task ScheduledTask, ctx context.Context, cfg ConfigState) {
	// command 工具自身的上限是 600s；更长的任务超时只约束外层 ctx。
	timeout := task.TimeoutSeconds
	if timeout <= 0 || timeout > 600 {
		timeout = 600
	}
	result, runErr := m.app.runCommandWithConfig(ctx, cfg, CommandRequest{
		Command: task.Command,
		Timeout: timeout,
	})
	status := "completed"
	summary := ""
	errText := ""
	switch {
	case runErr != nil:
		status = "failed"
		errText = runErr.Error()
	case result.PromotedToService:
		// 超时命令已被收编为后台服务继续运行：任务本身没失败，但也没等到
		// 退出。标记 timed_out 并在摘要里指向服务，便于事后诊断。
		status = "timed_out"
		errText = fmt.Sprintf("command exceeded %ds; promoted to background service (still running)", timeout)
	case result.TimedOut:
		status = "timed_out"
		errText = fmt.Sprintf("command exceeded %ds", timeout)
	case result.Cancelled:
		status = "cancelled"
		errText = "command cancelled"
	case result.ExitCode != 0:
		status = "failed"
		errText = fmt.Sprintf("exit code %d", result.ExitCode)
	}
	// 摘要承载输出尾部（有界），任务中心预览直接看到命令实际打印的内容。
	if output := strings.TrimSpace(result.Output); output != "" {
		summary = tailString(output, scheduledTaskSummaryLimit)
	} else if status == "completed" {
		summary = fmt.Sprintf("exit 0 in %dms", result.DurationMS)
	}
	m.finish(task.ID, status, summary, tailString(errText, 8*1024))
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
	// One-shot tasks live on disk only while pending (NextRunAt > 0); once
	// fired or elapsed they vanish, so restarts never re-run or catch them up.
	stored := make([]ScheduledTask, 0, len(m.tasks))
	for _, task := range m.tasks {
		if task.Schedule.Type == "once" && task.NextRunAt == 0 {
			continue
		}
		stored = append(stored, cloneScheduledTask(task))
	}
	sort.Slice(stored, func(i, j int) bool { return stored[i].ID < stored[j].ID })
	data, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return err
	}
	// 落盘走会话索引那条唯一收口（writeAtomicBytes → atomicReplaceFile）：临时
	// 文件 + 原子替换，中途崩溃最多留下一个临时文件，绝不会是半截 store；且
	// Windows 上目标被占用/只读时直接 rename 会失败，helper 会先把旧文件挪开再
	// 重试，而这里的调用点大多是 `_ =` 忽略返回值的后台状态更新。
	return writeAtomicBytes(m.path, data, 0o600)
}

func normalizeScheduledTask(task *ScheduledTask, now time.Time) error {
	task.ID = strings.TrimSpace(task.ID)
	task.Name = strings.TrimSpace(task.Name)
	task.Instruction = strings.TrimSpace(task.Instruction)
	task.Command = strings.TrimSpace(task.Command)
	task.Workspace = strings.TrimSpace(task.Workspace)
	// 模型身份只保留两个字段的去空格版本；空 model id 视为没记。
	if task.Model != nil {
		identity := ModelIdentity{ProviderName: strings.TrimSpace(task.Model.ProviderName), Model: strings.TrimSpace(task.Model.Model)}
		if identity.Model == "" {
			task.Model = nil
		} else {
			task.Model = &identity
		}
	}
	if task.ID == "" {
		return errors.New("task id is required")
	}

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
		ID: task.ID, Name: task.Name, Instruction: task.Instruction, Command: task.Command,
		Workspace: task.Workspace, Schedule: task.Schedule,
		MaxSteps: task.MaxSteps, TimeoutSeconds: task.TimeoutSeconds,
		NextRunAt: task.NextRunAt, LastRunAt: task.LastRunAt, LastStatus: task.LastStatus,
		RunCount: task.RunCount, Running: task.Running,
	}
}

func cloneScheduledTask(task *ScheduledTask) ScheduledTask {
	if task == nil {
		return ScheduledTask{}
	}
	copyTask := *task
	if task.Model != nil {
		identity := *task.Model
		copyTask.Model = &identity
	}
	return copyTask
}

// modelIdentityModelID 取身份里的 model id（无身份时空串），只用于报错文案。
func modelIdentityModelID(id *ModelIdentity) string {
	if id == nil {
		return ""
	}
	return strings.TrimSpace(id.Model)
}

// modelConfigForTask 按任务记下的模型身份展开模型字段。
//   - 记了身份且能在 models[] 里找到：展开成那一条（ok=true）。
//   - 没记身份（旧任务文件 / 命令型任务）：用 cfg 自带的模型（即配置里展开
//     出来的"最近使用模型"），这与记录模型之前的旧行为一致；cfg 没有模型时
//     ok=false。
//   - 记了身份但已失效（预设被删）：ok=false，调用方按失败处理。
func modelConfigForTask(cfg ConfigState, id *ModelIdentity) (ConfigState, bool) {
	if id == nil || strings.TrimSpace(id.Model) == "" {
		return cfg, strings.TrimSpace(cfg.Model) != ""
	}
	index := modelIndexByIdentity(cfg.Models, *id)
	if index < 0 {
		return cfg, false
	}
	applyModelEntry(&cfg, cfg.Models[index])
	return cfg, true
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

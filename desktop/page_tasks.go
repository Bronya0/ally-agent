// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/desktop/uikit"
	"fmt"
	"math"
	"strings"
	"time"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

func (a *desktop) loadTaskCenter(manual ...bool) {
	isManual := len(manual) > 0 && manual[0]
	if a.tasksF.Loading {
		// A click that lands while a snapshot is in flight must still produce
		// a fresh load; remember it and start one when the current one ends.
		if isManual {
			a.tasksF.ManualPending = true
		}
		return
	}
	a.tasksF.Loading = true
	a.tasksF.ScheduledChanges = make(map[string]scheduledTaskChange)
	a.tasksF.ServiceChanges = make(map[string]backend.ServiceInfo)
	go func() {
		tasks := a.core.ListScheduledTasks()
		services := a.core.ListServices().Services
		a.win.Update(func() {
			// The backend snapshots can predate lifecycle events already delivered
			// to this window while the requests were in flight. Reapply the latest
			// event per ID before publishing the snapshot.
			for id, change := range a.tasksF.ScheduledChanges {
				if change.deleted {
					tasks = removeScheduledTask(tasks, id)
				} else {
					tasks = upsertScheduledTask(tasks, change.task)
				}
			}
			for _, service := range a.tasksF.ServiceChanges {
				services = upsertServiceInfo(services, service)
			}
			a.tasksF.Loading = false
			a.tasksF.ScheduledChanges = nil
			a.tasksF.ServiceChanges = nil
			a.tasks = tasks
			a.services = services
			if a.tasksF.ManualPending {
				a.tasksF.ManualPending = false
				a.loadTaskCenter(true)
			} else if isManual {
				a.notifySuccess("任务列表已刷新")
			}
		})
	}()
}

func (a *desktop) tasksPageBody(c *ui.Context) {
	if a.tasksF.InstructionOpen == nil {
		a.tasksF.InstructionOpen = map[string]bool{}
	}
	if a.tasksF.StopBusy == nil {
		a.tasksF.StopBusy = map[string]bool{}
	}
	if a.tasksF.DeleteBusy == nil {
		a.tasksF.DeleteBusy = map[string]bool{}
	}
	workspace := ""
	if t := a.current(); t != nil {
		workspace = t.Workspace
	}
	if a.tasksF.Scope == 0 && workspace == "" {
		a.tasksF.Scope = 1
	}
	ui.Row(c).Gap(gapField).AlignItems(ui.Center).Wrap().Children(func() {
		if segmentedInk(c, "task-scope", &a.tasksF.Scope, a.secondaryInk(), "当前工作区", "全部").Changed() && a.tasksF.Scope == 0 && workspace == "" {
			a.tasksF.Scope = 1
		}
		if a.tasksF.Scope == 0 && workspace != "" {
			uikit.Tooltip(c, ui.Text(c, short(workspace, 60)).Font(monoFamily).FontSize(fsTiny).TextColor(a.secondaryInk()).SingleLine(), workspace)
		}
		ui.Spacer(c)
		if a.tasksF.Loading {
			ui.Spinner(c).Size(13, 13)
		}
		flat(c, "刷新").OnClick(func() { a.loadTaskCenter(true) })
	})
	runningServices, runningTasks := 0, 0
	for _, s := range a.services {
		if s.Status == "running" {
			runningServices++
		}
	}
	for _, t := range a.tasks {
		if t.Running {
			runningTasks++
		}
	}
	tabs := []string{
		fmt.Sprintf("后台服务 (%d/%d)", runningServices, len(a.services)),
		fmt.Sprintf("定时任务 (%d/%d)", runningTasks, len(a.tasks)),
	}
	a.scrollTabs(c, "tasks-tabs", &a.tasksF.Tab, tabs...)
	ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Gap(gapSection).Children(func() {
			if a.tasksF.Tab == 0 {
				a.servicesSection(c, workspace)
			} else {
				a.scheduledSection(c, workspace)
			}
		})
	})
	a.taskLogDialog(c)
}

func (a *desktop) servicesSection(c *ui.Context, workspace string) {
	services := a.filteredServices(workspace)
	running := 0
	for _, s := range services {
		if s.Status == "running" {
			running++
		}
	}
	fieldNote(c, fmt.Sprintf("运行中 %d", running), a.secondaryInk())
	if len(services) == 0 {
		emptyState(c, "暂无后台服务", "模型通过 service 工具启动的后台进程会出现在这里。", a.secondaryInk())
		return
	}
	for _, service := range services {
		service := service
		card(c, "service-"+service.ID, func() {
			ui.Row(c).Gap(gapField).AlignItems(ui.Center).Wrap().Children(func() {
				name := service.Name
				if name == "" {
					name = service.ID
				}
				ui.Text(c, name).FontSize(fsBody).Bold()
				statusBadge(c, serviceStatusLabel(service.Status), service.Status)
				if service.Promoted {
					statusBadge(c, "超时转后台", "warn")
				}
				ui.Spacer(c)
				if service.Status == "running" {
					label := "停止"
					if a.tasksF.StopBusy[service.ID] {
						label = "停止中…"
					}
					cardDanger(c, label).Disabled(a.tasksF.StopBusy[service.ID]).OnClick(func() { a.stopService(service) })
				}
			})
			if service.Description != "" {
				uikit.Tooltip(c, ui.Text(c, service.Description).FontSize(fsSmall).TextColor(a.secondaryInk()).SingleLine(), service.Description)
			}
			if a.tasksF.Scope == 1 && service.Workspace != "" {
				fieldNote(c, "工作区 · "+service.Workspace, a.secondaryInk())
			}
			detailsOpen := a.mcpF.ToolOpen["service-"+service.ID]
			detailsIcon := "chevron"
			if detailsOpen {
				detailsIcon = "chevrondown"
			}
			disclosure(c, "card-action-详情-"+service.ID, "详情", disclosureCard, detailsIcon).AlignSelf(ui.Start).OnClick(func() {
				if a.mcpF.ToolOpen == nil {
					a.mcpF.ToolOpen = map[string]bool{}
				}
				a.mcpF.ToolOpen["service-"+service.ID] = !detailsOpen
			})
			if detailsOpen {
				kv(c, "ID", service.ID, a.secondaryInk())
				kv(c, "PID", fmt.Sprint(service.PID), a.secondaryInk())
				kv(c, "工作目录", service.Cwd, a.secondaryInk())
				if service.Command != "" {
					codeBlock(c, "service-cmd-"+service.ID, service.Command, 60, a.secondaryInk())
				}
				if service.Status == "running" {
					kv(c, "启动时间", fmtTime(service.StartedAt), a.secondaryInk())
				} else {
					kv(c, "退出码", fmt.Sprint(service.ExitCode), a.secondaryInk())
					kv(c, "停止时间", fmtTime(service.StoppedAt), a.secondaryInk())
				}
				kv(c, "输出缓冲", fmt.Sprintf("%s（保留最新部分）", fmtBytes(service.OutputBytes)), a.secondaryInk())
			}
			if service.OutputTail != "" {
				holder := ui.Column(c.Key("service-tail-holder-" + service.ID))
				preview := memoAt[textMemo](holder, "preview").get(service.OutputTail, func(s string) string { return tailLines(asciiStrip(s), 6) })
				holder.Children(func() { codeBlock(c, "service-tail-"+service.ID, preview, 100, a.secondaryInk()) })
			}
			if service.Error != "" {
				inlineError(c, "service-error-"+service.ID, service.Error)
			}
			cardFlat(c, "查看完整日志").AlignSelf(ui.Start).OnClick(func() {
				a.openServiceLog(service)
			})
		})
	}
}

func (a *desktop) filteredServices(workspace string) []backend.ServiceInfo {
	var out []backend.ServiceInfo
	for _, s := range a.services {
		if a.tasksF.Scope == 0 && workspace != "" && !sameWorkspace(s.Workspace, workspace) {
			continue
		}
		out = append(out, s)
	}
	return out
}

func serviceStatusLabel(status string) string {
	switch status {
	case "running":
		return "运行中"
	case "exited", "stopped":
		return "已停止"
	case "failed":
		return "失败"
	}
	return status
}

func (a *desktop) stopService(service backend.ServiceInfo) {
	if a.tasksF.StopBusy[service.ID] {
		return
	}
	a.tasksF.StopBusy[service.ID] = true
	go func() {
		info, err := a.core.StopService(backend.StopServiceRequest{ID: service.ID})
		a.win.Update(func() {
			delete(a.tasksF.StopBusy, service.ID)
			if err != nil {
				a.notifyError(err.Error())
				return
			}
			for i := range a.services {
				if a.services[i].ID == info.ID {
					a.services[i] = info
				}
			}
			a.notify("服务已停止")
		})
	}()
}

// showServiceLog opens the dialog on the newest output the list already has; it
// keeps the fetch separate so the dialog state can be checked on its own.
func (a *desktop) showServiceLog(service backend.ServiceInfo) {
	a.tasksF.LogOpen = true
	a.tasksF.LogTitle = service.Name
	if a.tasksF.LogTitle == "" {
		a.tasksF.LogTitle = service.ID
	}
	a.tasksF.LogService = service.ID
	a.tasksF.LogRequestSeq++
	a.tasksF.LogComplete = false
	a.tasksF.LogTruncated = false
	a.tasksF.LogMeta = serviceMeta(service)
	a.tasksF.LogBody = service.OutputTail
	a.followServiceLog()
}

func (a *desktop) openServiceLog(service backend.ServiceInfo) {
	a.showServiceLog(service)
	a.fetchServiceLog(service.ID)
}

// loggedService is the service the open log dialog belongs to. The dialog stays
// usable when that service has already left the list (stopped, or a refreshed
// list raced the dialog), so the callers treat a miss as "nothing to refresh".
func (a *desktop) loggedService() (backend.ServiceInfo, bool) {
	for _, service := range a.services {
		if a.tasksF.LogService != "" && service.ID == a.tasksF.LogService {
			return service, true
		}
	}
	return backend.ServiceInfo{}, false
}

// followServiceLog puts the log dialog on its newest line. math.MaxFloat32 is
// the end of a tracked scroll container, and the container writes the settled
// offset back, so asking once is enough and the pointer still scrolls.
func (a *desktop) followServiceLog() {
	a.tasksF.LogScroll.Y = math.MaxFloat32
}

// serviceLogAtEnd reports whether the dialog shows the newest line, including
// the frame right after followServiceLog, before layout settles the offset.
func (a *desktop) serviceLogAtEnd() bool {
	s := a.tasksF.LogScroll
	return s.Y > 1e8 || s.Y >= s.MaxY-2
}

func (a *desktop) fetchServiceLog(id string) {
	a.tasksF.LogRequestSeq++
	seq := a.tasksF.LogRequestSeq
	go func() {
		result, err := a.core.GetServiceOutput(id)
		a.win.Update(func() {
			if a.tasksF.LogService != id || !a.tasksF.LogOpen || a.tasksF.LogRequestSeq != seq {
				return
			}
			if err != nil {
				if !a.tasksF.LogComplete {
					a.tasksF.LogBody = err.Error()
				}
				a.notifyError("读取服务日志失败：" + err.Error())
				return
			}
			// The full output replaces the tail the card showed, and the end moved
			// with it: keep following unless the reader scrolled up.
			atEnd := a.serviceLogAtEnd()
			a.tasksF.LogBody = result.Output
			a.tasksF.LogComplete = true
			a.tasksF.LogTruncated = result.Truncated
			if atEnd {
				a.followServiceLog()
			}
		})
	}()
}

func serviceMeta(service backend.ServiceInfo) string {
	parts := []string{serviceStatusLabel(service.Status)}
	if service.PID > 0 {
		parts = append(parts, fmt.Sprintf("PID %d", service.PID))
	}
	if service.Cwd != "" {
		parts = append(parts, service.Cwd)
	}
	if service.OutputBytes > 0 {
		parts = append(parts, fmtBytes(service.OutputBytes))
	}
	return strings.Join(parts, " · ")
}

func (a *desktop) scheduledSection(c *ui.Context, workspace string) {
	tasks := a.filteredTasks(workspace)
	running := 0
	for _, task := range tasks {
		if task.Running {
			running++
		}
	}
	fieldNote(c, fmt.Sprintf("运行中 %d · 任务会持久保存，重新启动 Ally 后继续运行", running), a.secondaryInk())
	if len(tasks) == 0 {
		emptyState(c, "暂无定时任务", "可以让模型通过 scheduled_task 工具创建。", a.secondaryInk())
		return
	}
	for _, task := range tasks {
		task := task
		card(c, "task-"+task.ID, func() {
			ui.Row(c).Gap(gapField).AlignItems(ui.Center).Wrap().Children(func() {
				ui.Text(c, task.Name).FontSize(fsBody).Bold()
				status := task.LastStatus
				if task.Running {
					status = "running"
				}
				statusBadge(c, taskStatusLabel(status), status)
				if task.Command != "" {
					neutralChip(c, "命令")
				} else {
					neutralChip(c, "Agent")
				}
				fieldNote(c, task.ID, a.secondaryInk())
				ui.Spacer(c)
				if task.LastSummary != "" || task.LastError != "" {
					cardFlat(c, "查看完整日志").OnClick(func() { a.openTaskLog(task) })
				}
				busy := a.tasksF.DeleteBusy[task.ID]
				label := "删除任务"
				if busy {
					label = "删除中…"
				}
				cardDanger(c, label).Disabled(busy).OnClick(func() { a.deleteTask(task) })
			})
			schedule := scheduleText(task.Schedule)
			next := "不再触发"
			if task.NextRunAt > 0 {
				next = "下次运行 " + time.UnixMilli(task.NextRunAt).Local().Format("2006-01-02 15:04")
			}
			ui.Row(c).Gap(gapSection).Children(func() {
				fieldNote(c, schedule, a.secondaryInk())
				ui.Spacer(c)
				fieldNote(c, next, a.secondaryInk())
			})
			if task.Command != "" {
				codeBlock(c, "task-cmd-"+task.ID, task.Command, 60, a.secondaryInk())
			} else {
				holder := ui.Column(c.Key("task-instruction-holder-" + task.ID))
				preview := memoAt[memoOf[string, taskInstructionPreview]](holder, "preview").get(task.Instruction, compactTaskInstructionPreview)
				open := a.tasksF.InstructionOpen[task.ID]
				instruction := preview.text
				label, icon := "展开指令", "chevron"
				if open {
					instruction = task.Instruction
					label, icon = "收起指令", "chevrondown"
				}
				holder.Children(func() {
					ui.Row(c).Gap(gapField).AlignItems(ui.Start).Children(func() {
						text := ui.Text(c, instruction).FontSize(fsSmall).TextColor(a.secondaryInk()).Grow(1)
						if !open {
							text.MaxLines(2)
						}
						if preview.truncated {
							disclosure(c, "task-instruction-"+task.ID, label, disclosureCard, icon).Shrink(0).OnClick(func() {
								a.tasksF.InstructionOpen[task.ID] = !open
							})
						}
					})
				})
			}
			if a.tasksF.Scope == 1 && task.Workspace != "" {
				fieldNote(c, "工作区 · "+task.Workspace, a.secondaryInk())
			}
			facts := []string{fmt.Sprintf("上次运行 %s", fmtTime(task.LastRunAt)), fmt.Sprintf("运行 %d 次", task.RunCount)}
			if task.ConsecutiveFailures > 0 {
				facts = append(facts, fmt.Sprintf("连续失败 %d 次", task.ConsecutiveFailures))
			}
			if task.MaxSteps > 0 || task.TimeoutSeconds > 0 {
				facts = append(facts, fmt.Sprintf("单次 %d 步 · %d 秒", task.MaxSteps, task.TimeoutSeconds))
			}
			fieldNote(c, strings.Join(facts, "  ·  "), a.secondaryInk())
		})
	}
}

type taskInstructionPreview struct {
	text      string
	truncated bool
}

func compactTaskInstructionPreview(s string) taskInstructionPreview {
	flat := strings.Join(strings.Fields(s), " ")
	preview, truncated := cutRunes(flat, 140)
	if truncated {
		preview = strings.TrimRight(preview, " ,.;:，。；：") + "…"
	}
	return taskInstructionPreview{text: preview, truncated: truncated}
}

func (a *desktop) filteredTasks(workspace string) []backend.ScheduledTask {
	var out []backend.ScheduledTask
	for _, task := range a.tasks {
		if a.tasksF.Scope == 0 && workspace != "" && !sameWorkspace(task.Workspace, workspace) {
			continue
		}
		out = append(out, task)
	}
	return out
}

func taskStatusLabel(status string) string {
	switch strings.ToLower(status) {
	case "running":
		return "运行中"
	case "done", "completed":
		return "已完成"
	case "pending", "waiting":
		return "等待中"
	case "failed", "error":
		return "失败"
	case "timeout":
		return "超时"
	case "cancelled", "canceled":
		return "已取消"
	case "skipped":
		return "已跳过"
	case "missed":
		return "已错过"
	case "interrupted":
		return "已中断"
	case "invalid":
		return "无效"
	}
	if status == "" {
		return "未运行"
	}
	return status
}

func scheduleText(s backend.ScheduledTaskSchedule) string {
	switch {
	case s.At != "":
		return "单次 · " + s.At
	case s.Every != "":
		return "每 " + s.Every
	case s.Cron != "":
		return "Cron " + s.Cron
	}
	return "调度未知"
}

func (a *desktop) deleteTask(task backend.ScheduledTask) {
	if a.tasksF.DeleteBusy[task.ID] {
		return
	}
	a.confirmAction("删除定时任务", "删除后会取消正在运行的任务，且无法恢复。", func() error {
		return a.core.DeleteScheduledTask(task.ID)
	}, func() {
		a.tasks = filterTasks(a.tasks, task.ID)
		delete(a.tasksF.InstructionOpen, task.ID)
	})
}

func filterTasks(tasks []backend.ScheduledTask, id string) []backend.ScheduledTask {
	out := tasks[:0]
	for _, task := range tasks {
		if task.ID != id {
			out = append(out, task)
		}
	}
	return out
}

func (a *desktop) openTaskLog(task backend.ScheduledTask) {
	a.tasksF.LogOpen = true
	a.tasksF.LogTitle = task.Name
	a.tasksF.LogService = ""
	a.tasksF.LogRequestSeq++
	a.tasksF.LogComplete = false
	a.tasksF.LogTruncated = false
	a.tasksF.LogMeta = scheduleText(task.Schedule)
	a.tasksF.LogBody = task.LastSummary
	a.tasksF.LogScroll.X, a.tasksF.LogScroll.Y = 0, 0
	if task.LastError != "" {
		if a.tasksF.LogBody != "" {
			a.tasksF.LogBody += "\n\n"
		}
		a.tasksF.LogBody += task.LastError
	}
}

func (a *desktop) taskLogDialog(c *ui.Context) {
	if !a.tasksF.LogOpen {
		return
	}
	_, h := c.Size()
	modalCard(c, &a.tasksF.LogOpen, 900, func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, a.tasksF.LogTitle).FontSize(fsDialogTitle).Bold()
			ui.Spacer(c)
			if a.tasksF.LogService != "" {
				flat(c, "回到最新").OnClick(func() {
					// Jump first: the backdrop already shows the newest tail, so the
					// button has to work even when the refresh below finds nothing.
					a.followServiceLog()
					if service, ok := a.loggedService(); ok {
						a.fetchServiceLog(service.ID)
					}
				})
			}
			flat(c, "复制").OnClick(func() {
				c.WriteClipboard(a.tasksF.LogBody)
				c.Toast("已复制日志")
			})
		})
		meta := a.tasksF.LogMeta
		if a.tasksF.LogTruncated {
			meta += " · 已截断"
		}
		fieldNote(c, meta, a.secondaryInk())
		// The log keeps its own scroll position: a running service appends, and the
		// reader needs to get back to the newest line without reopening the dialog.
		scroll := ui.ScrollBoth(c.Key("task-log-body")).FillWidth().MinWidth(0).Height(min(float32(460), max(float32(160), h*0.45))).Shrink(0).Clip().
			Label("服务日志").Background(c.Theme().Background).Radius(radiusSmall).
			TrackScroll(&a.tasksF.LogScroll)
		body := memoAt[textMemo](scroll, "log-body").get(a.tasksF.LogBody, asciiStrip)
		scroll.Children(func() {
			toolCodeLines(c, body, "", a.secondaryInk())
		})
		modalActions(c, func() { flat(c, "关闭").OnClick(func() { a.tasksF.LogOpen = false }) })
	})
}

// asciiStrip removes ANSI escape sequences so the native text view shows the
// same readable output the web terminal did.
func asciiStrip(s string) string {
	if !strings.ContainsRune(s, '\x1b') {
		return s
	}
	var out strings.Builder
	out.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != 0x1b {
			out.WriteByte(s[i])
			continue
		}
		if i+1 < len(s) && s[i+1] == '[' {
			i += 2
			for i < len(s) && (s[i] < '@' || s[i] > '~') {
				i++
			}
			continue
		}
		if i+1 < len(s) {
			i++
		}
	}
	return out.String()
}

func (a *desktop) taskPopupDialog(c *ui.Context) {
	if !a.taskPopup {
		return
	}
	if a.tasksF.StopBusy == nil {
		a.tasksF.StopBusy = map[string]bool{}
	}
	if a.tasksF.DeleteBusy == nil {
		a.tasksF.DeleteBusy = map[string]bool{}
	}
	workspace := ""
	if t := a.current(); t != nil {
		workspace = t.Workspace
	}
	modalCard(c, &a.taskPopup, widthModalWide, func() {
		title := "后台服务"
		if a.tasksF.Tab == 1 {
			title = "定时任务"
		}
		ui.Row(c).Children(func() {
			ui.Text(c, title).FontSize(fsDialogTitle).Bold()
			ui.Spacer(c)
			iconAction(c, "task-popup-close", "关闭", "close", 24, func() { a.taskPopup = false })
		})
		ui.Row(c).Children(func() {
			segmentedInk(c, "popup-task-scope", &a.tasksF.Scope, a.secondaryInk(), "当前工作区", "全部")
			if a.tasksF.Scope == 0 && workspace != "" {
				uikit.Tooltip(c, ui.Text(c, workspace).FontSize(fsSmall).SingleLine().MinWidth(0), workspace)
			}
			ui.Spacer(c)
			flat(c, "刷新").OnClick(func() { a.loadTaskCenter(true) })
		})
		if a.tasksF.Tab == 0 {
			a.servicesSection(c, workspace)
		} else {
			a.scheduledSection(c, workspace)
		}
	})
	a.taskLogDialog(c)
}

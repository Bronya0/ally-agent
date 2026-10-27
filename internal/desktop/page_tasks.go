// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"fmt"
	"strings"
	"time"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

func (a *desktop) loadTaskCenter() {
	if a.tasksF.Loading {
		return
	}
	a.tasksF.Loading = true
	go func() {
		tasks := a.core.ListScheduledTasks()
		services := a.core.ListServices().Services
		a.win.Update(func() {
			a.tasksF.Loading = false
			a.tasks = tasks
			a.services = services
		})
	}()
}

func (a *desktop) tasksPageBody(c *ui.Context) {
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
	if a.tasksF.Scope == 1 && workspace == "" {
		a.tasksF.Scope = 0
	}
	ui.Row(c).Gap(gapField).AlignItems(ui.Center).Wrap().Children(func() {
		if segmented(c, "task-scope", &a.tasksF.Scope, "当前工作区", "全部").Changed() && a.tasksF.Scope == 1 && workspace == "" {
			a.tasksF.Scope = 0
		}
		if a.tasksF.Scope == 1 && workspace != "" {
			ui.Text(c, short(workspace, 60)).Font(monoFamily).FontSize(fsTiny).TextColor(c.Theme().TextMuted).Tooltip(workspace)
		}
		ui.Spacer(c)
		if a.tasksF.Loading {
			ui.Spinner(c).Size(13, 13)
		}
		flat(c, "刷新").OnClick(func() { a.loadTaskCenter() })
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
	scrollTabs(c, &a.tasksF.Tab, tabs...)
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
	fieldNote(c, fmt.Sprintf("运行中 %d", running))
	if len(services) == 0 {
		emptyState(c, "暂无后台服务", "模型通过 service 工具启动的后台进程会出现在这里。")
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
					dangerButton(c, label).Disabled(a.tasksF.StopBusy[service.ID]).OnClick(func() { a.stopService(service) })
				}
			})
			if service.Description != "" {
				ui.Text(c, service.Description).FontSize(fsSmall).TextColor(c.Theme().TextMuted).SingleLine().Tooltip(service.Description)
			}
			if a.tasksF.Scope == 1 && service.Workspace != "" {
				fieldNote(c, "工作区 · "+service.Workspace)
			}
			detailsOpen := a.mcpF.ToolOpen["service-"+service.ID]
			if flat(c, map[bool]string{true: "收起详情 ⌃", false: "详情 ⌄"}[detailsOpen]).AlignSelf(ui.Start).Clicked() {
				if a.mcpF.ToolOpen == nil {
					a.mcpF.ToolOpen = map[string]bool{}
				}
				a.mcpF.ToolOpen["service-"+service.ID] = !detailsOpen
			}
			if detailsOpen {
				kv(c, "ID", service.ID)
				kv(c, "PID", fmt.Sprint(service.PID))
				kv(c, "工作目录", service.Cwd)
				if service.Command != "" {
					codeBlock(c, "service-cmd-"+service.ID, service.Command, 60)
				}
				if service.Status == "running" {
					kv(c, "启动时间", fmtTime(service.StartedAt))
				} else {
					kv(c, "退出码", fmt.Sprint(service.ExitCode))
					kv(c, "停止时间", fmtTime(service.StoppedAt))
				}
				kv(c, "输出缓冲", fmt.Sprintf("%s（保留最新部分）", fmtBytes(service.OutputBytes)))
			}
			if service.OutputTail != "" {
				codeBlock(c, "service-tail-"+service.ID, tailLines(asciiStrip(service.OutputTail), 6), 100)
			}
			if service.Error != "" {
				inlineError(c, "service-error-"+service.ID, service.Error)
			}
			flat(c, "查看完整日志").AlignSelf(ui.Start).OnClick(func() {
				a.openServiceLog(service)
			})
		})
	}
}

func (a *desktop) filteredServices(workspace string) []backend.ServiceInfo {
	var out []backend.ServiceInfo
	for _, s := range a.services {
		if a.tasksF.Scope == 1 && workspace != "" && !sameWorkspace(s.Workspace, workspace) {
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
				a.notify(err.Error())
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

func (a *desktop) openServiceLog(service backend.ServiceInfo) {
	a.tasksF.LogOpen = true
	a.tasksF.LogTitle = service.Name
	if a.tasksF.LogTitle == "" {
		a.tasksF.LogTitle = service.ID
	}
	a.tasksF.LogService = service.ID
	a.tasksF.LogMeta = serviceMeta(service)
	a.tasksF.LogBody = service.OutputTail
	go func() {
		result, err := a.core.GetServiceOutput(service.ID)
		a.win.Update(func() {
			if err != nil {
				a.tasksF.LogBody = err.Error()
				return
			}
			a.tasksF.LogBody = result.Output
			if result.Truncated {
				a.tasksF.LogMeta += " · 已截断"
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
	fieldNote(c, fmt.Sprintf("运行中 %d · 任务会持久保存，重新启动 Ally 后继续运行", running))
	if len(tasks) == 0 {
		emptyState(c, "暂无定时任务", "可以让模型通过 scheduled_task 工具创建。")
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
				fieldNote(c, task.ID)
				ui.Spacer(c)
				if task.LastSummary != "" || task.LastError != "" {
					flat(c, "查看完整日志").OnClick(func() { a.openTaskLog(task) })
				}
				busy := a.tasksF.DeleteBusy[task.ID]
				label := "删除任务"
				if busy {
					label = "删除中…"
				}
				dangerButton(c, label).Disabled(busy).OnClick(func() { a.deleteTask(task) })
			})
			schedule := scheduleText(task.Schedule)
			next := "不再触发"
			if task.NextRunAt > 0 {
				next = "下次运行 " + time.UnixMilli(task.NextRunAt).Local().Format("2006-01-02 15:04")
			}
			ui.Row(c).Gap(gapSection).Children(func() {
				fieldNote(c, schedule)
				ui.Spacer(c)
				fieldNote(c, next)
			})
			if task.Command != "" {
				codeBlock(c, "task-cmd-"+task.ID, task.Command, 60)
			} else {
				ui.Text(c, task.Instruction).FontSize(fsSmall).TextColor(c.Theme().TextMuted).MaxLines(3)
			}
			if a.tasksF.Scope == 1 && task.Workspace != "" {
				fieldNote(c, "工作区 · "+task.Workspace)
			}
			facts := []string{fmt.Sprintf("上次运行 %s", fmtTime(task.LastRunAt)), fmt.Sprintf("运行 %d 次", task.RunCount)}
			if task.ConsecutiveFailures > 0 {
				facts = append(facts, fmt.Sprintf("连续失败 %d 次", task.ConsecutiveFailures))
			}
			if task.MaxSteps > 0 || task.TimeoutSeconds > 0 {
				facts = append(facts, fmt.Sprintf("单次 %d 步 · %d 秒", task.MaxSteps, task.TimeoutSeconds))
			}
			fieldNote(c, strings.Join(facts, "  ·  "))
			if task.LastSummary != "" {
				codeBlock(c, "task-summary-"+task.ID, tailLines(asciiStrip(task.LastSummary), 4), 80)
			}
			if task.LastError != "" {
				inlineError(c, "task-error-"+task.ID, short(task.LastError, 240))
			}
		})
	}
}

func (a *desktop) filteredTasks(workspace string) []backend.ScheduledTask {
	var out []backend.ScheduledTask
	for _, task := range a.tasks {
		if a.tasksF.Scope == 1 && workspace != "" && !sameWorkspace(task.Workspace, workspace) {
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
	a.tasksF.LogMeta = scheduleText(task.Schedule)
	a.tasksF.LogBody = task.LastSummary
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
	modalCard(c, &a.tasksF.LogOpen, 900, func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, a.tasksF.LogTitle).FontSize(17).Bold()
			ui.Spacer(c)
			if a.tasksF.LogService != "" {
				flat(c, "回到最新").OnClick(func() {
					for _, service := range a.services {
						if service.ID == a.tasksF.LogService {
							a.openServiceLog(service)
						}
					}
				})
			}
			flat(c, "复制").OnClick(func() {
				c.WriteClipboard(a.tasksF.LogBody)
				c.Toast("已复制日志")
			})
		})
		fieldNote(c, a.tasksF.LogMeta)
		codeBlock(c, "task-log-body", asciiStrip(a.tasksF.LogBody), 460)
		modalActions(c, func() { flat(c, "关闭").OnClick(func() { a.tasksF.LogOpen = false }) })
	})
}

// asciiStrip removes ANSI escape sequences so the native text view shows the
// same readable output the web terminal did.
func asciiStrip(s string) string {
	out := make([]rune, 0, len(s))
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		if runes[i] != 0x1b {
			out = append(out, runes[i])
			continue
		}
		if i+1 < len(runes) && runes[i+1] == '[' {
			i += 2
			for i < len(runes) && (runes[i] < '@' || runes[i] > '~') {
				i++
			}
			continue
		}
		if i+1 < len(runes) {
			i++
		}
	}
	return string(out)
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
			ui.Text(c, title).FontSize(17).Bold()
			ui.Spacer(c)
			iconAction(c, "task-popup-close", "关闭", "close", 24, func() { a.taskPopup = false })
		})
		ui.Row(c).Children(func() {
			segmented(c, "popup-task-scope", &a.tasksF.Scope, "当前工作区", "全部")
			ui.Spacer(c)
			flat(c, "刷新").OnClick(func() { a.loadTaskCenter() })
		})
		if a.tasksF.Tab == 0 {
			a.servicesSection(c, workspace)
		} else {
			a.scheduledSection(c, workspace)
		}
	})
	a.taskLogDialog(c)
}

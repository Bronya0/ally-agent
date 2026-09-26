// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"path/filepath"
	"testing"
)

// TestScheduledTaskRecordsModelIdentity: 配置里只有「最近使用模型」身份、且它会被
// 用户切走，所以 LLM 委托型任务必须在创建时把自己的模型身份记进任务记录（之后切
// 模型不影响已建任务）；命令型任务不碰模型，留空。
func TestScheduledTaskRecordsModelIdentity(t *testing.T) {
	root := t.TempDir()
	app := NewApp()
	app.configPath = filepath.Join(root, "config.json")
	app.config = ConfigState{
		Workspace: root,
		Models: []ModelConfig{
			{ProviderName: "Relay", Model: "relay-model", APIFormat: apiFormatOpenAIChat, APIKeys: []string{"k1"}},
			{ProviderName: "Other", Model: "other-model", APIFormat: apiFormatOpenAIChat, APIKeys: []string{"k2"}},
		},
	}
	if err := app.startScheduledTaskManager(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.stopScheduledTaskManager)

	// 创建任务时执行方交给工具的请求配置（StartChat 展开后的形状）。
	runCfg := ConfigState{
		Workspace:    root,
		ProviderName: "Relay",
		APIFormat:    apiFormatOpenAIChat,
		Model:        "relay-model",
		Models:       app.config.Models,
	}
	if _, err := app.executeScheduledTaskTool(runCfg, ScheduledTaskToolRequest{
		Action:      "create",
		Name:        "llm",
		Instruction: "summarize the repo",
		Schedule:    "30m",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.executeScheduledTaskTool(runCfg, ScheduledTaskToolRequest{
		Action:   "create",
		Name:     "cmd",
		Command:  "echo hi",
		Schedule: "30m",
	}); err != nil {
		t.Fatal(err)
	}

	tasks := app.ListScheduledTasks()
	if len(tasks) != 2 {
		t.Fatalf("expected two tasks, got %#v", tasks)
	}
	byName := map[string]ScheduledTask{}
	for _, task := range tasks {
		byName[task.Name] = task
	}
	llm := byName["llm"]
	if llm.Model == nil || llm.Model.ProviderName != "Relay" || llm.Model.Model != "relay-model" {
		t.Fatalf("LLM task must record the model in use at creation, got %#v", llm.Model)
	}
	if cmd := byName["cmd"]; cmd.Model != nil {
		t.Fatalf("command task must not record a model, got %#v", cmd.Model)
	}

	// 任务记录的模型按身份在 models[] 里展开：命中就用那一条，预设被删就明确失败，
	// 不静默换一个模型跑用户的任务。
	resolved, ok := modelConfigForTask(ConfigState{Models: app.config.Models}, llm.Model)
	if !ok || resolved.ProviderName != "Relay" || resolved.Model != "relay-model" {
		t.Fatalf("recorded identity must expand from models[], got ok=%v cfg=%#v", ok, resolved)
	}
	if _, ok := modelConfigForTask(ConfigState{Models: []ModelConfig{{Model: "other-model"}}}, llm.Model); ok {
		t.Fatal("a dangling identity must not silently fall back to another model")
	}
	// 旧任务文件没有模型身份：回落到配置里的模型（与记录模型之前的行为一致）。
	legacy, ok := modelConfigForTask(ConfigState{Model: "other-model"}, nil)
	if !ok || legacy.Model != "other-model" {
		t.Fatalf("tasks without a recorded identity must fall back to the request config, got ok=%v cfg=%#v", ok, legacy)
	}
	if _, ok := modelConfigForTask(ConfigState{}, nil); ok {
		t.Fatal("no recorded identity and no configured model must be reported as unusable")
	}
}

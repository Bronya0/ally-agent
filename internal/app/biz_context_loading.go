// SPDX-License-Identifier: GPL-3.0-only
package app

import "slices"

// ContextLoadingOption describes one optional automatic context source.
type ContextLoadingOption struct{ ID, Label, Description string }

var contextLoadingOptions = []ContextLoadingOption{
	{"user_instructions", "用户级指令", "加载 ~/.agents/AGENTS.md。"},
	{"project_instructions", "工作区根指令", "加载根目录 AGENTS.md，兼容 CLAUDE.md。"},
	{"subdirectory_instructions", "子目录指令", "加载一级非隐藏子目录的 AGENTS.md。"},
	{"user_profile", "用户档案", "加载 USER.md 中的跨项目偏好。"},
	{"memory_index", "记忆索引", "加载记忆路径和描述，正文仍按需读取。"},
	{"project_lessons", "项目教训", "加载 LESSONS.md 的近期教训。"},
	{"code_graph", "代码图谱", "加载 CODEGRAPH.md 的项目导航。"},
	{"custom_prompt", "自定义提示词", "加载设置中的自定义提示词。"},
	{"skills", "技能清单", "加载已启用技能的元数据，正文按需加载。"},
	{"workspace_map", "工作区结构", "加载目录、文件大小及技术栈，不包含文件正文。"},
}

func ContextLoadingOptions() []ContextLoadingOption { return slices.Clone(contextLoadingOptions) }
func (cfg ConfigState) ContextEnabled(id string) bool {
	return !slices.Contains(cfg.DisabledContext, id)
}

type contextSettingsSnapshot struct{ disabledContext, disabledTools []string }

// Keep choices across prompt refresh/compaction; release them with the session.
// Preview callers must never create a snapshot.
func (a *App) contextSettingsForSession(sid string, cfg ConfigState, freeze bool) ConfigState {
	if sid == "" {
		return cfg
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	snapshot, exists := a.sessionContextSettings[sid]
	if !exists {
		if !freeze {
			return cfg
		}
		snapshot = contextSettingsSnapshot{slices.Clone(cfg.DisabledContext), slices.Clone(cfg.DisabledTools)}
		if a.sessionContextSettings == nil {
			a.sessionContextSettings = map[string]contextSettingsSnapshot{}
		}
		a.sessionContextSettings[sid] = snapshot
	}
	cfg.DisabledContext, cfg.DisabledTools = slices.Clone(snapshot.disabledContext), slices.Clone(snapshot.disabledTools)
	return cfg
}

func optionalContext(cfg ConfigState, id string, load func() string) string {
	if !cfg.ContextEnabled(id) {
		return ""
	}
	return load()
}

// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"fmt"
	"sort"
	"strings"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

var skillSources = []struct{ id, label string }{
	{"builtin", "内置"},
	{"project", "项目"},
	{"user", "用户"},
}

func (a *desktop) skillsPageBody(c *ui.Context) {
	if a.skillsF.ToggleBusy == nil {
		a.skillsF.ToggleBusy = map[string]bool{}
	}
	ui.Row(c).Gap(gapField).AlignItems(ui.Center).Wrap().Children(func() {
		ui.TextInput(c.Key("skill-search"), &a.skillsF.Search).Placeholder("搜索").Label("搜索技能").Width(200)
		ui.Spacer(c)
		flat(c, "刷新").OnClick(func() { a.loadSkills() })
		a.linkMenu(c, "skills-market", [][2]string{{"魔搭 ModelScope", "https://modelscope.cn"}, {"OpenAgentSkill", "https://openagentskill.com"}, {"skills.sh Trending", "https://skills.sh"}})
		primary(c, "添加").OnClick(func() { a.openSkillDialog() })
	})
	enabled := 0
	for _, s := range a.skills {
		if !a.skillDisabled(s.Name) {
			enabled++
		}
	}
	fieldNote(c, fmt.Sprintf("%d 个已启用 · %d 个可用", enabled, len(a.skills)))
	filtered := a.filteredSkills()
	if len(a.skills) > 0 {
		counts := map[string]int{}
		for _, s := range filtered {
			counts[sourceKey(s.Source)]++
		}
		var labels []string
		var ids []string
		for _, src := range skillSources {
			if counts[src.id] > 0 {
				labels = append(labels, fmt.Sprintf("%s %d", src.label, counts[src.id]))
				ids = append(ids, src.id)
			}
		}
		if len(ids) > 0 {
			index := 0
			for i, id := range ids {
				if id == a.skillsF.SelectedSource() {
					index = i
				}
			}
			if index >= len(ids) {
				index = 0
			}
			if scrollTabs(c, &index, labels...) {
				a.skillsF.Source = index
				a.skillsF.sourceIDs = ids
			}
			a.skillsF.sourceIDs = ids
			a.skillsF.Source = index
			active := ids[index]
			var visible []backend.SkillDefinition
			for _, s := range filtered {
				if sourceKey(s.Source) == active {
					visible = append(visible, s)
				}
			}
			filtered = visible
		}
	}
	ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Gap(gapSection).Children(func() {
			if a.skillsF.Loading && len(a.skills) == 0 {
				busyRow(c, "正在读取技能…")
				return
			}
			if len(a.skills) == 0 {
				emptyState(c, "暂无可用技能", "把 SKILL.md 放进 ~/.agents/skills 或项目的 .agents/skills。")
				return
			}
			if len(filtered) == 0 {
				if strings.TrimSpace(a.skillsF.Search) != "" {
					emptyState(c, "无匹配结果", "")
				} else {
					emptyState(c, "该来源暂无技能", "")
				}
				return
			}
			sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].Name < filtered[j].Name })
			for _, s := range filtered {
				s := s
				disabled := a.skillDisabled(s.Name)
				enabled := !disabled
				rowCard(c, "skill-"+s.Name, func() {
					ui.Row(c).Gap(gapSection).AlignItems(ui.Center).Children(func() {
						ui.Column(c).Grow(1).Gap(2).Children(func() {
							ui.Row(c).Gap(gapInline).AlignItems(ui.Center).Children(func() {
								ui.Text(c, s.Name).FontSize(fsBody).Bold()
								neutralChip(c, skillSourceLabel(s.Source))
								fieldNote(c, "/"+s.Name)
							})
							desc := s.Description
							if desc == "" {
								desc = s.WhenToUse
							}
							if desc == "" {
								desc = "无描述"
							}
							ui.Text(c, desc).FontSize(fsSmall).TextColor(c.Theme().TextMuted).SingleLine().Tooltip(desc)
							path := s.Path
							if path == "" {
								path = s.Dir
							}
							if path != "" {
								link := ui.Text(c, path).FontSize(fsTiny).TextColor(c.Theme().TextMuted).SingleLine().Tooltip("点击在文件管理器中打开: " + path)
								link.Cursor(ui.CursorPointer)
								if link.Clicked() {
									a.job(func() error { return a.core.OpenPathInFileManager(path) })
								}
							}
						})
						if path := s.Path; path != "" {
							flat(c, "查看").Tooltip("打开 SKILL.md").OnClick(func() {
								a.job(func() error { return a.core.OpenPathInFileManager(path) })
							})
						}
						if a.skillsF.ToggleBusy[s.Name] {
							ui.Spinner(c).Size(13, 13)
						}
						if sw := ui.Switch(c.Key("skill-switch-"+s.Name), &enabled).Label(s.Name); sw.Changed() {
							a.toggleSkill(s.Name, enabled)
						}
					})
				})
			}
		})
	})
	a.skillDialog(c)
}

func sourceKey(source string) string {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "builtin":
		return "builtin"
	case "project":
		return "project"
	case "user":
		return "user"
	}
	return "other"
}

func (a *desktop) filteredSkills() []backend.SkillDefinition {
	query := strings.ToLower(strings.TrimSpace(a.skillsF.Search))
	var out []backend.SkillDefinition
	for _, s := range a.skills {
		if query != "" {
			haystack := strings.ToLower(s.Name + " " + s.Description + " " + s.WhenToUse + " " + s.Path + " " + s.Dir)
			if !strings.Contains(haystack, query) {
				continue
			}
		}
		out = append(out, s)
	}
	return out
}

func skillSourceLabel(source string) string {
	switch sourceKey(source) {
	case "builtin":
		return "内置"
	case "project":
		return "项目"
	case "user":
		return "用户"
	}
	return "其他"
}

func (s *skillsForm) SelectedSource() string {
	if s.Source >= 0 && s.Source < len(s.sourceIDs) {
		return s.sourceIDs[s.Source]
	}
	return ""
}

func (a *desktop) skillDisabled(name string) bool {
	for _, disabled := range a.config.DisabledSkills {
		if strings.EqualFold(disabled, name) {
			return true
		}
	}
	return false
}

func (a *desktop) toggleSkill(name string, enabled bool) {
	if a.skillsF.ToggleBusy[name] {
		return
	}
	a.skillsF.ToggleBusy[name] = true
	go func() {
		var err error
		if enabled {
			_, err = a.core.ActivateSkill(name)
		} else {
			err = a.core.DeactivateSkill(name)
		}
		state, stateErr := a.core.GetSkillState()
		cfg, cfgErr := a.core.GetConfig()
		a.win.Update(func() {
			delete(a.skillsF.ToggleBusy, name)
			if err != nil {
				a.status = fmt.Sprintf("%s技能失败：%s", map[bool]string{true: "启用", false: "停用"}[enabled], err.Error())
				return
			}
			if stateErr == nil {
				a.skills = state.Skills
			}
			if cfgErr == nil {
				a.config = cfg
			}
		})
	}()
}

func (a *desktop) loadSkills() {
	if a.skillsF.Loading {
		return
	}
	a.skillsF.Loading = true
	go func() {
		state, err := a.core.GetSkillState()
		a.win.Update(func() {
			a.skillsF.Loading = false
			if err != nil {
				a.status = "读取技能状态失败：" + err.Error()
				return
			}
			a.skills = state.Skills
		})
	}()
}

func (a *desktop) openSkillDialog() {
	a.skillsF.AddOpen = true
	a.skillsF.AddTarget = "user"
	a.skillsF.AddName = ""
	a.skillsF.AddDesc = ""
	a.skillsF.AddWhen = ""
	a.skillsF.AddContent = ""
}

func (a *desktop) skillDialog(c *ui.Context) {
	if !a.skillsF.AddOpen {
		return
	}
	formDialog(c, &a.skillsF.AddOpen, widthModalWide, 680, "添加技能", "", func() {
		fieldLabel(c, "保存位置")
		target := 0
		if a.skillsF.AddTarget == "project" {
			target = 1
		}
		if segmented(c, "skill-target", &target, "用户级", "项目级").AlignSelf(ui.Start).Changed() {
			a.skillsF.AddTarget = []string{"user", "project"}[target]
		}
		switch a.skillsF.AddTarget {
		case "project":
			root := ""
			if t := a.current(); t != nil {
				root = t.Workspace
			}
			fieldNote(c, fmt.Sprintf("写入 %s/.agents/skills，只对当前工作区生效。", root))
			if root == "" {
				fieldNote(c, "当前没有打开工作区，无法写入项目级技能。")
			}
		default:
			fieldNote(c, "写入 ~/.agents/skills/（每个技能一个子目录，里面是 SKILL.md），本机所有工作区都可用。")
		}
		fieldRow(c, "skill-name", "名称", &a.skillsF.AddName, false)
		fieldNote(c, "首字符必须是字母或数字，只允许字母、数字、点、短横线和下划线，不能以点结尾。")
		fieldLabel(c, "描述")
		textArea(c, "skill-desc", "描述", "技能列表用它说明什么时候该用这个技能", &a.skillsF.AddDesc, 60)
		fieldRow(c, "skill-when", "何时使用（可选）", &a.skillsF.AddWhen, false)
		fieldLabel(c, "SKILL.md 内容")
		textArea(c, "skill-content", "SKILL.md 内容", "这个技能要让模型怎么做？写正文（Markdown）。", &a.skillsF.AddContent, 200)
	}, func() {
		flat(c, "取消").OnClick(func() { a.skillsF.AddOpen = false })
		primary(c, "保存").Disabled(a.skillsF.AddBusy).OnClick(func() { a.saveSkill() })
	})
}

func (a *desktop) saveSkill() {
	name := strings.TrimSpace(a.skillsF.AddName)
	desc := strings.TrimSpace(a.skillsF.AddDesc)
	if name == "" {
		a.status = "请填写技能名称"
		return
	}
	if desc == "" {
		a.status = "请填写描述（技能列表用它说明什么时候该用这个技能）"
		return
	}
	draft := backend.SkillDraft{
		Target:      a.skillsF.AddTarget,
		Name:        name,
		Description: desc,
		WhenToUse:   strings.TrimSpace(a.skillsF.AddWhen),
		Content:     a.skillsF.AddContent,
	}
	a.skillsF.AddBusy = true
	go func() {
		path, err := a.core.SaveSkill(draft)
		state, _ := a.core.GetSkillState()
		a.win.Update(func() {
			a.skillsF.AddBusy = false
			if err != nil {
				a.status = "保存技能失败：" + err.Error()
				return
			}
			a.skillsF.AddOpen = false
			a.skillsF.Search = ""
			if state.Skills != nil {
				a.skills = state.Skills
			}
			a.notify("技能已保存：" + path)
		})
	}()
}

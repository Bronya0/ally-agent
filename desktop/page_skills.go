// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/desktop/uikit"
	"ally-dev/locales"
	"fmt"
	"sort"
	"strings"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

const skillSourcesHelpText = `技能扫描目录

项目级（当前工作区）
<工作区>/.agents/skills/
<工作区>/.claude/skills/
<工作区>/.codex/skills/

用户级（~ 表示用户主目录）
~/.agents/skills/
~/.claude/skills/
~/.codex/skills/

内置技能随 Ally 发布，不来自上述目录。

同名优先级：项目 > 用户 > 内置。
同级优先级：.agents > .claude > .codex。
目录结构：<技能名>/SKILL.md。
新建只写 .agents/skills；另外两个目录只读兼容。`

func (a *desktop) skillSourcesHelp(c *ui.Context) {
	badge := ui.Box(c.Key("skills-source-help")).Size(22, 22).Shrink(0).Center().Focusable().Label(locales.Text("技能来源帮助"))
	badge.Children(func() { iconGlyph(c, "info", 15).TextColor(a.secondaryInk()) })
	uikit.Tooltip(c, badge, locales.Text(skillSourcesHelpText))
}

var skillSources = []struct{ id, label string }{
	{"builtin", "内置"},
	{"project", "项目"},
	{"user", "用户"},
}

func (a *desktop) skillsPageBody(c *ui.Context) {
	var list *indexedMemo[skillListKey, skillList]
	if a.skillsF.ToggleBusy == nil {
		a.skillsF.ToggleBusy = map[string]bool{}
	}
	ui.Row(c).Gap(gapField).AlignItems(ui.Center).Wrap().Children(func() {
		search := uikit.Input(c.Key("skill-search"), &a.skillsF.Search).Placeholder(locales.Text("搜索")).Label(locales.Text("搜索技能")).Width(200)
		list = memoAt[indexedMemo[skillListKey, skillList]](search, "skill-list")
		ui.Spacer(c)
		flat(c, locales.Text("刷新")).OnClick(func() { a.loadSkills(true) })
		a.linkMenu(c, "skills-market", [][2]string{{locales.Text("魔搭 ModelScope"), "https://modelscope.cn"}, {"OpenAgentSkill", "https://openagentskill.com"}, {"skills.sh Trending", "https://skills.sh"}})
		primary(c, locales.Text("添加")).OnClick(func() { a.openSkillDialog() })
	})
	enabled := 0
	for _, s := range a.skills {
		if !a.skillDisabled(s.Name) {
			enabled++
		}
	}
	fieldNote(c, fmt.Sprintf(locales.Text("%d 个已启用 · %d 个可用"), enabled, len(a.skills)), a.secondaryInk())
	data := list.get(len(a.skills), a.skillsF.Search,
		func(i int) skillListKey { return skillListKeyFor(a.skills[i]) },
		func() skillList { return buildSkillList(a.skills, a.skillsF.Search) })
	filtered := data.indices
	if len(a.skills) > 0 {
		labels, ids := data.labels, data.sources
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
			if a.scrollTabs(c, "skills-source", &index, labels...) {
				a.skillsF.Source = index
				a.skillsF.sourceIDs = ids
			}
			active := ids[index]
			filtered = data.groups[active]
		}
	}
	ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Gap(gapInline).Children(func() {
			if a.skillsF.Loading && len(a.skills) == 0 {
				busyRow(c, locales.Text("正在读取技能…"), a.secondaryInk())
				return
			}
			if len(a.skills) == 0 {
				emptyState(c, locales.Text("暂无可用技能"), locales.Text("把 SKILL.md 放进 ~/.agents/skills 或项目的 .agents/skills。"), a.secondaryInk())
				return
			}
			if len(filtered) == 0 {
				if strings.TrimSpace(a.skillsF.Search) != "" {
					emptyState(c, locales.Text("无匹配结果"), "", a.secondaryInk())
				} else {
					emptyState(c, locales.Text("该来源暂无技能"), "", a.secondaryInk())
				}
				return
			}
			for _, index := range filtered {
				s := a.skills[index]
				disabled := a.skillDisabled(s.Name)
				enabled := !disabled
				rowCard(c, "skill-"+s.Name, func() {
					ui.Row(c).Gap(gapSection).AlignItems(ui.Center).Children(func() {
						ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
							ui.Row(c).Gap(gapInline).AlignItems(ui.Center).Children(func() {
								ui.Text(c, s.Name).FontSize(fsBody).Bold()
								neutralChip(c, skillSourceLabel(s.Source))
							})
							desc := s.Description
							if desc == "" {
								desc = s.WhenToUse
							}
							if desc == "" {
								desc = locales.Text("无描述")
							}
							uikit.Tooltip(c, ui.Text(c, desc).FontSize(fsSmall).TextColor(a.secondaryInk()).SingleLine(), desc)
							path := s.Path
							if path == "" {
								path = s.Dir
							}
							if sourceKey(s.Source) == "builtin" {
								uikit.Tooltip(c, ui.Text(c, locales.Text("随 Ally 内置")).FontSize(fsTiny).TextColor(a.secondaryInk()).SingleLine(), locales.Text("内置技能随应用发布"))
							} else if path != "" {
								link := uikit.Tooltip(c, ui.Text(c, path).FontSize(fsTiny).TextColor(a.secondaryInk()).SingleLine(), locales.Text("点击在文件管理器中打开: ")+path)
								link.Cursor(ui.CursorPointer)
								if link.Clicked() {
									a.job(func() error { return a.core.OpenPathInFileManager(path) })
								}
							}
						})
						skillCardActions(c.Key("skill-actions"), s.Name, s.Path, &enabled, a.skillsF.ToggleBusy[s.Name], func() { a.viewSkill(s.Name) }, func(value bool) { a.toggleSkill(s.Name, value) })
					})
				})
			}
		})
	})
	a.skillDialog(c)
	a.skillViewer(c)
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

type skillListKey struct{ name, description, when, path, dir, source string }

func skillListKeyFor(s backend.SkillDefinition) skillListKey {
	return skillListKey{s.Name, s.Description, s.WhenToUse, s.Path, s.Dir, s.Source}
}

type skillList struct {
	indices         []int
	groups          map[string][]int
	sources, labels []string
}

func buildSkillList(skills []backend.SkillDefinition, search string) skillList {
	query := strings.ToLower(strings.TrimSpace(search))
	out := skillList{groups: map[string][]int{}}
	for i, s := range skills {
		if query != "" {
			haystack := strings.ToLower(s.Name + " " + s.Description + " " + s.WhenToUse + " " + s.Path + " " + s.Dir)
			if !strings.Contains(haystack, query) {
				continue
			}
		}
		out.indices = append(out.indices, i)
	}
	sort.SliceStable(out.indices, func(i, j int) bool { return skills[out.indices[i]].Name < skills[out.indices[j]].Name })
	for _, i := range out.indices {
		source := sourceKey(skills[i].Source)
		out.groups[source] = append(out.groups[source], i)
	}
	for _, source := range skillSources {
		if count := len(out.groups[source.id]); count > 0 {
			out.sources = append(out.sources, source.id)
			out.labels = append(out.labels, fmt.Sprintf("%s %d", locales.Text(source.label), count))
		}
	}
	return out
}

func skillSourceLabel(source string) string {
	switch sourceKey(source) {
	case "builtin":
		return locales.Text("内置")
	case "project":
		return locales.Text("项目")
	case "user":
		return locales.Text("用户")
	}
	return locales.Text("其他")
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
				a.notifyError(fmt.Sprintf(locales.Text("%s技能失败：%s"), map[bool]string{true: locales.Text("启用"), false: locales.Text("停用")}[enabled], err.Error()))
				return
			}
			a.notifySuccess(fmt.Sprintf(locales.Text("技能 %s 已%s"), name, map[bool]string{true: locales.Text("启用"), false: locales.Text("停用")}[enabled]))
			if stateErr == nil {
				a.skills = state.Skills
			}
			if cfgErr == nil {
				a.config = cfg
			}
		})
	}()
}

func (a *desktop) loadSkills(manual ...bool) {
	if a.skillsF.Loading {
		return
	}
	a.skillsF.Loading = true
	refresh := len(manual) > 0 && manual[0]
	go func() {
		var state backend.SkillState
		var err error
		if refresh {
			state, err = a.core.RefreshSkillState()
		} else {
			state, err = a.core.GetSkillState()
		}
		a.win.Update(func() {
			a.skillsF.Loading = false
			if err != nil {
				a.notifyError(locales.Text("读取技能状态失败：") + err.Error())
				return
			}
			a.skills = state.Skills
			if refresh {
				a.notifySuccess(locales.Text("技能列表已刷新"))
			}
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
	formDialog(c, &a.skillsF.AddOpen, widthModalWide, 680, locales.Text("添加技能"), "", func() {
		fieldLabel(c, locales.Text("保存位置"))
		target := 0
		if a.skillsF.AddTarget == "project" {
			target = 1
		}
		if segmentedInk(c, "skill-target", &target, a.secondaryInk(), locales.Text("用户级"), locales.Text("项目级")).AlignSelf(ui.Start).Changed() {
			a.skillsF.AddTarget = []string{"user", "project"}[target]
		}
		switch a.skillsF.AddTarget {
		case "project":
			root := ""
			if t := a.current(); t != nil {
				root = t.Workspace
			}
			fieldNote(c, fmt.Sprintf(locales.Text("写入 %s/.agents/skills，只对当前工作区生效。"), root), a.secondaryInk())
			if root == "" {
				fieldNote(c, locales.Text("当前没有打开工作区，无法写入项目级技能。"), a.secondaryInk())
			}
		default:
			fieldNote(c, locales.Text("写入 ~/.agents/skills/（每个技能一个子目录，里面是 SKILL.md），本机所有工作区都可用。"), a.secondaryInk())
		}
		fieldRow(c, "skill-name", locales.Text("名称"), &a.skillsF.AddName, false)
		fieldNote(c, locales.Text("首字符必须是字母或数字，只允许字母、数字、点、短横线和下划线，不能以点结尾。"), a.secondaryInk())
		fieldLabel(c, locales.Text("描述"))
		textArea(c, "skill-desc", locales.Text("描述"), locales.Text("技能列表用它说明什么时候该用这个技能"), &a.skillsF.AddDesc, 60)
		fieldRow(c, "skill-when", locales.Text("何时使用（可选）"), &a.skillsF.AddWhen, false)
		fieldLabel(c, locales.Text("SKILL.md 内容"))
		textArea(c, "skill-content", locales.Text("SKILL.md 内容"), locales.Text("这个技能要让模型怎么做？写正文（Markdown）。"), &a.skillsF.AddContent, 200)
	}, func() {
		flat(c, locales.Text("取消")).OnClick(func() { a.skillsF.AddOpen = false })
		primary(c, locales.Text("保存")).Disabled(a.skillsF.AddBusy).OnClick(func() { a.saveSkill() })
	})
}

func (a *desktop) viewSkill(name string) {
	a.skillsF.ViewRequestSeq++
	seq := a.skillsF.ViewRequestSeq
	a.skillsF.ViewOpen = true
	a.skillsF.ViewLoading = true
	a.skillsF.ViewName = name
	a.skillsF.ViewContent = ""
	a.skillsF.ViewError = ""
	go func() {
		content, err := a.core.GetSkill(name)
		a.win.Update(func() {
			if !a.skillsF.ViewOpen || a.skillsF.ViewRequestSeq != seq {
				return
			}
			a.skillsF.ViewLoading = false
			if err != nil {
				a.skillsF.ViewError = err.Error()
				return
			}
			a.skillsF.ViewContent = content
		})
	}()
}

func (a *desktop) skillViewer(c *ui.Context) {
	if !a.skillsF.ViewOpen {
		return
	}
	formDialog(c.Key("skill-viewer"), &a.skillsF.ViewOpen, widthModalWide, 760, locales.Text("查看技能"), a.skillsF.ViewName, func() {
		switch {
		case a.skillsF.ViewLoading:
			busyRow(c, locales.Text("正在读取技能内容…"), a.secondaryInk())
		case a.skillsF.ViewError != "":
			inlineError(c, "skill-view-error", a.skillsF.ViewError)
		case a.skillsF.ViewContent == "":
			emptyState(c, locales.Text("技能内容为空"), "", a.secondaryInk())
		default:
			if a.skillsF.ViewMD == nil {
				a.skillsF.ViewMD = a.readingMarkdownOptions("skill-viewer-markdown")
			}
			a.configureReadingMarkdown(a.skillsF.ViewMD)
			markdown.View(c.Key("skill-viewer-markdown"), a.skillsF.ViewContent, a.skillsF.ViewMD).FillWidth().MinWidth(0)
		}
	}, func() {
		flat(c, locales.Text("关闭")).OnClick(func() { a.skillsF.ViewOpen = false })
	})
}

func (a *desktop) saveSkill() {
	name := strings.TrimSpace(a.skillsF.AddName)
	desc := strings.TrimSpace(a.skillsF.AddDesc)
	if name == "" {
		a.notify(locales.Text("请填写技能名称"))
		return
	}
	if desc == "" {
		a.notify(locales.Text("请填写描述（技能列表用它说明什么时候该用这个技能）"))
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
				a.notifyError(locales.Text("保存技能失败：") + err.Error())
				return
			}
			a.skillsF.AddOpen = false
			a.skillsF.Search = ""
			if state.Skills != nil {
				a.skills = state.Skills
			}
			a.notifySuccess(locales.Text("技能已保存：") + path)
		})
	}()
}

// Busy feedback stays inside the fixed-size switch; adjacent actions never move.
func skillCardActions(c *ui.Context, name, _ string, enabled *bool, busy bool, view func(), toggle func(bool)) ui.Element {
	return ui.Row(c).Shrink(0).Gap(gapSection).AlignItems(ui.Center).Children(func() {
		uikit.Tooltip(c, cardFlat(c.Key("view"), locales.Text("查看")), locales.Text("查看 SKILL.md 内容")).OnClick(view)
		uikit.SwitchWithOptions(c.Key("enabled"), enabled, name, uikit.SwitchOptions{Loading: busy}).OnClick(func() { toggle(*enabled) })
	})
}

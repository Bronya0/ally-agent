// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/desktop/uikit"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

// sshUngrouped 是界面用的未分组标签，直接取后端常量：两边各写一份“未分组”正是
// 未分组被列成两个标签的来由——后端把空分组存成这个字面量，界面又无条件补一次。
const sshUngrouped = backend.SSHUngroupedGroup

func (a *desktop) sshPageBody(c *ui.Context) {
	if a.sshF.TestBusy == nil {
		a.sshF.TestBusy = map[string]bool{}
	}
	ui.Row(c).Gap(gapField).AlignItems(ui.Center).Wrap().Children(func() {
		uikit.Input(c.Key("ssh-search"), &a.sshF.Search).Placeholder("搜索").Label("搜索 SSH 服务器").Width(200)
		ui.Spacer(c)
		flat(c, "导入").OnClick(func() { a.importSSH() })
		uikit.Tooltip(c, flat(c, "导出").Disabled(len(a.sshServers) == 0), "导出文件含密码等凭据明文，请妥善保管").OnClick(func() { a.exportSSH() })
		flat(c, "刷新").OnClick(func() { a.refreshSSH(true) })
		primary(c, "添加").OnClick(func() { a.openSSHEditor("", "") })
	})
	groups := a.sshGroups()
	if len(a.sshServers) == 0 {
		emptyState(c, "暂无配置的 SSH 服务器", "点击“添加”录入第一台服务器。", a.secondaryInk())
		a.sshEditorDialog(c)
		return
	}
	if len(groups) > 1 {
		index := 0
		for i, g := range groups {
			if g == a.sshF.GroupName {
				index = i
			}
		}
		if index >= len(groups) {
			index = 0
		}
		if a.scrollTabs(c, "ssh-groups", &index, groups...) {
			a.sshF.Group = index
			a.sshF.GroupName = groups[index]
		}
		a.sshF.GroupName = groups[index]
		a.sshF.Group = index
	}
	ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Gap(gapInline).Children(func() {
			active := sshUngrouped
			if a.sshF.GroupName != "" {
				active = a.sshF.GroupName
			}
			query := strings.ToLower(strings.TrimSpace(a.sshF.Search))
			shown := 0
			for _, node := range a.sshServers {
				group := node.Group
				if group == "" {
					group = sshUngrouped
				}
				if len(groups) > 1 && group != active {
					continue
				}
				if query != "" {
					haystack := strings.ToLower(node.Alias + " " + node.Host + " " + node.Username + " " + node.Description + " " + node.Group)
					if !strings.Contains(haystack, query) {
						continue
					}
				}
				shown++
				node := node
				rowCard(c, "ssh-"+node.ID, func() {
					ui.Row(c).Gap(gapField).AlignItems(ui.Center).Wrap().Children(func() {
						uikit.Tooltip(c, ui.Text(c, node.Host).Font(monoFamily).FontSize(fsBody).Bold(), fmt.Sprintf("%s@%s:%d", node.Username, node.Host, node.Port))
						if node.RiskLevel != "" {
							sshRiskBadge(c, node.RiskLevel)
						}
						neutralChip(c, node.Username)
						neutralChip(c, fmt.Sprintf(":%d", node.Port))
						if node.Alias != "" {
							neutralChip(c, node.Alias)
						}
						if node.Status == "pending_approval" {
							statusBadge(c, "待审批", "warn")
						}
						ui.Spacer(c)
						cardFlatBusy(c, "测试", a.sshF.TestBusy[node.ID]).OnClick(func() { a.testSSHNode(node) })
						cardFlat(c, "复制").OnClick(func() { a.openSSHClone(node) })
						cardFlat(c, "编辑").OnClick(func() { a.openSSHEditor(node.ID, node.Group) })
						cardDanger(c, "删除").OnClick(func() {
							a.confirmAction("删除服务器", node.Alias, func() error { return a.core.DeleteSSHServer(node.ID) }, func() { a.refreshSSH() })
						})
					})
					if node.Description != "" {
						uikit.Tooltip(c, ui.Text(c, node.Description).FontSize(fsSmall).TextColor(a.secondaryInk()).SingleLine(), node.Description)
					}
				})
			}
			if shown == 0 {
				emptyState(c, "无匹配结果", "", a.secondaryInk())
			}
		})
	})
	a.sshEditorDialog(c)
}

func (a *desktop) sshGroups() []string {
	seen := map[string]bool{}
	named := make([]string, 0, len(a.sshServers)+1)
	for _, node := range a.sshServers {
		group := strings.TrimSpace(node.Group)
		// 空分组与字面量“未分组”是同一个桶（后端把前者存成后者），只收一次；
		// 未分组桶最后统一追加，所以这里不能再把它当成具名分组。
		if group == "" || group == sshUngrouped || seen[group] {
			continue
		}
		seen[group] = true
		named = append(named, group)
	}
	sort.Strings(named)
	return append(named, sshUngrouped)
}

func sshGroupInput(group string) string {
	if strings.TrimSpace(group) == "" {
		return sshUngrouped
	}
	return group
}

func sshPortInput(port int) float64 {
	if port < 1 || port > 65535 {
		return 22
	}
	return float64(port)
}

func (a *desktop) openSSHEditor(id, group string) {
	a.sshF.EditorOpen = true
	a.sshF.CloneMode = false
	a.sshF.CloneSource = ""
	a.sshF.EditingID = id
	a.sshF.GroupInput = sshGroupInput(group)
	a.sshF.Port = 22
	if id != "" {
		for _, node := range a.sshServers {
			if node.ID == id {
				a.sshF.Draft = node
				a.sshF.GroupInput = sshGroupInput(node.Group)
				a.sshF.Port = sshPortInput(node.Port)
				a.sshF.OrgGroup = node.Group
				return
			}
		}
	}
	a.sshF.Draft = backend.SSHServerNode{Port: 22, AuthType: "password", RiskLevel: "low", CreatedBy: "user", Status: "approved", Group: group}
}

func (a *desktop) openSSHClone(source backend.SSHServerNode) {
	clone := source
	clone.ID = ""
	clone.Alias = nextSSHCloneAlias(source.Alias, a.sshServers)
	clone.CreatedAtMS = 0
	clone.UpdatedAtMS = 0
	a.sshF.EditorOpen = true
	a.sshF.CloneMode = true
	a.sshF.CloneSource = source.Alias
	a.sshF.EditingID = ""
	a.sshF.OrgGroup = source.Group
	a.sshF.Draft = clone
	a.sshF.GroupInput = sshGroupInput(clone.Group)
	a.sshF.Port = sshPortInput(clone.Port)
}

func nextSSHCloneAlias(base string, servers []backend.SSHServerNode) string {
	stem := strings.TrimSpace(base)
	if stem == "" {
		stem = "node"
	}
	stem += "-copy"
	taken := make(map[string]bool, len(servers))
	for _, server := range servers {
		taken[strings.ToLower(strings.TrimSpace(server.Alias))] = true
	}
	if !taken[strings.ToLower(stem)] {
		return stem
	}
	for i := 2; i <= len(servers)+2; i++ {
		candidate := fmt.Sprintf("%s-%d", stem, i)
		if !taken[strings.ToLower(candidate)] {
			return candidate
		}
	}
	return stem + "-copy"
}

func (a *desktop) sshEditorDialog(c *ui.Context) {
	if !a.sshF.EditorOpen {
		return
	}
	d := &a.sshF.Draft
	title := "添加 SSH 服务器"
	if a.sshF.CloneMode {
		title = "复制 SSH 服务器"
	} else if a.sshF.EditingID != "" {
		title = "编辑 SSH 服务器"
	}
	height := float32(600)
	if d.AuthType == "password" {
		height = 650
	} else if d.AuthType == "key" {
		height = 700
	}
	formDialog(c, &a.sshF.EditorOpen, 560, height, title, "", func() {
		fieldRow(c, "ssh-alias", "昵称", &d.Alias, false)
		if a.sshF.CloneMode {
			source := a.sshF.CloneSource
			if source == "" {
				source = "原服务器配置"
			}
			fieldNote(c, "从「"+source+"」复制；保存后会创建新的服务器配置。", a.secondaryInk())
		}
		fieldLabel(c, "分组")
		uikit.ComboboxField(c.Key("ssh-group"), &a.sshF.GroupInput, a.sshGroups(), "分组", "选择或输入分组", a.secondaryInk()).FillWidth()
		ui.Row(c).Gap(gapSection).AlignItems(ui.Start).Children(func() {
			ui.Column(c).Grow(2).Basis(0).Children(func() { fieldRow(c, "ssh-host", "主机", &d.Host, false) })
			ui.Column(c).Grow(1).Basis(0).Gap(gapTight).Children(func() {
				fieldLabel(c, "端口")
				ui.NumberInput(c.Key("ssh-port"), &a.sshF.Port, 1, 65535, 1).Label("端口")
			})
		})
		ui.Row(c).Gap(gapSection).AlignItems(ui.Start).Children(func() {
			ui.Column(c).Grow(1).Basis(0).Children(func() { fieldRow(c, "ssh-user", "用户名", &d.Username, false) })
			ui.Column(c).Grow(1).Basis(0).Gap(gapTight).Children(func() {
				fieldLabel(c, "认证方式")
				authOptions := []string{"本机免密", "私钥文件", "密码"}
				current := map[string]string{"agent": "本机免密", "key": "私钥文件", "password": "密码"}[d.AuthType]
				if current == "" {
					current = "密码"
				}
				if uikit.Select(c.Key("ssh-auth"), &current, authOptions, a.secondaryInk()).Label("认证方式").FillWidth().Changed() {
					d.AuthType = map[string]string{"本机免密": "agent", "私钥文件": "key", "密码": "password"}[current]
				}
			})
		})
		switch d.AuthType {
		case "agent":
			fieldNote(c, "使用本机 ~/.ssh/config、默认私钥或 SSH Agent，无需填写密码或私钥。", a.secondaryInk())
		case "key":
			ui.Row(c).Gap(gapField).AlignItems(ui.End).Children(func() {
				ui.Column(c).Grow(1).Children(func() { fieldRow(c, "ssh-key", "私钥路径", &d.KeyPath, false) })
				flat(c, "选择文件").OnClick(func() {
					go func() {
						p, err := a.core.SelectPrivateKeyFile()
						if err != nil {
							a.fail(err)
							return
						}
						if p != "" {
							a.win.Update(func() { d.KeyPath = p })
						}
					}()
				})
			})
			fieldRow(c, "ssh-passphrase", "私钥口令（可选）", &d.Password, true)
			fieldNote(c, "明文保存在本机配置目录，请勿共享该文件。", a.secondaryInk())
		case "password":
			fieldRow(c, "ssh-password", "密码", &d.Password, true)
			fieldNote(c, "明文保存在本机配置目录，请勿共享该文件。", a.secondaryInk())
		}
		fieldLabel(c, "用途描述")
		textArea(c, "ssh-desc", "用途描述", "例如：测试环境 Redis 节点，负责商品缓存", &d.Description, 60)
		fieldLabel(c, "风险等级")
		risk := 0
		if d.RiskLevel == "high" {
			risk = 1
		}
		ui.Row(c).Gap(8).Children(func() {
			for n, title := range []string{"常规", "生产（高危保护）"} {
				n := n
				tone := c.Theme().Success
				if n == 1 {
					tone = c.Theme().Danger
				}
				choiceButton(c, fmt.Sprintf("ssh-risk-%d", n), title, risk == n, tone).
					OnClick(func() { d.RiskLevel = []string{"low", "high"}[n] })
			}
		})
	}, func() {
		flat(c, "取消").OnClick(func() { a.sshF.EditorOpen = false; a.sshF.CloneMode = false; a.sshF.CloneSource = "" })
		primaryBusy(c, "保存", a.sshF.Busy, a.sshF.Busy).OnClick(func() { a.saveSSHNode() })
	}, func() {
		a.sshF.CloneMode = false
		a.sshF.CloneSource = ""
	})
}

func (a *desktop) saveSSHNode() {
	d := a.sshF.Draft
	d.Alias = strings.TrimSpace(d.Alias)
	d.Host = strings.TrimSpace(d.Host)
	d.Username = strings.TrimSpace(d.Username)
	d.Description = strings.TrimSpace(d.Description)
	d.Group = strings.TrimSpace(a.sshF.GroupInput)
	if d.Group == sshUngrouped {
		d.Group = ""
	}
	if d.Alias == "" {
		a.notify("昵称必填")
		return
	}
	for _, node := range a.sshServers {
		if !strings.EqualFold(node.Alias, d.Alias) {
			continue
		}
		if node.ID != a.sshF.EditingID {
			a.notify("该昵称已被其他服务器使用")
			return
		}
	}
	if d.Host == "" {
		a.notify("主机必填")
		return
	}
	if d.Username == "" {
		a.notify("用户名必填")
		return
	}
	if d.Description == "" {
		a.notify("用途描述必填")
		return
	}
	if d.AuthType == "password" && d.Password == "" {
		a.notify("请填写密码，或改用其它认证方式")
		return
	}
	if d.AuthType == "key" && d.KeyPath == "" {
		a.notify("请填写私钥路径")
		return
	}
	port := a.sshF.Port
	if math.IsNaN(port) || math.IsInf(port, 0) || port < 1 || port > 65535 || port != math.Trunc(port) {
		a.notify("端口必须是 1 至 65535 的整数")
		return
	}
	d.Port = int(port)
	if d.RiskLevel == "" {
		d.RiskLevel = "low"
	}
	a.sshF.Busy = true
	go func() {
		err := a.core.SaveSSHServer(d)
		servers := a.core.ListAllSSHServers()
		a.win.Update(func() {
			a.sshF.Busy = false
			if err != nil {
				a.notifyError(err.Error())
				return
			}
			a.sshF.EditorOpen = false
			a.sshF.CloneMode = false
			a.sshF.CloneSource = ""
			a.sshServers = servers
			a.notifySuccess("服务器配置已保存")
		})
	}()
}

func (a *desktop) testSSHNode(node backend.SSHServerNode) {
	a.sshF.TestBusy[node.ID] = true
	go func() {
		result := a.core.TestSSHServer(node)
		a.win.Update(func() {
			delete(a.sshF.TestBusy, node.ID)
			if result.OK {
				a.notify(fmt.Sprintf("SSH 连通性测试通过（%d ms）", result.DurationMS))
			} else {
				a.notifyError("SSH 连通性测试失败：" + result.Error)
			}
		})
	}()
}

func (a *desktop) importSSH() {
	go func() {
		path, err := a.core.SelectJSONFile("导入 SSH 集群配置")
		if err != nil || path == "" {
			if err != nil {
				a.fail(err)
			}
			return
		}
		data, err := a.core.ReadLocalTextFile(path)
		if err != nil {
			a.fail(err)
			return
		}
		var payload struct {
			Nodes []backend.SSHServerNode `json:"nodes"`
		}
		if err := json.Unmarshal([]byte(data), &payload); err != nil || len(payload.Nodes) == 0 {
			a.win.Update(func() { a.notify("文件格式不正确，需要包含 nodes 数组的 JSON") })
			return
		}
		added, updated := 0, 0
		for _, node := range payload.Nodes {
			existing := false
			for _, current := range a.core.ListAllSSHServers() {
				if strings.EqualFold(current.Alias, node.Alias) {
					node.ID = current.ID
					existing = true
					break
				}
			}
			if err := a.core.SaveSSHServer(node); err != nil {
				continue
			}
			if existing {
				updated++
			} else {
				added++
			}
		}
		servers := a.core.ListAllSSHServers()
		a.win.Update(func() {
			a.sshServers = servers
			a.notifySuccess(fmt.Sprintf("导入完成：新增 %d，更新 %d", added, updated))
		})
	}()
}

func (a *desktop) exportSSH() {
	payload := map[string]any{"nodes": a.sshServers}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		a.notifyError(err.Error())
		return
	}
	a.exportText("ally-ssh-cluster-"+nowDate()+".json", string(data))
}

// ---- Workspace authorization modal --------------------------------------

func (a *desktop) loadSSHAuth(workspace string) {
	a.sshF.AuthWorkspace = workspace
	a.sshF.AuthReady = false
	a.sshF.AuthDraft = nil
	a.sshF.AuthLoadSeq++
	seq, tail := a.sshF.AuthLoadSeq, a.sshAuthTail
	go func() {
		if tail != nil {
			<-tail
		}
		servers := a.core.ListAllSSHServers()
		allowed := a.core.GetWorkspaceAllowedServers(workspace)
		a.win.Update(func() {
			if seq != a.sshF.AuthLoadSeq || !a.sshF.AuthOpen || !sameWorkspace(a.sshF.AuthWorkspace, workspace) {
				return
			}
			a.sshServers = servers
			a.sshF.AuthDraft = map[string]bool{}
			for _, id := range allowed {
				a.sshF.AuthDraft[id] = true
			}
			a.sshF.AuthReady = true
			if a.sshAllowed == nil {
				a.sshAllowed = map[string][]string{}
			}
			a.sshAllowed[workspace] = allowed
		})
	}()
}

func (a *desktop) sshAuthDialog(c *ui.Context) {
	if !a.sshF.AuthOpen {
		return
	}
	workspace := a.sshF.AuthWorkspace
	dialog := modalCard(c, &a.sshF.AuthOpen, widthModalWide, func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "SSH 集群授权").FontSize(17).Bold()
			ui.Spacer(c)
			flat(c, "管理集群").OnClick(func() {
				a.commitSSHAuth(workspace)
				a.sshF.AuthOpen = false
				a.changePage("ssh")
			})
			iconAction(c, "ssh-auth-close", "关闭 SSH 集群授权", "close", 24, func() { a.commitSSHAuth(workspace); a.sshF.AuthOpen = false })
		})
		fieldNote(c, "勾选的服务器允许当前工作区的会话使用；凭据由 Ally 管理，不会传给模型。", a.secondaryInk())
		if !a.sshF.AuthReady {
			busyRow(c, "正在读取工作区授权…", a.secondaryInk())
			return
		}
		query := strings.ToLower(strings.TrimSpace(a.sshF.AuthSearch))
		onlyAllowed := a.sshF.AuthOnlyAllowed
		ui.Row(c).Gap(gapField).AlignItems(ui.Center).Children(func() {
			uikit.Input(c.Key("ssh-auth-search"), &a.sshF.AuthSearch).Placeholder("搜索").Label("搜索服务器").Width(200)
			ui.Checkbox(c.Key("ssh-auth-only"), &a.sshF.AuthOnlyAllowed, "只看已授权")
		})
		allowedCount := 0
		for _, v := range a.sshF.AuthDraft {
			if v {
				allowedCount++
			}
		}
		fieldNote(c, fmt.Sprintf("已授权 %d 台", allowedCount), a.secondaryInk())
		groups := []string{}
		seen := map[string]bool{}
		for _, node := range a.sshServers {
			if onlyAllowed && !a.sshF.AuthDraft[node.ID] {
				continue
			}
			if query != "" && !strings.Contains(strings.ToLower(node.Alias+" "+node.Host+" "+node.Username+" "+node.Description+" "+node.Group), query) {
				continue
			}
			group := strings.TrimSpace(node.Group)
			if group == "" {
				group = sshUngrouped
			}
			if !seen[group] {
				seen[group] = true
				groups = append(groups, group)
			}
		}
		sort.Strings(groups)
		if len(groups) > 0 {
			// Pin the selected group before drawing the strip: the strip
			// must sit above the list exactly like MCP's, never between
			// the note lines, or its height shifts with the selection.
			index := 0
			for i, group := range groups {
				if group == a.sshF.AuthGroup {
					index = i
					break
				}
			}
			a.sshF.AuthGroup = groups[index]
			if changed := a.scrollTabs(c, "ssh-auth-groups", &index, groups...); changed {
				for i, group := range groups {
					if i == index {
						a.sshF.AuthGroup = group
					}
				}
			}
		}
		// The tab strip and its list stay outside the scroller: only the rows
		// scroll (main caps .ssh-auth-scroll at 52vh). The list gets a fixed
		// height so switching tabs never moves the tab strip.
		ui.Scroll(c).MaxHeight(380).Children(func() {
			ui.Column(c).Gap(1).Children(func() {
				for _, node := range a.sshServers {
					node := node
					group := strings.TrimSpace(node.Group)
					if group == "" {
						group = sshUngrouped
					}
					if len(groups) > 1 && group != a.sshF.AuthGroup {
						continue
					}
					checked := a.sshF.AuthDraft[node.ID]
					if onlyAllowed && !checked {
						continue
					}
					if query != "" {
						haystack := strings.ToLower(node.Alias + " " + node.Host + " " + node.Username + " " + node.Description + " " + node.Group)
						if !strings.Contains(haystack, query) {
							continue
						}
					}
					row := ui.Row(c.Key("ssh-auth-"+node.ID)).Height(28).Padding(0, padRow).Gap(gapField).AlignItems(ui.Center).Radius(radiusSmall)
					if row.Hovered() {
						row.Background(c.Theme().SurfaceHover)
					}
					// Main toggles on row click (checkbox stops propagation);
					// the row also gets the pointer cursor like every rail row.
					row.Cursor(ui.CursorPointer)
					row.Children(func() {
						ui.Checkbox(c.Key("ssh-auth-check-"+node.ID), &checked, "").OnClick(func() {
							a.sshF.AuthDraft[node.ID] = !a.sshF.AuthDraft[node.ID]
						})
						ui.Text(c, node.Alias).FontSize(fsSmall).Width(140).SingleLine()
						ui.Text(c, node.Username+"@"+node.Host+":"+fmt.Sprint(node.Port)).Font(monoFamily).FontSize(fsTiny).TextColor(a.secondaryInk()).Width(200).SingleLine()
						uikit.Tooltip(c, ui.Text(c, node.Description).FontSize(fsTiny).TextColor(a.secondaryInk()), node.Description).SingleLine().Grow(1)
						sshRiskBadge(c, node.RiskLevel)
					})
					if row.Clicked() {
						a.sshF.AuthDraft[node.ID] = !a.sshF.AuthDraft[node.ID]
					}
				}
			})
		})
		if len(a.sshServers) == 0 {
			emptyState(c, "暂无配置的 SSH 服务器", "先在 SSH 集群页添加服务器。", a.secondaryInk())
		}

	})
	if dialog.Dismissed() && a.sshF.AuthReady {
		a.commitSSHAuth(workspace)
	}
}

func (a *desktop) commitSSHAuth(workspace string) {
	if workspace == "" || !a.sshF.AuthReady || !sameWorkspace(a.sshF.AuthWorkspace, workspace) {
		return
	}
	var ids []string
	for id, allowed := range a.sshF.AuthDraft {
		if allowed {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	// A close click can also produce Dismissed; submit this draft only once.
	a.sshF.AuthReady = false
	result := a.queueSSHAuthSave(workspace, ids)
	go func() {
		err := <-result
		a.win.Update(func() {
			a.refreshSSH()
			if err != nil {
				a.notifyError(err.Error())
				return
			}
			a.notify("当前工作区授权已更新")
		})
	}()
}

// Capture queue state on the UI thread; disk writes stay in ordered workers.
func (a *desktop) queueSSHAuthSave(workspace string, ids []string) <-chan error {
	previous := a.sshAuthTail
	done := make(chan struct{})
	result := make(chan error, 1)
	a.sshAuthTail = done
	ids = append([]string(nil), ids...)
	go func() {
		defer close(done)
		if previous != nil {
			<-previous
		}
		err := a.core.SetWorkspaceAllowedServers(workspace, ids)
		a.writeFailures.record("ssh:"+workspace, err)
		result <- err
		close(result)
	}()
	return result
}

func sshRiskBadge(c *ui.Context, level string) ui.Element {
	label, tone := "低风险", uikit.Success
	if level == "high" {
		label, tone = "高风险", uikit.Error
	}
	return uikit.ColorTag(c, label, uikit.TagOptions{Tone: tone, Compact: true, Round: true, Borderless: true})
}

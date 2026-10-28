// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/pkg/uikit"
	"encoding/json"
	"fmt"
	"net/url"
	"runtime"
	"strings"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

var settingsSections = []string{"通用", "高级", "网络", "安全围栏", "API", "版本亮点", "关于"}

// settingsPageBody is the inline settings surface: a left section rail and the
// form for the active section. Values are saved when the user leaves the page
// or presses the header button, mirroring the original "commit on leave".
func (a *desktop) settingsPageBody(c *ui.Context) {
	if a.settings.Tab == "" {
		a.settings.Tab = "通用"
	}
	a.settingsTab = a.settings.Tab
	ui.Row(c).Grow(1).MinHeight(0).Gap(gapSection).AlignItems(ui.Stretch).Justify(ui.Start).Children(func() {
		ui.Column(c).Width(140).Shrink(0).Gap(3).Justify(ui.Start).Children(func() {
			for _, section := range settingsSections {
				btn := flat(c.Key("settings-"+section), section).FillWidth().Justify(ui.Start)
				if a.settings.Tab == section {
					btn.TextColor(c.Theme().Accent).Background(c.Theme().SurfaceHover)
				}
				btn.OnClick(func() { a.selectSettingsSection(section) })
			}
		})
		ui.Column(c).Grow(1).MinWidth(0).FillHeight().Justify(ui.Start).Children(func() {
			ui.Row(c).Gap(gapField).AlignItems(ui.Center).Children(func() {
				ui.Spacer(c)
				if a.settings.UpdateNote != "" {
					fieldNote(c, a.settings.UpdateNote)
				}

			})
			ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
				ui.Column(c).Padding(2, 0, 18).Gap(gapSection).Children(func() {
					switch a.settings.Tab {
					case "通用":
						a.settingsGeneral(c)
					case "高级":
						a.settingsAdvanced(c)
					case "网络":
						a.settingsNetwork(c)
					case "安全围栏":
						a.settingsFence(c)
					case "API":
						a.settingsAPI(c)
					case "版本亮点":
						a.settingsReleaseNotes(c)
					case "关于":
						a.settingsAbout(c)
					}
				})
			})
		})
	})
}

func (a *desktop) selectSettingsSection(section string) {
	if a.settings.Tab != section {
		a.commitSettingsOnLeave()
	}
	a.settings.Tab = section
	a.settingsTab = section
	switch section {
	case "API":
		a.loadAPIService()
	case "关于":
		a.loadAutostart()
	case "网络":
		if a.configDraft.ProxyMode == "system" {
			a.detectProxy()
		}
	}
}

func (a *desktop) saveSettings() {
	cfg := a.configDraft
	cfg.LLMRetries = int(a.settings.Retries)
	cfg.ToolConcurrency = int(a.settings.Concurrency)
	allow := a.settings.AllowPrivate
	cfg.AllowPrivateNetwork = &allow
	autoUpdate := a.settings.AutoUpdate
	cfg.AutoUpdate = &autoUpdate
	cfg.CompactTimeoutSeconds = int(a.settings.CompactTimeout)
	cfg.CompactThreshold = a.settings.Threshold
	cfg.MessageFontSize = a.settings.FontMessage
	cfg.CodeFontSize = a.settings.FontCode
	cfg.ToolFontSize = a.settings.FontTool
	cfg.SubFontSize = a.settings.FontSub
	cfg.AuxFontSize = a.settings.FontAux
	cfg.BackgroundOpacity = a.settings.BgOpacity
	cfg.AutoValidationPython = boolPtr(a.settings.Validation["Python"])
	cfg.AutoValidationGo = boolPtr(a.settings.Validation["Go"])
	cfg.AutoValidationJavaScript = boolPtr(a.settings.Validation["JavaScript"])
	cfg.AutoValidationTypeScript = boolPtr(a.settings.Validation["TypeScript"])
	cfg.AutoValidationVue = boolPtr(a.settings.Validation["Vue"])
	cfg.AutoValidationJava = boolPtr(a.settings.Validation["Java"])
	cfg.AutoValidationJSON = boolPtr(a.settings.Validation["JSON"])
	cfg.AutoValidationYAML = boolPtr(a.settings.Validation["YAML"])
	cfg.AutoValidationXML = boolPtr(a.settings.Validation["XML"])
	a.configDraft = cfg
	a.saveConfig(cfg)
}

// commitSettingsOnLeave is called when the rail switches away from settings.
func (a *desktop) commitSettingsOnLeave() {
	if a.core == nil || a.win == nil {
		return
	}
	if a.settings.Tab == "" || a.settings.Tab == "关于" || a.settings.Tab == "安全围栏" || a.settings.Tab == "版本亮点" || a.settings.Tab == "完整配置" {
		return
	}
	a.saveSettings()
}

// ---- General -------------------------------------------------------------

func (a *desktop) settingsGeneral(c *ui.Context) {
	selected := a.settingsModule(c, "settingsGeneral", "外观", "提示词", "路径", "对话行为", "应用", "背景", "字体")

	fieldNote(c, "每次对话都会附带给模型的偏好")
	if selected == 0 {
		card(c, "settings-appearance", func() {
			sectionTitle(c, "外观")
			mode := 0
			if !a.currentPalette().dark {
				mode = 1
			}
			if segmented(c, "appearance-mode", &mode, "Dark", "Light").Changed() {
				a.prefs.Appearance = []string{"dark", "light"}[mode]
				a.syncNativeAppearance()
				a.savePrefs()
			}
			divider(c)
			fieldLabel(c, "强调色")
			ui.Row(c).Gap(8).Wrap().Children(func() {
				for _, idx := range []int{0, 2, 3, 4, 5} {
					p := palettes[idx]
					label := p.name
					if idx == 0 {
						label = "蓝色"
					} else if idx == 2 {
						label = "默认琥珀"
					}
					button := flat(c.Key(fmt.Sprintf("palette-%d", idx)), label).TextColor(p.accent)
					if a.prefs.Palette == idx || (idx == 0 && a.prefs.Palette == 1) {
						button.Border(2, p.accent).Background(p.accent.Alpha(0.12))
					}
					button.OnClick(func() {
						if a.prefs.Appearance == "" {
							if a.currentPalette().dark {
								a.prefs.Appearance = "dark"
							} else {
								a.prefs.Appearance = "light"
							}
						}
						a.prefs.Palette = idx
						a.syncNativeAppearance()
						a.savePrefs()
					})
				}
			})
			fieldNote(c, "强调色只改变高亮与选中态，背景保持中性。")
			divider(c)
			sectionTitle(c, "欢迎页形象")
			ui.Row(c).Gap(gapInline).Wrap().Children(func() {
				for _, style := range []struct{ id, label string }{{"eye", "大眼睛"}, {"whale", "蓝色大肥鱼"}, {"bot", "小机器人"}, {"gpt", "GPT 龙娘"}, {"claude", "Claude 橙花娘"}, {"deepseek", "DeepSeek 女仆"}, {"gemini", "Gemini 星猫娘"}, {"glm", "GLM 狐狸女仆"}, {"qwen", "千问千金"}} {
					style := style
					btn := flat(c.Key("avatar-"+style.id), style.label)
					if a.prefs.Avatar == style.id {
						btn.TextColor(c.Theme().Accent).Background(c.Theme().SurfaceHover)
					}
					btn.OnClick(func() { a.selectAvatarStyle(style.id) })
				}
			})
			ui.Row(c).Gap(gapField).Children(func() {
				flat(c, "上传自定义形象").OnClick(func() { a.chooseCustomAvatar() })
				if a.customAvatar != nil {
					flat(c, "使用自定义形象").OnClick(func() { a.selectAvatarStyle("custom") })
				}
			})
			fieldNote(c, "点击欢迎页头像可以唤醒樱花特效。")
		})
	}
	if selected == 1 {
		card(c, "settings-prompt", func() {
			sectionTitle(c, "自定义提示词")
			a.configDraft.CustomPrompt = strings.TrimSpace(a.configDraft.CustomPrompt)
			textArea(c, "custom-prompt", "自定义提示词", "例如：请用中文回答、保持回复简洁", &a.configDraft.CustomPrompt, 240)
			fieldNote(c, "会随每次对话一起发送给模型。")
			switchRow(c, "allow-private", "允许访问内网地址", "允许 http_request、web_fetch 与 MCP 访问内网地址。", &a.settings.AllowPrivate)
		})
	}
	if selected == 2 {
		card(c, "settings-paths", func() {
			sectionTitle(c, "路径")
			ui.Row(c).Gap(gapField).AlignItems(ui.End).Children(func() {
				ui.Column(c).Grow(1).Gap(gapTight).Children(func() {
					fieldLabel(c, "知识库根目录")
					uikit.Input(c.Key("kb-root"), &a.configDraft.KBRoot).Label("知识库根目录").FillWidth().Disabled(true)
				})
				flat(c, "选择目录").OnClick(func() { a.pickKBRoot() })
				if a.configDraft.KBRoot != "" {
					dangerButton(c, "清除").OnClick(func() { a.configDraft.KBRoot = "" })
				}
			})
			fieldNote(c, "其中 sources/ 子目录对模型只读。")
			if runtime.GOOS == "windows" {
				fieldRow(c, "git-bash", "Git Bash 路径", &a.configDraft.GitBashPath, false)
				fieldNote(c, "留空自动检测；也可手动指定，例如 C:\\Program Files\\Git\\bin\\bash.exe。")
			}
			fieldLabel(c, "自定义 User-Agent")
			uaPresets := []string{"AllyAgent（默认）", "Codex CLI", "Claude Code", "OpenCode", "Pi"}
			currentUA := a.configDraft.UserAgent
			if currentUA == "" {
				currentUA = uaPresets[0]
			}
			uikit.ComboboxField(c.Key("user-agent"), &currentUA, uaPresets, "User-Agent", "").FillWidth()
			if currentUA != uaPresets[0] {
				a.configDraft.UserAgent = strings.TrimSpace(currentUA)
			} else {
				a.configDraft.UserAgent = ""
			}
			fieldNote(c, "仅用于模型 API 请求；http_request / web_fetch 不受影响。")
		})
	}
	if selected == 3 {
		card(c, "settings-behavior", func() {
			sectionTitle(c, "对话行为")
			ui.Grid(c.Key("settings-behavior-grid")).ColumnTracks(ui.Fr(1), ui.Fr(1)).FillWidth().Gap(gapSection).Children(func() {
				ui.Column(c).Gap(gapTight).Children(func() {
					fieldLabel(c, "LLM 请求重试次数")
					numberField(c, "llm-retries", "LLM 请求重试次数", &a.settings.Retries, 0, 10, 1)
					fieldNote(c, "0 表示使用默认值 6。")
				})
				ui.Column(c).Gap(gapTight).Children(func() {
					fieldLabel(c, "上下文自动压缩阈值（比例）")
					ui.Slider(c.Key("compact-threshold"), &a.settings.Threshold, 0.2, 0.95).Label("压缩阈值")
					fieldNote(c, fmt.Sprintf("当前 %.0f%%，占模型上下文窗口的比例。", a.settings.Threshold*100))
				})
				ui.Column(c).Gap(gapTight).Children(func() {
					fieldLabel(c, "上下文自动压缩方式")
					uikit.Select(c.Key("compact-method"), &a.configDraft.AutoCompactMethod, []string{"recent", "summary"}).Label("压缩方式").FillWidth()
					fieldNote(c, "summary = 调模型总结；recent = 本地裁剪，不调模型。")
				})
				ui.Column(c).Gap(gapTight).Children(func() {
					fieldLabel(c, "压缩超时（秒）")
					numberField(c, "compact-timeout", "压缩超时", &a.settings.CompactTimeout, 30, 3600, 30)
					fieldNote(c, "等待摘要生成的最长时间。")
				})
				ui.Column(c).Gap(gapTight).Children(func() {
					fieldLabel(c, "工具执行并发度")
					numberField(c, "tool-concurrency", "工具执行并发度", &a.settings.Concurrency, 1, 16, 1)
					fieldNote(c, "同时执行的工具数，默认 4。")
				})
			})
		})
	}
	if selected == 4 {
		card(c, "settings-app", func() {
			sectionTitle(c, "应用")
			if runtime.GOOS == "windows" {
				switchRow(c, "auto-update", "自动更新", "有新版本时后台下载，完成提示重启。", &a.settings.AutoUpdate)
			}
			ui.Row(c).Gap(gapField).AlignItems(ui.Center).Children(func() {
				flat(c, "检查更新").OnClick(func() {
					a.settings.UpdateBusy = true
					a.settings.UpdateNote = "正在检查…"
					a.checkUpdates(true)
					a.settings.UpdateBusy = false
				})
				if a.upd.ReleaseVer != "" {
					flat(c, "查看更新日志").OnClick(func() { a.settings.Tab = "版本亮点" })
				}
				ui.Spacer(c)
				if a.settings.UpdateNote != "" {
					fieldNote(c, a.settings.UpdateNote)
				}
			})
			if sw := switchRow(c, "autostart", "开机自启", "登录系统时自动启动 Ally。", &a.settings.Autostart); sw.Changed() {
				a.setAutostart(a.settings.Autostart)
			}
		})
	}
	if selected == 5 {
		card(c, "settings-background", func() {
			sectionTitle(c, "对话背景图")
			ui.Row(c).Gap(gapField).AlignItems(ui.Center).Children(func() {
				label := "选择图片"
				if a.configDraft.BackgroundImage != "" {
					label = "更换图片"
				}
				flat(c, label).OnClick(func() { a.selectBackgroundImage() })
				if a.configDraft.BackgroundImage != "" {
					dangerButton(c, "清除背景图").OnClick(func() { a.clearBackgroundImage() })
				}
				ui.Spacer(c)
				state := "未设置背景图"
				if a.configDraft.BackgroundImage != "" {
					state = "已设置：" + a.configDraft.BackgroundImage
				}
				fieldNote(c, state)
			})
			fieldNote(c, "淡色铺在对话区背景，建议不超过 12 MB。")
			ui.Slider(c.Key("bg-opacity"), &a.settings.BgOpacity, 0, 1).Label("背景图透明度")
			fieldNote(c, fmt.Sprintf("当前 %.0f%%，越大越明显。", a.settings.BgOpacity*100))
		})
	}
	if selected == 6 {
		card(c, "settings-fonts", func() {
			sectionTitle(c, "字号")
			ui.Grid(c.Key("font-grid")).Columns(5).Gap(gapSection).Children(func() {
				for _, item := range []struct {
					label string
					value *float64
					step  float64
				}{
					{"消息正文", &a.settings.FontMessage, 0.5},
					{"代码", &a.settings.FontCode, 0.5},
					{"工具卡", &a.settings.FontTool, 0.5},
					{"次级正文", &a.settings.FontSub, 0.5},
					{"辅助文字", &a.settings.FontAux, 0.5},
				} {
					ui.Column(c).Gap(gapTight).Children(func() {
						fieldLabel(c, item.label)
						uikit.NumberInput(c, "font-"+item.label, item.label, item.value, 10, 24, item.step).FillWidth()
					})
				}
			})
			fieldNote(c, "范围 10–24px，保存后即时生效。")
		})
	}
}

// ---- Advanced ------------------------------------------------------------

func (a *desktop) settingsAdvanced(c *ui.Context) {
	selected := a.settingsModule(c, "settingsAdvanced", "代码校验", "页面显隐")

	if selected == 0 {
		card(c, "settings-validation", func() {
			sectionTitle(c, "自动代码校验")
			fieldNote(c, "写入文件后做轻量语法检查，默认全部关闭。")
			languages := []struct{ key, label, note string }{
				{"Python", "Python", "对改动的 .py 执行语法检查（py_compile）。"},
				{"Go", "Go", "对受影响包执行 go vet，不下载依赖。"},
				{"JavaScript", "JavaScript", "使用工作区依赖做语法检查。"},
				{"TypeScript", "TypeScript / TSX", "使用工作区依赖做类型检查。"},
				{"Vue", "Vue", "使用工作区依赖检查单文件组件。"},
				{"Java", "Java", "使用工作区构建工具做编译检查。"},
				{"JSON", "JSON", "解析 JSON 文件确认结构合法。"},
				{"YAML", "YAML", "解析 YAML 文件确认结构合法。"},
				{"XML", "XML", "解析 XML 文件确认结构合法。"},
			}
			for _, lang := range languages {
				v := a.settings.Validation[lang.key]
				if sw := switchRow(c, "validation-"+lang.key, lang.label, lang.note, &v); sw.Changed() {
					a.settings.Validation[lang.key] = v
					a.saveSettings()
				}
			}
		})
	}
	if selected == 1 {
		card(c, "settings-visibility", func() {
			sectionTitle(c, "页面显隐")
			fieldNote(c, "隐藏后左侧菜单不再显示该入口；Agent 与设置始终保留。")
			for _, mode := range baseModes {
				if mode.id == "chat" || mode.id == "plugins" {
					continue
				}
				mode := mode
				visible := !a.modeHidden(mode.id)
				if sw := switchRow(c, "visible-"+mode.id, mode.label, "", &visible); sw.Changed() {
					a.setModeVisible(mode.id, visible)
				}
			}
		})
	}
}

func (a *desktop) setModeVisible(id string, visible bool) {
	var hidden []string
	for _, name := range a.configDraft.HiddenModes {
		if !strings.EqualFold(name, id) {
			hidden = append(hidden, name)
		}
	}
	if !visible {
		hidden = append(hidden, id)
	}
	a.configDraft.HiddenModes = hidden
	a.saveSettings()
}

// ---- Network -------------------------------------------------------------

func (a *desktop) settingsNetwork(c *ui.Context) {
	card(c, "settings-proxy", func() {
		sectionTitle(c, "网络代理")
		fieldNote(c, "应用于模型、HTTP 工具、MCP、命令和后台服务。")
		modeIndex := 0
		switch a.configDraft.ProxyMode {
		case "system":
			modeIndex = 1
		case "manual":
			modeIndex = 2
		}
		if segmented(c, "proxy-mode", &modeIndex, "关闭代理", "跟随系统代理", "手动代理").Changed() {
			a.configDraft.ProxyMode = []string{"off", "system", "manual"}[modeIndex]
			if a.configDraft.ProxyMode == "system" {
				a.detectProxy()
			}
		}
		if a.configDraft.ProxyMode == "manual" {
			fieldRow(c, "proxy-url", "代理地址", &a.configDraft.ProxyURL, false)
			if a.configDraft.ProxyURL != "" && !validProxyURL(a.configDraft.ProxyURL) {
				inlineError(c, "proxy-error", "请输入有效的 HTTP、HTTPS 或 SOCKS5 代理地址；填写完整后自动生效。")
			}
			fieldNote(c, "例如 http://127.0.0.1:7890 或 socks5://127.0.0.1:7891。")
		}
		if a.configDraft.ProxyMode != "off" {
			fieldRow(c, "proxy-no-proxy", "不使用代理的地址", &a.configDraft.ProxyNoProxy, false)
			fieldNote(c, "例如 localhost,127.0.0.1,::1。")
		}
		notice(c, "warn", "代理可见连接元数据；代理不可用时不会回退直连。")
		ui.Row(c).Gap(gapField).Children(func() {
			flat(c, "检测系统代理").OnClick(func() { a.detectProxy() })
			flat(c, "测试连接").Disabled(a.configDraft.ProxyMode == "off").OnClick(func() { a.testProxy() })
			if a.settings.ProxyBusy {
				ui.Spinner(c).Size(13, 13)
			}
			ui.Spacer(c)
			if a.settings.ProxyNote != "" {
				fieldNote(c, a.settings.ProxyNote)
			}
		})
	})
	if a.settings.Proxy.Enabled || a.settings.Proxy.Error != "" {
		card(c, "settings-proxy-result", func() {
			sectionTitle(c, "代理检测结果")
			if a.settings.Proxy.Error != "" {
				notice(c, "error", a.settings.Proxy.Error)
			}
			kv(c, "来源", a.settings.Proxy.Source)
			kv(c, "HTTP", a.settings.Proxy.HTTPProxy)
			kv(c, "HTTPS", a.settings.Proxy.HTTPSProxy)
			kv(c, "NO_PROXY", a.settings.Proxy.NoProxy)
			if a.settings.Proxy.PACURL != "" {
				kv(c, "PAC", a.settings.Proxy.PACURL)
				notice(c, "warn", "检测到 PAC 自动配置，但当前版本暂不执行 PAC 脚本。")
			}
		})
	}
}

func validProxyURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "socks5", "socks5h":
		return u.Hostname() != ""
	}
	return false
}

func (a *desktop) detectProxy() {
	a.settings.ProxyBusy = true
	go func() {
		status := a.core.DetectSystemProxy()
		a.win.Update(func() {
			a.settings.ProxyBusy = false
			a.settings.Proxy = status
			if status.Error != "" {
				a.settings.ProxyNote = status.Error
			} else {
				a.settings.ProxyNote = "系统代理：" + status.Source
			}
		})
	}()
}

func (a *desktop) testProxy() {
	a.settings.ProxyBusy = true
	a.settings.ProxyNote = "正在测试代理…"
	req := backend.ProxyTestRequest{Mode: a.configDraft.ProxyMode, URL: a.configDraft.ProxyURL, NoProxy: a.configDraft.ProxyNoProxy}
	go func() {
		result := a.core.TestProxy(req)
		a.win.Update(func() {
			a.settings.ProxyBusy = false
			if result.OK {
				a.settings.ProxyNote = fmt.Sprintf("连接成功 · HTTP %d · %d ms · %s", result.StatusCode, result.DurationMS, result.Proxy)
			} else {
				a.settings.ProxyNote = "连接失败：" + result.Error
			}
		})
	}()
}

// ---- Security fence ------------------------------------------------------

func (a *desktop) settingsFence(c *ui.Context) {
	card(c, "settings-fence", func() {
		sectionTitle(c, "安全围栏机制")
		fieldNote(c, "命令与文件改动统一受以下规则约束，模型无法绕过。")
		rules := []string{
			"删除命令一律拦下，改用 delete 工具。",
			"写入 .git / .svn / .hg 一律拦下。",
			"写入工作区之外已存在的目标一律拦下。",
			"路径先展开 ~ 再解析符号链接，避免借链接越界。",
			"mkfs、shutdown、reboot、dd 等破坏性命令一律拦下。",
			"工作区 = 主工作区 + 会话附加工作区。",
			"知识库 sources/ 目录对模型只读。",
			"远端 SSH 写入需要三道检查并弹窗确认。",
			"各平台使用同一套规则，不因系统不同而放宽。",
			"写工具可写范围 = 工作区 + Ally 数据目录。",
			"密钥与凭据位置禁止读取。",
			"命令在超时或取消时终止整个进程组。",
			"后台服务同样受工作区与代理设置约束。",
			"插件 HTTP 只能访问清单声明的域名。",
			"更新包在应用前校验摘要与来源。",
		}
		for _, rule := range rules {
			ui.Row(c).Gap(gapField).AlignItems(ui.Start).Children(func() {
				iconGlyph(c, "shield", 12).TextColor(c.Theme().Accent)
				ui.Text(c, rule).FontSize(fsSmall).LineHeight(1.6)
			})
		}
	})
}

// ---- API service ---------------------------------------------------------

func (a *desktop) settingsAPI(c *ui.Context) {

	{
		card(c, "settings-api", func() {
			sectionTitle(c, "API 服务")
			fieldNote(c, "仅监听 127.0.0.1，供本机工具调用；请求需带 Bearer Token。")
			ui.Row(c).Gap(gapField).AlignItems(ui.Center).Wrap().Children(func() {
				enabled := a.settings.API.Enabled
				ui.Switch(c.Key("api-enabled"), &enabled).Label("启用 API 服务")
				if enabled != a.settings.API.Enabled {
					a.setAPIServiceEnabled(enabled)
				}
				fieldLabel(c, "端口")
				uikit.NumberInput(c, "api-port", "API 端口", &a.settings.APIPort, 1024, 65535, 1).Width(110)
				fieldLabel(c, "Token")
				uikit.Input(c.Key("api-token"), &a.settings.API.Token).Label("API Token").Width(240)
				flat(c, "复制").OnClick(func() {
					c.WriteClipboard(a.settings.API.Token)
					c.Toast("Token 已复制")
				})
				flat(c, "重新生成").OnClick(func() {
					a.settings.API.Token = randomToken()
					a.saveAPISettings()
					c.Toast("已生成新 Token")
				})
				flat(c, "保存").OnClick(func() { a.saveAPISettings() })
				services := c.Services()
				flat(c, "接口文档").OnClick(func() {
					services.OpenURL("https://github.com/Bronya0/ally-agent/blob/main/docs/api.md")
				})
				flat(c, "刷新").OnClick(func() { a.loadAPIService() })
			})
			fieldNote(c, "端口与 Token 改动后需点保存；Token 为空时后端会自动生成。")
			if a.settings.APINote != "" {
				notice(c, "info", a.settings.APINote)
			}
			base := a.settings.API.BaseURL
			if base == "" {
				base = fmt.Sprintf("http://127.0.0.1:%d", int(a.settings.APIPort))
			}
			fieldNote(c, fmt.Sprintf("地址 %s，请求头加 Authorization: Bearer <Token>。", base))
		})
	}
	{
		card(c, "settings-api-endpoints", func() {
			sectionTitle(c, "接口列表")
			for _, ep := range apiEndpoints {
				ui.Row(c).Gap(gapField).AlignItems(ui.Center).Children(func() {
					methodColor := c.Theme().Accent
					switch ep[0] {
					case "POST":
						methodColor = colSuccess()
					case "PUT":
						methodColor = colWarning()
					}
					chip(c, ep[0], methodColor, c.Theme().Surface.Mix(methodColor, 0.12))
					ui.Text(c, ep[1]).Font(monoFamily).FontSize(fsSmall)
					ui.Spacer(c)
					ui.Text(c, ep[2]).FontSize(fsTiny).TextColor(c.Theme().TextMuted)
				})
			}
		})
	}
}

var apiEndpoints = [][3]string{
	{"GET", "/api/v1/health", "健康检查"},
	{"POST", "/api/v1/ai/complete", "直接调用模型补全"},
	{"GET", "/api/v1/sessions", "会话列表"},
	{"POST", "/api/v1/sessions", "创建会话"},
	{"GET", "/api/v1/sessions/{id}/messages", "读取会话消息"},
	{"POST", "/api/v1/sessions/{id}/send", "发送消息"},
	{"POST", "/api/v1/sessions/{id}/cancel", "取消运行"},
	{"POST", "/api/v1/sessions/{id}/compact", "压缩上下文"},
	{"GET", "/api/v1/models", "模型列表"},
	{"POST", "/api/v1/models", "保存模型"},
	{"POST", "/api/v1/models/activate", "切换模型"},
	{"GET", "/api/v1/mcp", "MCP 状态"},
	{"PUT", "/api/v1/mcp/config", "保存 MCP 配置"},
	{"GET", "/api/v1/skills", "技能列表"},
	{"POST", "/api/v1/skills/{name}/activate", "启用技能"},
	{"GET", "/api/v1/tools", "工具清单"},
	{"GET", "/api/v1/subagents", "子代理运行记录"},
	{"GET", "/api/v1/workspace", "工作区信息"},
	{"GET", "/api/v1/services", "后台服务"},
	{"POST", "/api/v1/services/{id}/stop", "停止服务"},
	{"GET", "/api/v1/tasks", "定时任务"},
	{"DELETE", "/api/v1/tasks/{id}", "删除任务"},
}

func (a *desktop) loadAPIService() {
	go func() {
		state, err := a.core.GetApiServiceState()
		a.win.Update(func() {
			if err != nil {
				a.settings.APINote = err.Error()
				return
			}
			a.settings.API = state
			a.settings.APIPort = float64(state.Port)
			a.settings.APINote = ""
		})
	}()
}

func (a *desktop) saveAPISettings() {
	port := int(a.settings.APIPort)
	if port < 1024 || port > 65535 {
		a.settings.APIPort = float64(a.settings.API.Port)
		a.settings.APINote = "端口需为 1024-65535 之间的整数"
		return
	}
	req := backend.ApiSettingsRequest{Port: port, Token: a.settings.API.Token}
	go func() {
		state, err := a.core.SaveApiSettings(req)
		a.win.Update(func() {
			if err != nil {
				a.settings.APINote = err.Error()
				return
			}
			a.settings.API = state
			a.settings.APIPort = float64(state.Port)
			a.settings.APINote = "API 设置已保存"
		})
	}()
}

func (a *desktop) setAPIServiceEnabled(enabled bool) {
	if a.settings.APIPort < 1024 || a.settings.APIPort > 65535 || strings.TrimSpace(a.settings.API.Token) == "" {
		a.settings.APINote = "请先填写有效端口与 Token"
		return
	}
	go func() {
		state, err := a.core.SetApiServiceEnabled(enabled)
		a.win.Update(func() {
			if err != nil {
				a.settings.APINote = err.Error()
				return
			}
			a.settings.API = state
			a.settings.APIPort = float64(state.Port)
			if state.Enabled {
				a.settings.APINote = "API 服务已开启"
			} else {
				a.settings.APINote = "API 服务已关闭"
			}
		})
	}()
}

func randomToken() string {
	const alphabet = "0123456789abcdef"
	buf := make([]byte, 32)
	seed := uint64(nowUnixNano())
	for i := range buf {
		seed = seed*6364136223846793005 + 1442695040888963407
		buf[i] = alphabet[(seed>>33)%16]
	}
	return string(buf)
}

// ---- Release notes / about / raw ----------------------------------------

func (a *desktop) settingsReleaseNotes(c *ui.Context) {
	card(c, "settings-notes", func() {
		sectionTitle(c, "版本亮点")
		fieldNote(c, "版本 "+a.upd.ReleaseVer)
		if strings.TrimSpace(a.upd.ReleaseBody) == "" {
			fieldNote(c, "此构建未内置更新日志。")
			return
		}
		ui.Text(c, a.upd.ReleaseBody).Selectable().FontSize(fsBody).LineHeight(1.7)
	})
}

func (a *desktop) settingsAbout(c *ui.Context) {

	services := c.Services()
	{
		card(c, "settings-about", func() {
			ui.Text(c, "关于 Ally").FontSize(18).Bold()
			fieldNote(c, "自由、开源的桌面 AI 编程助手。")
			divider(c)
			ui.Text(c, "GPLv3 only").FontSize(fsBody).Bold()
			fieldNote(c, "你可以自由使用、研究、修改与分发本软件；衍生作品必须以同一许可开放源代码；本软件不提供任何担保。")
			ui.Row(c).Gap(gapField).Wrap().Children(func() {
				flat(c, "查看源代码与许可证").OnClick(func() { services.OpenURL("https://github.com/Bronya0/ally-agent") })
				flat(c, "提意见 / 反馈 Issue").OnClick(func() { services.OpenURL("https://github.com/Bronya0/ally-agent/issues") })
			})
		})
	}
	{
		card(c, "settings-credits", func() {
			sectionTitle(c, "欢迎页形象素材署名")
			fieldNote(c, "欢迎页立绘沿用原项目素材，许可为 CC BY-NC-SA 4.0：署名、非商业、相同方式共享。")
			flat(c, "查看素材来源").OnClick(func() {
				services.OpenURL("https://github.com/Bronya0/ally-agent/blob/main/resources/avatar/NOTICE.txt")
			})
		})
	}
	{
		card(c, "settings-update", func() {
			sectionTitle(c, "更新")
			ui.Row(c).Gap(gapField).AlignItems(ui.Center).Children(func() {
				flat(c, "检查更新").OnClick(func() {
					a.settings.UpdateNote = "正在检查…"
					a.checkUpdates(true)
				})
				if a.upd.Tag != "" {
					flat(c, "查看更新日志").OnClick(func() { a.settings.Tab = "版本亮点" })
				}
				ui.Spacer(c)
				fieldNote(c, a.settings.UpdateNote)
			})
		})
	}
}

func (a *desktop) settingsRaw(c *ui.Context) {
	card(c, "settings-raw", func() {
		sectionTitle(c, "完整配置 JSON")
		fieldNote(c, "高级用法：直接编辑后端完整配置（包含密钥），保存后立即生效。")
		textArea(c, "raw-config", "配置 JSON", "{}", &a.settings.FullJSON, 420)
		ui.Row(c).Gap(gapField).Children(func() {
			primary(c, "保存 JSON").OnClick(func() {
				var cfg backend.ConfigState
				if err := json.Unmarshal([]byte(a.settings.FullJSON), &cfg); err != nil {
					a.status = "配置解析失败：" + err.Error()
					return
				}
				a.saveConfig(cfg)
			})
			flat(c, "从后端重新载入").OnClick(func() {
				go func() {
					cfg, err := a.core.GetConfig()
					a.win.Update(func() {
						if err != nil {
							a.notify(err.Error())
							return
						}
						a.configDraft = cfg
						a.settings.FullJSON = pretty(cfg)
					})
				}()
			})
		})
	})
}

// loadSettings refreshes the settings page: config draft, sandbox state,
// API service state and the autostart flag.
func (a *desktop) loadSettings() {
	go func() {
		cfg, err := a.core.GetConfig()
		if err != nil {
			a.win.Update(func() { a.notify(err.Error()) })
			return
		}
		sandbox, _ := a.core.GetSandboxStatus()
		api, _ := a.core.GetApiServiceState()
		autostart, _ := a.core.GetAutostartEnabled()
		a.win.Update(func() {
			a.config = cfg
			a.configDraft = cfg
			a.sandboxState = sandbox
			a.apiState = api
			a.apiPort = float64(api.Port)
			a.autostartValue = autostart
			a.syncSettingsForm()
		})
	}()
}

func (a *desktop) settingsModule(c *ui.Context, key string, labels ...string) int {
	if a.settings.Modules == nil {
		a.settings.Modules = map[string]int{}
	}
	index := a.settings.Modules[key]
	if scrollTabs(c, &index, labels...) {
		a.settings.Modules[key] = index
	}
	return index
}

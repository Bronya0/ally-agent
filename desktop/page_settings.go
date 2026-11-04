// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/desktop/uikit"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"runtime"
	"strings"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

const historyPageSizeHelp = `按从最新往前的展示块计数，不按对话轮数或工具调用次数。
• 每条用户消息、助手回复各算 1 块。
• 连续的读取/搜索/目录工具合为 1 块；连续本地命令也合为 1 块。其他工具各算 1 块。
• 分组展开后仍算 1 块，组内先展示 100 项；大 diff 另有限制，可继续展开。
例如：提问 + 回复 + 连续 10 次读取 + 回复，共 4 块。
默认 80 块，适合一般任务；长任务想多看历史可设 100。数值越大，界面通常占用越多内存。
超出的历史可翻页查看，不删除消息、不改变模型上下文，也不是精确的内存限额。`

var settingsSections = []string{"通用", "高级", "网络", "安全围栏", "API", "版本亮点", "关于"}

const defaultUserAgentOption = "AllyAgent（默认）"

// autoCompactOptions keeps the stored values ("summary" / "recent") and changes
// only what the user reads: a raw English value in the settings form is neither
// self-explanatory nor honest about which one is the default.
var autoCompactOptions = []uikit.Option{
	{Value: "summary", Label: "LLM 全文总结（默认）"},
	{Value: "recent", Label: "本地裁剪（必要时回退总结）"},
}

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
				section := section
				selectionRow(c, "settings-"+section, section, a.settings.Tab == section, a.secondaryInk()).
					OnClick(func() { a.selectSettingsSection(section) })
			}
		})
		ui.Column(c).Grow(1).MinWidth(0).FillHeight().Justify(ui.Start).Children(func() {
			ui.Row(c).Gap(gapField).AlignItems(ui.Center).Children(func() {
				ui.Spacer(c)
				if a.settings.UpdateNote != "" {
					fieldNote(c, a.settings.UpdateNote, a.secondaryInk())
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
	if a.settings.Tab != section && a.settings.Tab != "" && a.settings.Tab != "关于" && a.settings.Tab != "安全围栏" && a.settings.Tab != "版本亮点" && a.settings.Tab != "完整配置" {
		a.saveSettings()
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

// settingsConfigDraft projects the page state onto the config that would be
// saved. Leaving the page compares it with the saved config instead of always
// writing, so an untouched settings page is silent.
func (a *desktop) settingsConfigDraft() backend.ConfigState {
	cfg := a.configDraft
	if cfg.UserAgent == defaultUserAgentOption {
		cfg.UserAgent = ""
	}
	cfg.LLMRetries = int(a.settings.Retries)
	cfg.ToolConcurrency = int(a.settings.Concurrency)
	allow := a.settings.AllowPrivate
	cfg.AllowPrivateNetwork = &allow
	autoUpdate := a.settings.AutoUpdate
	cfg.AutoUpdate = &autoUpdate
	cfg.SendSessionIDHeader = boolPtr(a.settings.SendSessionID)
	cfg.SendXSessionIDHeader = boolPtr(a.settings.SendXSessionID)
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
	return cfg
}

// settingsPendingEdits reports whether the page holds anything the backend does
// not have yet. Both sides are normalized through the form's own defaults first,
// so an untouched page with a sparse config file never looks edited. Fields that
// live in prefs rather than the config are compared here too: 每页展示块数 is only
// written by saveSettings, so ignoring it made a real edit disappear on leave.
func (a *desktop) settingsPendingEdits() bool {
	if normalizeHistoryPageSize(int(a.settings.HistoryPageSize)) != a.messageWindowSize() {
		return true
	}
	return !reflect.DeepEqual(normalizeSettingsConfig(a.settingsConfigDraft()), normalizeSettingsConfig(a.config))
}

func (a *desktop) saveSettings(notice ...string) {
	size := normalizeHistoryPageSize(int(a.settings.HistoryPageSize))
	if size != a.messageWindowSize() {
		a.prefs.HistoryPageSize = size
		a.savePrefs()
	}
	cfg := a.settingsConfigDraft()
	a.configDraft = cfg
	if len(notice) == 0 {
		notice = []string{""}
	}
	a.saveConfig(cfg, notice...)
}

// commitSettingsOnLeave is called when the rail switches away from settings.
// Edits inside the page save silently, and leaving only writes and reports once
// when the draft actually differs from the saved config: announcing a save that
// changed nothing is noise every time the page is closed.
func (a *desktop) commitSettingsOnLeave() {
	if a.core == nil || a.win == nil {
		return
	}
	if !a.settingsPendingEdits() {
		return
	}
	a.saveSettings("设置已保存")
}

// ---- General -------------------------------------------------------------

// Settings actions need a visible outline at rest so file pickers, update
// checks and other one-shot operations read as buttons rather than labels.
func settingsAction(c *ui.Context, label string, tone ...uikit.Tone) ui.Element {
	options := uikit.ActionOptions{Style: uikit.Ghost, Small: true}
	if len(tone) > 0 {
		options.Tone = tone[0]
	}
	return uikit.Action(c.Key("settings-action-"+label), label, options)
}

func (a *desktop) settingsGeneral(c *ui.Context) {
	selected := a.settingsModule(c, "settingsGeneral", "外观", "提示词", "路径", "对话行为", "应用", "语音输入")

	fieldNote(c, "每次对话都会附带给模型的偏好", a.secondaryInk())
	if selected == 0 {
		card(c, "settings-appearance", func() {
			sectionTitle(c, "外观")
			if segmentedInk(c, "appearance-mode", &a.settings.AppearanceMode, a.secondaryInk(), "深色", "浅色", "自动").Changed() {
				a.selectAppearance([]string{"dark", "light", "auto"}[a.settings.AppearanceMode])
			}
			if a.prefs.Appearance == "auto" {
				fieldNote(c, "按电脑本地时间自动切换：07:00–17:59 浅色，18:00–06:59 深色。", a.secondaryInk())
			}
			divider(c)
			fieldLabel(c, "强调色")
			uikit.OptionGroup(c).Children(func() {
				// The retired 亮色 and 海洋 presets are not offered: a stored value of
				// either still shows 蓝色 as selected and paints the blue accent.
				for _, idx := range []int{paletteBlue, paletteAmber, paletteForest, paletteViolet} {
					idx := idx
					p := palettes[idx]
					selected := a.prefs.Palette == idx || (idx == paletteBlue && (a.prefs.Palette == paletteLight || a.prefs.Palette == paletteOcean))
					choiceSwatch(c, fmt.Sprintf("palette-%d", idx), p.name, selected, p.accent).OnClick(func() {
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
			fieldNote(c, "强调色只改变高亮与选中态，背景保持中性。", a.secondaryInk())
			divider(c)
			sectionTitle(c, "欢迎页形象")
			uikit.OptionGroup(c).Children(func() {
				for _, style := range []struct{ id, label string }{{"eye", "大眼睛"}, {"whale", "大肥鱼"}, {"bot", "小机器人"}, {"gpt", "GPT 龙娘"}, {"claude", "Claude 橙花娘"}, {"deepseek", "DeepSeek 女仆"}, {"gemini", "Gemini 星猫娘"}, {"glm", "GLM 狐狸女仆"}, {"qwen", "千问千金"}} {
					style := style
					choiceButton(c, "avatar-"+style.id, style.label, a.prefs.Avatar == style.id, c.Theme().Accent).
						OnClick(func() { a.selectAvatarStyle(style.id) })
				}
			})
			ui.Row(c).Gap(gapField).Children(func() {
				settingsAction(c, "上传自定义形象").OnClick(func() { a.chooseCustomAvatar() })
				if a.customAvatar != nil {
					settingsAction(c, "使用自定义形象").OnClick(func() { a.selectAvatarStyle("custom") })
				}
			})
			fieldNote(c, "点击欢迎页头像可以切换樱花特效；桌宠未开启时也可以摸摸头。", a.secondaryInk())
			divider(c)
			a.petSettings(c)
		})
		card(c, "settings-background", func() {
			sectionTitle(c, "对话背景图")
			ui.Row(c).Gap(gapField).AlignItems(ui.Center).Children(func() {
				label := "选择图片"
				if a.configDraft.BackgroundImage != "" {
					label = "更换图片"
				}
				settingsAction(c, label).OnClick(func() { a.selectBackgroundImage() })
				if a.configDraft.BackgroundImage != "" {
					settingsAction(c, "清除背景图", uikit.Error).OnClick(func() { a.clearBackgroundImage() })
				}
				ui.Spacer(c)
				state := "未设置背景图"
				if a.configDraft.BackgroundImage != "" {
					state = "已设置：" + a.configDraft.BackgroundImage
				}
				fieldNote(c, state, a.secondaryInk())
			})
			fieldNote(c, "淡色铺在对话区背景，建议不超过 12 MB。", a.secondaryInk())
			ui.Slider(c.Key("bg-opacity"), &a.settings.BgOpacity, 0, 1).Label("背景图透明度")
			fieldNote(c, fmt.Sprintf("当前 %.0f%%，越大越明显。", a.settings.BgOpacity*100), a.secondaryInk())
		})
		card(c, "settings-fonts", func() {
			sectionTitle(c, "字号")
			ui.Grid(c.Key("font-grid")).Columns(5).Gap(gapSection).Children(func() {
				for _, item := range []struct {
					label string
					value *float64
					step  float64
				}{
					{"消息正文", &a.settings.FontMessage, 0.5},
					{"代码块", &a.settings.FontCode, 0.5},
					{"工具卡", &a.settings.FontTool, 0.5},
					{"次级正文", &a.settings.FontSub, 0.5},
					{"辅助文字", &a.settings.FontAux, 0.5},
				} {
					ui.Column(c).Gap(gapTight).Children(func() {
						fieldLabel(c, item.label)
						uikit.NumberInput(c, "font-"+item.label, item.label, item.value, 10, 24, item.step, a.secondaryInk()).FillWidth()
					})
				}
			})
			fieldNote(c, "范围 10–24px，保存后即时生效。", a.secondaryInk())
		})
	}
	if selected == 1 {
		card(c, "settings-prompt", func() {
			sectionTitle(c, "自定义提示词")
			textArea(c, "custom-prompt", "自定义提示词", "例如：请用中文回答、保持回复简洁", &a.configDraft.CustomPrompt, 240)
			fieldNote(c, "会随每次对话一起发送给模型。", a.secondaryInk())
		})
	}
	if selected == 2 {
		card(c, "settings-paths", func() {
			sectionTitle(c, "路径")
			if runtime.GOOS == "windows" {
				fieldRow(c, "git-bash", "Git Bash 路径", &a.configDraft.GitBashPath, false)
				fieldNote(c, "留空自动检测；也可手动指定，例如 C:\\Program Files\\Git\\bin\\bash.exe。", a.secondaryInk())
			}
			fieldLabel(c, "自定义 User-Agent")
			uaPresets := []string{defaultUserAgentOption, "Codex CLI", "Claude Code", "OpenCode", "Pi"}
			uikit.ComboboxField(c.Key("user-agent"), &a.configDraft.UserAgent, uaPresets, "User-Agent", uaPresets[0], a.secondaryInk()).FillWidth()
			fieldNote(c, "仅用于模型 API 请求；http_request / web_fetch 不受影响。", a.secondaryInk())
		})
	}
	if selected == 3 {
		card(c, "settings-behavior", func() {
			sectionTitle(c, "对话行为")
			ui.Column(c).Gap(gapTight).Children(func() {
				uikit.RightTooltip(c, ui.Text(c.Key("history-page-label"), "每页展示块数 ⓘ").FontSize(fsBody).AlignSelf(ui.Start), historyPageSizeHelp)
				uikit.RightTooltip(c, numberField(c, "history-page-size", "每页展示块数", &a.settings.HistoryPageSize, 20, 200, 10), historyPageSizeHelp)
				fieldNote(c, "默认 80，一般任务建议 80，长任务可设 100。范围 20–200；悬浮查看计算方式，保存后生效。", a.secondaryInk())
			})
			ui.Grid(c.Key("settings-behavior-grid")).ColumnTracks(ui.Fr(1), ui.Fr(1)).FillWidth().Gap(gapSection).Children(func() {
				ui.Column(c).Gap(gapTight).Children(func() {
					fieldLabel(c, "LLM 请求重试次数")
					numberField(c, "llm-retries", "LLM 请求重试次数", &a.settings.Retries, 0, 10, 1)
					fieldNote(c, "0 表示使用默认值 6。", a.secondaryInk())
				})
				ui.Column(c).Gap(gapTight).Children(func() {
					fieldLabel(c, "上下文自动压缩阈值")
					ui.Slider(c.Key("compact-threshold"), &a.settings.Threshold, 0.2, 0.95).Label("压缩阈值")
					fieldNote(c, fmt.Sprintf("当前 %.0f%%：上下文占模型窗口的比例超过此值时自动压缩，范围 20%%–95%%，默认 60%%。", a.settings.Threshold*100), a.secondaryInk())
				})
				ui.Column(c).Gap(gapTight).Children(func() {
					fieldLabel(c, "上下文自动压缩方式")
					// 未设置就是后端的默认值：全文总结。存进去的仍是 "summary"，
					// 界面上只显示中文标签，用户不改就不写回 draft。
					stored := a.configDraft.AutoCompactMethod
					method := stored
					if method == "" {
						method = "summary"
					}
					uikit.SelectField(c.Key("compact-method"), &method, autoCompactOptions, a.secondaryInk()).Label("压缩方式").FillWidth()
					if method != stored && method != "" {
						a.configDraft.AutoCompactMethod = method
					}
					fieldNote(c, "全文总结：调用模型生成摘要。本地裁剪：不调用模型，先清空旧工具调用的参数，此前的回复与工具结果只留首尾摘录，回复正文合计最多约 8k tokens；减不下来时自动回退到全文总结。修改对新任务生效。", a.secondaryInk())
				})
				ui.Column(c).Gap(gapTight).Children(func() {
					fieldLabel(c, "压缩超时时间")
					numberField(c, "compact-timeout", "压缩超时时间", &a.settings.CompactTimeout, 30, 3600, 30)
					fieldNote(c, "等待摘要生成的最长时间，30–3600 秒，默认 180。", a.secondaryInk())
				})
				ui.Column(c).Gap(gapTight).Children(func() {
					fieldLabel(c, "工具执行并发度")
					numberField(c, "tool-concurrency", "工具执行并发度", &a.settings.Concurrency, 1, 16, 1)
					fieldNote(c, "同时执行的工具数，默认 4。", a.secondaryInk())
				})
			})
			divider(c)
			sectionTitle(c, "会话请求头")
			if sw := switchRow(c, "send-session-id", "自动发送 session-id", "用于同一会话的后端路由与缓存复用，默认开启。", &a.settings.SendSessionID); sw.Changed() && !a.settings.SendSessionID {
				a.settings.SendSessionID = true
				a.settings.SessionHeaderToDisable = "session-id"
				a.settings.SessionHeaderConfirmOpen = true
			}
			if sw := switchRow(c, "send-x-session-id", "自动发送 x-session-id", "与 session-id 使用同一会话标识，可独立关闭，默认开启。", &a.settings.SendXSessionID); sw.Changed() && !a.settings.SendXSessionID {
				a.settings.SendXSessionID = true
				a.settings.SessionHeaderToDisable = "x-session-id"
				a.settings.SessionHeaderConfirmOpen = true
			}
			fieldNote(c, "保存后对后续任务生效；手动配置的同名请求头不受影响。", a.secondaryInk())
		})
	}
	if selected == 5 {
		a.settingsVoiceInput(c)
	}
	if selected == 4 {
		card(c, "settings-app", func() {
			sectionTitle(c, "应用")
			if runtime.GOOS == "windows" {
				switchRow(c, "auto-update", "自动更新", "有新版本时后台下载，完成提示重启。", &a.settings.AutoUpdate)
			}
			ui.Row(c).Gap(gapField).AlignItems(ui.Center).Children(func() {
				settingsAction(c, "检查更新").OnClick(func() {
					a.settings.UpdateNote = "正在检查…"
					a.checkUpdates(false)
				})
				if strings.TrimSpace(a.upd.ReleaseBody) != "" {
					settingsAction(c, "查看更新日志").OnClick(func() { a.settings.Tab = "版本亮点" })
				}
				ui.Spacer(c)
				if a.settings.UpdateNote != "" {
					fieldNote(c, a.settings.UpdateNote, a.secondaryInk())
				}
			})
			if sw := switchRow(c, "autostart", "开机自启", "登录系统时自动启动 Ally。", &a.settings.Autostart); sw.Changed() {
				a.setAutostart(a.settings.Autostart)
			}
		})
	}
	a.sessionHeaderConfirmDialog(c)
}

func (a *desktop) sessionHeaderConfirmDialog(c *ui.Context) {
	if !a.settings.SessionHeaderConfirmOpen {
		return
	}
	header := a.settings.SessionHeaderToDisable
	closeDialog := func() {
		a.settings.SessionHeaderConfirmOpen = false
		a.settings.SessionHeaderToDisable = ""
	}
	dialog := modalCard(c.Key("session-header-confirm"), &a.settings.SessionHeaderConfirmOpen, widthModalSmall, func() {
		modalTitle(c, "关闭 "+header+" 请求头？", "")
		ui.Text(c, "关闭后，OpenCode、GPT（ChatGPT）和 OpenRouter 等服务的会话路由可能受到影响，严重降低缓存命中率，并可能导致请求不稳定或可用性下降。实际影响取决于服务端实现，建议保持开启。").
			FontSize(fsSmall).TextColor(a.secondaryInk()).LineHeight(1.5).Selectable()
		modalActions(c, func() {
			flat(c, "取消").OnClick(closeDialog)
			primary(c, "继续关闭").OnClick(func() {
				switch header {
				case "session-id":
					a.settings.SendSessionID = false
				case "x-session-id":
					a.settings.SendXSessionID = false
				}
				closeDialog()
			})
		})
	})
	if dialog.Dismissed() || !a.settings.SessionHeaderConfirmOpen {
		a.settings.SessionHeaderToDisable = ""
	}
}

func (a *desktop) settingsVoiceInput(c *ui.Context) {
	// The recorder and the keyboard hook are Windows-only today. Offering a switch
	// that fails the moment it is turned on is worse than saying so once.
	if !voiceInputSupported() {
		card(c, "settings-voice-input", func() {
			sectionTitle(c, "语音输入")
			fieldNote(c, "仅支持 Windows 桌面版", a.secondaryInk())
		})
		return
	}
	card(c, "settings-voice-input", func() {
		sectionTitle(c, "语音输入")
		// Only ASR models can drive dictation. TTS models are stored and shown on the
		// models page, but they must not appear as a choice here. Build the options in
		// one pass: this runs on every frame while the settings page is open.
		options := make([]uikit.Option, 0, len(a.config.VoiceModels))
		for _, model := range a.config.VoiceModels {
			if backend.VoiceKindOf(model.Kind) != backend.VoiceKindASR {
				continue
			}
			options = append(options, uikit.Option{Value: model.ID, Label: voiceModelLabel(model)})
		}
		if len(options) == 0 {
			fieldNote(c, "请先在模型页添加 ASR 模型", a.secondaryInk())
			return
		}
		selected := a.prefs.VoiceModelID
		stored := selected
		if !voiceModelIDExistsInOptions(options, selected) {
			selected = options[0].Value
		}
		fieldLabel(c, "语音模型")
		uikit.SelectField(c.Key("voice-input-model"), &selected, options, a.secondaryInk()).Label("语音模型").FillWidth().OnChange(func() {
			if selected != stored {
				a.prefs.VoiceModelID = selected
				a.savePrefs()
				a.configureVoiceInput()
			}
		})
		fieldLabel(c, "输入设备")
		deviceID := a.prefs.VoiceInputDeviceID
		deviceOptions := []uikit.Option{{Value: "", Label: "系统默认设备"}}
		foundDevice := deviceID == ""
		for _, device := range a.voiceInputDevices {
			deviceOptions = append(deviceOptions, uikit.Option{Value: device.ID, Label: device.Name})
			foundDevice = foundDevice || device.ID == deviceID
		}
		if !foundDevice {
			deviceOptions = append(deviceOptions, uikit.Option{Value: deviceID, Label: "已选设备不可用（请选择其他设备）"})
		}
		ui.Row(c.Key("voice-input-device-row")).Gap(gapField).AlignItems(ui.Center).Children(func() {
			uikit.SelectField(c.Key("voice-input-device"), &deviceID, deviceOptions, a.secondaryInk()).Label("输入设备").FillWidth().Grow(1).OnChange(func() {
				if deviceID != a.prefs.VoiceInputDeviceID {
					a.selectVoiceInputDevice(deviceID)
				}
			})
			cardFlat(c, "刷新设备").OnClick(func() { a.refreshVoiceInputDevices() })
			if a.voiceInputDevicesBusy {
				ui.Spinner(c).Size(13, 13)
			}
		})
		if a.voiceInputDevicesError != "" {
			inlineError(c, "voice-input-device-error", a.voiceInputDevicesError)
		} else {
			fieldNote(c, "跟随系统默认，或固定一个麦克风", a.secondaryInk())
		}
		hotkeyParts, hotkeyPlaceholder := a.hotkeyPartsFor(a.prefs.VoiceHotkey), ""
		if a.voiceHotkeyCapturing {
			hotkeyParts = a.voiceHotkeyPreviewParts
			hotkeyPlaceholder = "请按下快捷键组合…"
		}
		// One row holds the bound keys, which modifier sides count and the change
		// action: three separate rows pushed the rest of the card off screen.
		ui.Row(c.Key("voice-hotkey-row")).Gap(gapField).AlignItems(ui.Center).Children(func() {
			fieldLabel(c, "按住录音快捷键")
			ui.Row(c.Key("voice-hotkey-keys-row")).Gap(gapInline).AlignItems(ui.Center).Children(func() {
				a.hotkeyKeycaps(c, hotkeyParts, hotkeyPlaceholder)
			})
			ui.Row(c.Key("voice-hotkey-row-spacer")).Grow(1)
			ui.Row(c.Key("voice-modifier-sides")).Gap(gapInline).AlignItems(ui.Center).Children(func() {
				fieldLabel(c, "修饰键")
				choiceButton(c, "voice-modifier-left", "左", a.prefs.VoiceModifierLeft, c.Theme().Accent).OnClick(func() {
					a.setVoiceModifierSide(!a.prefs.VoiceModifierLeft, a.prefs.VoiceModifierRight)
				})
				choiceButton(c, "voice-modifier-right", "右", a.prefs.VoiceModifierRight, c.Theme().Accent).OnClick(func() {
					a.setVoiceModifierSide(a.prefs.VoiceModifierLeft, !a.prefs.VoiceModifierRight)
				})
			})
			if a.voiceHotkeyCapturing {
				cardFlat(c, "取消更换").OnClick(func() { a.finishVoiceHotkeyCapture("") })
			} else {
				cardFlat(c, "更换").OnClick(func() { a.beginVoiceHotkeyCapture() })
			}
		})
		if a.voiceHotkeyCapturing {
			fieldNote(c, "正在录入：按下想要的组合，全部松开后生效；Esc 取消。", a.secondaryInk())
		} else {
			fieldNote(c, "直接按键，Esc 取消", a.secondaryInk())
			fieldNote(c, voiceModifierSidesNote(a.prefs.VoiceModifierLeft, a.prefs.VoiceModifierRight), a.secondaryInk())
		}
		if sw := switchRow(c, "voice-input-enabled", "语音输入", "按住快捷键说话，松开后转写并填入输入框。", &a.prefs.VoiceInputEnabled); sw.Changed() {
			if a.prefs.VoiceInputEnabled {
				if !voiceModelIDExists(a.config.VoiceModels, selected) {
					a.prefs.VoiceInputEnabled = false
					a.notifyError("请先选择有效的语音模型")
					return
				}
				a.prefs.VoiceModelID = selected
			}
			a.savePrefs()
			a.configureVoiceInput()
		}
	})
}

// ---- Advanced ------------------------------------------------------------

func (a *desktop) settingsAdvanced(c *ui.Context) {
	selected := a.settingsModule(c, "settingsAdvanced", "代码校验", "页面显隐")

	if selected == 0 {
		card(c, "settings-validation", func() {
			sectionTitle(c, "自动代码校验")
			fieldNote(c, "写入文件后做轻量语法检查，默认全部关闭。", a.secondaryInk())
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
			fieldNote(c, "隐藏后左侧菜单不再显示该入口；Agent 与设置始终保留。", a.secondaryInk())
			for _, mode := range baseModes {
				if mode.id == "chat" || mode.id == "plugins" {
					continue
				}
				mode := mode
				visible := !modeHiddenIn(a.configDraft.HiddenModes, mode.id)
				if sw := switchRow(c, "visible-"+mode.id, mode.label, "", &visible); sw.Changed() {
					a.setModeVisible(mode.id, visible)
				}
			}
		})
	}
}

func (a *desktop) setModeVisible(id string, visible bool) {
	// Keep an explicit empty slice when the last hidden page is restored. Nil
	// means "field omitted" to settingsConfig and would preserve the old list.
	hidden := make([]string, 0, len(a.configDraft.HiddenModes)+1)
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
		fieldNote(c, "应用于模型、HTTP 工具、MCP、命令和后台服务。", a.secondaryInk())
		if segmentedInk(c, "proxy-mode", &a.settings.ProxyModeIndex, a.secondaryInk(), "关闭代理", "跟随系统代理", "手动代理").Changed() {
			a.configDraft.ProxyMode = []string{"off", "system", "manual"}[a.settings.ProxyModeIndex]
			if a.configDraft.ProxyMode == "system" {
				a.detectProxy()
			}
		}
		if a.configDraft.ProxyMode == "manual" {
			fieldRow(c, "proxy-url", "代理地址", &a.configDraft.ProxyURL, false)
			if a.configDraft.ProxyURL != "" && !validProxyURL(a.configDraft.ProxyURL) {
				inlineError(c, "proxy-error", "请输入有效的 HTTP、HTTPS 或 SOCKS5 代理地址；填写完整后自动生效。")
			}
			fieldNote(c, "例如 http://127.0.0.1:7890 或 socks5://127.0.0.1:7891。", a.secondaryInk())
		}
		if a.configDraft.ProxyMode != "off" {
			fieldRow(c, "proxy-no-proxy", "不使用代理的地址", &a.configDraft.ProxyNoProxy, false)
			fieldNote(c, "例如 localhost,127.0.0.1,::1。", a.secondaryInk())
		}
		notice(c, "warn", "代理可见连接元数据；代理不可用时不会回退直连。", a.secondaryInk())
		ui.Row(c).Gap(gapField).Children(func() {
			settingsAction(c, "检测系统代理").OnClick(func() { a.detectProxy() })
			settingsAction(c, "测试连接").Disabled(a.configDraft.ProxyMode == "off").OnClick(func() { a.testProxy() })
			if a.settings.ProxyBusy {
				ui.Spinner(c).Size(13, 13)
			}
			ui.Spacer(c)
			if a.settings.ProxyNote != "" {
				fieldNote(c, a.settings.ProxyNote, a.secondaryInk())
			}
		})
	})
	if a.settings.Proxy.Enabled || a.settings.Proxy.Error != "" {
		card(c, "settings-proxy-result", func() {
			sectionTitle(c, "代理检测结果")
			if a.settings.Proxy.Error != "" {
				notice(c, "error", a.settings.Proxy.Error, a.secondaryInk())
			}
			kv(c, "来源", a.settings.Proxy.Source, a.secondaryInk())
			kv(c, "HTTP", a.settings.Proxy.HTTPProxy, a.secondaryInk())
			kv(c, "HTTPS", a.settings.Proxy.HTTPSProxy, a.secondaryInk())
			kv(c, "NO_PROXY", a.settings.Proxy.NoProxy, a.secondaryInk())
			if a.settings.Proxy.PACURL != "" {
				kv(c, "PAC", a.settings.Proxy.PACURL, a.secondaryInk())
				notice(c, "warn", "检测到 PAC 自动配置，但当前版本暂不执行 PAC 脚本。", a.secondaryInk())
			}
		})
	}
	card(c, "settings-private-network", func() {
		sectionTitle(c, "内网访问")
		switchRow(c, "allow-private", "允许访问内网地址", "允许 http_request、web_fetch 与 MCP 访问内网地址。", &a.settings.AllowPrivate)
		fieldNote(c, "关闭后，这些工具与 MCP 的 HTTP/SSE 连接将拒绝 localhost 和内网地址。", a.secondaryInk())
	})
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
				note := fmt.Sprintf("连接成功 · HTTP %d", result.StatusCode)
				if dur := fmtDur(result.DurationMS); dur != "" {
					note += " · " + dur
				}
				if result.Proxy != "" {
					note += " · " + result.Proxy
				}
				a.settings.ProxyNote = note
			} else {
				a.settings.ProxyNote = "连接失败：" + result.Error
			}
		})
	}()
}

// ---- Security fence ------------------------------------------------------

// fenceRuleDot is the bullet in front of every safety fence rule. A dot marks a
// list item; the shield it replaced was a status mark, and a row of identical
// shields read like a column of warnings.
const fenceRuleDot float32 = 5

// fenceRuleLine is the height of a rule's first text line. The bullet's gutter
// matches it and centres the dot inside, which is what keeps the mark on the
// text's line: aligning the row to the top left the 12px shield riding above
// the 13px text, and a rule that wraps keeps its dot on the first line.
func fenceRuleLine() float32 { return fsSmall * 1.6 }

func fenceRule(c *ui.Context, text string) {
	ui.Row(c).Gap(gapField).AlignItems(ui.Start).Children(func() {
		ui.Box(c).Width(fenceRuleDot).Height(fenceRuleLine()).Center().Shrink(0).Children(func() {
			ui.Box(c).Size(fenceRuleDot, fenceRuleDot).Radius(fenceRuleDot / 2).Background(c.Theme().Accent)
		})
		ui.Text(c, text).FontSize(fsSmall).LineHeight(1.6)
	})
}

func (a *desktop) settingsFence(c *ui.Context) {
	card(c, "settings-fence", func() {
		sectionTitle(c, "安全围栏机制")
		fieldNote(c, "命令与文件改动统一受以下规则约束，模型无法绕过。", a.secondaryInk())
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
			fenceRule(c, rule)
		}
	})
}

// ---- API service ---------------------------------------------------------

func (a *desktop) settingsAPI(c *ui.Context) {

	{
		card(c, "settings-api", func() {
			sectionTitle(c, "API 服务")
			fieldNote(c, "仅监听 127.0.0.1，供本机工具调用；请求需带 Bearer Token。", a.secondaryInk())
			ui.Row(c).Gap(gapField).AlignItems(ui.Center).Wrap().Children(func() {
				enabled := a.settings.API.Enabled
				uikit.SwitchNative(c.Key("api-enabled"), &enabled).Label("启用 API 服务").Disabled(a.settings.APIBusy).OnClick(func() { a.setAPIServiceEnabled(enabled) })
				fieldLabel(c, "端口")
				uikit.NumberInput(c, "api-port", "API 端口", &a.settings.APIPort, 1024, 65535, 1, a.secondaryInk()).Width(200).Disabled(a.settings.APIBusy)
				fieldLabel(c, "Token")
				uikit.Input(c.Key("api-token"), &a.settings.API.Token).Label("API Token").Width(240).Disabled(a.settings.APIBusy)
				settingsAction(c, "复制").OnClick(func() {
					c.WriteClipboard(a.settings.API.Token)
					c.Toast("Token 已复制")
				})
				settingsAction(c, "重新生成").Disabled(a.settings.APIBusy).OnClick(func() { a.regenerateAPIToken() })
				settingsAction(c, "保存").Disabled(a.settings.APIBusy).OnClick(func() { a.saveAPISettings() })
				services := c.Services()
				settingsAction(c, "接口文档").OnClick(func() {
					services.OpenURL("https://github.com/Bronya0/ally-agent/blob/main/docs/api.md")
				})
				settingsAction(c, "刷新").Disabled(a.settings.APIBusy).OnClick(func() { a.loadAPIService(true) })
			})
			fieldNote(c, "端口与 Token 改动后需点保存；Token 为空时后端会自动生成。", a.secondaryInk())
			if a.settings.APINote != "" {
				notice(c, "info", a.settings.APINote, a.secondaryInk())
			}
			base := a.settings.API.BaseURL
			if base == "" {
				base = fmt.Sprintf("http://127.0.0.1:%d", int(a.settings.APIPort))
			}
			fieldNote(c, fmt.Sprintf("地址 %s，请求头加 Authorization: Bearer <Token>。", base), a.secondaryInk())
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
						methodColor = c.Theme().Success
					case "PUT":
						methodColor = c.Theme().Warning
					}
					chip(c, ep[0], methodColor, c.Theme().Surface.Mix(methodColor, 0.12))
					ui.Text(c, ep[1]).Font(monoFamily).FontSize(fsSmall)
					ui.Spacer(c)
					ui.Text(c, ep[2]).FontSize(fsTiny).TextColor(a.secondaryInk())
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

func (a *desktop) loadAPIService(manual ...bool) {
	if a.settings.APIBusy {
		return
	}
	a.settings.APIRevision++
	revision := a.settings.APIRevision
	go func() {
		state, err := a.core.GetApiServiceState()
		a.win.Update(func() {
			if revision != a.settings.APIRevision {
				return
			}
			if err != nil {
				a.settings.APINote = err.Error()
				if len(manual) > 0 && manual[0] {
					a.notifyError(err.Error())
				}
				return
			}
			a.settings.API = state
			a.settings.APIPort = float64(state.Port)
			a.settings.APINote = ""
			if len(manual) > 0 && manual[0] {
				a.notifySuccess("API 状态已刷新")
			}
		})
	}()
}

func (a *desktop) saveAPISettings() {
	a.persistAPISettings(a.settings.API.Token, false)
}

func (a *desktop) regenerateAPIToken() {
	// An empty token asks the backend to generate one with crypto/rand. Keep the
	// current value visible until the save succeeds, so a disk/listener error
	// cannot leave the UI claiming that a weak or unsaved token is active.
	a.persistAPISettings("", true)
}

func (a *desktop) persistAPISettings(token string, regenerated bool) {
	if a.settings.APIBusy {
		return
	}
	port := int(a.settings.APIPort)
	if port < 1024 || port > 65535 {
		a.settings.APIPort = float64(a.settings.API.Port)
		a.settings.APINote = "端口需为 1024-65535 之间的整数"
		return
	}
	a.settings.APIBusy = true
	a.settings.APIRevision++
	revision := a.settings.APIRevision
	req := backend.ApiSettingsRequest{Port: port, Token: token}
	go func() {
		state, err := a.core.SaveApiSettings(req)
		a.win.Update(func() {
			if revision != a.settings.APIRevision {
				return
			}
			a.settings.APIBusy = false
			if err != nil {
				a.settings.APINote = err.Error()
				a.notifyError(err.Error())
				return
			}
			a.settings.API = state
			a.settings.APIPort = float64(state.Port)
			if regenerated {
				a.settings.APINote = "API Token 已重新生成并保存"
				a.notifySuccess("已生成新 Token")
			} else {
				a.settings.APINote = "API 设置已保存"
			}
		})
	}()
}

func (a *desktop) setAPIServiceEnabled(enabled bool) {
	if a.settings.APIBusy {
		return
	}
	if a.settings.APIPort < 1024 || a.settings.APIPort > 65535 || strings.TrimSpace(a.settings.API.Token) == "" {
		a.settings.APINote = "请先填写有效端口与 Token"
		return
	}
	previousEnabled := a.settings.API.Enabled
	a.settings.APIBusy = true
	a.settings.API.Enabled = enabled
	a.settings.APIRevision++
	revision := a.settings.APIRevision
	go func() {
		state, err := a.core.SetApiServiceEnabled(enabled)
		a.win.Update(func() {
			if revision != a.settings.APIRevision {
				return
			}
			a.settings.APIBusy = false
			if err != nil {
				a.settings.API.Enabled = previousEnabled
				a.settings.APINote = err.Error()
				a.notifyError(err.Error())
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

// ---- Release notes / about / raw ----------------------------------------

func (a *desktop) settingsReleaseNotes(c *ui.Context) {
	card(c, "settings-notes", func() {
		sectionTitle(c, "版本亮点")
		if a.upd.ReleaseVer != "" {
			fieldNote(c, "版本 "+a.upd.ReleaseVer, a.secondaryInk())
		}
		if strings.TrimSpace(a.upd.ReleaseBody) == "" {
			fieldNote(c, "此构建未内置更新日志。", a.secondaryInk())
			return
		}
		a.releaseNotesMarkdown(c, &a.upd.ReleaseSettingsMD, "release-notes-settings-markdown")
	})
}

func (a *desktop) settingsAbout(c *ui.Context) {

	card(c, "settings-memory-diagnostics", func() {
		sectionTitle(c, "内存诊断")
		fieldNote(c, "导出 Go 堆与分配调用栈、协程和回收前后内存数据；会短暂触发垃圾回收。也可在聊天页按 Ctrl+Shift+F12。建议在空闲时、内存增长后各导出一次。", a.secondaryInk())
		settingsAction(c, "导出内存诊断").Disabled(a.memoryDiagnosticBusy).OnClick(a.exportMemoryDiagnostics)
	})

	services := c.Services()
	{
		card(c, "settings-about", func() {
			ui.Text(c, "关于 Ally").FontSize(fsHeading).Bold()
			fieldNote(c, "自由、开源的桌面 AI 编程助手。", a.secondaryInk())
			divider(c)
			ui.Text(c, "GPLv3 only").FontSize(fsBody).Bold()
			fieldNote(c, "你可以自由使用、研究、修改与分发本软件；衍生作品必须以同一许可开放源代码；本软件不提供任何担保。", a.secondaryInk())
			ui.Row(c).Gap(gapField).Wrap().Children(func() {
				settingsAction(c, "查看源代码与许可证").OnClick(func() { services.OpenURL("https://github.com/Bronya0/ally-agent") })
				settingsAction(c, "提意见 / 反馈 Issue").OnClick(func() { services.OpenURL("https://github.com/Bronya0/ally-agent/issues") })
			})
		})
	}
	{
		card(c, "settings-credits", func() {
			sectionTitle(c, "欢迎页形象素材署名")
			fieldNote(c, "欢迎页立绘沿用原项目素材，许可为 CC BY-NC-SA 4.0：署名、非商业、相同方式共享。", a.secondaryInk())
			settingsAction(c, "查看素材来源").OnClick(func() {
				services.OpenURL("https://github.com/Bronya0/ally-agent/blob/main/resources/avatar/NOTICE.txt")
			})
		})
	}
	{
		card(c, "settings-update", func() {
			sectionTitle(c, "更新")
			ui.Row(c).Gap(gapField).AlignItems(ui.Center).Children(func() {
				settingsAction(c, "检查更新").OnClick(func() {
					a.settings.UpdateNote = "正在检查…"
					a.checkUpdates(false)
				})
				if strings.TrimSpace(a.upd.ReleaseBody) != "" {
					settingsAction(c, "查看更新日志").OnClick(func() { a.settings.Tab = "版本亮点" })
				}
				ui.Spacer(c)
				fieldNote(c, a.settings.UpdateNote, a.secondaryInk())
			})
		})
	}
}

func (a *desktop) settingsRaw(c *ui.Context) {
	card(c, "settings-raw", func() {
		sectionTitle(c, "完整配置 JSON")
		fieldNote(c, "高级用法：直接编辑后端完整配置（包含密钥），保存后立即生效。", a.secondaryInk())
		textArea(c, "raw-config", "配置 JSON", "{}", &a.settings.FullJSON, 420)
		ui.Row(c).Gap(gapField).Children(func() {
			settingsAction(c, "保存 JSON", uikit.Primary).OnClick(func() {
				var cfg backend.ConfigState
				if err := json.Unmarshal([]byte(a.settings.FullJSON), &cfg); err != nil {
					a.notifyError("配置解析失败：" + err.Error())
					return
				}
				a.saveConfig(cfg)
			})
			settingsAction(c, "从后端重新载入").OnClick(func() {
				go func() {
					cfg, err := a.core.GetConfig()
					a.win.Update(func() {
						if err != nil {
							a.notifyError(err.Error())
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
			a.win.Update(func() { a.notifyError(err.Error()) })
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
	if a.scrollTabs(c, "settings-"+key, &index, labels...) {
		a.settings.Modules[key] = index
	}
	return index
}

// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/desktop/uikit"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

const customProviderPreset = "自定义配置"

func applyCatalogModel(a *desktop, model modelCatalogModel) {
	a.modelDraft.Model = model.ID
	if model.Name != "" && !strings.EqualFold(model.Name, model.ID) {
		a.modelDraft.DisplayName = model.Name
	} else {
		a.modelDraft.DisplayName = ""
	}
	if model.MaxTokens > 0 {
		a.modelDraft.MaxTokens = model.MaxTokens
		a.modelMaxTokens = float64(model.MaxTokens)
	}
	if model.ContextWindow > 0 {
		a.modelDraft.ContextWindow = model.ContextWindow
		a.modelContextWindow = float64(model.ContextWindow)
	}
	if model.ReasoningTag != "" {
		a.modelDraft.ReasoningTag = model.ReasoningTag
	} else if a.modelDraft.ReasoningTag == "" {
		a.modelDraft.ReasoningTag = "reasoning_content"
	}
	a.modelDraft.VisionCapable = model.VisionCapable
}

// modelsPageBody is the model management surface: search/import/export/add,
// two tabs, and provider-grouped card lists.
func (a *desktop) modelsPageBody(c *ui.Context) {
	if a.modelTab == "" {
		a.modelTab = "对话模型"
	}
	wantTab := 0
	if a.modelTab == "图片模型" {
		wantTab = 1
	}
	if a.modelTabIdx != wantTab {
		a.modelTabIdx = wantTab
	}
	ui.Row(c).Gap(gapField).AlignItems(ui.Center).Wrap().Children(func() {
		uikit.Input(c.Key("model-search"), &a.modelSearch).Placeholder("搜索").Label("搜索模型").Width(200)
		ui.Spacer(c)
		flat(c, "导入").OnClick(func() { a.importModels() })
		flat(c, "导出").Disabled(len(a.config.Models) == 0 && (a.config.ImageGeneration == nil || len(a.config.ImageGeneration.Models) == 0)).
			OnClick(func() { a.exportModels() })
		primary(c, "添加").OnClick(func() {
			if a.modelTab == "图片模型" {
				a.ensureImageModels()
				a.imageModels.openForm(-1)
				return
			}
			a.editModel(-1)
			a.modelForm.Open = true
		})
	})
	tabIndex := &a.modelTabIdx
	if scrollTabs(c, tabIndex, "对话模型", "图片模型") {
		if *tabIndex == 1 {
			a.cancelOAuthWhenLeaving()
			a.ensureImageModels()
			a.modelTab = "图片模型"
		} else {
			a.modelTab = "对话模型"
			a.openChatModelEditorIfNeeded()
		}
	}
	if a.modelTab == "对话模型" {
		a.chatModelProviderTabs(c)
	}
	ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
		ui.Column(c).Gap(gapInline).Children(func() {
			if a.modelTab == "图片模型" {
				a.imageModelsSection(c)
			} else {
				a.chatModelsSection(c)
			}
		})
	})
	if a.modelTab == "图片模型" {
		a.imageModelDialog(c)
		a.imageModelTestDialog(c)
	} else {
		a.chatModelDialog(c)
	}
}

func (a *desktop) openChatModelEditorIfNeeded() {
	if a.modelForm.Open && a.modelDraft.AuthType == "chatgpt_oauth" && a.oauth != nil {
		a.loadOAuthAccounts()
	}
}

func (a *desktop) chatModelProviderTabs(c *ui.Context) {
	groups := map[string]bool{}
	var order []string
	for _, model := range a.filteredChatModels() {
		name := model.ProviderName
		if name == "" {
			name = customProviderName
		}
		if !groups[name] {
			groups[name] = true
			order = append(order, name)
		}
	}
	if len(order) == 0 {
		return
	}
	providerIndex := 0
	for i, name := range order {
		if name == a.modelProvider {
			providerIndex = i
		}
	}
	if providerIndex >= len(order) {
		providerIndex = 0
	}
	a.modelProvider = order[providerIndex]
	if len(order) > 1 && scrollSubTabs(c, &providerIndex, order...) {
		a.modelProvider = order[providerIndex]
	}
}

func (a *desktop) chatModelsSection(c *ui.Context) {
	models := a.filteredChatModels()
	groups := map[string][]backend.ModelConfig{}
	for _, m := range models {
		name := m.ProviderName
		if name == "" {
			name = customProviderName
		}
		groups[name] = append(groups[name], m)
	}
	if len(models) == 0 {
		emptyState(c, "没有匹配的模型", "点击“添加”创建第一个模型。")
		return
	}

	for _, m := range groups[a.modelProvider] {
		m := m
		rowCard(c, "chat-model-"+modelOption(m), func() {
			ui.Row(c).Gap(gapInline).AlignItems(ui.Center).Wrap().Children(func() {
				title := m.DisplayName
				if title == "" {
					title = m.Model
				}
				if title == "" {
					title = "未选择模型"
				}
				ui.Text(c, title).FontSize(fsBody).Bold()
				neutralChip(c, apiFormatLabel(m.APIFormat))
				if m.DisplayName != "" && m.DisplayName != m.Model {
					neutralChip(c, m.Model)
				}
				ui.Spacer(c)
				cardFlat(c, "编辑").Disabled(a.modelSaving).OnClick(func() {
					for i, item := range a.config.Models {
						if sameModelIdentity(modelIdentity(item), modelIdentity(m)) {
							a.editModel(i)
							break
						}
					}
					a.modelForm.Open = true
				})
				cardDanger(c, "删除").Disabled(a.modelSaving).OnClick(func() { a.deleteModel(m) })
			})
			ui.Row(c).Gap(gapSection).Children(func() {
				fieldNote(c, fmt.Sprintf("max %s · context %s", dashZero(m.MaxTokens), dashZero(m.ContextWindow)))
			})
			base := m.BaseURL
			if base == "" {
				base = "未配置 Base URL"
			}
			fieldNote(c, base)
		})
	}
}

func dashZero(v int) string {
	if v <= 0 {
		return "-"
	}
	return fmt.Sprint(v)
}

func apiFormatLabel(format string) string {
	switch format {
	case "openai_responses":
		return "OpenAI Responses"
	case "anthropic":
		return "Anthropic Messages"
	}
	return "OpenAI Chat Completions"
}

func (a *desktop) filteredChatModels() []backend.ModelConfig {
	query := strings.ToLower(strings.TrimSpace(a.modelSearch))
	var out []backend.ModelConfig
	for _, m := range a.config.Models {
		if query != "" {
			haystack := strings.ToLower(m.ProviderName + " " + m.DisplayName + " " + m.Model + " " + m.BaseURL)
			if !strings.Contains(haystack, query) {
				continue
			}
		}
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Model < out[j].Model })
	return out
}

// ---- Chat model editor ---------------------------------------------------

func (a *desktop) chatModelDialog(c *ui.Context) {
	open := a.modelForm.Open
	if !open {
		return
	}
	if a.modelDraft.AuthType == "" {
		a.modelDraft.AuthType = "api_key"
	}
	title := "编辑模型"
	if a.modelEdit < 0 {
		title = "添加模型"
	}
	formDialog(c, &a.modelForm.Open, widthModal, 740, title, "", func() {
		fieldLabel(c, "认证方式")
		auth := "API Key 认证"
		if a.modelDraft.AuthType == "chatgpt_oauth" {
			auth = "ChatGPT 登录"
		}
		if uikit.Select(c.Key("model-auth"), &auth, []string{"API Key 认证", "ChatGPT 登录"}).Label("认证方式").FillWidth().Changed() {
			if auth == "ChatGPT 登录" {
				a.changeModelAuth("chatgpt_oauth")
			} else {
				a.changeModelAuth("api_key")
			}
		}
		if a.modelDraft.AuthType == "chatgpt_oauth" {
			a.oauthPanel(c)
			options := []string{}
			if a.oauth != nil {
				for _, m := range a.oauth.models {
					options = append(options, m.Slug)
				}
			}
			if a.modelDraft.Model != "" && !containsString(options, a.modelDraft.Model) {
				options = append(options, a.modelDraft.Model)
			}
			ui.Grid(c.Key("oauth-model-fields")).ColumnTracks(ui.Fr(1), ui.Fr(1)).FillWidth().Gap(gapSection).Children(func() {
				ui.Column(c).Gap(gapTight).Children(func() {
					fieldLabel(c, "供应商名称")
					uikit.Input(c.Key("oauth-provider"), &a.modelDraft.ProviderName).Label("供应商名称").FillWidth().Disabled(true)
				})
				ui.Column(c).Gap(gapTight).Children(func() {
					fieldLabel(c, "ChatGPT 模型")
					if uikit.Select(c.Key("oauth-model"), &a.modelDraft.Model, options).Label("ChatGPT 模型").FillWidth().Disabled(a.modelSaving).Changed() {
						if a.oauth != nil {
							for _, m := range a.oauth.models {
								if m.Slug == a.modelDraft.Model {
									a.modelDraft.DisplayName = m.DisplayName
									break
								}
							}
						}
					}
				})
				ui.Column(c).Gap(gapTight).Children(func() {
					fieldLabel(c, "思考强度")
					uikit.Select(c.Key("oauth-reasoning"), &a.modelDraft.ReasoningEffort, []string{"", "auto", "off", "low", "medium", "high", "xhigh", "max"}).Label("思考强度").FillWidth()
				})
				ui.Column(c).Gap(gapTight).Children(func() {
					fieldLabel(c, "上下文窗口")
					numberField(c, "oauth-context-window", "上下文窗口", &a.modelContextWindow, 0, 10000000, 1).FillWidth()
				})
			})
		} else {
			fieldLabel(c, "供应商快速配置")
			names := catalogProviderOptions()
			chosen := a.modelProviderPreset
			if chosen == "" {
				chosen = customProviderPreset
			}
			uikit.ComboboxField(c.Key("model-provider-preset"), &chosen, names, "供应商快速配置", "").FillWidth()
			if chosen != a.modelProviderPreset {
				a.modelProviderPreset = chosen
				if provider := findCatalogProviderByName(chosen); provider != nil {
					a.modelDraft.ProviderName = provider.Name
					a.modelDraft.BaseURL = provider.BaseURL
					a.modelDraft.APIFormat = provider.APIFormat
					model := findCatalogModel(provider, a.modelDraft.Model)
					if model == nil && len(provider.Models) > 0 {
						model = &provider.Models[0]
					}
					if model != nil {
						a.modelProviderModel = catalogModelLabel(*model)
						applyCatalogModel(a, *model)
					}
				} else {
					a.modelProviderModel = ""
				}
			}
			if provider := findCatalogProviderByName(a.modelProviderPreset); provider != nil {
				modelOptions := make([]string, 0, len(provider.Models))
				for _, model := range provider.Models {
					modelOptions = append(modelOptions, catalogModelLabel(model))
				}
				chosenModel := a.modelProviderModel
				if chosenModel == "" && len(modelOptions) > 0 {
					chosenModel = modelOptions[0]
				}
				ui.Row(c).Gap(gapField).AlignItems(ui.Center).Children(func() {
					uikit.ComboboxField(c.Key("model-provider-model"), &chosenModel, modelOptions, "供应商模型", "").Grow(1)
					if provider.Doc != "" {
						services := c.Services()
						flat(c, "查看供应商文档").OnClick(func() { services.OpenURL(provider.Doc) })
					}
				})
				if chosenModel != a.modelProviderModel {
					a.modelProviderModel = chosenModel
					if model := findCatalogModel(provider, chosenModel); model != nil {
						applyCatalogModel(a, *model)
					}
				}
				fieldNote(c, fmt.Sprintf("已从内置目录填入 %s 的接口地址、格式和模型参数；API Key 仍需自行填写。", provider.Name))
			}
			ui.Grid(c.Key("model-main-fields")).ColumnTracks(ui.Fr(1), ui.Fr(1)).FillWidth().Gap(gapSection).Children(func() {
				fieldRow(c, "provider-name", "供应商名称", &a.modelDraft.ProviderName, false)
				ui.Column(c).Gap(gapTight).Children(func() {
					fieldLabel(c, "接口格式")
					uikit.Select(c.Key("api-format"), &a.modelDraft.APIFormat, []string{"openai", "openai_responses", "anthropic"}).Label("接口格式").FillWidth()
				})
				ui.Column(c).Gap(gapTight).Children(func() {
					fieldLabel(c, "Model")
					ui.Row(c).Gap(gapField).Children(func() {
						// Mirrors the Vue editor's Model n-select (filterable + tag): one
						// searchable field that lists the models fetched from the provider
						// and still accepts a hand-typed id.
						uikit.ComboboxField(c.Key("model-id"), &a.modelDraft.Model, a.modelDraftOptions(), "Model ID", modelIDPlaceholder(a.modelDraft.APIFormat)).Grow(1)
						flat(c, "拉取模型").Disabled(a.modelForm.FetchBusy).OnClick(func() { a.fetchModelList() })
					})
					if a.modelForm.FetchNote != "" {
						inlineError(c, "model-fetch-note", a.modelForm.FetchNote)
					}
				})
				fieldRow(c, "base-url", "Base URL", &a.modelDraft.BaseURL, false)
			})
			fieldLabel(c, "API Keys")
			for i := range a.modelKeys {
				i := i
				ui.Row(c.Key(fmt.Sprintf("chat-key-%d-%d", a.modelForm.KeyEpoch, i))).Gap(gapTight).AlignItems(ui.Center).Children(func() {
					ui.Text(c, fmt.Sprint(i+1)).FontSize(fsTiny).TextColor(c.Theme().TextMuted).Width(14)
					uikit.PasswordInput(c, "password", fmt.Sprintf("API Key %d", i+1), &a.modelKeys[i]).Grow(1)
					if len(a.modelKeys) > 1 {
						dangerIconAction(c, "remove-key-"+fmt.Sprint(i), "移除密钥", "close", 22, func() {
							a.modelKeys = append(a.modelKeys[:i], a.modelKeys[i+1:]...)
							a.modelForm.KeyEpoch++
						})
					}
				})
			}
			flat(c, "＋ 添加 Key").OnClick(func() { a.modelKeys = append(a.modelKeys, "") })
			fieldNote(c, "可留空（本地推理无需鉴权）。多个 Key 按顺序故障切换，只在尚未开始输出时切换。")
			ui.Grid(c.Key("model-parameters")).ColumnTracks(ui.Fr(1), ui.Fr(1)).FillWidth().Gap(gapSection).Children(func() {
				ui.Column(c).Gap(gapTight).Children(func() {
					fieldLabel(c, "Max Tokens")
					numberField(c, "max-tokens", "Max Tokens", &a.modelMaxTokens, 0, 2000000, 1).FillWidth()
				})
				ui.Column(c).Gap(gapTight).Children(func() {
					fieldLabel(c, "推理标签")
					uikit.ComboboxField(c.Key("reasoning-tag"), &a.modelDraft.ReasoningTag, []string{"reasoning_content", "reasoning", "think", "thinking", "thought", "reason"}, "推理标签", "").FillWidth()
				})
			})
			if a.modelDraft.APIFormat == "openai" {
				fieldLabel(c, "Token 参数")
				uikit.Select(c.Key("token-param"), &a.modelDraft.TokenParam, []string{"auto", "max_completion_tokens"}).Label("Token 参数").FillWidth()
			}
			ui.Grid(c.Key("model-effort-context")).ColumnTracks(ui.Fr(1), ui.Fr(1)).FillWidth().Gap(gapSection).Children(func() {
				ui.Column(c).Gap(gapTight).Children(func() {
					fieldLabel(c, "思考强度")
					uikit.Select(c.Key("reasoning-effort"), &a.modelDraft.ReasoningEffort, []string{"", "auto", "off", "low", "medium", "high", "xhigh", "max"}).Label("思考强度").FillWidth()
				})
				ui.Column(c).Gap(gapTight).Children(func() {
					fieldLabel(c, "上下文窗口")
					numberField(c, "context-window", "上下文窗口", &a.modelContextWindow, 0, 10000000, 1).FillWidth()
				})
			})
			advancedLabel := "高级设置"
			advancedIcon := "chevron"
			if a.modelForm.AdvancedOpen {
				advancedIcon = "chevrondown"
			}
			advancedButton := flat(c, advancedLabel).AlignSelf(ui.Start).OnClick(func() { a.modelForm.AdvancedOpen = !a.modelForm.AdvancedOpen })
			caretButton(c, advancedButton, advancedLabel, advancedIcon, fsSmall, nil)
			if a.modelForm.AdvancedOpen {
				fieldLabel(c, "自定义请求头（JSON）")
				textArea(c, "custom-headers", "自定义请求头", `{"X-Header": "value"}`, &a.modelHeaders, 90)
				fieldNote(c, "随该模型每个 API 请求发送，可覆盖 Authorization / User-Agent；传输层头会被忽略。")
			}
		}
	}, func() {
		flat(c, "测试连接").Disabled(a.modelSaving).OnClick(func() { a.testModelDraft() })
		flat(c, "取消").OnClick(func() {
			a.modelForm.Open = false
			a.cancelOAuthWhenLeaving()
		})
		primary(c, map[bool]string{true: "添加", false: "保存修改"}[a.modelEdit < 0]).Disabled(a.modelSaving).OnClick(func() { a.saveModelDraft() })
	}, a.cancelOAuthWhenLeaving)
}

// modelDraftOptions is what the Model dropdown can offer: the models returned
// by the last 拉取模型 call first, then the ones already configured against the
// same endpoint, so an existing id stays pickable before any fetch happens.
func (a *desktop) modelDraftOptions() []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(a.modelForm.Fetched)+len(a.config.Models)+1)
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	for _, name := range a.modelForm.Fetched {
		add(name)
	}
	for _, model := range a.config.Models {
		if model.AuthType == "chatgpt_oauth" || !sameModelEndpoint(model, a.modelDraft) {
			continue
		}
		add(model.Model)
	}
	add(a.modelDraft.Model)
	return out
}

// sameModelEndpoint reports whether two drafts point at the same API base; an
// empty Base URL falls back to the provider name.
func sameModelEndpoint(model, draft backend.ModelConfig) bool {
	trim := func(s string) string { return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s), "/")) }
	if base, ref := trim(model.BaseURL), trim(draft.BaseURL); base != "" && ref != "" {
		return base == ref
	}
	return strings.EqualFold(strings.TrimSpace(model.ProviderName), strings.TrimSpace(draft.ProviderName))
}

func modelIDPlaceholder(format string) string {
	switch format {
	case "openai_responses":
		return "gpt-4.1-mini"
	case "anthropic":
		return "claude-sonnet-5"
	}
	return "deepseek-v4-flash"
}

func (a *desktop) fetchModelList() {
	if a.modelForm.FetchBusy {
		return
	}
	key := ""
	if len(a.modelKeys) > 0 {
		key = strings.TrimSpace(a.modelKeys[0])
	}
	headers := map[string]string{}
	if strings.TrimSpace(a.modelHeaders) != "" {
		if err := json.Unmarshal([]byte(a.modelHeaders), &headers); err != nil {
			a.modelForm.FetchNote = "拉取模型列表失败：自定义请求头必须是 JSON 对象"
			return
		}
	}
	a.modelForm.FetchBusy = true
	a.modelForm.FetchNote = "拉取中…"
	base, format := a.modelDraft.BaseURL, a.modelDraft.APIFormat
	go func() {
		models, err := a.core.FetchModelList(base, key, headers)
		a.win.Update(func() {
			a.modelForm.FetchBusy = false
			if err != nil {
				a.modelForm.FetchNote = "拉取模型列表失败：" + err.Error()
				return
			}
			if len(models) == 0 {
				a.modelForm.FetchNote = "接口未返回任何模型"
				return
			}
			a.modelForm.FetchNote = fmt.Sprintf("已获取 %d 个模型，可在 Model 下拉中选择。", len(models))
			a.modelForm.Fetched = models
			_ = format
		})
	}()
}

func (a *desktop) testModelDraft() {
	m, err := a.preparedModelDraft()
	if err != nil {
		a.notifyError(err.Error())
		return
	}
	a.modelForm.TestBusy = true
	a.job(func() error {
		err := a.core.TestModelConnection(m)
		a.win.Update(func() {
			a.modelForm.TestBusy = false
			if err != nil {
				a.notifyError("连接失败：" + err.Error())
			} else {
				a.notifySuccess("连接成功")
			}
		})
		return err
	})
}

// ---- Image models -------------------------------------------------------

func (a *desktop) imageModelsSection(c *ui.Context) {
	s := a.imageModels
	if s == nil {
		return
	}
	card(c, "image-defaults", func() {
		ui.Row(c).Gap(gapField).AlignItems(ui.Center).Wrap().Children(func() {
			if uikit.SwitchNative(c.Key("image-enabled"), &s.config.Enabled).Label("使用生图工具").Changed() {
				a.applyImageConfig()
			}
			ui.Text(c, "使用生图工具").FontSize(fsSmall)
			ui.Spacer(c)
			fieldLabel(c, "默认生图模型")
			if uikit.Select(c.Key("image-generate"), &s.generate, s.options(false)).Label("默认生图模型").Width(220).Disabled(s.busy).Changed() {
				s.chooseDefault(s.generate, false)
				a.applyImageConfig()
			}
			fieldLabel(c, "默认编辑模型")
			if uikit.Select(c.Key("image-edit"), &s.edit, s.options(true)).Label("默认编辑模型").Width(220).Disabled(s.busy).Changed() {
				s.chooseDefault(s.edit, true)
				a.applyImageConfig()
			}
			cardFlat(c, "测试").Disabled(s.busy || s.config.GenerateModel == nil).OnClick(func() {
				if s.config.GenerateModel != nil {
					s.test = &imageModelTest{model: *s.config.GenerateModel}
					s.testOpen = true
				}
			})
		})
		fieldNote(c, "供所有会话共用，原图与每次编辑结果保存在本地。MiniMax 仅支持文生图；工具列表变更在新会话生效。")
	})
	models := a.filteredImageModels(s)
	if len(models) == 0 {
		emptyState(c, "尚未配置生图模型", "点击“添加”创建第一个生图模型。")
	} else {
		for _, m := range models {
			m := m
			rowCard(c, "image-model-"+imageIdentityKey(m.ProviderName, m.Model), func() {
				ui.Row(c).Gap(gapInline).AlignItems(ui.Center).Wrap().Children(func() {
					ui.Text(c, m.Model).FontSize(fsBody).Bold()
					neutralChip(c, imageProtocolLabel(m.Protocol))
					if imageLabel(s.config.GenerateModel) == modelLabel(m.ProviderName, m.Model) {
						accentChip(c, "默认生图模型")
					}
					if imageLabel(s.config.EditModel) == modelLabel(m.ProviderName, m.Model) {
						accentChip(c, "默认编辑模型")
					}
					ui.Spacer(c)
					cardFlat(c, "编辑").Disabled(s.busy).OnClick(func() {
						for i, item := range s.config.Models {
							if imageIdentityKey(item.ProviderName, item.Model) == imageIdentityKey(m.ProviderName, m.Model) {
								s.openForm(i)
								break
							}
						}
					})
					cardFlat(c, "测试").Disabled(s.busy).OnClick(func() {
						s.test = &imageModelTest{model: *imageIdentity(m)}
						s.testOpen = true
					})
					cardDanger(c, "删除").Disabled(s.busy).OnClick(func() {
						a.deleteImageModel(m)
					})
				})
				fieldNote(c, m.ProviderName+"  ·  "+m.BaseURL)
			})
		}
	}
}

func (a *desktop) filteredImageModels(s *imageModelsState) []backend.ImageModelConfig {
	query := strings.ToLower(strings.TrimSpace(a.modelSearch))
	var out []backend.ImageModelConfig
	for _, m := range s.config.Models {
		if query != "" {
			haystack := strings.ToLower(m.ProviderName + " " + m.Model + " " + m.BaseURL)
			if !strings.Contains(haystack, query) {
				continue
			}
		}
		out = append(out, m)
	}
	return out
}

func imageProtocolLabel(protocol string) string {
	switch protocol {
	case "xai-images":
		return "xAI"
	case "minimax-images":
		return "MiniMax"
	}
	return "OpenAI"
}

func (a *desktop) imageModelDialog(c *ui.Context) {
	s := a.imageModels
	if s == nil || !s.formOpen {
		return
	}
	title := "编辑生图模型"
	if s.editing < 0 {
		title = "添加生图模型"
	}
	formDialog(c, &s.formOpen, widthModal, 620, title, "", func() {
		fieldRow(c, "image-provider", "供应商名称", &s.draft.ProviderName, false)
		ui.Column(c).Gap(gapTight).Children(func() {
			fieldLabel(c, "Model")
			before := s.draft.Model
			uikit.Input(c.Key("image-model-id"), &s.draft.Model).Label("Model").Placeholder("gpt-image-2 / grok-imagine-image / image-01").FillWidth()
			if s.draft.Model != before && s.draft.Protocol == "" {
				s.draft.Protocol = inferImageProtocolView(s.draft.Model)
			}
		})
		ui.Column(c).Gap(gapTight).Children(func() {
			fieldLabel(c, "生图协议")
			protocols := []string{"openai-images", "xai-images", "minimax-images"}
			beforeProtocol := s.draft.Protocol
			uikit.Select(c.Key("image-protocol"), &s.draft.Protocol, protocols).Label("生图协议").FillWidth()
			if s.draft.Protocol != beforeProtocol {
				switch s.draft.Protocol {
				case "openai-images":
					s.draft.BaseURL = "https://api.openai.com/v1"
				case "xai-images":
					s.draft.BaseURL = "https://api.x.ai/v1"
				case "minimax-images":
					s.draft.BaseURL = "https://api.minimax.io/v1"
				}
			}
		})
		fieldRow(c, "image-base-url", "Base URL", &s.draft.BaseURL, false)
		fieldLabel(c, "API Keys")
		for i := range s.draftKeys {
			i := i
			ui.Row(c.Key(fmt.Sprintf("image-key-%d-%d", s.keyEpoch, i))).Gap(gapTight).AlignItems(ui.Center).Children(func() {
				ui.Text(c, fmt.Sprint(i+1)).FontSize(fsTiny).TextColor(c.Theme().TextMuted).Width(14)
				uikit.PasswordInput(c, "password", fmt.Sprintf("图片 API Key %d", i+1), &s.draftKeys[i]).Grow(1)
				if len(s.draftKeys) > 1 {
					dangerIconAction(c, "image-remove-key-"+fmt.Sprint(i), "移除密钥", "close", 22, func() {
						s.draftKeys = append(s.draftKeys[:i], s.draftKeys[i+1:]...)
						s.keyEpoch++
					})
				}
			})
		}
		flat(c, "＋ 添加 Key").OnClick(func() { s.draftKeys = append(s.draftKeys, "") })
		advancedIcon := "chevron"
		if s.advancedOpen {
			advancedIcon = "chevrondown"
		}
		advancedButton := flat(c, "高级设置").AlignSelf(ui.Start).OnClick(func() { s.advancedOpen = !s.advancedOpen })
		caretButton(c, advancedButton, "高级设置", advancedIcon, fsSmall, nil)
		if s.advancedOpen {
			fieldLabel(c, "自定义请求头（JSON）")
			textArea(c, "image-headers", "自定义请求头", `{"X-Header": "value"}`, &s.headers, 80)
		}
	}, func() {
		flat(c, "取消").OnClick(func() { s.formOpen = false })
		primary(c, "保存").Disabled(s.busy).OnClick(func() { a.saveImageModelDraft() })
	})
}

func inferImageProtocolView(model string) string {
	name := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(name, "gpt-image-"), name == "chatgpt-image-latest":
		return "openai-images"
	case strings.HasPrefix(name, "grok-imagine-image"):
		return "xai-images"
	case name == "image-01", name == "image-01-live":
		return "minimax-images"
	}
	return ""
}

func (a *desktop) saveImageModelDraft() {
	s := a.imageModels
	model, err := normalizeImageDraft(s.draft, s.draftKeys, s.headers)
	if err == nil {
		err = s.replace(s.editing, &model)
	}
	if err != nil {
		a.notifyError(err.Error())
		return
	}
	s.formOpen = false
	a.applyImageConfig()
}

func (a *desktop) deleteImageModel(m backend.ImageModelConfig) {
	s := a.imageModels
	index := -1
	for i, item := range s.config.Models {
		if imageIdentityKey(item.ProviderName, item.Model) == imageIdentityKey(m.ProviderName, m.Model) {
			index = i
			break
		}
	}
	if index < 0 {
		return
	}
	a.confirmAction("删除图片模型", modelLabel(m.ProviderName, m.Model), func() error { return nil }, func() {
		_ = s.replace(index, nil)
		s.formOpen = false
		a.applyImageConfig()
	})
}

func (a *desktop) imageModelTestDialog(c *ui.Context) {
	s := a.imageModels
	if s == nil || s.test == nil || !s.testOpen {
		return
	}
	t := s.test
	modalCard(c, &s.testOpen, widthModalWide, func() {
		modalTitle(c, "测试", modelLabel(t.model.ProviderName, t.model.Model))
		textArea(c, "test-prompt", "提示词", "描述要生成或编辑的图片…", &t.prompt, 100)
		canEdit := false
		for _, m := range s.config.Models {
			if imageIdentityKey(m.ProviderName, m.Model) == imageIdentityKey(t.model.ProviderName, t.model.Model) {
				canEdit = m.Protocol != "minimax-images"
				break
			}
		}
		ui.Row(c).Gap(gapField).AlignItems(ui.Center).Wrap().Children(func() {
			flat(c, "添加参考图").Disabled(t.busy || !canEdit || len(t.inputs) >= 5).OnClick(func() { a.chooseImageTestInputs(s, t) })
			uikit.Select(c.Key("test-ratio"), &t.ratio, []string{"", "1:1", "3:2", "2:3"}).Label("比例").Width(120).Disabled(t.busy)
			if canEdit {
				fieldNote(c, "有参考图时使用图生图。最多 5 张 PNG/JPEG/WebP，合计 20 MiB。")
			} else {
				fieldNote(c, "此模型仅支持文生图。")
			}
		})
		if len(t.inputs) > 0 {
			ui.Row(c).Gap(gapField).Wrap().Children(func() {
				for i, in := range t.inputs {
					i, in := i, in
					ui.Column(c.Key(fmt.Sprintf("test-input-%d", i))).Width(96).Gap(gapTight).Children(func() {
						if in.preview != nil {
							ui.Image(c, in.preview).Size(88, 88).Fit(ui.Contain)
						}
						dangerButton(c, "删除").Disabled(t.busy).OnClick(func() { t.inputs = append(t.inputs[:i], t.inputs[i+1:]...) })
					})
				}
			})
		}
		inlineError(c, "test-err", t.err)
		ui.Row(c).Gap(gapField).AlignItems(ui.Center).Children(func() {
			fieldNote(c, "仅显示本次打开期间的结果，原图自动保存在本地。")
			ui.Spacer(c)
			label := "文生图"
			if len(t.inputs) > 0 {
				label = "图生图"
			}
			primary(c, label).Disabled(t.busy || strings.TrimSpace(t.prompt) == "").OnClick(func() { a.runImageModelTest(s, t) })
			if t.busy {
				ui.Spinner(c).Size(14, 14)
			}
		})
		for _, result := range t.results {
			result := result
			card(c, "test-result-"+result.meta.Ref, func() {
				ui.Text(c, result.meta.Prompt).FontSize(fsSmall)
				fieldNote(c, fmt.Sprintf("%s · %d × %d", fmtTime(result.meta.CreatedAt), result.meta.Width, result.meta.Height))
				if result.preview != nil {
					ui.Image(c, result.preview).Size(260, 260).Fit(ui.Contain).Radius(radiusSmall)
				}
				ref := result.meta.Ref
				cardFlat(c, "下载原图").OnClick(func() { a.job(func() error { _, err := a.core.ExportGeneratedImage(ref); return err }) })
			})
		}
	})
}

// ---- Import / export ----------------------------------------------------

func (a *desktop) importModels() {
	image := a.modelTab == "图片模型"
	go func() {
		path, err := a.core.SelectJSONFile("导入模型配置")
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
			Kind          string                 `json:"kind"`
			Models        []json.RawMessage      `json:"models"`
			ImageGenerate *backend.ModelIdentity `json:"generateModel"`
			ImageEdit     *backend.ModelIdentity `json:"editModel"`
		}
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			a.win.Update(func() { a.notify("无法导入，请选择有效的模型配置文件。") })
			return
		}
		if image {
			if payload.Kind != "" && payload.Kind != "ally-image-models" {
				a.win.Update(func() { a.notify("这是对话模型配置文件，请切换到「对话模型」分类导入。") })
				return
			}
			var models []backend.ImageModelConfig
			raw, _ := json.Marshal(payload.Models)
			if err := json.Unmarshal(raw, &models); err != nil {
				a.win.Update(func() { a.notify("无法导入，请选择有效的 Ally 生图模型配置文件。") })
				return
			}
			a.win.Update(func() {
				a.ensureImageModels()
				added, updated := 0, 0
				for _, m := range models {
					replaced := false
					for i := range a.imageModels.config.Models {
						if imageIdentityKey(a.imageModels.config.Models[i].ProviderName, a.imageModels.config.Models[i].Model) == imageIdentityKey(m.ProviderName, m.Model) {
							a.imageModels.config.Models[i] = m
							replaced = true
							updated++
							break
						}
					}
					if !replaced {
						a.imageModels.config.Models = append(a.imageModels.config.Models, m)
						added++
					}
				}
				a.applyImageConfig()
				a.notifySuccess(fmt.Sprintf("导入完成：新增 %d 个，更新 %d 个", added, updated))
			})
			return
		}
		if payload.Kind != "" && payload.Kind != "ally-chat-models" {
			a.win.Update(func() { a.notify("这是生图模型配置文件，请切换到「图片模型」分类导入。") })
			return
		}
		var models []backend.ModelConfig
		raw, _ := json.Marshal(payload.Models)
		if err := json.Unmarshal(raw, &models); err != nil {
			a.win.Update(func() { a.notify("无法导入，请选择有效的 Ally 对话模型配置文件。") })
			return
		}
		a.win.Update(func() {
			cfg := a.config
			added, updated := 0, 0
			for _, m := range models {
				replaced := false
				for i := range cfg.Models {
					if sameModelIdentity(modelIdentity(cfg.Models[i]), modelIdentity(m)) {
						cfg.Models[i] = m
						replaced = true
						updated++
						break
					}
				}
				if !replaced {
					cfg.Models = append(cfg.Models, m)
					added++
				}
			}
			a.saveConfig(cfg)
			a.notifySuccess(fmt.Sprintf("导入完成：新增 %d 个，更新 %d 个", added, updated))
		})
	}()
}

func (a *desktop) exportModels() {
	image := a.modelTab == "图片模型"
	var kind string
	var models any
	if image {
		a.ensureImageModels()
		kind = "ally-image-models"
		models = a.imageModels.config.Models
	} else {
		kind = "ally-chat-models"
		models = a.config.Models
	}
	payload := map[string]any{"kind": kind, "formatVersion": 1, "models": models}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		a.notifyError(err.Error())
		return
	}
	name := "ally-chat-models.json"
	if image {
		name = "ally-image-models.json"
	}
	a.exportText(name, string(data))
}

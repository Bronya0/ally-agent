// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"strings"

	"ally-dev/desktop/uikit"
	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

func (a *desktop) voiceModelsSection(c *ui.Context) {
	a.ensureVoiceModels()
	s := a.voiceModels
	fieldNote(c, "支持本地 ASR 或远端语音 API。实时测试会录制最多 20 秒麦克风音频，点击停止后上传当前配置的语音服务进行转写；录音只暂存于内存，不会存盘。", a.secondaryInk())
	query := strings.ToLower(strings.TrimSpace(a.modelSearch))
	count := 0
	for i, m := range s.models {
		if query != "" && !strings.Contains(strings.ToLower(m.Name+" "+m.Provider+" "+m.Endpoint+" "+m.Model), query) {
			continue
		}
		count++
		idx, m := i, m
		rowCard(c, "voice-model-"+m.ID, func() {
			ui.Row(c).Gap(gapInline).AlignItems(ui.Center).Wrap().Children(func() {
				title := m.Name
				if title == "" {
					title = m.Model
				}
				ui.Text(c, title).FontSize(fsBody).Bold()
				neutralChip(c, m.Deployment)
				neutralChip(c, backend.VoiceProtocolLabel(m.Protocol))
				ui.Spacer(c)
				cardFlat(c, "编辑").Disabled(s.busy).OnClick(func() { s.open(idx) })
				cardDanger(c, "删除").Disabled(s.busy).OnClick(func() { a.deleteVoiceModel(idx) })
			})
			fieldNote(c, m.Provider+" · "+m.Endpoint, a.secondaryInk())
			if m.Model != "" {
				fieldNote(c, "模型："+m.Model, a.secondaryInk())
			}
		})
	}
	if count == 0 {
		emptyState(c, "尚未配置语音模型", "添加本地 ASR 或兼容语音 API。", a.secondaryInk())
	}
}

func (a *desktop) voiceModelsDialog(c *ui.Context) {
	a.ensureVoiceModels()
	s := a.voiceModels
	if !s.formOpen {
		return
	}
	title := "编辑语音模型"
	if s.editing < 0 {
		title = "添加语音模型"
	}
	formDialog(c, &s.formOpen, widthModal, 680, title, "", func() {
		protocol, _ := backend.VoiceProtocolSpec(s.draft.Protocol)
		presetOptions := make([]uikit.Option, 0, 4)
		for _, preset := range backend.VoiceProviderPresets() {
			presetOptions = append(presetOptions, uikit.Option{Value: preset.ID, Label: preset.Label})
		}
		uikit.SelectField(c.Key("voice-preset-select"), &s.preset, presetOptions, a.secondaryInk()).Label("服务预设").FillWidth().OnChange(func() {
			preset, ok := backend.VoiceProviderPresetSpec(s.preset)
			if !ok {
				return
			}
			s.presetMode = preset.DefaultMode()
			s.applyVoicePreset(preset, s.presetMode)
			protocol, _ = backend.VoiceProtocolSpec(s.draft.Protocol)
		})
		if preset, ok := backend.VoiceProviderPresetSpec(s.preset); ok && len(preset.Modes) > 0 {
			ui.Row(c.Key("voice-preset-modes")).Gap(gapInline).AlignItems(ui.Center).Children(func() {
				fieldLabel(c, "模式")
				for _, mode := range preset.Modes {
					mode := mode
					choiceButton(c, "voice-preset-mode-"+mode.ID, mode.Label, s.presetMode == mode.ID, c.Theme().Accent).OnClick(func() {
						s.presetMode = mode.ID
						s.draft = preset.Apply(s.draft, mode.ID)
						s.endpointState, s.endpointPassKey = "", ""
						protocol, _ = backend.VoiceProtocolSpec(s.draft.Protocol)
					})
				}
			})
		}
		ui.Row(c.Key("voice-protocol-chip")).Gap(gapInline).AlignItems(ui.Center).Children(func() {
			neutralChip(c, backend.VoiceProtocolLabel(s.draft.Protocol))
			if s.draft.Model != "" && protocol.Uses(backend.VoiceFieldModel) {
				neutralChip(c, s.draft.Model)
			}
			ui.Spacer(c)
			fieldNote(c, "选好预设后只需填 API Key", a.secondaryInk())
		})
		if s.draft.Endpoint != "" && !s.endpointEditing {
			// A preset already points at the right route; editing is opt-in so the
			// common path never risks a typo in the URL.
			ui.Row(c.Key("voice-endpoint-readonly")).Gap(gapField).AlignItems(ui.Center).Children(func() {
				fieldLabel(c, "Endpoint")
				ui.Text(c, s.draft.Endpoint).FontSize(fsTiny).TextColor(a.secondaryInk()).Selectable().Grow(1)
				cardFlat(c, "修改").OnClick(func() { s.endpointEditing = true })
			})
		} else {
			fieldRow(c, "voice-endpoint", "Endpoint", &s.draft.Endpoint, false)
		}
		if protocol.Uses(backend.VoiceFieldModel) {
			ui.Column(c).Gap(gapTight).Children(func() {
				ui.Row(c.Key("voice-model-row")).Gap(gapField).AlignItems(ui.End).Children(func() {
					uikit.ComboboxField(c.Key("voice-model"), &s.draft.Model, s.modelOptions(), "模型名", "拉取或手填模型名", a.secondaryInk()).Grow(1)
					choiceButton(c, "voice-model-all", "全部", s.includeAllModels, c.Theme().Accent).OnClick(func() { s.includeAllModels = !s.includeAllModels })
					flatBusy(c, "拉取模型", s.fetchBusy, s.fetchBusy || s.busy).OnClick(func() { a.fetchVoiceModelList() })
				})
				if s.fetchNote != "" {
					fieldNote(c, s.fetchNote, a.secondaryInk())
				}
			})
		}
		if protocol.Uses(backend.VoiceFieldAPIKey) {
			fieldRow(c, "voice-api-key", "API Key", &s.draft.APIKey, true)
		}
		fieldNote(c, "Endpoint 检查不会上传录音；通过后可开始麦克风录音测试。", a.secondaryInk())
		if s.endpointState != "" {
			fieldNote(c, s.endpointState, a.secondaryInk())
		}
		if s.endpointPassKey != "" && !a.endpointVerified(s) {
			fieldNote(c, "连接参数已修改，请重新测试 Endpoint。", colDanger())
		}
	}, func() {
		flatBusy(c, "测试 Endpoint", s.endpointBusy, s.busy || s.endpointBusy || s.testBusy).OnClick(func() { a.testVoiceEndpoint() })
		primaryBusy(c, "测试转写效果", s.endpointBusy || s.testBusy, s.busy || s.endpointBusy || s.testBusy || !a.endpointVerified(s)).OnClick(func() { a.openVoiceRecorder() })
		flat(c, "取消").OnClick(func() { a.cancelVoiceRecording(); s.formOpen = false })
		primaryBusy(c, map[bool]string{true: "添加", false: "保存修改"}[s.editing < 0], s.busy, s.busy || s.endpointBusy || s.testBusy || s.recording != nil).OnClick(func() { a.saveVoiceModels() })
	}, func() { a.cancelVoiceRecording() })
}

func (a *desktop) voiceRecorderDialog(c *ui.Context) {
	s := a.voiceModels
	if s == nil || !s.recorderOpen {
		return
	}
	dialog := modalCard(c, &s.recorderOpen, widthModal, func() {
		modalTitle(c, "语音转写测试", s.draft.Name+" · "+s.draft.Model)
		ui.Column(c).FillWidth().AlignItems(ui.Center).Gap(gapSection).Children(func() {
			ui.Box(c.Key("voice-recorder-mic")).Size(68, 68).Radius(34).Background(selectionFill(c.Theme().Accent)).Children(func() { iconGlyph(c, "microphone", 32).TextColor(c.Theme().Accent) })
			status := "准备录音"
			if s.recording != nil {
				status = s.recordState
			} else if s.testBusy {
				status = "正在识别录音…"
			} else if s.recordState != "" {
				status = s.recordState
			}
			ui.Text(c, status).FontSize(fsBody).Bold()
			voiceWaveform(c, s, a.currentPalette().accent, colLine(a))
			fieldNote(c, "点击开始后实时显示麦克风波形，最长录音 20 秒；停止后显示转写结果。", a.secondaryInk())
		})
		if s.transcript != "" {
			card(c, "voice-transcript-result", func() { sectionTitle(c, "识别结果"); ui.Text(c, s.transcript).FontSize(fsBody).Selectable().Wrap() })
		}
		ui.Row(c).Gap(gapField).Justify(ui.End).Children(func() {
			if s.recording != nil {
				primaryBusy(c, "停止并转写", s.testBusy, s.testBusy).OnClick(func() { a.stopVoiceRecording() })
			} else if s.testBusy {
				primaryBusy(c, "正在处理…", true, true)
			} else {
				primary(c, "开始录音").Disabled(!a.endpointVerified(s)).OnClick(func() { a.testVoiceModel() })
			}
			flat(c, "关闭").OnClick(func() { a.cancelVoiceRecording(); s.recorderOpen = false })
		})
	})
	if dialog.Dismissed() {
		a.cancelVoiceRecording()
		s.recorderOpen = false
	}
}

func voiceWaveform(c *ui.Context, s *voiceModelsState, accent, line ui.Color) {
	levels := s.waveLevels
	active := s.recording != nil
	ui.Box(c.Key("voice-input-waveform")).FillWidth().Height(94).Radius(radiusCard).Background(c.Theme().Surface).Draw(func(p *ui.Painter, r ui.Rect) {
		if active {
			p.AnimationFrameRate(30)
		}
		mid := r.Y + r.H/2
		p.Line(r.X+10, mid, r.X+r.W-10, mid, 1, line)
		const bars = 56
		gap := float32(3)
		barWidth := max(float32(2), (r.W-24-float32(bars-1)*gap)/bars)
		for i := 0; i < bars; i++ {
			level := float32(0)
			index := i - (bars - len(levels))
			if index >= 0 && index < len(levels) {
				level = levels[index]
			}
			if active && len(levels) == 0 {
				level = s.micLevel
			}
			h := float32(3) + level*(r.H-18)
			x := r.X + 12 + float32(i)*(barWidth+gap)
			p.Fill(ui.Rect{X: x, Y: mid - h/2, W: barWidth, H: h}, accent, barWidth/2)
		}
	})
}

// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/locales"
	"fmt"
	"strings"
	"time"

	"ally-dev/desktop/uikit"
	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

func (a *desktop) voiceModelsSection(c *ui.Context) {
	a.ensureVoiceModels()
	s := a.voiceModels
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
				ui.Text(c, voiceModelLabel(m)).FontSize(fsBody).Bold()
				neutralChip(c, backend.VoiceKindLabel(m.Kind))
				neutralChip(c, backend.VoiceDeploymentLabel(m.Deployment))
				neutralChip(c, backend.VoiceProtocolLabel(m.Protocol))
				ui.Spacer(c)
				cardFlat(c, locales.Text("编辑")).Disabled(s.busy).OnClick(func() { s.open(idx) })
				cardDanger(c, locales.Text("删除")).Disabled(s.busy).OnClick(func() { a.deleteVoiceModel(idx) })
			})
			lines := s.rowLines(m)
			fieldNote(c, lines.providerLine, a.secondaryInk())
			if lines.modelLine != "" {
				fieldNote(c, lines.modelLine, a.secondaryInk())
			}
		})
	}
	if count == 0 {
		// count only counts search hits, so it cannot answer "is anything configured?".
		// A typo used to hide every row behind 尚未配置语音模型 and read as lost config.
		if len(s.models) == 0 {
			emptyState(c, locales.Text("尚未配置语音模型"), "", a.secondaryInk())
		} else {
			emptyState(c, locales.Text("没有匹配的语音模型"), locales.Text("清空上方搜索框可查看全部语音模型。"), a.secondaryInk())
		}
	}
}

func (a *desktop) voiceModelsDialog(c *ui.Context) {
	a.ensureVoiceModels()
	s := a.voiceModels
	if !s.formOpen {
		return
	}
	title := locales.Text("编辑语音模型")
	if s.editing < 0 {
		title = locales.Text("添加语音模型")
	}
	// One shape check per frame: it derives the auth chrome and the deployment label,
	// so asking for it three times per frame was pure waste. The body and the footer
	// are separate closures, so it has to live outside both.
	verified := a.endpointVerified(s)
	formDialog(c, &s.formOpen, widthModal, 680, title, "", func() {
		protocol, _ := backend.VoiceProtocolSpec(s.draft.Protocol)
		ui.Row(c.Key("voice-kind-row")).Gap(gapField).AlignItems(ui.Center).Children(func() {
			fieldLabel(c, locales.Text("类别"))
			neutralChip(c, backend.VoiceKindLabel(s.draft.Kind))
		})
		uikit.SelectField(c.Key("voice-preset-select"), &s.preset, s.presetOptions, a.secondaryInk()).Label(locales.Text("服务预设")).FillWidth().OnChange(func() {
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
				fieldLabel(c, locales.Text("模式"))
				for _, mode := range preset.Modes {
					mode := mode
					choiceButton(c, "voice-preset-mode-"+mode.ID, locales.Text(mode.Label), s.presetMode == mode.ID, c.Theme().Accent).OnClick(func() {
						s.presetMode = mode.ID
						s.draft = preset.Apply(s.draft, mode.ID)
						s.endpointState, s.endpointChecked = "", false
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
			fieldNote(c, locales.Text("只需填 API Key"), a.secondaryInk())
		})
		if s.draft.Endpoint != "" && !s.endpointEditing {
			// A preset already points at the right route; editing is opt-in so the
			// common path never risks a typo in the URL.
			ui.Row(c.Key("voice-endpoint-readonly")).Gap(gapField).AlignItems(ui.Center).Children(func() {
				fieldLabel(c, "Endpoint")
				ui.Text(c, s.draft.Endpoint).FontSize(fsTiny).TextColor(a.secondaryInk()).Selectable().Grow(1)
				cardFlat(c, locales.Text("修改")).OnClick(func() { s.endpointEditing = true })
			})
		} else {
			fieldRow(c, "voice-endpoint", "Endpoint", &s.draft.Endpoint, false)
		}
		if protocol.Uses(backend.VoiceFieldModel) {
			ui.Column(c).Gap(gapTight).Children(func() {
				ui.Row(c.Key("voice-model-row")).Gap(gapField).AlignItems(ui.End).Children(func() {
					uikit.ComboboxField(c.Key("voice-model"), &s.draft.Model, s.modelOptions(), locales.Text("模型名"), locales.Text("拉取或手填模型名"), a.secondaryInk()).Grow(1)
					choiceButton(c, "voice-model-all", locales.Text("全部"), s.includeAllModels, c.Theme().Accent).OnClick(func() { s.includeAllModels = !s.includeAllModels })
					flatBusy(c, locales.Text("拉取模型"), s.fetchBusy, s.fetchBusy || s.busy).OnClick(func() { a.fetchVoiceModelList() })
				})
				if s.fetchNote != "" {
					fieldNote(c, s.fetchNote, a.secondaryInk())
				}
			})
		}
		if protocol.Uses(backend.VoiceFieldAPIKey) {
			fieldRow(c, "voice-api-key", "API Key", &s.draft.APIKey, true)
		}
		fieldNote(c, locales.Text("Endpoint 检查不会上传录音；通过后可开始麦克风录音测试。"), a.secondaryInk())
		if s.endpointState != "" {
			fieldNote(c, s.endpointState, a.secondaryInk())
		}
		if s.endpointChecked && !verified {
			fieldNote(c, locales.Text("连接参数已修改，请重新测试 Endpoint。"), colDanger())
		}
	}, func() {
		flatBusy(c, locales.Text("测试 Endpoint"), s.endpointBusy, s.busy || s.endpointBusy || s.testBusy).OnClick(func() { a.testVoiceEndpoint() })
		primaryBusy(c, locales.Text("测试转写效果"), s.endpointBusy || s.testBusy, s.busy || s.endpointBusy || s.testBusy || !verified).OnClick(func() { a.openVoiceRecorder() })
		flat(c, locales.Text("取消")).OnClick(func() { a.cancelVoiceRecording(); s.formOpen = false })
		primaryBusy(c, map[bool]string{true: locales.Text("添加"), false: locales.Text("保存修改")}[s.editing < 0], s.busy, s.busy || s.endpointBusy || s.testBusy || s.recording != nil).OnClick(func() { a.saveVoiceModels() })
	}, func() { a.cancelVoiceRecording() })
}

func (a *desktop) voiceRecorderDialog(c *ui.Context) {
	s := a.voiceModels
	if s == nil || !s.recorderOpen {
		return
	}
	dialog := modalCard(c, &s.recorderOpen, widthModal, func() {
		modalTitle(c, locales.Text("语音转写测试"), s.draft.Name+" · "+s.draft.Model)
		ui.Column(c).FillWidth().AlignItems(ui.Center).Gap(gapSection).Children(func() {
			ui.Box(c.Key("voice-recorder-mic")).Size(68, 68).Radius(34).Background(selectionFill(c.Theme().Accent)).Children(func() { iconGlyph(c, "microphone", 32).TextColor(c.Theme().Accent) })
			status := locales.Text("准备录音")
			if s.recording != nil {
				// The elapsed second lives in an atomic, so the clock tick only repaints.
				status = fmt.Sprintf(locales.Text("录音中 · %d 秒 / %d 秒"), s.recordSeconds.Load(), int(voiceCaptureMaxDuration/time.Second))
			} else if s.testBusy {
				status = locales.Text("正在识别录音…")
			} else if s.recordState != "" {
				status = s.recordState
			}
			ui.Text(c, status).FontSize(fsBody).Bold()
			voiceWaveform(c, s, a.currentPalette().accent, colLine(a))
			fieldNote(c, locales.Text("点击开始后实时显示麦克风波形，最长录音 20 秒；停止后显示转写结果。"), a.secondaryInk())
		})
		if s.transcript != "" {
			card(c, "voice-transcript-result", func() {
				sectionTitle(c, locales.Text("识别结果"))
				ui.Text(c, s.transcript).FontSize(fsBody).Selectable().Wrap()
			})
		}
		ui.Row(c).Gap(gapField).Justify(ui.End).Children(func() {
			if s.recording != nil {
				primaryBusy(c, locales.Text("停止并转写"), s.testBusy, s.testBusy).OnClick(func() { a.stopVoiceRecording() })
			} else if s.testBusy {
				primaryBusy(c, locales.Text("正在处理…"), true, true)
			} else {
				primary(c, locales.Text("开始录音")).Disabled(!a.endpointVerified(s)).OnClick(func() { a.testVoiceModel() })
			}
			flat(c, locales.Text("关闭")).OnClick(func() { a.cancelVoiceRecording(); s.recorderOpen = false })
		})
	})
	if dialog.Dismissed() {
		a.cancelVoiceRecording()
		s.recorderOpen = false
	}
}

func voiceWaveform(c *ui.Context, s *voiceModelsState, accent, line ui.Color) {
	active := s.recording != nil
	ui.Box(c.Key("voice-input-waveform")).FillWidth().Height(94).Radius(radiusCard).Background(c.Theme().Surface).Draw(func(p *ui.Painter, r ui.Rect) {
		if active {
			p.AnimationFrameRate(30)
		}
		mid := r.Y + r.H/2
		p.Line(r.X+10, mid, r.X+r.W-10, mid, 1, line)
		gap := float32(3)
		barWidth := max(float32(2), (r.W-24-float32(voiceWaveBars-1)*gap)/voiceWaveBars)
		filled := s.wave.filled()
		for i := 0; i < voiceWaveBars; i++ {
			// Bars grow from the right, so the newest sample is the rightmost one.
			level := s.wave.level(voiceWaveBars - 1 - i)
			if i < voiceWaveBars-filled {
				level = 0
			}
			h := float32(3) + level*(r.H-18)
			x := r.X + 12 + float32(i)*(barWidth+gap)
			p.Fill(ui.Rect{X: x, Y: mid - h/2, W: barWidth, H: h}, accent, barWidth/2)
		}
	})
}

// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/desktop/uikit"
	"ally-dev/locales"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// Only fixed UI options use this adapter. Values stay unchanged across languages;
// model IDs, account names and other user data use the ordinary SelectField.
func localizedSelect(c *ui.Context, value *string, options []string, ink ...ui.Color) ui.Element {
	items := make([]uikit.Option, len(options))
	for i, option := range options {
		items[i] = uikit.Option{Value: option, Label: locales.Text(option)}
	}
	return uikit.SelectField(c, value, items, ink...)
}

// Only the first item is application text; the rest are provider/model names.
func imageModelSelect(c *ui.Context, value *string, options []string, ink ...ui.Color) ui.Element {
	items := make([]uikit.Option, len(options))
	for i, option := range options {
		items[i] = uikit.Option{Value: option, Label: option}
		if i == 0 {
			items[i].Label = locales.Text(option)
		}
	}
	return uikit.SelectField(c, value, items, ink...)
}

func (a *desktop) languageSetting(c *ui.Context) {
	current := c.Locale()
	a.settingsRow(c, "language", locales.Text("界面语言"), locales.Text("语言切换立即生效。聊天内容、代码和模型返回的消息保留原文。"), settingsControlWidth, func() ui.Element {
		options := []uikit.Option{
			{Value: "", Label: locales.Text("跟随系统")},
			{Value: "en", Label: "English"},
			{Value: "zh-Hans", Label: "简体中文"},
			{Value: "ja", Label: "日本語"},
			{Value: "ko", Label: "한국어"},
		}
		control := uikit.SelectField(c.Key("ui-language"), &current, options, a.secondaryInk()).Label(locales.Text("界面语言"))
		if control.Changed() {
			mygo.App.SetLocale(current)
		}
		return control
	})
}

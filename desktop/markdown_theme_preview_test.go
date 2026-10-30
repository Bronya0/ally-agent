package desktop

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"image/png"
	"os"
	"testing"
)

func TestReadingPalettePreview(t *testing.T) {
	if os.Getenv("ALLY_MD_PREVIEW") == "" {
		t.Skip("optional preview")
	}
	a := &desktop{}
	a.prefs.Palette = 0
	if os.Getenv("ALLY_MD_PREVIEW_ACCENT") == "amber" {
		a.prefs.Palette = 2
	}
	if os.Getenv("ALLY_MD_PREVIEW") == "light" {
		a.prefs.Appearance = "light"
	}
	conversation := &conversation{Messages: []map[string]any{{"role": "assistant"}}}
	options := a.markdownOptions(conversation, 0)
	source := `## 统一阅读样式，保持信息层次

正文使用 Inter，**重点内容略微加重**。内联代码的 MaxHeight 与 ` + "`MaxHeight(420)`" + ` 使用相同字号，以中性色和轻底色区分，减少色偏。

- 用 ` + "`contextBreakdownHeight`" + ` 统一管理高度，避免重复设置。
- [查看详细说明](https://example.com)：标题、链接和正文各有层次。

### 代码块保持独立字号

` + "```go" + `
func contextUsage() {
    const contextBreakdownHeight = 420
    renderMarkdown("Inter", 16)
}
` + "```" + `

> 辅助说明使用较淡的颜色，正文和内联代码保持一致的基线。

## 五、这三个坑别照抄

1. **模拟执行默认开**：` + "`LGLOG_ENTERPRISE_SIMULATE`" + ` 默认 true，正常用例直接拿规范样例当响应。我们要做演示模式可以，但必须默认关，结果上明确标“模拟”。
2. **入站鉴权失败回 HTTP 401**：成功请求 HTTP 固定 200，业务结果看 body 的 code。注意把接口约定和运行证据分别说明。
3. **轮询响应需要保留证据**：真实模式下请求发了，但存进证据的响应和耗时仍需核验，避免只检查最终 passed 状态。`
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		ui.Column(c).Fill().Background(a.currentPalette().bg).Padding(28).Children(func() { markdown.View(c, source, options).FillWidth() })
	}, 1000, 780)
	var data bytes.Buffer
	if err := png.Encode(&data, tt.Image()); err != nil {
		t.Fatal(err)
	}
	fmt.Println("MD_PREVIEW=" + base64.StdEncoding.EncodeToString(data.Bytes()))
}

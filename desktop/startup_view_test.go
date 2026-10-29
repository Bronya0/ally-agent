package desktop

import (
	"bytes"
	"github.com/egoist/mygo/ui"
	"testing"
	"time"
)

func TestStartupShellIsStatic(t *testing.T) {
	for _, appearance := range []string{"light", "dark"} {
		a := &desktop{page: "chat", prefs: defaultPrefs(), status: "正在启动…"}
		a.prefs.Appearance = appearance
		builds := 0
		tt := ui.NewTester(func(c *ui.Context) { builds++; a.view(c) }, 900, 620)
		before := append([]byte(nil), tt.Image().Pix...)
		count := builds
		tt.Advance(time.Second)
		if builds != count {
			t.Fatalf("startup scheduled repeated view builds: %d", builds-count)
		}
		if !bytes.Equal(before, tt.Image().Pix) {
			t.Fatal("startup shell animated while waiting")
		}
		for _, text := range tt.Texts() {
			if text == "正在启动…" {
				t.Fatal("startup splash still visible")
			}
		}
	}
}
func TestStartupFailureRemainsVisible(t *testing.T) {
	a := &desktop{startupError: "启动失败：测试错误"}
	tt := ui.NewTester(func(c *ui.Context) { c.SetTheme(a.theme()); a.startupBody(c) }, 600, 200)
	if _, ok := tt.Find(a.startupError); !ok {
		t.Fatal("startup error was hidden")
	}
}

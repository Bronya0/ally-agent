package desktop

import (
	"math"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func requireVerticalCenters(t *testing.T, tester *ui.Tester, names ...string) {
	t.Helper()
	var center float32
	for i, name := range names {
		r, ok := tester.Find(name)
		if !ok {
			t.Fatalf("missing toolbar control %q: %v", name, tester.Texts())
		}
		got := r.Y + r.H/2
		if i == 0 {
			center = got
		} else if math.Abs(float64(got-center)) > 0.1 {
			t.Fatalf("toolbar control %q has center %v, want %v: bounds=%+v", name, got, center, r)
		}
	}
}

func TestHeaderMenuIconsShareVerticalCenter(t *testing.T) {
	for _, appearance := range []string{"dark", "light"} {
		t.Run(appearance, func(t *testing.T) {
			a := &desktop{ready: true, page: "chat", prefs: defaultPrefs(), win: &mygo.Window{}, tabs: []*conversation{{ID: "aligned", Workspace: "project"}}}
			a.prefs.Appearance = appearance
			tester := ui.NewTester(func(c *ui.Context) {
				c.SetTheme(a.theme())
				c.DisableAnimations()
				a.headerBar(c)
			}, 900, 100)
			requireVerticalCenters(t, tester, "最小化", "工作区历史", "添加工作区", "打开工作区文件浏览器", "打开 GitHub 项目主页")
		})
	}
}

func TestComposerMenuIconsShareVerticalCenter(t *testing.T) {
	for _, busy := range []bool{false, true} {
		a := &desktop{prefs: defaultPrefs()}
		conv := &conversation{ID: "aligned", Workspace: "project", Model: "provider / model", Reasoning: "medium", Busy: busy}
		tester := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(a.theme())
			c.DisableAnimations()
			ui.Column(c).Fill().Justify(ui.End).Padding(12).Children(func() { a.composerInfoBar(c, conv) })
		}, 1100, 160)
		requireVerticalCenters(t, tester, "新建会话", "查看和切换历史会话", "provider · model", "思考强度", "在文件管理器中打开工作区：project", "附加目录", "在终端中打开工作区")
	}
}

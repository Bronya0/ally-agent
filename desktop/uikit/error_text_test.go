package uikit

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestErrorTextCanCopyFullDiagnostic(t *testing.T) {
	message := "网络错误：详细信息\n" + strings.Repeat("诊断内容 ", 60)
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() { ErrorText(c.Key("error"), message) })
	}, 320, 150)
	tt.RightClickAt(30, 10)
	if err := tt.Click("复制错误全文"); err != nil {
		t.Fatal(err)
	}
	if tt.Clipboard() != message {
		t.Fatal("copied text lost part of the diagnostic")
	}
}

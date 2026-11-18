package uikit

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func TestContentAdaptersRenderAndEdit(t *testing.T) {
	state := &editor.State{Buffer: editor.NewBuffer("first")}
	tt := chineseTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			Markdown(c.Key("markdown"), "**说明**", nil)
			CodeEditor(c.Key("editor"), state).Label("代码").Height(120).FillWidth()
		})
	}, 400, 250)
	if !tt.HasText("说明") {
		t.Fatal("markdown content missing")
	}
	if err := tt.Click("代码"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("updated")
	if got := state.Buffer.String(); got != "updated" {
		t.Fatalf("editor text = %q", got)
	}
}

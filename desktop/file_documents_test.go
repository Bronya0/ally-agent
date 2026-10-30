package desktop

import (
	"ally-dev/internal/documentpreview"
	"context"
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func TestDocumentPreviewUsesNativeVirtualRowsAndReleasesData(t *testing.T) {
	root := t.TempDir()
	file := &workspaceFile{Path: "report.docx", Document: &documentpreview.Document{}, Editor: &editor.State{Buffer: editor.NewBuffer("")}}
	for i := 0; i < 2000; i++ {
		file.Document.Paragraphs = append(file.Document.Paragraphs, documentpreview.Paragraph{Text: fmt.Sprintf("文档正文段落 %d", i)})
	}
	a := &desktop{prefs: defaultPrefs(), tabs: []*conversation{{Workspace: root}}, workspaceFiles: map[string]*workspaceFile{workspaceKey(root): file}}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		ui.Column(c).Fill().Children(func() {
			if f := a.activeFile(); f != nil {
				a.fileEditor(c, f)
			}
		})
	}, 700, 420)
	if !tt.HasText("文档正文段落 0") || tt.HasText("文档正文段落 1999") || len(tt.Texts()) > 100 {
		t.Fatalf("document was eagerly laid out: %d texts", len(tt.Texts()))
	}
	if err := tt.Click("关闭编辑器"); err != nil {
		t.Fatal(err)
	}
	if a.activeFile() != nil || file.Document != nil || file.Editor != nil {
		t.Fatal("closing preview retained document/editor")
	}
	tt.Frame()
}

func TestClosingWorkspaceCancelsPendingPreviewAndProperties(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	a := &desktop{filePreviewRoot: workspaceKey(root), filePreviewCancel: cancel, fileLoadID: 1}
	a.releaseWorkspaceFile(root)
	if ctx.Err() == nil || a.fileLoadID == 1 {
		t.Fatal("pending preview can reappear after closing workspace")
	}
	infoCtx, infoCancel := context.WithCancel(context.Background())
	a.filesF.InfoCancel = infoCancel
	a.filesF.InfoOpen = true
	a.closeFileInfo()
	if infoCtx.Err() == nil || a.filesF.InfoOpen {
		t.Fatal("property request survived closing dialog")
	}
}

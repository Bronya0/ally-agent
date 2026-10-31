package desktop

import (
	"ally-dev/internal/documentpreview"
	"context"
	"strings"
	"testing"
)

func TestDocumentPreviewRendersEscapedHTMLAndReleasesData(t *testing.T) {
	root := t.TempDir()
	document := &documentpreview.Document{Paragraphs: []documentpreview.Paragraph{{Text: "<script>alert('x')</script>"}}}
	file := &workspaceFile{Path: "report.docx", Document: document}
	content := documentPreviewHTML(file.Path, file.Document, "")
	if strings.Contains(content, "<script>") || !strings.Contains(content, "&lt;script&gt;") {
		t.Fatal("document text was not escaped in preview HTML")
	}
	a := &desktop{prefs: defaultPrefs(), tabs: []*conversation{{Workspace: root}}, workspaceFiles: map[string]*workspaceFile{workspaceKey(root): file}}
	a.releaseWorkspaceFile(root)
	if a.activeFile() != nil || file.Document != nil || file.PreviewPath != "" || file.PreviewHTML != "" {
		t.Fatal("closing preview retained document data")
	}
}

func TestClosingWorkspaceCancelsPendingPreviewAndProperties(t *testing.T) {
	root := t.TempDir()
	a := &desktop{}
	ctx, _ := a.workspacePreview.beginLoad(root, "pending.txt")
	a.releaseWorkspaceFile(root)
	if ctx.Err() == nil || a.workspacePreview.pending != nil {
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

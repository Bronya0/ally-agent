package desktop

import (
	"ally-dev/internal/documentpreview"
	"context"
	"testing"
)

func TestClosingDocumentPreviewReleasesDocument(t *testing.T) {
	root := t.TempDir()
	document := &documentpreview.Document{Paragraphs: []documentpreview.Paragraph{{Text: "正文"}}}
	file := &workspaceFile{Path: "report.docx", Document: document}
	a := &desktop{prefs: defaultPrefs(), tabs: []*conversation{{Workspace: root}}, workspaceFiles: map[string]*workspaceFile{workspaceKey(root): file}}
	a.releaseWorkspaceFile(root)
	if a.activeFile() != nil || file.Document != nil || file.PreviewPath != "" || file.PreviewHTML != "" {
		t.Fatal("closing preview retained document data")
	}
}

func TestClosingActiveFileClearsPlaybackIntent(t *testing.T) {
	root := t.TempDir()
	file := &workspaceFile{Path: "one.mp4"}
	a := &desktop{tabs: []*conversation{{Workspace: root}}, workspaceFiles: map[string]*workspaceFile{workspaceKey(root): file}}
	a.media.mode = mediaModeRandom
	a.workspaceMedia().playing, a.workspaceMedia().autoplay = true, true
	a.releaseWorkspaceFile(root)
	if len(a.mediaPlayback) != 0 || a.media.mode != mediaModeRandom {
		t.Fatal("closing a file retained play intent or erased the app's mode selection")
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

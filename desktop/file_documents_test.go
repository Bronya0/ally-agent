package desktop

import (
	"ally-dev/internal/documentpreview"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui/editor"
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

func TestMissingFileLoadPreservesCurrentEditorAndPreview(t *testing.T) {
	for _, path := range []string{"missing.txt", "missing.md", "missing.html", "missing.png", "missing.svg", "missing.pdf", "missing.docx"} {
		for _, retain := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/retain=%t", path, retain), func(t *testing.T) {
				root := t.TempDir()
				conv := &conversation{Workspace: root}
				a := &desktop{tabs: []*conversation{conv}, workspaceFiles: map[string]*workspaceFile{}}
				var old *workspaceFile
				if retain {
					buffer := editor.NewBuffer("original")
					buffer.Set("unsaved content")
					old = &workspaceFile{Path: "current.txt", Editor: &editor.State{Buffer: buffer}, PreviewPath: "current.png"}
					a.workspaceFiles[workspaceKey(root)] = old
				}
				_, err := os.Stat(filepath.Join(root, path))
				if err == nil {
					t.Fatal("test target unexpectedly exists")
				}
				a.handleFileLoadError(root, path, workspaceFileKindForPath(path), old, fmt.Errorf("load: %w", err))
				if a.activeFile() != old || !retain && len(a.workspaceFiles) != 0 {
					t.Fatal("missing file replaced the current editor or created an empty one")
				}
				if retain && (old.PreviewPath != "current.png" || old.Editor.Buffer.String() != "unsaved content" || !old.Editor.Buffer.Modified()) {
					t.Fatal("missing file load cleared existing preview or unsaved contents")
				}
				if len(a.notifications) != 1 || a.notifications[0].Type != "error" {
					t.Fatal("missing file did not produce a user-visible failure")
				}
			})
		}
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

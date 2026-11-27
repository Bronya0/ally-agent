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

func TestMediaLoadFailureOnlyClearsPendingAutoplay(t *testing.T) {
	for _, autoplay := range []bool{false, true} {
		t.Run(fmt.Sprintf("autoplay=%t", autoplay), func(t *testing.T) {
			root := t.TempDir()
			key := workspaceKey(root)
			state := &workspaceMediaState{playing: true, autoplay: autoplay}
			a := &desktop{mediaPlayback: map[string]*workspaceMediaState{key: state}}
			a.handleFileLoadError(root, "missing.mp4", workspaceFileKindForPath("missing.mp4"), nil, os.ErrNotExist)
			if autoplay && (state.playing || state.autoplay) {
				t.Fatal("failed playlist transition retained its automatic playback intent")
			}
			if !autoplay && !state.playing {
				t.Fatal("unrelated failed media open cleared the current playback state")
			}
		})
	}
}

func TestClosingInactiveMediaPreviewClearsPlaybackIntent(t *testing.T) {
	root := t.TempDir()
	video := &workspaceFile{Path: "clip.mp4", PreviewPath: "clip.mp4", Editor: emptyFileEditor()}
	text := &workspaceFile{Path: "notes.go", Editor: emptyFileEditor()}
	key := workspaceKey(root)
	state := &workspaceEditorState{
		Groups: map[int]*editorGroupState{
			0: {Tabs: []string{video.Path}, Active: video.Path},
			1: {Tabs: []string{text.Path}, Active: text.Path},
		},
		Layout:       &editorLayoutNode{Direction: splitRight, First: &editorLayoutNode{Group: 0}, Second: &editorLayoutNode{Group: 1}},
		ActiveGroup:  1,
		PreviewGroup: 0, PreviewFile: video.Path, PreviewGroupSet: true,
	}
	a := &desktop{
		tabs:                       []*conversation{{Workspace: root}},
		workspaceFiles:             map[string]*workspaceFile{key: text},
		editorDocumentsByWorkspace: map[string]map[string]*workspaceFile{key: {editorDocumentKey(video.Path): video, editorDocumentKey(text.Path): text}},
		editorStates:               map[string]*workspaceEditorState{key: state},
		mediaPlayback:              map[string]*workspaceMediaState{key: {playing: true, autoplay: true}},
	}
	preview := &fakeWorkspacePreview{}
	previewKey := workspacePreviewTestKey(root, video.Path)
	if err := a.workspacePreview.mount(previewKey, true, func() (workspacePreviewSurface, error) {
		return preview, nil
	}, func(workspacePreviewSurface) error { return nil }); err != nil {
		t.Fatal(err)
	}

	a.releaseWorkspaceDocument(root, video)
	if _, ok := a.mediaPlayback[key]; ok || preview.destroys != 1 {
		t.Fatal("closing the inactive media owner retained playback state or its preview")
	}
}

func TestClosingInactiveImageDoesNotReleaseOtherPreview(t *testing.T) {
	root := t.TempDir()
	current := &workspaceFile{Path: "current.jpg", PreviewPath: "current.jpg", Editor: emptyFileEditor()}
	other := &workspaceFile{Path: "other.jpg", PreviewPath: "other.jpg", Editor: emptyFileEditor()}
	key := workspaceKey(root)
	state := &workspaceEditorState{
		Groups: map[int]*editorGroupState{
			0: {Tabs: []string{current.Path}, Active: current.Path},
			1: {Tabs: []string{other.Path}, Active: other.Path},
		},
		Layout:       &editorLayoutNode{Direction: splitRight, First: &editorLayoutNode{Group: 0}, Second: &editorLayoutNode{Group: 1}},
		ActiveGroup:  1,
		PreviewGroup: 0, PreviewFile: current.Path, PreviewGroupSet: true,
	}
	a := &desktop{
		tabs:                       []*conversation{{Workspace: root}},
		workspaceFiles:             map[string]*workspaceFile{key: other},
		editorDocumentsByWorkspace: map[string]map[string]*workspaceFile{key: {editorDocumentKey(current.Path): current, editorDocumentKey(other.Path): other}},
		editorStates:               map[string]*workspaceEditorState{key: state},
		mediaPlayback:              map[string]*workspaceMediaState{key: {playing: true}},
	}
	preview := &fakeWorkspacePreview{body: "current image"}
	previewKey := workspacePreviewTestKey(root, current.Path)
	if err := a.workspacePreview.mount(previewKey, true, func() (workspacePreviewSurface, error) {
		return preview, nil
	}, func(workspacePreviewSurface) error { return nil }); err != nil {
		t.Fatal(err)
	}

	a.releaseWorkspaceDocument(root, other)
	if a.workspacePreview.mountedFor(previewKey) != preview || preview.destroys != 0 || a.mediaPlayback[key] == nil || !a.mediaPlayback[key].playing {
		t.Fatal("closing a non-owner image released the active preview or playback state")
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

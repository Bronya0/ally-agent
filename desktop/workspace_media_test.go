package desktop

import (
	"testing"

	backend "ally-dev/internal/app"
)

func TestMediaModeDefaultAndSelection(t *testing.T) {
	var state workspaceMediaState
	state.ensureMode()
	if state.mode != mediaModeNone {
		t.Fatalf("new app defaults to %q instead of no-repeat", state.mode)
	}
	state.mode = mediaModeRandom
	state.ensureMode()
	if state.mode != mediaModeRandom {
		t.Fatal("opening another preview reset the selected playback mode")
	}
	var restarted workspaceMediaState
	restarted.ensureMode()
	if restarted.mode != mediaModeNone {
		t.Fatal("fresh app inherited another instance's playback mode")
	}
}

func TestMediaPlaybackEndPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, mode                  string
		playing, autoplay           bool
		advance, wantPlay, wantAuto bool
	}{
		{"default stops", "", true, true, false, false, false},
		{"no-repeat stops", mediaModeNone, true, true, false, false, false},
		{"sequential continues", mediaModeSequential, true, false, true, true, true},
		{"random continues", mediaModeRandom, true, false, true, true, true},
		{"single stays in player", mediaModeSingle, true, false, false, true, false},
		{"paused sequential stays paused", mediaModeSequential, false, false, false, false, false},
		{"paused random stays paused", mediaModeRandom, false, false, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := workspaceMediaState{mode: tc.mode, playing: tc.playing, autoplay: tc.autoplay}
			advance := state.continueAfterEnd()
			if advance != tc.advance || state.playing != tc.wantPlay || state.autoplay != tc.wantAuto {
				t.Fatalf("advance=%v playing=%v autoplay=%v; want %v/%v/%v", advance, state.playing, state.autoplay, tc.advance, tc.wantPlay, tc.wantAuto)
			}
		})
	}
}

func TestMediaEventsBelongToTheirDocument(t *testing.T) {
	for _, action := range []string{"next", "previous", "ended", "play", "pause"} {
		title := mediaTitlePrefix + "current:" + action + ":1"
		if got, ok := workspaceMediaAction(title, "current"); !ok || got != action {
			t.Fatalf("current document event rejected: %q, %v", got, ok)
		}
		if _, ok := workspaceMediaAction(title, "replacement"); ok {
			t.Fatal("retired document event accepted by a replacement")
		}
	}
	for _, title := range []string{mediaTitlePrefix + "retired:ended:1", mediaTitlePrefix + "current:ended", mediaTitlePrefix + "current:unknown:1", "other:current:ended:1"} {
		if _, ok := workspaceMediaAction(title, "current"); ok {
			t.Fatalf("invalid event accepted: %q", title)
		}
	}
	if _, ok := workspaceMediaAction(mediaTitlePrefix+"current:ended:1", ""); ok {
		t.Fatal("cleared media binding accepted an event")
	}
}

func TestMediaNavigation(t *testing.T) {
	for _, tc := range []struct{ index, count, delta, want int }{
		{0, 3, 1, 1}, {2, 3, 1, 0}, {0, 3, -1, 2}, {0, 1, 1, 0}, {-1, 3, 1, -1}, {0, 0, 1, -1},
	} {
		if got := mediaNextIndex(tc.index, tc.count, "顺序循环", tc.delta); got != tc.want {
			t.Fatalf("%+v: got %d", tc, got)
		}
	}
	for count := 2; count < 20; count++ {
		for index := 0; index < count; index++ {
			got := mediaNextIndex(index, count, "随机循环", 1)
			if got < 0 || got >= count || got == index {
				t.Fatalf("random next %d/%d: %d", index, count, got)
			}
		}
	}
}

func TestInactiveImageEndEventAdvancesInOwnerGroup(t *testing.T) {
	root := t.TempDir()
	first := &workspaceFile{Path: "first.jpg", PreviewPath: "first.jpg", Editor: emptyFileEditor()}
	next := &workspaceFile{Path: "next.jpg", PreviewPath: "next.jpg", Editor: emptyFileEditor()}
	text := &workspaceFile{Path: "notes.go", Editor: emptyFileEditor()}
	key := workspaceKey(root)
	state := &workspaceEditorState{
		Groups: map[int]*editorGroupState{
			0: {Tabs: []string{first.Path}, Active: first.Path},
			1: {Tabs: []string{text.Path}, Active: text.Path},
		},
		Layout:      &editorLayoutNode{Direction: splitRight, First: &editorLayoutNode{Group: 0}, Second: &editorLayoutNode{Group: 1}},
		ActiveGroup: 1, PreviewGroup: 0, PreviewFile: first.Path, PreviewGroupSet: true,
	}
	a := &desktop{
		tabs:                       []*conversation{{Workspace: root}},
		page:                       "chat",
		explorer:                   true,
		workspaceFiles:             map[string]*workspaceFile{key: text},
		editorDocumentsByWorkspace: map[string]map[string]*workspaceFile{key: {editorDocumentKey(first.Path): first, editorDocumentKey(next.Path): next, editorDocumentKey(text.Path): text}},
		editorStates:               map[string]*workspaceEditorState{key: state},
		files:                      []backend.FileEntry{{Path: first.Path}, {Path: next.Path}},
		media:                      workspaceMediaState{mode: mediaModeSequential},
		mediaPlayback:              map[string]*workspaceMediaState{key: {mode: mediaModeSequential, playing: true}},
	}
	surface, _ := queuedPreview(t, &fakePreviewBrowser{})
	surface.mediaToken = "image-token"
	previewKey := workspacePreviewTestKey(root, first.Path)
	a.workspacePreview.mounted = make(map[string]*mountedWorkspacePreview)
	a.workspacePreview.mounted[workspaceKey(root)] = &mountedWorkspacePreview{key: previewKey, surface: surface}

	a.handleWorkspaceMediaAction(surface, first, previewKey, "image-token", "ended")
	if state.ActiveGroup != 1 || state.Groups[1].Active != text.Path || state.Groups[0].Active != next.Path {
		t.Fatalf("inactive image event disturbed the other group: active=%d groups=%+v", state.ActiveGroup, state.Groups)
	}
	if state.PreviewGroup != 0 || state.PreviewFile != next.Path || !a.mediaPlayback[key].autoplay {
		t.Fatal("inactive image event did not continue playback in the owning group")
	}
}

func TestInactiveMediaPlaylistAdvancesInItsOwnGroup(t *testing.T) {
	for _, ext := range []string{"mp4", "jpg"} {
		t.Run(ext, func(t *testing.T) {
			root := t.TempDir()
			first := &workspaceFile{Path: "first." + ext, PreviewPath: "first." + ext, Editor: emptyFileEditor()}
			next := &workspaceFile{Path: "next." + ext, PreviewPath: "next." + ext, Editor: emptyFileEditor()}
			text := &workspaceFile{Path: "notes.go", Editor: emptyFileEditor()}
			key := workspaceKey(root)
			state := &workspaceEditorState{
				Groups: map[int]*editorGroupState{
					0: {Tabs: []string{first.Path}, Active: first.Path},
					1: {Tabs: []string{text.Path}, Active: text.Path},
				},
				Layout:      &editorLayoutNode{Direction: splitRight, First: &editorLayoutNode{Group: 0}, Second: &editorLayoutNode{Group: 1}},
				ActiveGroup: 1, PreviewGroup: 0, PreviewFile: first.Path, PreviewGroupSet: true,
			}
			a := &desktop{
				tabs:                       []*conversation{{Workspace: root}},
				page:                       "chat",
				explorer:                   true,
				workspaceFiles:             map[string]*workspaceFile{key: text},
				editorDocumentsByWorkspace: map[string]map[string]*workspaceFile{key: {editorDocumentKey(first.Path): first, editorDocumentKey(next.Path): next, editorDocumentKey(text.Path): text}},
				editorStates:               map[string]*workspaceEditorState{key: state},
				files:                      []backend.FileEntry{{Path: first.Path}, {Path: next.Path}},
				media:                      workspaceMediaState{mode: mediaModeSequential},
				mediaPlayback:              map[string]*workspaceMediaState{key: {mode: mediaModeSequential, playing: true}},
			}
			preview := &fakeWorkspacePreview{body: "first frame"}
			previewKey := workspacePreviewTestKey(root, first.Path)
			if err := a.workspacePreview.mount(previewKey, true, func() (workspacePreviewSurface, error) {
				return preview, nil
			}, func(workspacePreviewSurface) error { return nil }); err != nil {
				t.Fatal(err)
			}

			if !a.stepWorkspaceMedia(first, 1, true) {
				t.Fatal("visible inactive-group media did not advance")
			}
			if state.ActiveGroup != 1 || state.Groups[1].Active != text.Path || state.Groups[0].Active != next.Path {
				t.Fatalf("playlist transition disturbed the other group: active=%d groups=%+v", state.ActiveGroup, state.Groups)
			}
			if !state.PreviewGroupSet || state.PreviewGroup != 0 || state.PreviewFile != next.Path {
				t.Fatalf("playlist transition lost media-group ownership: %+v", state)
			}
			if ext == "jpg" && a.workspacePreview.retainedImageFor(workspacePreviewTestKey(root, next.Path)) != preview {
				t.Fatal("image transition did not retain the previous frame until the next loads")
			}
		})
	}
}

package desktop

import (
	"errors"
	"testing"
)

func TestExplorerPathChangeClosesOnlyOriginatingWorkspaceFile(t *testing.T) {
	for _, path := range []string{"video.mp4", "."} {
		t.Run(path, func(t *testing.T) {
			a, views := retainedPreviewDesktop(t)
			root := a.tabs[0].Workspace
			a.active = 1 // The operation started in A and completed after switching to B.
			other := a.activeFile()
			_, pending := a.workspacePreview.beginLoad(a.current().Workspace, "next.jpg")
			a.explorerPathChanged(root, path)
			if a.workspaceFiles[workspaceKey(root)] != nil || views[0].destroys != 1 {
				t.Fatal("renamed/deleted path left its document and preview open")
			}
			if a.activeFile() != other || views[1].destroys != 0 || a.workspacePreview.pending != pending {
				t.Fatal("completion closed or cancelled the other workspace's file")
			}
		})
	}
}

func TestWorkspacePreviewRetainsEachWorkspaceAndReusesMedia(t *testing.T) {
	roots := []string{t.TempDir(), t.TempDir()}
	var life workspacePreviewLifecycle
	var created []*fakeWorkspacePreview
	create := func() (workspacePreviewSurface, error) {
		view := &fakeWorkspacePreview{}
		created = append(created, view)
		return view, nil
	}
	paths := []string{"one.mp4", "two.jpg"}
	for i, root := range roots {
		_, pending := life.beginLoad(root, paths[i])
		life.finishLoad(pending)
		if err := life.mount(workspacePreviewTestKey(root, paths[i]), true, create, func(p workspacePreviewSurface) error {
			p.LoadHTML(paths[i])
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 6; i++ {
		index := i % 2
		key := workspacePreviewTestKey(roots[index], paths[index])
		if err := life.mount(key, true, create, func(workspacePreviewSurface) error {
			t.Fatal("returning to a workspace reloaded its page")
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if life.mountedFor(key) != created[index] || created[index].body != paths[index] {
			t.Fatal("workspace lost its own surface or document")
		}
	}
	_, pending := life.beginLoad(roots[0], "replacement.jpg")
	life.finishLoad(pending)
	if err := life.mount(workspacePreviewTestKey(roots[0], "replacement.jpg"), true, create, func(p workspacePreviewSurface) error {
		p.LoadHTML("replacement.jpg")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(created) != 2 || created[0].resets != 1 || created[0].destroys != 0 || created[1].resets != 0 || created[1].destroys != 0 || created[1].body != paths[1] {
		t.Fatal("changing media churned views or affected another workspace")
	}
	life.releaseWorkspace(roots[0])
	if created[0].destroys != 1 || created[1].destroys != 0 || len(life.mounted) != 1 {
		t.Fatal("closing one preview did not isolate its resources")
	}
	life.release()
	if created[0].destroys != 1 || created[1].destroys != 1 || len(life.mounted) != 0 {
		t.Fatal("shutdown did not release all workspace previews exactly once")
	}
}

func retainedPreviewDesktop(t *testing.T) (*desktop, []*fakeWorkspacePreview) {
	t.Helper()
	roots := []string{t.TempDir(), t.TempDir()}
	a := &desktop{ready: true, page: "chat", explorer: true, workspaceFiles: make(map[string]*workspaceFile)}
	views := make([]*fakeWorkspacePreview, 2)
	for i, root := range roots {
		a.tabs = append(a.tabs, &conversation{ID: root, Workspace: root})
		file := &workspaceFile{Path: "video.mp4", Editor: emptyFileEditor()}
		a.workspaceFiles[workspaceKey(root)] = file
		views[i] = &fakeWorkspacePreview{body: root}
		if err := a.workspacePreview.mount(workspacePreviewTestKey(root, file.Path), true,
			func() (workspacePreviewSurface, error) { return views[i], nil },
			func(workspacePreviewSurface) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(a.releaseAllFilePreviews)
	return a, views
}

func TestWorkspacePreviewNavigationPreservesPages(t *testing.T) {
	a, views := retainedPreviewDesktop(t)
	for _, index := range []int{1, 0, 1, 0} {
		a.activateTab(index)
		a.prepareFileTree(a.current().Workspace)
		a.changePage("plugin:test")
		a.changePage("chat")
		a.mainWindowHidden = true
		a.releaseHiddenWindowResources()
		a.mainWindowShown()
		a.hideFilePreview()
		for i, view := range views {
			if view.resets != 0 || view.destroys != 0 || view.body != a.tabs[i].Workspace {
				t.Fatal("workspace/page/hide transition retired a preview")
			}
		}
	}
	a.releaseFilePreview()
	if views[0].destroys != 1 || views[1].destroys != 0 {
		t.Fatal("closing active preview affected another workspace")
	}
}

func TestWorkspacePreviewExplorerCloseReleasesAllPages(t *testing.T) {
	a, views := retainedPreviewDesktop(t)
	a.setExplorerOpen(false)
	if views[0].destroys != 1 || views[1].destroys != 1 || len(a.workspacePreview.mounted) != 0 {
		t.Fatal("closing the explorer retained workspace previews")
	}
}

func TestWorkspacePreviewTabCloseReleasesOnlyLastWorkspaceTab(t *testing.T) {
	for _, index := range []int{0, 1} {
		t.Run([]string{"active", "inactive"}[index], func(t *testing.T) {
			a, views := retainedPreviewDesktop(t)
			a.page = "plugin:test" // Avoid unrelated Git refresh in this no-window test.
			a.closeWorkspaceTab(index)
			if views[index].destroys != 1 || views[1-index].destroys != 0 || len(a.workspacePreview.mounted) != 1 {
				t.Fatal("tab close released the wrong workspace preview")
			}
		})
	}
	a, views := retainedPreviewDesktop(t)
	a.page = "plugin:test"
	a.tabs = append(a.tabs, &conversation{ID: "same-workspace", Workspace: a.tabs[0].Workspace})
	a.closeWorkspaceTab(0)
	if views[0].destroys != 0 || len(a.workspacePreview.mounted) != 2 {
		t.Fatal("closing a sibling tab retired a workspace still open")
	}
	a.closeWorkspaceTab(1)
	if views[0].destroys != 1 || views[1].destroys != 0 {
		t.Fatal("last workspace tab did not release its preview")
	}
}

func TestWorkspaceMediaEventsStayWithRetainedWorkspace(t *testing.T) {
	a, _ := retainedPreviewDesktop(t)
	root := a.tabs[0].Workspace
	file := a.workspaceFiles[workspaceKey(root)]
	browser := &fakePreviewBrowser{}
	surface, _ := queuedPreview(t, browser)
	surface.mediaToken = "first"
	key := workspacePreviewTestKey(root, file.Path)
	a.active = 1
	other := a.workspaceMedia()
	a.handleWorkspaceMediaAction(surface, file, key, "first", "play")
	first := a.mediaForWorkspace(root)
	if !first.playing || other.playing {
		t.Fatal("background media event changed the active workspace")
	}
	a.handleWorkspaceMediaAction(surface, file, key, "retired", "pause")
	if !first.playing {
		t.Fatal("retired document token changed playback")
	}
	a.active = 0
	if !a.workspaceMedia().playing {
		t.Fatal("returning to workspace lost playback state")
	}
	a.active = 1
	a.handleWorkspaceMediaAction(surface, file, key, "first", "ended")
	if first.playing || other.playing {
		t.Fatal("background end event did not update its own workspace")
	}
	a.releaseWorkspaceFile(root)
	a.handleWorkspaceMediaAction(surface, file, key, "first", "play")
	if a.mediaPlayback[workspaceKey(root)] != nil {
		t.Fatal("late event revived playback after preview closed")
	}
	surface.Destroy()
}

func TestRetainedWorkspacePreviewFailureDoesNotAffectActiveWorkspace(t *testing.T) {
	a, _ := retainedPreviewDesktop(t)
	root := a.tabs[0].Workspace
	file := a.workspaceFiles[workspaceKey(root)]
	surface, _ := queuedPreview(t, &fakePreviewBrowser{})
	key := workspacePreviewTestKey(root, file.Path)
	a.workspacePreview.releaseMountedWorkspace(root)
	a.workspacePreview.mounted[workspaceKey(root)] = &mountedWorkspacePreview{key: key, surface: surface}
	a.watchWorkspacePreviewFailure(surface, file, key)
	first := a.mediaForWorkspace(root)
	first.playing = true
	a.active = 1
	other := a.workspaceMedia()
	other.playing = true
	surface.failContent(surface.state.generation, errors.New("background renderer failed"), true)
	if file.PreviewError == "" || first.playing || !other.playing || a.activeFile().PreviewError != "" {
		t.Fatal("background preview failure was lost or changed the active workspace")
	}
}

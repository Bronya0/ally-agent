package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui/editor"
)

func TestReselectOpenFileCancelsPendingSwitch(t *testing.T) {
	root := t.TempDir()
	file := &workspaceFile{Path: "current.go", Editor: &editor.State{Buffer: editor.NewBuffer("package current")}}
	a := &desktop{tabs: []*conversation{{Workspace: root}}, workspaceFiles: map[string]*workspaceFile{workspaceKey(root): file}}
	ctx, _ := a.workspacePreview.beginLoad(root, "other.go")
	a.openFile("current.go")
	if ctx.Err() == nil || a.workspacePreview.pending != nil || a.activeFile() != file {
		t.Fatal("reselecting the open file must cancel the pending switch")
	}
}

func TestInitialEditorAssistRejectsStaleResults(t *testing.T) {
	root := t.TempDir()
	buffer := editor.NewBuffer("package first")
	file := &workspaceFile{Path: "first.go", Editor: &editor.State{Buffer: buffer}}
	a := &desktop{workspaceFiles: map[string]*workspaceFile{workspaceKey(root): file}}
	index := buildEditorAssistIndex(file.Path, buffer.String(), buffer.Version())
	a.installEditorAssist(root, file, buffer, index)
	if file.Assist != index {
		t.Fatal("current index was not installed")
	}
	buffer.Set("package changed")
	newer := buildEditorAssistIndex(file.Path, buffer.String(), buffer.Version())
	file.Assist = newer
	a.installEditorAssist(root, file, buffer, index)
	if file.Assist != newer {
		t.Fatal("older initial index replaced a newer one")
	}
	file.Assist = nil
	a.installEditorAssist(root, file, buffer, index)
	if file.Assist != index {
		t.Fatal("initial completions disappeared after editing during indexing")
	}
	file.Assist = nil
	replacement := &workspaceFile{Path: "second.go", Editor: &editor.State{Buffer: editor.NewBuffer("package second")}}
	a.workspaceFiles[workspaceKey(root)] = replacement
	a.installEditorAssist(root, file, buffer, buildEditorAssistIndex(file.Path, buffer.String(), buffer.Version()))
	if file.Assist != nil || replacement.Assist != nil {
		t.Fatal("index for a closed file was installed")
	}
}

package desktop

import (
	backend "ally-dev/internal/app"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
)

func TestExplorerCopyFileClipboard(t *testing.T) {
	root := t.TempDir()
	a := &desktop{
		tabs:  []*conversation{{Workspace: root}},
		files: []backend.FileEntry{{Name: "folder", Path: "folder", Dir: true}},
		fileChildren: map[string][]backend.FileEntry{
			"folder": {{Name: "中文 file.txt", Path: "folder/中文 file.txt"}},
		},
	}
	tt := ui.NewTester(func(c *ui.Context) { ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() { a.fileTree(c) }) }, 300, 500)
	if err := tt.Click("folder"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyDown)
	tt.Key(ui.Cmd, ui.KeyC)
	want := filepath.Join(root, "folder", "中文 file.txt")
	assertFiles := func() {
		t.Helper()
		files, err := tt.ClipboardData().Files()
		if err != nil || len(files) != 1 || files[0] != want {
			t.Fatalf("native clipboard files = %v, %v; want %s", files, err, want)
		}
	}
	assertFiles()
	tt.SetClipboard("old text")
	if err := tt.RightClick("中文 file.txt"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("复制文件"); err != nil {
		t.Fatal(err)
	}
	assertFiles()
	if err := tt.RightClick("folder"); err != nil {
		t.Fatal(err)
	}
	if _, exists := tt.Find("复制文件"); exists {
		t.Fatal("directory exposes file-copy action")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Key(0, ui.KeyLeft)
	tt.SetClipboard("keep directory clipboard unchanged")
	tt.Key(ui.Cmd, ui.KeyC)
	if tt.Clipboard() != "keep directory clipboard unchanged" {
		t.Fatal("Ctrl+C copied a directory")
	}
}

func TestExplorerPasteFileDestinations(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("ALLY_DATA_DIR", home)
	core := backend.NewApp()
	if _, err := core.GetConfig(); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"blank-menu", "folder-menu", "blank-keyboard", "folder-keyboard"} {
		t.Run(action, func(t *testing.T) {
			root, external := t.TempDir(), t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "target"), 0700); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(external, "中文 file.txt")
			if err := os.WriteFile(source, []byte("clipboard payload"), 0600); err != nil {
				t.Fatal(err)
			}
			directory := filepath.Join(external, "ignored-directory")
			if err := os.Mkdir(directory, 0700); err != nil {
				t.Fatal(err)
			}
			a := &desktop{
				core: core, tabs: []*conversation{{Workspace: root}},
				files: []backend.FileEntry{{Name: "target", Path: "target", Dir: true}},
			}
			tt := ui.NewTester(func(c *ui.Context) { ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() { a.fileTree(c) }) }, 300, 500)
			data, err := transfer.FileData(source, directory)
			if err != nil {
				t.Fatal(err)
			}
			tt.SetClipboardData(data)
			target := ""
			switch action {
			case "blank-menu":
				tt.RightClickAt(100, 400)
				if err := tt.Click("粘贴文件"); err != nil {
					t.Fatal(err)
				}
			case "folder-menu":
				target = "target"
				if err := tt.RightClick("target"); err != nil {
					t.Fatal(err)
				}
				if err := tt.Click("粘贴文件"); err != nil {
					t.Fatal(err)
				}
			case "blank-keyboard":
				tt.ClickAt(100, 400)
				tt.Key(ui.Cmd, ui.KeyV)
			case "folder-keyboard":
				target = "target"
				// Keyboard navigation focuses the folder without starting a read.
				tt.ClickAt(100, 400)
				tt.Key(0, ui.KeyTab)
				if !tt.Focused("target") {
					t.Fatal("folder did not receive focus")
				}
				tt.Key(ui.Cmd, ui.KeyV)
			}
			want := filepath.Join(root, target, filepath.Base(source))
			deadline := time.Now().Add(3 * time.Second)
			for {
				data, err := os.ReadFile(want)
				if err == nil && string(data) == "clipboard payload" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("paste did not reach %s: %v", want, err)
				}
				time.Sleep(5 * time.Millisecond)
			}
			// Drain the serialized operation before inspecting its final effects.
			if _, err := core.CopyFilesIntoWorkspace(backend.CopyFilesIntoWorkspaceRequest{Workspace: root}); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(root, target, filepath.Base(directory))); !os.IsNotExist(err) {
				t.Fatal("clipboard paste copied a directory")
			}
		})
	}
}

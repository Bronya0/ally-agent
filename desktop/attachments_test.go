// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	backend "ally-dev/internal/app"
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
)

func TestPrepareAttachment(t *testing.T) {
	dir := t.TempDir()
	textPath := filepath.Join(dir, "example.go")
	if err := os.WriteFile(textPath, []byte("package example\n"), 0600); err != nil {
		t.Fatal(err)
	}
	text := prepareAttachment(textPath).input
	if text.Kind != "text" || text.Text != "package example\n" || text.Error != "" {
		t.Fatalf("text: %+v", text)
	}
	largePath := filepath.Join(dir, "large.txt")
	if err := os.WriteFile(largePath, bytes.Repeat([]byte("x"), maxAttachmentTextBytes+1), 0600); err != nil {
		t.Fatal(err)
	}
	large := prepareAttachment(largePath).input
	if !large.Truncated || large.Error == "" || large.Text != "" {
		t.Fatalf("oversize text: %+v", large)
	}
	pngPath := filepath.Join(dir, "image.png")
	f, err := os.Create(pngPath)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(0, 0, color.White)
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	f.Close()
	imageAtt := prepareAttachment(pngPath)
	if imageAtt.input.Kind != "image" || !strings.HasPrefix(imageAtt.input.DataURL, "data:image/png;base64,") || imageAtt.preview == nil {
		t.Fatalf("image: %+v", imageAtt.input)
	}
	binaryPath := filepath.Join(dir, "other.bin")
	if err := os.WriteFile(binaryPath, []byte{0, 1}, 0600); err != nil {
		t.Fatal(err)
	}
	if got := prepareAttachment(binaryPath).input; got.Error == "" {
		t.Fatal("unsupported attachment was silently accepted")
	}
}
func TestAttachmentDraftAndImageEvent(t *testing.T) {
	a := demoDesktop()
	tab := a.current()
	tab.PendingAttachments = []backend.AttachmentInput{{ID: "a", Name: "example.txt", Kind: "text", Text: "hello"}}
	tt := ui.NewTester(a.view, 1280, 850)
	if !tt.HasText("example.txt") {
		t.Fatal("pending attachment missing")
	}
	if err := tt.Click("移除 example.txt"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(tab.PendingAttachments) != 0 {
		t.Fatal("attachment was not removed")
	}
	tab.Messages = append(tab.Messages, map[string]any{"role": "user", "content": "x", "attachments": []any{map[string]any{"id": "a", "kind": "image", "name": "x.png"}}})
	a.applyEvent("run:attachment", map[string]any{"sessionId": tab.ID, "id": "a", "imageRef": "image:example", "url": "/generated-images/example"})
	att := messageAttachmentList(tab.Messages[len(tab.Messages)-1])[0]
	if str(att, "imageRef") != "image:example" {
		t.Fatal("image reference was not recorded")
	}
	a.applyEvent("run:image", map[string]any{"sessionId": tab.ID, "id": "generated-1", "mimeType": "image/png"})
	tt.Frame()
	if !tt.HasText("生成的图片") {
		t.Fatal("generated image card missing")
	}
}

func TestSnapshotDropsImagePayloadOnlyAfterReference(t *testing.T) {
	tab := &conversation{Messages: []map[string]any{{"role": "user", "attachments": []any{map[string]any{"id": "saved", "imageRef": "image:ref", "dataUrl": "large"}, map[string]any{"id": "pending", "dataUrl": "still-needed"}}}}}
	snap := snapshot(tab)
	items := messageAttachmentList(snap.Messages[0])
	if str(items[0], "dataUrl") != "" || str(items[1], "dataUrl") != "still-needed" {
		t.Fatal("snapshot did not preserve only the unsaved payload")
	}
	if str(messageAttachmentList(tab.Messages[0])[0], "dataUrl") != "large" {
		t.Fatal("snapshot mutated the live attachment")
	}
}

func TestTextPasteStillWorks(t *testing.T) {
	a := demoDesktop()
	tab := a.current()
	tt := ui.NewTester(a.view, 1280, 850)
	tt.SetClipboardData(transfer.New(transfer.NewItem(transfer.Bytes(transfer.Text, []byte("粘贴文本")))))
	if err := tt.Click("消息输入框"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyV)
	tt.Frame()
	if !strings.Contains(tab.Draft, "粘贴文本") {
		t.Fatalf("text paste lost: %q", tab.Draft)
	}
}

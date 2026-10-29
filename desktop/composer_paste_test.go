// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
)

func clipboardPNG(t *testing.T) transfer.Data {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for x := 0; x < 8; x++ {
		for y := 0; y < 8; y++ {
			img.Set(x, y, color.RGBA{R: 200, G: 30, B: 30, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return transfer.New(transfer.NewItem(transfer.Bytes(transfer.PNG, buf.Bytes())))
}

func pasteInto(t *testing.T, conv *conversation) *ui.Tester {
	t.Helper()
	a := &desktop{}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		ui.Column(c).Padding(12).Children(func() { a.composer(c, conv) })
	}, 700, 400)
	box, ok := tt.Find("消息输入框")
	if !ok {
		t.Fatalf("missing composer input: %v", tt.Texts())
	}
	tt.ClickAt(box.X+box.W/2, box.Y+box.H/2)
	tt.Frame()
	return tt
}

// A run does not close the composer, so pasting has to work while it goes on:
// typing already did.
func TestComposerPastesTextWhileBusy(t *testing.T) {
	conv := &conversation{ID: "paste-text", Model: "provider / model", Busy: true}
	tt := pasteInto(t, conv)
	tt.SetClipboard("粘贴的内容")
	tt.Key(ui.Cmd, ui.KeyV)
	tt.Frame()
	if conv.Draft != "粘贴的内容" {
		t.Fatalf("paste during a run left the draft at %q", conv.Draft)
	}
}

// A pasted screenshot is an attachment of the next message, which a run does
// not touch: taking the paste has to start, not be dropped without a word.
func TestComposerPastesImageWhileBusy(t *testing.T) {
	conv := &conversation{ID: "paste-image", Model: "provider / model", Busy: true}
	tt := pasteInto(t, conv)
	tt.SetClipboardData(clipboardPNG(t))
	tt.Key(ui.Cmd, ui.KeyV)
	tt.Frame()
	if !conv.AttachmentLoading {
		t.Fatal("an image pasted during a run was dropped")
	}
}

// Only one attachment is prepared at a time, and none once the window closes.
func TestCanAddAttachmentsWaitsForTheLoader(t *testing.T) {
	busy := &conversation{Busy: true}
	if !canAddAttachments(busy, false) {
		t.Fatal("a run blocked a pending attachment")
	}
	loading := &conversation{AttachmentLoading: true}
	if canAddAttachments(loading, false) {
		t.Fatal("a second attachment started while one was being prepared")
	}
	if canAddAttachments(busy, true) {
		t.Fatal("a closing window took an attachment")
	}
	if canAddAttachments(nil, false) {
		t.Fatal("a nil conversation took an attachment")
	}
}

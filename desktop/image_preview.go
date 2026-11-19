// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"fmt"

	"ally-dev/locales"

	"github.com/egoist/mygo/ui"
	"golang.org/x/image/draw"
)

const (
	// imagePreviewMargin keeps the panel off the window's edges; the rest of the
	// window is the picture's, which is what the transcript's tile cannot give.
	imagePreviewMargin float32 = 24
	// imagePreviewHeader is the row above the picture: name, pixel size, close.
	imagePreviewHeader float32 = 28
	// imagePreviewMaxEdge bounds the preview's retained RGBA pixels.
	imagePreviewMaxEdge = 2400
)

// imagePreview is the full-window viewer a message picture opens into. One
// exists at a time. It never owns the transcript's copy: the panel draws the
// conversation's cached bitmap until this preview's own copy lands, and that
// copy alone is what closing releases.
type imagePreview struct {
	Open bool
	// conversation and id locate the picture; the transcript's copy paints the
	// panel until the copy below arrives.
	conversation *conversation
	id, name     string
	// bitmap is the copy this preview decoded for itself. Nothing else draws it,
	// so closing releases its pixels.
	bitmap *ui.Bitmap
	cancel chan struct{}
	// sizeLabel is the pixel size shown beside the name, rebuilt only when it
	// changes: the panel renders it every frame.
	sizeLabel     string
	width, height int
	err           string
}

// setSize records the size of the copy the panel draws, which is the transcript's
// until the preview's own has decoded.
func (p *imagePreview) setSize(w, h int) {
	if p.width == w && p.height == h && p.sizeLabel != "" {
		return
	}
	p.width, p.height = w, h
	p.sizeLabel = fmt.Sprintf("%s %d×%d", locales.Text("预览尺寸"), w, h)
}

// openImagePreview shows a message picture over the window.
func (a *desktop) openImagePreview(t *conversation, att map[string]any) {
	if a.win == nil || t == nil || !attachmentHasImage(att) {
		return
	}
	a.closeImagePreview()
	p := &imagePreview{Open: true, conversation: t, id: str(att, "id"), name: str(att, "name"), cancel: make(chan struct{})}
	if p.name == "" {
		p.name = locales.Text("图片")
	}
	// Reuse a cached thumbnail while the preview decodes its own copy.
	if bm := t.AttachmentImages[p.id]; bm != nil {
		p.setSize(bm.Size())
	}
	a.imagePreview = p
	a.decodePreviewCopy(p, str(att, "imageRef"), str(att, "dataUrl"))
}

// decodePreviewCopy decodes the copy the panel shows, at up to
// imagePreviewMaxEdge. It never compares with the transcript's bitmap to decide
// whether it is worth decoding: that copy is whatever the cache holds, as small
// as the composer's 256px chip, so its size says nothing about the detail the
// source still has. One decode per preview, and closing releases it.
func (a *desktop) decodePreviewCopy(p *imagePreview, ref, dataURL string) {
	if a.core == nil || (ref == "" && dataURL == "") {
		return
	}
	if a.imagePreviewDecodeSlot == nil {
		a.imagePreviewDecodeSlot = make(chan struct{}, 1)
	}
	slot, cancel := a.imagePreviewDecodeSlot, p.cancel
	win := a.win
	go func() {
		if !acquireImagePreviewDecode(slot, cancel) {
			return
		}
		bitmap, err := a.decodeImagePreviewBitmap(ref, dataURL, imagePreviewMaxEdge)
		select {
		case <-cancel:
			<-slot
			return
		default:
		}
		win.Update(func() {
			defer func() { <-slot }()
			// A closed or replaced preview must not keep the decoded bitmap.
			if a.closing || a.imagePreview != p || !p.Open {
				return
			}
			if err != nil {
				p.err = err.Error()
				return
			}
			p.err = ""
			p.bitmap = bitmap
			p.setSize(bitmap.Size())
		})
	}()
}

func (a *desktop) decodeImagePreviewBitmap(ref, dataURL string, limit int) (*ui.Bitmap, error) {
	img, err := a.decodeAttachmentImage(ref, dataURL)
	if err != nil {
		return nil, err
	}
	return ui.NewBitmap(resizeImageWithScaler(img, limit, draw.ApproxBiLinear)), nil
}

// A cancelled waiter must not keep a slot, even when cancellation races with
// the preceding decode releasing it.
func acquireImagePreviewDecode(slot chan struct{}, cancel <-chan struct{}) bool {
	select {
	case slot <- struct{}{}:
	case <-cancel:
		return false
	}
	select {
	case <-cancel:
		<-slot
		return false
	default:
		return true
	}
}

// closeImagePreview drops the viewer and everything it decoded for itself. The
// transcript's own bitmaps stay: the message still draws them.
func (a *desktop) closeImagePreview() {
	p := a.imagePreview
	if p == nil {
		return
	}
	a.imagePreview = nil
	p.Open = false
	if p.cancel != nil {
		close(p.cancel)
		p.cancel = nil
	}
	// MyGo retains the painted scene for the exit transition.
	p.bitmap = nil
}

// imagePreviewDialog draws the viewer. It closes itself when the picture it
// shows is no longer on screen, so a hidden window or another conversation never
// keeps a full-size copy resident.
func (a *desktop) imagePreviewDialog(c *ui.Context) {
	p := a.imagePreview
	if p == nil {
		return
	}
	if a.mainWindowHidden || a.page != "chat" || a.current() != p.conversation {
		a.closeImagePreview()
		return
	}
	bitmap := p.bitmap
	if bitmap == nil && p.conversation != nil {
		bitmap = p.conversation.AttachmentImages[p.id]
	}
	if bitmap != nil {
		p.setSize(bitmap.Size())
	}
	w, h := c.Size()
	panelW := max(240, w-imagePreviewMargin*2)
	panelH := max(180, h-imagePreviewMargin*2)
	const padding float32 = 12
	dialog := ui.DialogBase(c, &p.Open, func(backdrop, panel ui.Element) {
		modalBackdrop(backdrop)
		panel.Width(panelW).Height(panelH).MaxWidth(max(240, w-16)).MaxHeight(max(180, h-16)).
			Padding(padding).Gap(gapField).Radius(radiusCard).Background(c.Theme().Surface).
			Border(1, c.Theme().Border).Shadow(0, 10, 30, 0, ui.RGBA(0, 0, 0, 0.25)).
			// Fade only: a scale transition redraws a full-window picture every
			// frame of it, and the panel is already as large as it gets.
			Transition(ui.ElementTransition{Duration: modalTransition().Duration, Enter: &ui.Motion{}, Exit: &ui.Motion{}})
		ui.Row(c).Shrink(0).Height(imagePreviewHeader).Gap(gapField).AlignItems(ui.Center).Children(func() {
			ui.Text(c, p.name).FontSize(fsBody).TextColor(c.Theme().Text).SingleLine().MinWidth(0).Grow(1)
			if p.width > 0 && p.height > 0 {
				fieldNote(c, p.sizeLabel, a.secondaryInk())
			}
			iconAction(c, "image-preview-close", locales.Text("关闭"), "close", 24, func() { a.closeImagePreview() })
		})
		ui.Box(c).Grow(1).FillWidth().MinHeight(0).Center().Children(func() {
			switch {
			case bitmap != nil:
				// At most the picture's own pixels. Enlarging a small screenshot to
				// the window is what read as blurry, and no copy carries more detail
				// than the one decoded here.
				dw, dh := fitBox(float32(p.width), float32(p.height), panelW-2*padding, panelH-2*padding-imagePreviewHeader-gapField)
				ui.Image(c, bitmap).Size(dw, dh).Fit(ui.Contain)
			case p.err != "":
				fieldNote(c, p.err, c.Theme().Danger)
			default:
				fieldNote(c, "Loading image…", a.secondaryInk())
			}
		})
	})
	if dialog.Dismissed() {
		a.closeImagePreview()
	}
}

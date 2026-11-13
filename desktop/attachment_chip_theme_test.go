package desktop

import (
	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
	"image"
	"image/color"
	"testing"
)

func TestPendingAttachmentRemoveClearsChips(t *testing.T) {
	for _, appearance := range []string{"light", "dark"} {
		a := &desktop{}
		a.prefs.Appearance = appearance
		bm := ui.NewBitmap(func() image.Image {
			im := image.NewRGBA(image.Rect(0, 0, 64, 8))
			for x := 0; x < 64; x++ {
				for y := 0; y < 8; y++ {
					im.Set(x, y, color.White)
				}
			}
			return im
		}())
		defer bm.ReleasePixels()
		conv := &conversation{PendingAttachments: []backend.AttachmentInput{{ID: "image", Name: "截图.png", Kind: "image"}}, PendingAttachmentImages: map[string]*ui.Bitmap{"image": bm}}
		tt := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(a.theme())
			ui.Column(c).Fill().Padding(10).Background(c.Theme().Background).Children(func() { a.pendingAttachmentRow(c, conv) })
		}, 400, 120)
		if _, ok := tt.Find("待发送附件 截图.png"); !ok {
			t.Fatal("missing attachment")
		}
		if err := tt.Click("移除 截图.png"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		if len(conv.PendingAttachments) != 0 || len(conv.PendingAttachmentImages) != 0 {
			t.Fatal("remove did not clear attachment")
		}
	}
}

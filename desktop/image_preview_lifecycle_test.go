package desktop

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"

	backend "ally-dev/internal/app"
)

func TestImageDecodeBudgetAccountsFor16BitPixels(t *testing.T) {
	cfg := image.Config{Width: 4096, Height: 4096, ColorModel: color.NRGBA64Model}
	if err := oversizedImageError(cfg, "png"); err == nil {
		t.Fatal("accepted 128 MiB of pixels above the decode budget")
	}
	cfg.ColorModel = color.NRGBAModel
	if err := oversizedImageError(cfg, "png"); err != nil {
		t.Fatalf("rejected 64 MiB of pixels within the decode budget: %v", err)
	}
}

func TestImagePreviewDecodeBoundsAndInvalidInput(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 80, 40))); err != nil {
		t.Fatal(err)
	}
	a := &desktop{core: &backend.App{}}
	data := "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())
	for _, tc := range []struct{ limit, width, height int }{{100, 80, 40}, {20, 20, 10}} {
		bitmap, err := a.decodeImagePreviewBitmap("", data, tc.limit)
		if err != nil {
			t.Fatal(err)
		}
		if w, h := bitmap.Size(); w != tc.width || h != tc.height {
			t.Fatalf("limit %d: got %dx%d, want %dx%d", tc.limit, w, h, tc.width, tc.height)
		}
	}
	if _, err := a.decodeImagePreviewBitmap("", "data:image/png;base64,invalid", 20); err == nil {
		t.Fatal("invalid image was accepted")
	}
}

func TestImagePreviewDecodeWaiterCancels(t *testing.T) {
	slot := make(chan struct{}, 1)
	slot <- struct{}{}
	cancel := make(chan struct{})
	result := make(chan bool, 1)
	go func() { result <- acquireImagePreviewDecode(slot, cancel) }()
	close(cancel)
	select {
	case acquired := <-result:
		if acquired {
			t.Fatal("cancelled preview started decoding")
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled preview remained blocked")
	}
	<-slot
	if acquireImagePreviewDecode(slot, cancel) || len(slot) != 0 {
		t.Fatal("cancelled preview retained a decode slot")
	}
	if !acquireImagePreviewDecode(slot, make(chan struct{})) {
		t.Fatal("active preview could not decode")
	}
	<-slot
}

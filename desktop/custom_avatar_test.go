// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"bytes"
	"github.com/egoist/mygo/ui"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func avatarTestPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func TestCustomAvatarDisplayBound(t *testing.T) {
	for _, size := range [][2]int{{2048, 1024}, {40, 80}, {512, 512}} {
		bm, err := decodeCustomAvatar(avatarTestPNG(t, size[0], size[1]))
		if err != nil {
			t.Fatal(err)
		}
		w, h := bm.Size()
		if w > 512 || h > 512 || w*size[1] != h*size[0] {
			t.Fatalf("unexpected display %dx%d", w, h)
		}
		if size[0] < 512 && size[1] < 512 && (w != size[0] || h != size[1]) {
			t.Fatal("small image upscaled")
		}
	}
	for _, data := range [][]byte{nil, []byte("invalid"), make([]byte, customAvatarLimit+1)} {
		if _, err := decodeCustomAvatar(data); err == nil {
			t.Fatal("invalid image accepted")
		}
	}
}
func TestCustomAvatarFileLifecycle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "native-avatar")
	first := avatarTestPNG(t, 10, 20)
	second := avatarTestPNG(t, 20, 10)
	for _, data := range [][]byte{first, second} {
		if err := writeCustomAvatar(dir, data); err != nil {
			t.Fatal(err)
		}
		got, err := readCustomAvatar(path)
		if err != nil || !bytes.Equal(data, got) {
			t.Fatalf("roundtrip: %v", err)
		}
	}
	if err := removeCustomAvatar(path); err != nil {
		t.Fatal(err)
	}
	if err := removeCustomAvatar(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file still present: %v", err)
	}
}
func TestCustomAvatarDeletionAndSwitch(t *testing.T) {
	for _, style := range []string{"custom", "eye"} {
		a := &desktop{prefs: defaultPrefs(), customAvatarAvailable: true, customAvatarBusy: true, avatarLoadID: 10}
		a.prefs.Avatar = style
		a.customAvatar = ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 2, 2)))
		a.finishCustomAvatarDelete(nil)
		if a.customAvatar != nil || a.customAvatarAvailable || a.customAvatarBusy {
			t.Fatal("custom state retained")
		}
		if style == "custom" && a.prefs.Avatar != defaultPrefs().Avatar {
			t.Fatal("missing fallback")
		}
		if style == "eye" && (a.prefs.Avatar != "eye" || a.avatarLoadID != 10) {
			t.Fatal("unrelated avatar load invalidated")
		}
	}
	a := &desktop{prefs: defaultPrefs(), customAvatarAvailable: true, customAvatar: ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 2, 2)))}
	a.selectAvatarStyle("eye")
	if a.customAvatar != nil || !a.customAvatarAvailable {
		t.Fatal("switch must unload pixels but keep saved choice")
	}
}
func TestCustomAvatarJPEGOrientation(t *testing.T) {
	// Little-endian TIFF: orientation 6 (90 degrees clockwise).
	exif := []byte{'E', 'x', 'i', 'f', 0, 0, 'I', 'I', 42, 0, 8, 0, 0, 0, 1, 0, 0x12, 1, 3, 0, 1, 0, 0, 0, 6, 0, 0, 0, 0, 0, 0, 0}
	var raw bytes.Buffer
	if err := jpeg.Encode(&raw, image.NewRGBA(image.Rect(0, 0, 20, 10)), nil); err != nil {
		t.Fatal(err)
	}
	data := append([]byte{255, 216, 255, 225, 0, byte(len(exif) + 2)}, exif...)
	data = append(data, raw.Bytes()[2:]...)
	bm, err := decodeCustomAvatar(data)
	if err != nil {
		t.Fatal(err)
	}
	if w, h := bm.Size(); w != 10 || h != 20 {
		t.Fatalf("orientation lost: %dx%d", w, h)
	}
	src := image.NewRGBA(image.Rect(0, 0, 2, 3))
	src.Set(0, 0, color.RGBA{255, 0, 0, 255})
	for o := 2; o <= 8; o++ {
		dst := orientAvatar(src, o)
		found := 0
		for y := 0; y < dst.Bounds().Dy(); y++ {
			for x := 0; x < dst.Bounds().Dx(); x++ {
				if dst.At(x, y) == src.At(0, 0) {
					found++
				}
			}
		}
		if found != 1 {
			t.Fatalf("orientation %d corrupted pixels", o)
		}
	}
}

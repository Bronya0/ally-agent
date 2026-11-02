package desktop

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"

	"ally-dev/resources"

	xdraw "golang.org/x/image/draw"
)

// Keep the original letter-to-plate ratio and the tray's existing outer margin.
const trayIconFill = 90

// trayTemplateFill is how much of the square the menu bar's letter covers. A
// letter tall enough to touch the bar reads as heavy, so it keeps a margin.
const trayTemplateFill = 82

// trayMarkPNG returns the artwork with its plate, at the size the place that
// draws it asked for.
func trayMarkPNG(size int) ([]byte, error) {
	src, err := traySource()
	if err != nil {
		return nil, err
	}
	return encodePNG(fitSquare(src, size, trayIconFill))
}

// trayTemplatePNG returns the letter alone, black on transparent. macOS tints a
// template image with the menu bar's own colour, so a plate would turn the whole
// square into a solid block and swallow the letter.
func trayTemplatePNG(size int) ([]byte, error) {
	src, err := traySource()
	if err != nil {
		return nil, err
	}
	return encodePNG(fitSquare(trayGlyph(src), size, trayTemplateFill))
}

func traySource() (image.Image, error) {
	data, err := resources.Assets.ReadFile("icon.png")
	if err != nil {
		return nil, err
	}
	return png.Decode(bytes.NewReader(data))
}

func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// fitSquare scales img to cover fill percent of a square of the given side and
// centres it, leaving the rest transparent.
func fitSquare(img image.Image, size, fill int) *image.RGBA {
	if size < 8 {
		size = 8
	}
	mark := size * fill / 100
	if mark < 8 {
		mark = 8
	}
	if mark > size {
		mark = size
	}
	off := (size - mark) / 2
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	// Resample once from the source. Repeated halving softens the tiny strokes.
	xdraw.CatmullRom.Scale(dst, image.Rect(off, off, off+mark, off+mark), img, img.Bounds(), draw.Src, nil)
	return dst
}

// trayGlyph turns the artwork into black on transparent, cropped to its letter:
// what the letter covers is how far its pixel is from the plate's tone, so an
// anti-aliased edge keeps a partly transparent edge in the template.
func trayGlyph(src image.Image) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	mask := image.NewAlpha(image.Rect(0, 0, w, h))
	lo, hi := 255, 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, bl, a := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
			if a < 0x8000 {
				continue
			}
			l := int((r*299 + g*587 + bl*114) / 1000 >> 8)
			mask.SetAlpha(x, y, color.Alpha{A: uint8(l)})
			if l < lo {
				lo = l
			}
			if l > hi {
				hi = l
			}
		}
	}
	// A flat image has no letter to keep.
	if hi-lo < 16 {
		return src
	}
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			l := int(mask.AlphaAt(x, y).A)
			_, _, _, a := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
			if a < 0x8000 {
				continue
			}
			cover := (hi - l) * 255 / (hi - lo)
			dst.SetNRGBA(x, y, color.NRGBA{A: uint8(cover)})
		}
	}
	return cropOpaque(dst)
}

// cropOpaque trims the transparent margin from img.
func cropOpaque(img *image.NRGBA) image.Image {
	b := img.Bounds()
	minX, minY, maxX, maxY := b.Max.X, b.Max.Y, b.Min.X, b.Min.Y
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if img.Pix[y*img.Stride+x*4+3] == 0 {
				continue
			}
			if x < minX {
				minX = x
			}
			if y < minY {
				minY = y
			}
			if x > maxX {
				maxX = x
			}
			if y > maxY {
				maxY = y
			}
		}
	}
	if minX > maxX || minY > maxY {
		return img
	}
	return img.SubImage(image.Rect(minX, minY, maxX+1, maxY+1))
}

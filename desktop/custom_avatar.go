// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const customAvatarLimit = 5 * 1024 * 1024
const customAvatarDisplaySize = 512

func decodeCustomAvatar(data []byte) (*ui.Bitmap, error) {
	if len(data) == 0 || len(data) > customAvatarLimit {
		return nil, fmt.Errorf("图片大小需小于 5 MiB")
	}
	info, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("无法读取图片：%w", err)
	}
	if info.Width <= 0 || info.Height <= 0 || int64(info.Width)*int64(info.Height) > 16000000 {
		return nil, fmt.Errorf("图片尺寸过大")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("无法读取图片：%w", err)
	}
	// Resize first; orientation only rotates the bounded display image.
	img = resizeImage(img, customAvatarDisplaySize)
	img = orientAvatar(img, avatarEXIFOrientation(data))
	var display bytes.Buffer
	if err := png.Encode(&display, img); err != nil {
		return nil, err
	}
	return ui.DecodeBitmap(display.Bytes())
}

func (a *desktop) loadCustomAvatar() *ui.Bitmap {
	path := filepath.Join(a.core.AppDataDir(), "native-avatar")
	data, err := readCustomAvatar(path)
	if err != nil {
		return nil
	}
	bitmap, _ := decodeCustomAvatar(data)
	return bitmap
}

func (a *desktop) chooseCustomAvatar() {
	if a.customAvatarBusy {
		return
	}
	a.customAvatarBusy = true
	id := a.avatarLoadID
	dir := a.core.AppDataDir()
	go func() {
		defer a.win.Update(func() { a.customAvatarBusy = false })
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: "选择欢迎页形象", Filters: []mygo.FileFilter{{Name: "图片", Extensions: []string{"png", "jpg", "jpeg", "gif", "webp", "bmp"}}}})
		if err != nil {
			a.fail(err)
			return
		}
		if len(paths) == 0 {
			return
		}
		file, err := os.Open(paths[0])
		if err != nil {
			a.fail(err)
			return
		}
		data, err := io.ReadAll(io.LimitReader(file, customAvatarLimit+1))
		file.Close()
		if err != nil {
			a.fail(err)
			return
		}
		bitmap, err := decodeCustomAvatar(data)
		if err != nil {
			a.fail(err)
			return
		}
		err = writeCustomAvatar(dir, data)
		if err != nil {
			a.fail(err)
			return
		}
		a.win.Update(func() {
			a.customAvatarAvailable = true
			if a.closing || id != a.avatarLoadID {
				return
			}
			a.customAvatar = bitmap
			a.selectAvatarStyle("custom")
		})
	}()
}

// Bound reads even for an old or externally replaced native-avatar file.
func readCustomAvatar(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, customAvatarLimit+1))
}
func writeCustomAvatar(dir string, data []byte) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".native-avatar-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(name, filepath.Join(dir, "native-avatar"))
}
func removeCustomAvatar(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
func (a *desktop) deleteCustomAvatar() {
	if a.customAvatarBusy {
		return
	}
	a.customAvatarBusy = true
	path := filepath.Join(a.core.AppDataDir(), "native-avatar")
	go func() {
		err := removeCustomAvatar(path)
		a.win.Update(func() { a.finishCustomAvatarDelete(err) })
	}()
}
func (a *desktop) finishCustomAvatarDelete(err error) {
	a.customAvatarBusy = false
	if err != nil {
		a.fail(err)
		return
	}
	a.customAvatarAvailable = false
	a.customAvatar = nil
	if a.prefs.Avatar == "custom" {
		a.selectAvatarStyle(defaultPrefs().Avatar)
	}
	a.invalidatePet()
}

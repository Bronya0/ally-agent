// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"bytes"
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const customAvatarLimit = 5 * 1024 * 1024

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
	return ui.DecodeBitmap(data)
}

func (a *desktop) loadCustomAvatar() *ui.Bitmap {
	path := filepath.Join(a.core.AppDataDir(), "native-avatar")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	bitmap, _ := decodeCustomAvatar(data)
	return bitmap
}

func (a *desktop) chooseCustomAvatar() {
	go func() {
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
		dir := a.core.AppDataDir()
		if err = os.MkdirAll(dir, 0700); err == nil {
			err = os.WriteFile(filepath.Join(dir, "native-avatar"), data, 0600)
		}
		if err != nil {
			a.fail(err)
			return
		}
		a.win.Update(func() { a.customAvatar = bitmap; a.selectAvatarStyle("custom") })
	}()
}

// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"bytes"
	"fmt"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
)

// ReadNativeWorkspaceImage uses the same workspace path checks as the editor.
func (a *App) ReadNativeWorkspaceImage(req WorkspacePathRequest) ([]byte, error) {
	cfg, err := a.configForWorkspace(req.Workspace)
	if err != nil {
		return nil, err
	}
	path, err := resolveReadPath(cfg, req.Path)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, workspaceEditorMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > workspaceEditorMaxBytes {
		return nil, fmt.Errorf("图片超过 16 MiB 预览限制")
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("无法读取图片：%w", err)
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 40000000 {
		return nil, fmt.Errorf("图片尺寸过大，无法预览")
	}
	return data, nil
}

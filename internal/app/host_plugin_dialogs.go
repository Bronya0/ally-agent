// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

// host_plugin_dialogs.go 是插件相关的原生对话框（host 层：唯一允许 import Wails
// 的代码）。这里只负责「拿到一个路径」——选包、选导出目标；校验、安装、打包全部
// 在 orch_plugin.go，业务规则不跨层。

import (
	"errors"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// SelectPluginPackage 打开原生文件选择框挑一个插件包，返回绝对路径；用户取消时
// 返回空串（不是错误）。
func (a *App) SelectPluginPackage() (string, error) {
	if err := a.ensureInitialized(); err != nil {
		return "", err
	}
	if a.wails == nil || a.wails.app == nil {
		return "", errors.New("desktop host not initialized")
	}
	dialog := a.wails.app.Dialog.OpenFile()
	dialog.SetOptions(&application.OpenFileDialogOptions{
		Title:                "选择插件包（.zip）",
		CanChooseFiles:       true,
		CanChooseDirectories: false,
		Filters:              []application.FileFilter{{DisplayName: "Ally 插件包 (*.zip)", Pattern: "*.zip"}},
	})
	selected, err := dialog.PromptForSingleSelection()
	if err != nil {
		return "", err
	}
	return selected, nil
}

// promptSavePluginPackage 打开原生保存框选导出目标；用户取消时返回空串。
// 之所以不走前端 blob 下载：WKWebView（macOS）忽略 <a download>，只有原生对话框
// 全平台可用（与 ExportTextFile 同一个理由）。
func (a *App) promptSavePluginPackage(suggested string) (string, error) {
	if a.wails == nil || a.wails.app == nil {
		return "", errors.New("desktop host not initialized")
	}
	dialog := a.wails.app.Dialog.SaveFile()
	dialog.SetOptions(&application.SaveFileDialogOptions{
		Title:    "导出插件包",
		Filename: suggested,
		Filters:  []application.FileFilter{{DisplayName: "Ally 插件包 (*.zip)", Pattern: "*.zip"}},
	})
	selected, err := dialog.PromptForSingleSelection()
	if err != nil || selected == "" {
		return selected, err
	}
	// Windows 的 IFileSaveDialog 不会自动补扩展名（macOS 的 NSSavePanel 会）。
	if filepath.Ext(selected) == "" {
		selected += ".zip"
	}
	return selected, nil
}

// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"ally-dev/internal/tools/pathutil"
	"context"
	"errors"
	"github.com/egoist/mygo"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The legacy field name is retained so Agent core code stays unchanged.
type wailsAppHandle struct {
	app    *mygo.Application
	window *mygo.Window
}

func (a *App) SetNativeWindow(w *mygo.Window) { a.wails = &wailsAppHandle{app: mygo.App, window: w} }

type nativeEventSink struct{ send func(string, any) }

func (s nativeEventSink) Emit(n string, p any) {
	if s.send != nil {
		s.send(n, p)
	}
}

// StartNative installs the host boundary before starting unchanged Agent services.
func (a *App) StartNative(ctx context.Context, send func(string, any)) error {
	a.events = nativeEventSink{send}
	if err := a.startRuntime(ctx); err != nil {
		return err
	}
	a.restoreApiService()
	return nil
}
func (a *App) ShutdownNative() error { return a.stopRuntime(20 * time.Second) }
func (a *App) quitApp()              { mygo.App.Quit() }
func (a *App) showMainWindow() {
	if a.wails != nil && a.wails.window != nil {
		a.wails.window.Show()
		a.wails.window.Focus()
	}
}
func (a *App) ShowMainWindow() { a.showMainWindow() }
func (a *App) SelectDirectory(current string) (string, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: "选择目录", DefaultPath: current, Directory: true})
	if err != nil || len(paths) == 0 {
		return "", err
	}
	return paths[0], nil
}
func (a *App) SelectWorkspace() (string, error) {
	cfg, err := a.GetConfig()
	if err != nil {
		return "", err
	}
	path, err := a.SelectDirectory(cfg.Workspace)
	if err != nil || path == "" {
		return path, err
	}
	if pathutil.IsSystemRootPath(path) {
		return "", errors.New("系统根目录不能用作工作区，请选择具体项目目录")
	}
	return path, a.updateConfigAndPersist(func(c *ConfigState) { c.Workspace = path })
}
func (a *App) SelectKnowledgeBaseRoot() (string, error) { return a.SelectDirectory("") }
func (a *App) selectFile(title string) (string, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: title})
	if err != nil || len(paths) == 0 {
		return "", err
	}
	return paths[0], nil
}
func (a *App) SelectBackgroundImage() (string, error) {
	p, e := a.selectFile("选择背景图片")
	if e != nil || p == "" {
		return p, e
	}
	return a.saveBackgroundImageFromFile(p)
}
func (a *App) SelectPrivateKeyFile() (string, error) { return a.selectFile("选择 SSH 私钥") }

// SelectJSONFile picks a configuration file for import. Filters keep the dialog
// honest while still accepting the generic .json files users export.
func (a *App) SelectJSONFile(title string) (string, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Title: title, Filters: []mygo.FileFilter{{Name: "JSON", Extensions: []string{"json"}}}})
	if err != nil || len(paths) == 0 {
		return "", err
	}
	return paths[0], nil
}

// ReadLocalTextFile reads an imported configuration file. It is a host
// adaptation for dialogs only: the path comes from a user file picker, and the
// size cap keeps a mistaken selection from loading a huge file.
func (a *App) ReadLocalTextFile(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", errors.New("请选择文件而不是目录")
	}
	const max = 2 << 20
	if info.Size() > max {
		return "", errors.New("文件过大（上限 2MB）")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ReadWorkspaceMedia returns the bytes of an image inside a workspace so the
// native explorer can preview it without a web media server.
func (a *App) ReadWorkspaceMedia(req WorkspacePathRequest) ([]byte, error) {
	if err := a.ensureInitialized(); err != nil {
		return nil, err
	}
	cfg, err := a.configForWorkspace(req.Workspace)
	if err != nil {
		return nil, err
	}
	resolved, err := resolveReadPath(cfg, req.Path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, errors.New("not a file")
	}
	const max = 20 << 20
	if info.Size() > max {
		return nil, errors.New("图片过大，无法预览（上限 20MB）")
	}
	return os.ReadFile(resolved)
}
func (a *App) ExportTextFile(name, content, filterName, pattern string) (string, error) {
	p, e := mygo.Dialog.Save(mygo.SaveDialogOptions{Title: "导出文件", DefaultPath: name})
	if e != nil || p == "" {
		return p, e
	}
	if filepath.Ext(p) == "" {
		p += filepath.Ext(name)
	}
	return p, os.WriteFile(p, []byte(content), 0600)
}
func (a *App) OpenWorkspaceInFileManager() error {
	if err := a.ensureInitialized(); err != nil {
		return err
	}
	a.mu.Lock()
	cfg := cloneConfig(a.config)
	a.mu.Unlock()
	root, err := workspaceRoot(cfg)
	if err != nil {
		return err
	}
	return openPathInFileManager(root)
}

func (a *App) OpenWorkspacePathInFileManagerAt(req WorkspacePathRequest) error {
	return a.openWorkspacePathInFileManagerAt(req.Workspace, req.Path)
}

func (a *App) openWorkspacePathInFileManagerAt(workspace, path string) error {
	if err := a.ensureInitialized(); err != nil {
		return err
	}
	cfg, err := a.configForWorkspace(workspace)
	if err != nil {
		return err
	}
	root, err := workspaceRoot(cfg)
	if err != nil {
		return err
	}
	if strings.TrimSpace(path) == "" {
		return openPathInFileManager(root)
	}
	target, err := resolveReadablePath(cfg, path)
	if err != nil {
		return err
	}
	if !insideRoot(root, target) {
		return errors.New("path is outside workspace")
	}
	return openPathInFileManager(target)
}

// OpenPathInFileManager opens a file or directory in the system file manager.
// If path points to a file, the parent directory is opened instead.
func (a *App) OpenPathInFileManager(path string) error {
	if err := a.ensureInitialized(); err != nil {
		return err
	}
	return openPathInFileManager(path)
}

// OpenWorkspaceFile opens only a resolved workspace file with its registered
// system application. Unlike OpenPathInFileManager it does not open its parent.
func (a *App) OpenWorkspaceFile(req WorkspacePathRequest) error {
	cfg, err := a.configForWorkspace(req.Workspace)
	if err != nil {
		return err
	}
	resolved, err := resolveReadPath(cfg, req.Path)
	if err != nil {
		return err
	}
	return mygo.Shell.OpenPath(resolved)
}

func openPathInFileManager(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("path is required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	// 文件：打开其所在目录（并可选选中该文件）
	if !info.IsDir() {
		path = filepath.Dir(path)
	}
	// 路径归一化为各平台原生分隔符，避免 explorer.exe 收到混合分隔符
	path = filepath.Clean(path)
	return mygo.Shell.OpenPath(path)
}

// GetAutostartEnabled 报告本构建是否已注册为登录自启。
//
// 这里只转发 mygo 的实现：框架已经按平台各自做好（macOS 登录项、Windows 的用户
// Run 项、Linux 的 XDG autostart）并统一了应用 id，自己再写一份 plist / 注册表
// 既重复，又容易和框架记录的 id 对不上。
func (a *App) GetAutostartEnabled() (bool, error) {
	return mygo.App.OpenAtLogin(), nil
}

// SetAutostartEnabled 注册或取消登录自启。
func (a *App) SetAutostartEnabled(enabled bool) error {
	return mygo.App.SetOpenAtLogin(enabled)
}

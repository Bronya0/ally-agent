// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"ally-dev/internal/tools/pathutil"
	"context"
	"errors"
	"github.com/egoist/mygo"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
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
	var cmd *exec.Cmd
	switch goruntime.GOOS {
	case "windows":
		cmd = explorerCommand(path)
	case "darwin":
		cmd = exec.Command("open", path)
	default:
		// Linux：优先 xdg-open，回退常见文件管理器
		if _, err := exec.LookPath("xdg-open"); err == nil {
			cmd = exec.Command("xdg-open", path)
		} else if _, err := exec.LookPath("nautilus"); err == nil {
			cmd = exec.Command("nautilus", path)
		} else if _, err := exec.LookPath("dolphin"); err == nil {
			cmd = exec.Command("dolphin", path)
		} else if _, err := exec.LookPath("thunar"); err == nil {
			cmd = exec.Command("thunar", path)
		} else {
			cmd = exec.Command("xdg-open", path)
		}
	}
	// explorer.exe（Windows）不退出属预期，交给 explorerCommand 内部处理；
	// POSIX 上 open/xdg-open 通常立即退出，Start 后不 Wait 会积累僵尸进程，
	// 异步收割即可。
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// completionNotifier is the minimal notifications surface the Agent core
// needs: sending a completion toast with sound. The concrete implementation
// is injected through SetNotifier from the host wiring (main.go); tests and
// headless embeddings leave it nil and get silent no-ops.
type completionNotification struct{ ID, Title, Body, Sound string }

type completionNotifier interface {
	SendNotification(options completionNotification) error
}

// mainWindowMinimised reports whether the main window is currently
// minimised. A missing window handle (headless embedding, tests) counts as
// not minimised so completion notifications stay silent.
func (a *App) mainWindowMinimised() bool {
	if a.wails == nil || a.wails.window == nil {
		return false
	}
	return a.wails.window.IsMinimized()
}

// SetNotifier injects the notifications service used for task completion
// sounds. Must be called before app.Run().
//
// This is a package-level function rather than an (a *App) method on purpose:
// the parameter is a non-empty interface, and Wails' binding generator would
// reject any exported method on *App (which is registered as a service) that
// takes one, since encoding/json cannot unmarshal into a concrete type it
// cannot infer. Package-level functions are not scanned by the generator, so
// keeping the injection here keeps the interface while leaving the generated
// frontend bindings clean.
func SetNotifier(a *App, n completionNotifier) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.notifier = n
}

// completionNotifyCooldown mirrors the old frontend sound throttle so rapid
// consecutive runs (e.g. quick send bursts) don't spam the Action Center.
const completionNotifyCooldown = 700 * time.Millisecond

// completionToastBodies is the toast body per run-end kind. The backend has
// no i18n pipeline (locale detection lives in the frontend), so the strings
// are centralized here in the primary UI language.
var completionToastBodies = map[string]string{
	"done":      "任务完成",
	"cancelled": "任务已取消",
	"error":     "任务出错",
}

// notifyCompletion sends a short sound-carrying system notification when a
// chat run finishes, but only while the main window is minimised. When the
// window is visible the run result is already on screen, so a toast would
// only interrupt. kind is "done", "error" or "cancelled". Non-Windows
// platforms use the platform default sound; Windows picks a distinct built-in
// toast event sound per kind. The send itself is async so a slow platform
// backend (Windows PowerShell fallback, unresponsive Linux daemon) never
// blocks the run teardown path (checkpoint save, session release).
func (a *App) notifyCompletion(kind, workspace string) {
	a.mu.Lock()
	n := a.notifier
	if n == nil || time.Since(a.lastCompletionNotifyAt) < completionNotifyCooldown {
		a.mu.Unlock()
		return
	}
	a.lastCompletionNotifyAt = time.Now()
	a.mu.Unlock()

	// 仅当主窗口处于最小化状态时才发系统通知：窗口可见（含最大化）时
	// 结果就在眼前，弹窗与提示音纯属打扰。窗口句柄缺失（headless
	// 嵌入/测试）时按不可见对待，保持静默。
	if !a.mainWindowMinimised() {
		return
	}

	body := completionToastBodies[kind]
	if body == "" {
		body = completionToastBodies["error"]
	}
	if name := workspaceDisplayName(workspace); name != statsUnknownName {
		body += " · " + name
	}

	opts := completionNotification{
		ID:    "ally-run-" + kind,
		Title: "Ally",
		Body:  body,
	}
	if goruntime.GOOS == "windows" {
		sound := "Default"
		if kind != "done" {
			sound = "Reminder"
		}
		opts.Sound = sound
	}
	go func() {
		_ = n.SendNotification(opts)
	}()
}

// GetAutostartEnabled reports whether the current build is registered to start
// with the user session. Only Windows has an implementation; elsewhere the
// setting is simply unavailable.
func (a *App) GetAutostartEnabled() (bool, error) {
	if goruntime.GOOS != "windows" {
		return false, nil
	}
	key := `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
	exe, err := os.Executable()
	if err != nil {
		return false, nil
	}
	cmd := exec.Command("reg", "query", key, "/v", "AllyAgent")
	hideCommandWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, nil
	}
	return strings.Contains(strings.ToLower(string(out)), strings.ToLower(exe)), nil
}

// SetAutostartEnabled writes or removes the per-user Run entry pointing at this
// executable.
func (a *App) SetAutostartEnabled(enabled bool) error {
	if goruntime.GOOS != "windows" {
		return errors.New("当前系统不支持开机自启动")
	}
	key := `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
	if !enabled {
		cmd := exec.Command("reg", "delete", key, "/v", "AllyAgent", "/f")
		hideCommandWindow(cmd)
		out, err := cmd.CombinedOutput()
		if err != nil && !strings.Contains(string(out), "unable to find") {
			return errors.New(strings.TrimSpace(string(out)))
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command("reg", "add", key, "/v", "AllyAgent", "/t", "REG_SZ", "/d", exe, "/f")
	hideCommandWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return errors.New(strings.TrimSpace(string(out)))
	}
	return nil
}

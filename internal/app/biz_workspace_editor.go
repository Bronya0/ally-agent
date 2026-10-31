// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const workspaceEditorMaxBytes = 16 * 1024 * 1024

type WorkspaceFileContent struct {
	Path       string `json:"path"`
	Content    string `json:"content"`
	Version    string `json:"version"`
	SHA256     string `json:"sha256"`
	Size       int64  `json:"size"`
	LineEnding string `json:"lineEnding"`
}

type SaveWorkspaceFileRequest struct {
	Workspace string `json:"workspace,omitempty"`
	Path      string `json:"path"`
	Version   string `json:"version"`
	Content   string `json:"content"`
}

type SaveWorkspaceFileResult struct {
	Path    string `json:"path"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
}

// ReadWorkspaceFileAt returns an unnumbered, complete text snapshot for the
// user-facing workspace editor. It is deliberately separate from the model
// read tool, whose bounded line-number preview is designed for model context.
func (a *App) ReadWorkspaceFileAt(req WorkspacePathRequest) (WorkspaceFileContent, error) {
	return a.readWorkspaceFileAt(req.Workspace, req.Path)
}

func (a *App) readWorkspaceFileAt(workspace, path string) (WorkspaceFileContent, error) {
	cfg, err := a.configForWorkspace(workspace)
	if err != nil {
		return WorkspaceFileContent{}, err
	}
	resolved, err := resolveReadPath(cfg, path)
	if err != nil {
		return WorkspaceFileContent{}, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return WorkspaceFileContent{}, err
	}
	if info.IsDir() {
		return WorkspaceFileContent{}, codedToolError("E_BAD_PATH", fmt.Errorf("not a file: %s", path))
	}
	if info.Size() > workspaceEditorMaxBytes {
		return WorkspaceFileContent{}, codedToolError("E_FILE_TOO_LARGE", fmt.Errorf("file is larger than the %d MiB editor limit", workspaceEditorMaxBytes/(1024*1024)))
	}
	data, info, err := readTextFile(resolved)
	if err != nil {
		return WorkspaceFileContent{}, err
	}
	if len(data) > workspaceEditorMaxBytes {
		return WorkspaceFileContent{}, codedToolError("E_FILE_TOO_LARGE", fmt.Errorf("file is larger than the %d MiB editor limit", workspaceEditorMaxBytes/(1024*1024)))
	}
	content, ending, _ := normalizeText(data)
	sha, version := hashBytesAndVersion(data)
	return WorkspaceFileContent{
		Path:       displayPathForConfig(cfg, resolved),
		Content:    content,
		Version:    version,
		SHA256:     sha,
		Size:       info.Size(),
		LineEnding: ending,
	}, nil
}

// SaveWorkspaceFile atomically replaces one editor snapshot after validating
// its optimistic-concurrency token. The shared file mutex keeps UI writes from
// racing Agent file mutations in the same process.
func (a *App) SaveWorkspaceFile(req SaveWorkspaceFileRequest) (SaveWorkspaceFileResult, error) {
	return a.saveWorkspaceFileAt(req.Workspace, req)
}

func (a *App) saveWorkspaceFileAt(workspace string, req SaveWorkspaceFileRequest) (SaveWorkspaceFileResult, error) {
	if strings.TrimSpace(req.Path) == "" {
		return SaveWorkspaceFileResult{}, codedToolError("E_BAD_PATH", errors.New("path is required"))
	}
	if err := validateVersion(req.Version); err != nil {
		return SaveWorkspaceFileResult{}, err
	}
	if len(req.Content) > workspaceEditorMaxBytes {
		return SaveWorkspaceFileResult{}, codedToolError("E_FILE_TOO_LARGE", fmt.Errorf("content is larger than the %d MiB editor limit", workspaceEditorMaxBytes/(1024*1024)))
	}

	cfg, err := a.configForWorkspace(workspace)
	if err != nil {
		return SaveWorkspaceFileResult{}, err
	}
	a.fileOpsMu.Lock()
	defer a.fileOpsMu.Unlock()

	roots, err := workspaceRoots(cfg)
	if err != nil {
		return SaveWorkspaceFileResult{}, err
	}
	// 与模型侧写路径（create/edit）同一边界：resolveWritableFilePath 会拒绝
	// 叶子符号链接并把已存在前缀解析到真实路径，避免工作区内的符号链接把
	// 编辑器保存定向到工作区之外（裸 safeJoin 只做词法拼接，会跟随链接）。
	resolved, err := resolveWritableFilePath(roots, req.Path)
	if err != nil {
		return SaveWorkspaceFileResult{}, err
	}
	before, info, err := readTextFile(resolved)
	if err != nil {
		return SaveWorkspaceFileResult{}, err
	}
	_, currentVersion := hashBytesAndVersion(before)
	if !strings.EqualFold(req.Version, currentVersion) {
		return SaveWorkspaceFileResult{}, codedToolError("E_VERSION_MISMATCH", fmt.Errorf("file %s changed outside the editor; reopen it before saving", req.Path))
	}
	_, ending, hadBOM := normalizeText(before)
	after := encodeText(req.Content, ending, hadBOM)
	if len(after) > workspaceEditorMaxBytes {
		return SaveWorkspaceFileResult{}, codedToolError("E_FILE_TOO_LARGE", fmt.Errorf("encoded content is larger than the %d MiB editor limit", workspaceEditorMaxBytes/(1024*1024)))
	}
	if err := safeWriteFile(resolved, after, info.Mode().Perm()); err != nil {
		return SaveWorkspaceFileResult{}, err
	}
	sha, version := hashBytesAndVersion(after)
	a.invalidateWorkspaceMapCache(cfg)
	return SaveWorkspaceFileResult{
		Path:    displayPathForConfig(cfg, resolved),
		Version: version,
		SHA256:  sha,
		Size:    int64(len(after)),
	}, nil
}

var imageExtensions = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp", ".bmp": "image/bmp",
	".svg": "image/svg+xml", ".ico": "image/x-icon",
}

var videoExtensions = map[string]string{
	".mp4": "video/mp4", ".webm": "video/webm", ".ogv": "video/ogg",
	".mov": "video/quicktime", ".m4v": "video/mp4",
}

var pdfExtensions = map[string]string{
	".pdf": "application/pdf",
}

var audioExtensions = map[string]string{
	".mp3": "audio/mpeg", ".wav": "audio/wav", ".m4a": "audio/mp4",
	".aac": "audio/aac", ".flac": "audio/flac", ".ogg": "audio/ogg",
	".oga": "audio/ogg", ".opus": "audio/ogg",
}

func imageExtMime(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	return imageExtensions[ext]
}

func videoExtMime(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	return videoExtensions[ext]
}

func pdfExtMime(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	return pdfExtensions[ext]
}

func audioExtMime(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	return audioExtensions[ext]
}

// ── 插件媒体流式服务 ──
//
// 资源树通过 GetWorkspaceMediaPath 让 WebView 直接读取本地文件。插件宿主
// 仍使用此回环 HTTP 接口，以便插件通过受限、带 Range 的 URL 播放媒体。
// 端口由操作系统分配，进程级随机 token 防止其他本机程序访问。

// mediaServer 持有媒体流式服务单例；首次请求媒体 URL 时懒启动。
type mediaServer struct {
	mu       sync.Mutex
	server   *http.Server
	listener net.Listener
	token    string
	baseURL  string
	running  bool
}

var mediaSvc = &mediaServer{}

// GetWorkspaceMediaPath returns a validated local path for the native desktop
// WebView. The browser reads the file directly, without routing its bytes through
// the app process.
func (a *App) GetWorkspaceMediaPath(req WorkspacePathRequest) (string, error) {
	return a.resolveWorkspaceMediaPath(req)
}

// ReadWorkspaceSVGAt returns bounded SVG source so the UI can render it inside
// an HTML document with a restrictive Content Security Policy.
func (a *App) ReadWorkspaceSVGAt(ctx context.Context, req WorkspacePathRequest) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	resolved, err := a.resolveWorkspaceMediaPath(req)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !strings.EqualFold(filepath.Ext(resolved), ".svg") {
		return "", codedToolError("E_BAD_PATH", errors.New("not an SVG file"))
	}
	f, err := os.Open(resolved)
	if err != nil {
		return "", err
	}
	defer f.Close()
	const maxSVGBytes = 2 << 20
	data, err := io.ReadAll(io.LimitReader(f, maxSVGBytes+1))
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(data) > maxSVGBytes {
		return "", codedToolError("E_FILE_TOO_LARGE", errors.New("SVG exceeds the 2 MiB preview limit"))
	}
	content, _, _ := normalizeText(data)
	return content, nil
}

// GetWorkspaceMediaURL remains the plugin host's authenticated streaming API.
// The resource-tree preview uses GetWorkspaceMediaPath and loads files directly.
func (a *App) GetWorkspaceMediaURL(req WorkspacePathRequest) (string, error) {
	_, err := a.resolveWorkspaceMediaPath(req)
	if err != nil {
		return "", err
	}
	base, token, err := a.ensureMediaServer()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/media?t=%s&ws=%s&p=%s", base, token,
		url.QueryEscape(req.Workspace), url.QueryEscape(req.Path)), nil
}

func (a *App) resolveWorkspaceMediaPath(req WorkspacePathRequest) (string, error) {
	if err := a.ensureInitialized(); err != nil {
		return "", err
	}
	if strings.TrimSpace(req.Path) == "" {
		return "", codedToolError("E_BAD_PATH", errors.New("path is required"))
	}
	cfg, err := a.configForWorkspace(req.Workspace)
	if err != nil {
		return "", err
	}
	resolved, err := resolveReadPath(cfg, req.Path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", codedToolError("E_BAD_PATH", fmt.Errorf("not a file: %s", req.Path))
	}
	if mediaExtMime(req.Path) == "" || mediaExtMime(resolved) == "" {
		return "", codedToolError("E_BAD_PATH", fmt.Errorf("unsupported media file: %s", req.Path))
	}
	return resolved, nil
}

func mediaExtMime(path string) string {
	if m := imageExtMime(path); m != "" {
		return m
	}
	if m := audioExtMime(path); m != "" {
		return m
	}
	if m := videoExtMime(path); m != "" {
		return m
	}
	return pdfExtMime(path)
}

// ensureMediaServer 幂等启动回环媒体服务（已运行直接返回）。
func (a *App) ensureMediaServer() (baseURL, token string, err error) {
	mediaSvc.mu.Lock()
	defer mediaSvc.mu.Unlock()
	if mediaSvc.running {
		return mediaSvc.baseURL, mediaSvc.token, nil
	}
	tok := generateApiToken()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", "", fmt.Errorf("media listen: %w", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /media", a.handleWorkspaceMedia)
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	mediaSvc.server = server
	mediaSvc.listener = ln
	mediaSvc.token = tok
	mediaSvc.baseURL = "http://" + ln.Addr().String()
	mediaSvc.running = true
	go func() {
		if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("[media] serve: %v", err)
		}
	}()
	if a.ctx != nil {
		go func() {
			<-a.ctx.Done()
			stopMediaServer()
		}()
	}
	return mediaSvc.baseURL, mediaSvc.token, nil
}

// stopMediaServer 停止媒体服务（幂等）。
func stopMediaServer() {
	mediaSvc.mu.Lock()
	server := mediaSvc.server
	mediaSvc.server = nil
	mediaSvc.listener = nil
	mediaSvc.token = ""
	mediaSvc.baseURL = ""
	mediaSvc.running = false
	mediaSvc.mu.Unlock()
	if server == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}

// handleWorkspaceMedia 校验 token 与工作区路径后交给 http.ServeContent，
// 由插件 WebView 按需拉取 Range / HEAD / 条件请求。
func (a *App) handleWorkspaceMedia(w http.ResponseWriter, r *http.Request) {
	mediaSvc.mu.Lock()
	token := mediaSvc.token
	mediaSvc.mu.Unlock()
	if token == "" {
		http.Error(w, "media server is not running", http.StatusServiceUnavailable)
		return
	}
	q := r.URL.Query()
	if subtle.ConstantTimeCompare([]byte(q.Get("t")), []byte(token)) != 1 {
		http.Error(w, "invalid or missing token", http.StatusUnauthorized)
		return
	}
	cfg, err := a.configForWorkspace(q.Get("ws"))
	if err != nil {
		http.Error(w, "invalid workspace", http.StatusBadRequest)
		return
	}
	resolved, err := resolveReadPath(cfg, q.Get("p"))
	if err != nil {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	info, err := os.Stat(resolved)
	if err != nil || info.IsDir() {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	mime := mediaExtMime(resolved)
	if mime == "" {
		http.Error(w, "unsupported media file", http.StatusBadRequest)
		return
	}
	f, err := os.Open(resolved)
	if err != nil {
		http.Error(w, "open failed", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", mime)
	if strings.EqualFold(filepath.Ext(resolved), ".svg") {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src data:; style-src 'unsafe-inline'")
	}
	http.ServeContent(w, r, filepath.Base(resolved), info.ModTime(), f)
}

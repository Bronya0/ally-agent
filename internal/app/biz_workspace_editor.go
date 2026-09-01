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

// ReadWorkspaceFile returns an unnumbered, complete text snapshot for the
// user-facing workspace editor. It is deliberately separate from ReadFile,
// whose bounded line-number preview is designed for model context.
func (a *App) ReadWorkspaceFile(path string) (WorkspaceFileContent, error) {
	return a.readWorkspaceFileAt("", path)
}

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
	resolved, err := safeJoin(roots, req.Path)
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
	".mp4": "video/mp4", ".webm": "video/webm", ".ogg": "video/ogg",
	".ogv": "video/ogg", ".mov": "video/quicktime", ".m4v": "video/mp4",
}

var pdfExtensions = map[string]string{
	".pdf": "application/pdf",
}

// IsWorkspaceImage reports whether the given path has an image extension.
func (a *App) IsWorkspaceImage(path string) bool {
	return imageExtMime(path) != ""
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

// ── 媒体流式服务 ──
//
// 图片/视频/PDF 预览不再走 base64 data URL（整文件 ×4 份内存副本，视频直接
// 上 GB），改用本机回环 HTTP 服务按需流式输出。端口由操作系统分配
// （127.0.0.1:0），不存在占用冲突；进程级随机 token 防止其他本机程序访问。

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

// GetWorkspaceMediaURL 校验文件后返回一个回环流式 URL，前端直接塞进
// <video>/<img>/<iframe> 的 src。浏览器原生支持 Range 请求，拖进度条按需拉取。
func (a *App) GetWorkspaceMediaURL(req WorkspacePathRequest) (string, error) {
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
	if mediaExtMime(req.Path) == "" {
		return "", codedToolError("E_BAD_PATH", fmt.Errorf("unsupported media file: %s", req.Path))
	}
	base, token, err := a.ensureMediaServer()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/media?t=%s&ws=%s&p=%s", base, token,
		url.QueryEscape(req.Workspace), url.QueryEscape(req.Path)), nil
}

func mediaExtMime(path string) string {
	if m := imageExtMime(path); m != "" {
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

// handleWorkspaceMedia 校验 token 与路径（必须位于工作区内）后交给
// http.ServeContent：自动处理 Range / HEAD / 条件请求，浏览器按需拉取。
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
	http.ServeContent(w, r, filepath.Base(resolved), info.ModTime(), f)
}

// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

// orch_plugin_assets.go 在 Wails 资源服务前插一层：把 /plugins/<id>/<entry> 指向
// <appData>/plugins/<id>/。于是插件资源与页面**同源**（http://wails.localhost），
// 原生 import、相对导入、CSS、图片全部可用，且 dev 与打包后路径完全一致（dev 时
// 请求先过这里，再被代理给 Vite）。
//
// 边界（全部默认拒绝）：
//   - 只处理 GET/HEAD，路径必须形如 /plugins/<合法 id>/<rel>，其余交给下一个处理器；
//   - 资源必须落在该插件目录内（词法路径与软链解析后各判一次）；
//   - 扩展名走白名单，其余显式 403（而不是静默 404，免得以后误判成"文件没找到"）；
//   - 被禁用的插件不提供任何资源。
//
// 这里只用标准库 net/http，所以实现留在 app 包、不碰 Wails 类型（见 main.go 接线）。

import (
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"ally-dev/internal/tools/plugin"
)

// pluginAssetExtTypes 是允许的插件资源扩展名 → Content-Type。刻意不用系统 MIME
// 表：Windows 注册表常把 .js 报成 text/plain，而模块脚本必须带正确的 JavaScript
// MIME 才会被浏览器执行。
var pluginAssetExtTypes = map[string]string{
	".js":    "text/javascript; charset=utf-8",
	".mjs":   "text/javascript; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".json":  "application/json; charset=utf-8",
	".map":   "application/json; charset=utf-8",
	".html":  "text/html; charset=utf-8",
	".svg":   "image/svg+xml",
	".png":   "image/png",
	".jpg":   "image/jpeg",
	".jpeg":  "image/jpeg",
	".gif":   "image/gif",
	".webp":  "image/webp",
	".ico":   "image/x-icon",
	".woff":  "font/woff",
	".woff2": "font/woff2",
	".ttf":   "font/ttf",
	".otf":   "font/otf",
	".txt":   "text/plain; charset=utf-8",
	".md":    "text/markdown; charset=utf-8",
}

// PluginAssetMiddleware 返回插件资源中间件，由 main.go 接到 Assets.Middleware。
// Wails 把它包在资源处理器外层，所以 dev 与生产两条路径都会先经过这里。
func PluginAssetMiddleware(a *App) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if a == nil || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
				next.ServeHTTP(w, r)
				return
			}
			id, rel, ok := splitPluginAssetPath(r.URL.Path)
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			a.servePluginAsset(w, r, id, rel)
		})
	}
}

// splitPluginAssetPath 把 /plugins/<id>/<rel> 拆开；不是插件资源时返回 false，
// 交给后面的处理器（页面与前端 assets 走原路）。
func splitPluginAssetPath(urlPath string) (string, string, bool) {
	const prefix = "plugins/"
	trimmed := strings.TrimPrefix(urlPath, "/")
	if !strings.HasPrefix(trimmed, prefix) {
		return "", "", false
	}
	id, rel, found := strings.Cut(strings.TrimPrefix(trimmed, prefix), "/")
	if !found || rel == "" {
		return "", "", false
	}
	id = strings.ToLower(id)
	if !plugin.ValidID(id) {
		return "", "", false
	}
	return id, rel, true
}

func (a *App) servePluginAsset(w http.ResponseWriter, r *http.Request, id, rel string) {
	if a.disabledPluginSet()[id] {
		http.NotFound(w, r)
		return
	}
	resolved, info, err := resolvePluginAssetFile(filepath.Join(a.pluginRoot(), id), rel)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	// 先看文件是否存在、是不是目录（目录不是资源 → 404），再看类型白名单：否则
	// 一次目录请求会拿到“不允许的资源类型”，把用户往错方向带。
	if resolved == "" {
		http.NotFound(w, r)
		return
	}
	contentType, allowed := pluginAssetExtTypes[strings.ToLower(path.Ext(resolved))]
	if !allowed {
		http.Error(w, "不允许的插件资源类型", http.StatusForbidden)
		return
	}
	file, err := os.Open(resolved)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", contentType)
	// no-cache（可缓存但每次校验）而不是 no-store：重新导入插件后必须立刻拿到新
	// 文件，同时不必每次重新下载整个 bundle。
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, filepath.Base(resolved), info.ModTime(), file)
}

// resolvePluginAssetFile 把资源路径解成插件目录内的真实文件：先按原样找，找不到再看首段
// 是不是宿主加的缓存戳（`@<stamp>/…`），是就剥掉再找一次（见 plugin.StripCacheStamp）。
//
// 顺序不能反：先剥会把插件里真叫 `@foo/` 的目录解析成另一个文件。返回 ("", nil, nil)
// 表示文件不存在（调用方按 404 处理），非空 error 表示路径越出插件目录（403）。
func resolvePluginAssetFile(dir, rel string) (string, os.FileInfo, error) {
	resolved, err := plugin.ResolveAsset(dir, rel)
	if err != nil {
		return "", nil, err
	}
	if info, statErr := statAssetFile(resolved); statErr == nil {
		return resolved, info, nil
	}
	stripped, ok := plugin.StripCacheStamp(rel)
	if !ok {
		return "", nil, nil
	}
	resolved, err = plugin.ResolveAsset(dir, stripped)
	if err != nil {
		return "", nil, err
	}
	if info, statErr := statAssetFile(resolved); statErr == nil {
		return resolved, info, nil
	}
	return "", nil, nil
}

// statAssetFile 报告路径是不是一个可服务的普通文件（目录不是资源）。
func statAssetFile(path string) (os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%s 是目录", path)
	}
	return info, nil
}

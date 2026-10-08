// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

// orch_plugin.go 是插件系统的编排层：把 internal/tools/plugin 的纯算法绑定到
// *App（安装目录、配置持久化、主机白名单代理、存储落盘）。
//
// 约定：
//   - 安装目录 <appData>/plugins/<id>/；旧版本备份在 <appData>/plugins/.old/<id>/，
//     点开头所以不会被目录扫描当成插件。
//   - 启停状态存 config.json 的 disabledPlugins，与 disabledSkills 同构（清洗收口在
//     persistableConfig，加载侧直接读配置，不另设内存镜像）。
//   - 插件资源由 host_plugin_assets.go 的中间件在 /plugins/<id>/<entry> 提供；
//     与页面同源，所以插件可以用原生相对 import。
//   - 解包复用既有的 extractZip（biz_update.go），本文件只做「解包前的静态校验 +
//     解包后的原子搬运」，安全解压实现全局只有一份。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ally-dev/internal/tools/plugin"
)

// PluginPermissions 是插件声明的能力边界，直接来自清单（前端管理页用它做权限摘要）。
type PluginPermissions struct {
	HTTP      []string `json:"http,omitempty"`
	Workspace string   `json:"workspace"`
}

// PluginMenu 是插件页在侧栏的展示信息。icon 只传名字，组件由前端按内置图标白名单
// 解析，未知名字回退默认图标——插件不能往宿主的组件树里塞东西。
type PluginMenu struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Icon  string `json:"icon,omitempty"`
}

// PluginGroup 是插件声明的侧栏分组（一级菜单）：key 是身份（同 key 即同组），
// title / icon 只是展示。多个插件声明同一个 key 时，侧栏把它们合到同一个一级菜单下。
type PluginGroup struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Icon  string `json:"icon,omitempty"`
}

// PluginInfo 是管理页与侧栏共用的一条插件记录。
type PluginInfo struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Author      string            `json:"author,omitempty"`
	Description string            `json:"description,omitempty"`
	Entry       string            `json:"entry"`
	Menu        PluginMenu        `json:"menu"`
	Permissions PluginPermissions `json:"permissions"`
	// KeepAlive 是清单归一后的页面存活策略（前端据此选 v-show / v-if）：
	// true = 切走保留页面，false = 切走卸载、下次进来重建。
	KeepAlive bool `json:"keepAlive"`
	// Group 是插件声明的一级菜单（没声明时为 nil，侧栏把它放在顶层）。
	Group *PluginGroup `json:"group,omitempty"`
	// Mode 是侧栏页面的 key（plugin:<id>）：后端算好，避免两侧各拼一次。
	Mode         string `json:"mode"`
	Enabled      bool   `json:"enabled"`
	Dir          string `json:"dir"`
	PackageBytes int64  `json:"packageBytes"`
	HasPackage   bool   `json:"hasPackage"`
	EntryBytes   int64  `json:"entryBytes"`
	// UpdatedAt 同时被前端当作模块缓存标识：重新导入后它变化，插件 URL 上的
	// stamp 随之变化，于是新代码一定会被加载（而不是命中旧模块）。
	UpdatedAt string `json:"updatedAt"`
	// Valid=false 表示清单坏掉或入口缺失：这种插件只能被看到和删除。
	Valid     bool   `json:"valid"`
	LoadError string `json:"loadError,omitempty"`
}

// PluginHTTPRequest 是插件 HTTP 代理的入参。刻意只保留插件真正需要的字段：工具的
// saveTo / followRedirects / allowPrivateNetwork 等旋钮不暴露给插件。
type PluginHTTPRequest struct {
	PluginID string            `json:"pluginId"`
	Method   string            `json:"method,omitempty"`
	URL      string            `json:"url"`
	Headers  map[string]string `json:"headers,omitempty"`
	Query    map[string]string `json:"query,omitempty"`
	Body     string            `json:"body,omitempty"`
	JSON     json.RawMessage   `json:"json,omitempty"`
	Timeout  int               `json:"timeout,omitempty"`
	MaxBytes int               `json:"maxBytes,omitempty"`
}

// ── 目录与状态 ──

func (a *App) pluginRoot() string { return plugin.PluginRoot(appDataDir()) }

// disabledPluginSet 返回禁用名单的判定集合（读配置，不另设内存镜像）。
func (a *App) disabledPluginSet() map[string]bool {
	a.mu.Lock()
	ids := append([]string(nil), a.config.DisabledPlugins...)
	a.mu.Unlock()
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[plugin.IdentityKey(id)] = true
	}
	return set
}

// pluginInfos 扫描安装目录并组装前端要的记录。坏掉的插件同样返回（Valid=false +
// 原因），否则用户在管理页里既看不到也删不掉它。
func (a *App) pluginInfos() ([]PluginInfo, error) {
	installed, err := plugin.Discover(a.pluginRoot())
	if err != nil {
		return nil, err
	}
	disabled := a.disabledPluginSet()
	out := make([]PluginInfo, 0, len(installed))
	for _, item := range installed {
		info := PluginInfo{
			Dir:          item.Dir,
			PackageBytes: item.PackageBytes,
			HasPackage:   item.HasPackage,
			EntryBytes:   item.EntryBytes,
			Valid:        item.Valid,
			LoadError:    item.LoadError,
		}
		// 清单坏掉时 Manifest 是零值，KeepPageAlive() 会给默认 true——反正这种插件进不去。
		info.KeepAlive = item.Manifest.KeepPageAlive()
		if !item.UpdatedAt.IsZero() {
			// RFC3339Nano 而不是 RFC3339：UpdatedAt 兼作前端模块缓存戳（pluginCacheStamp
			// 滤掉标点后取整段），秒级精度下同一秒内连续两次覆盖升级会撞出同一个戳——
			// 入口 URL 不变、模块表命中旧实例，重装后跑的还是旧代码。
			info.UpdatedAt = item.UpdatedAt.UTC().Format(time.RFC3339Nano)
		}
		if item.Valid {
			info.ID = item.Manifest.ID
			info.Name = item.Manifest.Name
			info.Version = item.Manifest.Version
			info.Author = item.Manifest.Author
			info.Description = item.Manifest.Description
			info.Entry = item.Manifest.Entry
			info.Menu = PluginMenu{
				Key:   item.Manifest.Menu[0].Key,
				Title: item.Manifest.Menu[0].Title,
				Icon:  item.Manifest.Menu[0].Icon,
			}
			info.Permissions = PluginPermissions{
				HTTP:      item.Manifest.Permissions.HTTP,
				Workspace: item.Manifest.WorkspaceLevel(),
			}
			if group := item.Manifest.Group; group != nil {
				info.Group = &PluginGroup{Key: group.Key, Title: group.Title, Icon: group.Icon}
			}
			info.Mode = plugin.ModeKey(item.Manifest.ID)
		} else {
			// 清单坏掉时至少让 id 等于目录名，管理页才有东西可显示与删除。这里的 id
			// **保留盘上原样大小写**：它要拿去拼路径（禁用 / 删除 / 导出），比较一律走
			// IdentityKey。
			info.ID = filepath.Base(item.Dir)
			info.Name = info.ID
			info.Permissions.Workspace = plugin.WorkspaceNone
			info.Mode = plugin.ModeKey(info.ID)
		}
		info.Enabled = !disabled[plugin.IdentityKey(info.ID)]
		out = append(out, info)
	}
	return out, nil
}

// ListPlugins 返回已安装插件清单（管理页与侧栏菜单共用）。
func (a *App) ListPlugins() ([]PluginInfo, error) {
	if err := a.ensureInitialized(); err != nil {
		return nil, err
	}
	return a.pluginInfos()
}

// pluginDirID 归一前端传进来的插件身份，返回**拼路径用的原样名字**。校验用 IdentityKey
// （清单与目录名的字符集规则都是小写那一套），但拼路径必须用盘上真实的大小写——手工拷
// 进来的目录可能是 `Foo-Bar`，拿小写形态去拼会在大小写敏感的文件系统上找不到目标，于是
// 禁用、删除、导出全部静默失效。
func pluginDirID(raw string) (string, error) {
	id := strings.TrimSpace(raw)
	if !plugin.ValidID(plugin.IdentityKey(id)) {
		return "", codedToolError("E_PLUGIN_BAD_ID", fmt.Errorf("插件 id 非法：%q", raw))
	}
	return id, nil
}

// installedManifest 读取某个已安装插件的清单；未安装或清单非法时拒绝。
func (a *App) installedManifest(id string) (plugin.Manifest, string, error) {
	dirID, err := pluginDirID(id)
	if err != nil {
		return plugin.Manifest{}, "", err
	}
	dir := filepath.Join(a.pluginRoot(), dirID)
	data, readErr := os.ReadFile(filepath.Join(dir, plugin.ManifestName))
	if readErr != nil {
		return plugin.Manifest{}, "", codedToolError("E_PLUGIN_NOT_INSTALLED", fmt.Errorf("插件 %s 未安装", dirID))
	}
	manifest, parseErr := plugin.ParseManifest(data)
	if parseErr != nil {
		return plugin.Manifest{}, "", codedToolError("E_PLUGIN_BAD_MANIFEST", parseErr)
	}
	if plugin.IdentityKey(manifest.ID) != plugin.IdentityKey(dirID) {
		return plugin.Manifest{}, "", codedToolError("E_PLUGIN_BAD_ID", fmt.Errorf("目录名 %s 与清单 id %s 不一致", dirID, manifest.ID))
	}
	return manifest, dir, nil
}

// installedDir 只校验「目录名是合法 id 且确实在插件根下」，不解析清单：清单坏掉的
// 插件也必须能被删除 / 导出（否则用户在管理页里既看不到出口，也删不掉它，只能手动
// 去数据目录里翻）。清单不可用时第二个返回值是 nil。
func (a *App) installedDir(id string) (string, *plugin.Manifest, error) {
	dirID, err := pluginDirID(id)
	if err != nil {
		return "", nil, err
	}
	dir := filepath.Join(a.pluginRoot(), dirID)
	stat, statErr := os.Stat(dir)
	if statErr != nil || !stat.IsDir() {
		return "", nil, codedToolError("E_PLUGIN_NOT_INSTALLED", fmt.Errorf("插件 %s 未安装", dirID))
	}
	data, readErr := os.ReadFile(filepath.Join(dir, plugin.ManifestName))
	if readErr != nil {
		return dir, nil, nil
	}
	manifest, parseErr := plugin.ParseManifest(data)
	if parseErr != nil || plugin.IdentityKey(manifest.ID) != plugin.IdentityKey(dirID) {
		return dir, nil, nil
	}
	return dir, &manifest, nil
}

// ── 安装 ──

// ImportPlugin 从 zip 包安装插件：静态校验 → 安全解包到暂存目录 → 备份旧版本 →
// 原子搬进安装目录 → 留存原始包副本（导出即原样复制，保证导入导出一致）。
func (a *App) ImportPlugin(srcPath string) (PluginInfo, error) {
	if err := a.ensureInitialized(); err != nil {
		return PluginInfo{}, err
	}
	srcPath = strings.TrimSpace(srcPath)
	if srcPath == "" {
		return PluginInfo{}, codedToolError("E_PLUGIN_BAD_PATH", errors.New("插件包路径为空"))
	}
	stat, err := os.Stat(srcPath)
	if err != nil || stat.IsDir() {
		return PluginInfo{}, codedToolError("E_PLUGIN_BAD_PATH", fmt.Errorf("插件包不存在或不是文件：%s", srcPath))
	}
	// 包**本体**也要卡上限：InspectPackage 只验解压后的体积，而这份原始包会原样留存
	// 在插件目录里供导出直接复制。一个用 store 模式塞满填充字节的包（几乎不压缩）
	// 能在解压体积合规的同时把几百 MB 留在用户磁盘上。
	if stat.Size() > int64(plugin.MaxPackageBytes) {
		return PluginInfo{}, codedToolError("E_PLUGIN_BAD_PACKAGE",
			fmt.Errorf("插件包本身 %d 字节，超过上限 %d", stat.Size(), plugin.MaxPackageBytes))
	}
	// 安装是「挪旧版到 .old → 新内容上位」两步，并发跑会互相毁掉备份（见 App.pluginMu）。
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	info, err := plugin.InspectPackage(srcPath)
	if err != nil {
		return PluginInfo{}, codedToolError("E_PLUGIN_BAD_PACKAGE", err)
	}
	if err := a.ensurePluginRoot(); err != nil {
		return PluginInfo{}, err
	}
	stage, err := os.MkdirTemp(a.pluginRoot(), ".stage-")
	if err != nil {
		return PluginInfo{}, err
	}
	defer os.RemoveAll(stage)
	if err := extractZip(srcPath, stage); err != nil {
		return PluginInfo{}, codedToolError("E_PLUGIN_BAD_PACKAGE", err)
	}
	// 解包**之后**立刻按真实字节复验整个暂存目录：包里除了插件内容之外还可以塞别的
	// 东西（哪怕它们最后会被清掉），解包本身已经把磁盘写满了，所以不能只验 contentDir。
	if err := plugin.VerifyContentSizes(stage); err != nil {
		return PluginInfo{}, codedToolError("E_PLUGIN_BAD_PACKAGE", err)
	}
	contentDir := stage
	if info.Prefix != "" {
		contentDir = filepath.Join(stage, filepath.FromSlash(info.Prefix))
		// 外层目录来自包内条目名（safeEntryName 已挡掉 `..`/绝对路径），这里再断言
		// 一次拼接结果确实落在暂存目录里。
		if !plugin.PathWithin(stage, contentDir) {
			return PluginInfo{}, codedToolError("E_PLUGIN_BAD_PACKAGE", fmt.Errorf("插件包的外层目录越出包根：%s", info.Prefix))
		}
	}
	target, err := a.installPluginContent(info.Manifest.ID, contentDir)
	if err != nil {
		return PluginInfo{}, err
	}
	// 留存原始包供导出直接复制。包里带了 data.json 的那份要重写成“无数据形态”：
	// 插件数据已经搬到数据目录，包本体里再留一份就会让「含数据导出 → 对方导入 →
	// 对方默认导出」把凭据一路传下去（默认导出必须真的不含数据）。
	// 原始包副本不是插件可用性的前置条件（导出会退化成按目录重新打包，见
	// writePluginExport），所以存不上不能把整次导入判死——那时插件已经装好了，报失败
	// 只会让用户对着一个「其实装上了」的插件反复重试。但**半截的 package.zip 必须清掉**：
	// 留着它，导出会直接复制出一个打不开的坏包（用户拿到手才发现）。
	if err := a.storePristinePackage(srcPath, target, plugin.ContainsFile(info.Files, dataEntryName(info.Prefix))); err != nil {
		a.logAppError("plugin pristine package not stored", "id", info.Manifest.ID, "error", err.Error())
		if removeErr := os.Remove(filepath.Join(target, plugin.PackageName)); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return PluginInfo{}, removeErr
		}
	}
	// 包里的 data.json 坏了按「没带数据」降级，不中断安装（见 adoptPackagedData）。
	if err := a.adoptPackagedData(info.Manifest.ID, target); err != nil {
		return PluginInfo{}, err
	}
	return a.pluginInfoByID(info.Manifest.ID)
}

// dataEntryName 返回包里 data.json 的条目名（带可选的外层目录前缀）。
func dataEntryName(prefix string) string {
	if prefix == "" {
		return plugin.DataName
	}
	return prefix + "/" + plugin.DataName
}

// ImportPluginFromDir 从本机一个已展开的目录安装插件。
//
// 刻意不限定目录位置：这条路径由用户用原生目录选择框触发（插件自己不能写工作区，
// 也种不下包），而真正的闸门是清单/体积/路径三类校验——它们与 zip 路径**完全同一套**，
// 换一个来源不会降低校验强度。限制目录位置只会拦住用户自己的源码目录。
func (a *App) ImportPluginFromDir(dir string) (PluginInfo, error) {
	if err := a.ensureInitialized(); err != nil {
		return PluginInfo{}, err
	}
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return PluginInfo{}, codedToolError("E_PLUGIN_BAD_PATH", errors.New("目录为空"))
	}
	resolved, err := filepath.Abs(dir)
	if err != nil {
		return PluginInfo{}, codedToolError("E_PLUGIN_BAD_PATH", err)
	}
	if stat, statErr := os.Stat(resolved); statErr != nil || !stat.IsDir() {
		return PluginInfo{}, codedToolError("E_PLUGIN_BAD_PATH", fmt.Errorf("目录不存在或不是目录：%s", dir))
	}
	info, err := plugin.InspectDir(resolved)
	if err != nil {
		return PluginInfo{}, codedToolError("E_PLUGIN_BAD_PACKAGE", err)
	}
	// 与 zip 路径同一把锁：安装/删除的每一步都不是原子的，串行化才能保住备份（见
	// App.pluginMu）。锁只覆盖「动 plugins/ 目录」这一段——扫描源目录不该占着它。
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	if err := a.ensurePluginRoot(); err != nil {
		return PluginInfo{}, err
	}
	stage, err := os.MkdirTemp(a.pluginRoot(), ".stage-")
	if err != nil {
		return PluginInfo{}, err
	}
	defer os.RemoveAll(stage)
	contentDir := filepath.Join(stage, "content")
	if err := plugin.CopyTree(resolved, contentDir); err != nil {
		return PluginInfo{}, err
	}
	// 与 zip 路径同一套落位规则：用户选到「插件文件夹的父目录」时，InspectDir 会把那个
	// 外层目录记成 Prefix，内容必须照它再进一层——否则装出来的插件多嵌套一层、入口文件
	// 找不到，而界面上只看到一行「不可用」。
	if info.Prefix != "" {
		nested := filepath.Join(contentDir, filepath.FromSlash(info.Prefix))
		if !plugin.PathWithin(contentDir, nested) {
			return PluginInfo{}, codedToolError("E_PLUGIN_BAD_PACKAGE", fmt.Errorf("插件目录的外层目录越出根目录：%s", info.Prefix))
		}
		contentDir = nested
	}
	target, err := a.installPluginContent(info.Manifest.ID, contentDir)
	if err != nil {
		return PluginInfo{}, err
	}
	// 源目录里若有 data.json，同样按“插件数据”处理（与 zip 路径一致）。
	if err := a.adoptPackagedData(info.Manifest.ID, target); err != nil {
		return PluginInfo{}, err
	}
	return a.pluginInfoByID(info.Manifest.ID)
}

// installPluginContent 把已校验的内容目录搬到 plugins/<id>/，旧版本先挪到 .old 备份。
// 第二步失败时把备份挪回原位——否则插件会在安装位置凭空消失（既不能用也回不去），
// 界面上表现为“插件自己没了”。
//
// 体积与条目数在这里按**真实字节**复验一次（两条安装路径共用这处，不会漏）：zip
// 头声明的尺寸是可伪造的，而 extractZip 用的是自更新那套宽松限额，所以解包前那道
// 静态校验顶不住一个谎报尺寸的包（见 plugin.VerifyContentSizes）。
func (a *App) installPluginContent(id, contentDir string) (string, error) {
	if err := plugin.VerifyContentSizes(contentDir); err != nil {
		return "", codedToolError("E_PLUGIN_BAD_PACKAGE", err)
	}
	target := filepath.Join(a.pluginRoot(), id)
	backup := ""
	if _, err := os.Stat(target); err == nil {
		backupRoot := filepath.Join(a.pluginRoot(), plugin.BackupDirName)
		if err := os.MkdirAll(backupRoot, 0o700); err != nil {
			return "", err
		}
		previous := filepath.Join(backupRoot, id)
		// 只保留一份上一版本：先清掉更早的备份，再做改名（Windows 下 rename 不覆盖）。
		if err := os.RemoveAll(previous); err != nil {
			return "", err
		}
		if err := os.Rename(target, previous); err != nil {
			return "", err
		}
		backup = previous
	}
	if err := os.Rename(contentDir, target); err != nil {
		if backup != "" {
			if restoreErr := os.Rename(backup, target); restoreErr != nil {
				return "", fmt.Errorf("%w（旧版本回滚也失败：%v）", err, restoreErr)
			}
		}
		return "", err
	}
	return target, nil
}

// storePristinePackage 把导入的包留一份供导出直接复制（round-trip 逐字节一致）。
// packagedData=true 说明包里带了 data.json：插件数据已经搬到数据目录，留存的那份
// 必须重写成“无数据形态”，否则默认导出会把上一次带进来的凭据再带出去。
func (a *App) storePristinePackage(srcPath, target string, packagedData bool) error {
	dest := filepath.Join(target, plugin.PackageName)
	if !packagedData {
		return copyFile(srcPath, dest)
	}
	// 临时文件刻意建在插件根目录（而不是 target 里），名字以 `.` 开头：下面这趟
	// WritePackage 重新打包的正是 target，临时文件落在里面会被它自己扫进去——包里凭空
	// 多出一个 0 字节的 `package.zip.tmp`，并被之后每一次默认导出一路传下去。点开头
	// 同时保证目录扫描（Discover）与打包都不会把它当成插件内容。
	file, err := os.CreateTemp(a.pluginRoot(), ".pkg-*.tmp")
	if err != nil {
		return err
	}
	temp := file.Name()
	writeErr := plugin.WritePackage(file, target, nil)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(temp)
		if writeErr != nil {
			return writeErr
		}
		return closeErr
	}
	if err := os.Rename(temp, dest); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

// adoptPackagedData 处理包里自带的 data.json（导出时勾了「包含数据」）：插件数据
// 统一住在 plugins/.data/<id>.json，所以把它从插件目录搬过去。包里有数据即导入并
// 覆盖——这正是「连数据一起导出」的用途；包里没带则保留现有数据（升级不丢设置）。
//
// 包里那份数据**坏了怎么办**：调用到这里时插件本体已经落位（旧版本已被顶替），把
// 错误抛回去就会变成「界面说导入失败、插件其实装好了」——用户会去重试，而重试只会
// 再顶替一次。所以坏数据按「包里没带数据」降级：记一笔日志、删掉它、保留现有数据。
// 顺序是**先验后删**：反过来会在坏数据这条分支上把文件内容连同判定依据一起丢掉，
// 而这份数据正是用户上一次勾了「包含数据」才带进来的。
func (a *App) adoptPackagedData(id, target string) error {
	packaged := filepath.Join(target, plugin.DataName)
	if _, err := os.Stat(packaged); err != nil {
		return nil
	}
	data, readErr := os.ReadFile(packaged)
	valid := readErr == nil && json.Valid(data) && len(data) <= plugin.MaxStoreBytes
	if valid {
		if err := os.Remove(packaged); err != nil {
			return err
		}
		path := plugin.DataPath(a.pluginRoot(), id)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		return writeAtomicBytes(path, data, 0o600)
	}
	// 坏的 / 读不出来的：留在插件目录里会被下一次导出带出去（导出按目录重新打包），
	// 所以照样删掉；删不掉才报错（那时插件目录本身也有问题）。
	if readErr != nil {
		a.logAppError("plugin packaged data unreadable, ignored", "id", id, "error", readErr.Error())
	} else if !json.Valid(data) {
		a.logAppError("plugin packaged data is not valid JSON, ignored", "id", id)
	} else {
		a.logAppError("plugin packaged data exceeds store limit, ignored", "id", id, "bytes", len(data))
	}
	if err := os.Remove(packaged); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// ensurePluginRoot 建好插件根目录（0700，与其它应用数据一致）。
func (a *App) ensurePluginRoot() error {
	return os.MkdirAll(a.pluginRoot(), 0o700)
}

func (a *App) pluginInfoByID(id string) (PluginInfo, error) {
	infos, err := a.pluginInfos()
	if err != nil {
		return PluginInfo{}, err
	}
	for _, info := range infos {
		if plugin.IdentityKey(info.ID) == plugin.IdentityKey(id) {
			return info, nil
		}
	}
	return PluginInfo{}, codedToolError("E_PLUGIN_NOT_INSTALLED", fmt.Errorf("插件 %s 未安装", id))
}

// ── 导出与删除 ──

// ExportPlugin 把插件导出成 zip。默认只导包本体：有原始包副本时直接复制（逐字节
// 一致），否则按目录重新打包；includeData=true 时把 data.json 一并打进包里。
func (a *App) ExportPlugin(id string, includeData bool) (string, error) {
	if err := a.ensureInitialized(); err != nil {
		return "", err
	}
	dirID, err := pluginDirID(id)
	if err != nil {
		return "", err
	}
	dir, manifest, err := a.installedDir(dirID)
	if err != nil {
		return "", err
	}
	suggested := dirID + ".zip"
	if manifest != nil {
		suggested = fmt.Sprintf("%s-%s.zip", manifest.ID, manifest.Version)
	}
	dest, err := a.promptSavePluginPackage(suggested)
	if err != nil || dest == "" {
		return dest, err
	}
	// writePluginExport 只用 id 定位插件数据文件（一直是按清单 id 落盘的），所以这里
	// 传归一后的身份键。
	if err := a.writePluginExport(plugin.IdentityKey(dirID), dir, includeData, dest); err != nil {
		return "", err
	}
	return dest, nil
}

// writePluginExport 是导出里“对话框之外”的全部逻辑（所以可以直接测试）：无数据时
// 优先复制留存的那份原始包，否则按目录重新打包。
func (a *App) writePluginExport(id, dir string, includeData bool, dest string) error {
	source := filepath.Join(dir, plugin.PackageName)
	if !includeData {
		if stat, statErr := os.Stat(source); statErr == nil && !stat.IsDir() {
			return copyFile(source, dest)
		}
	}
	// 重新打包的两种情况：目录安装来的插件没有原始包；或用户要连数据一起导。
	var data []byte
	if includeData {
		if stored, readErr := os.ReadFile(plugin.DataPath(a.pluginRoot(), id)); readErr == nil {
			data = stored
		}
	}
	file, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	return plugin.WritePackage(file, dir, data)
}

// DeletePlugin 删掉插件目录（连同上一版本备份）；purgeData 决定是否连插件数据一起删。
// 数据住在 plugins/.data/<id>.json，所以“删插件保留数据”是可实现的：重装同一插件
// 后设置还在。删除只要求“目录名合法且在插件根下”——清单坏掉的插件同样要能删掉，
// 否则用户在管理页里既找不到出口，也删不掉它（只能手动去数据目录里翻）。
func (a *App) DeletePlugin(id string, purgeData bool) error {
	if err := a.ensureInitialized(); err != nil {
		return err
	}
	dirID, err := pluginDirID(id)
	if err != nil {
		return err
	}
	// 删除与安装互斥：否则「删到一半时有人覆盖升级」会留下一个既不在安装位置、也没被
	// 删干净的插件（见 App.pluginMu）。
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	if _, _, err := a.installedDir(dirID); err != nil {
		return err
	}
	root := a.pluginRoot()
	// 目录与备份按**盘上原样**名字删（大小写敏感的文件系统上才算得准），数据文件按归一后
	// 的身份键删——它一直是按清单 id 落盘的（见 PluginStoreSet）。
	if err := os.RemoveAll(filepath.Join(root, dirID)); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(root, plugin.BackupDirName, dirID)); err != nil {
		return err
	}
	if purgeData {
		dataPath := plugin.DataPath(root, plugin.IdentityKey(dirID))
		if err := os.Remove(dataPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// ── 启停 ──

// SetPluginEnabled 写启停状态。插件目录必须存在：往配置里塞不存在的 id 只会让
// 名单越积越脏。
func (a *App) SetPluginEnabled(id string, enabled bool) error {
	if err := a.ensureInitialized(); err != nil {
		return err
	}
	dirID, err := pluginDirID(id)
	if err != nil {
		return err
	}
	// 与安装/删除互斥：否则「目录校验刚过、插件就被删掉」会把一个不存在的 id 写进名单。
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	if _, statErr := os.Stat(filepath.Join(a.pluginRoot(), dirID)); statErr != nil {
		return codedToolError("E_PLUGIN_NOT_INSTALLED", fmt.Errorf("插件 %s 未安装", dirID))
	}
	// 名单里存的是归一后的比较键（小写），列表侧同样用 IdentityKey 查——两边只有一套
	// 判「同一个插件」的规则，大小写不一致才不会让开关自己弹回去。
	key := plugin.IdentityKey(dirID)
	return a.updateConfigAndPersist(func(cfg *ConfigState) {
		next := make([]string, 0, len(cfg.DisabledPlugins)+1)
		for _, existing := range cfg.DisabledPlugins {
			if plugin.IdentityKey(existing) == key {
				continue
			}
			next = append(next, existing)
		}
		if !enabled {
			next = append(next, key)
		}
		cfg.DisabledPlugins = next
	})
}

// ── 插件存储 ──

// PluginStoreGet 读插件自己的 data.json（不存在返回空串）。
func (a *App) PluginStoreGet(id string) (string, error) {
	if err := a.ensureInitialized(); err != nil {
		return "", err
	}
	manifest, _, err := a.installedManifest(id)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(plugin.DataPath(a.pluginRoot(), manifest.ID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

// PluginStoreSet 写插件自己的 data.json（要求是合法 JSON，且有体积上限）。
func (a *App) PluginStoreSet(id, value string) error {
	if err := a.ensureInitialized(); err != nil {
		return err
	}
	manifest, _, err := a.installedManifest(id)
	if err != nil {
		return err
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return codedToolError("E_PLUGIN_BAD_STORE", errors.New("存储内容为空"))
	}
	if len(trimmed) > plugin.MaxStoreBytes {
		return codedToolError("E_PLUGIN_BAD_STORE", fmt.Errorf("存储内容超过上限 %d 字节", plugin.MaxStoreBytes))
	}
	if !json.Valid([]byte(trimmed)) {
		return codedToolError("E_PLUGIN_BAD_STORE", errors.New("存储内容必须是合法 JSON"))
	}
	return writeAtomicBytes(plugin.DataPath(a.pluginRoot(), manifest.ID), []byte(trimmed), 0o600)
}

// ── HTTP 代理 ──

// PluginHTTPRequest 代插件发出 HTTP 请求。目标主机必须命中插件清单里声明的
// permissions.http（默认拒绝，含重定向后的目标），所以这里把白名单交给统一的
// 目标判定，而不是开关一个全局的「允许内网」。
func (a *App) PluginHTTPRequest(req PluginHTTPRequest) (HTTPRequestToolResult, error) {
	if err := a.ensureInitialized(); err != nil {
		return HTTPRequestToolResult{}, err
	}
	manifest, _, err := a.installedManifest(req.PluginID)
	if err != nil {
		return HTTPRequestToolResult{}, err
	}
	if len(manifest.Permissions.HTTP) == 0 {
		return HTTPRequestToolResult{}, codedToolError("E_PLUGIN_HOST_DENIED",
			fmt.Errorf("插件 %s 没有声明 permissions.http，不能发起网络请求", manifest.ID))
	}
	if strings.TrimSpace(req.URL) == "" {
		return HTTPRequestToolResult{}, codedToolError("E_PLUGIN_BAD_REQUEST", errors.New("url 不能为空"))
	}
	cfg, err := a.getConfig()
	if err != nil {
		return HTTPRequestToolResult{}, err
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	fetched, err := a.doHTTPRequest(ctx, cfg, HTTPRequestToolRequest{
		Method:  req.Method,
		URL:     req.URL,
		Headers: req.Headers,
		Query:   req.Query,
		Body:    req.Body,
		JSON:    req.JSON,
		Timeout: req.Timeout,
		// 插件不落盘：要写文件请用 host.files（受工作区权限约束）。
		MaxBytes: req.MaxBytes,
		// 白名单交给统一的请求目标判定：名单说了算（默认拒绝，重定向目标同样复判）。
		// 函数第三个参数 true 只作用于传输层——不去拦已声明主机解析出的内网地址
		// （公司 Jira 就是内网地址）；非声明主机在发出请求前就被拒了。
		AllowedHosts: manifest.Permissions.HTTP,
	}, false, true)
	if err != nil {
		return HTTPRequestToolResult{}, err
	}
	return fetched.Result, nil
}

// copyFile 复制文件（导出插件包用）。目标权限 0600：导出的是用户的插件包，不需要
// 别的本机用户可读。源与目标是同一个文件时直接拒绝：O_TRUNC 会先把源截成 0 字节，
// 于是用户在保存框里选到插件自己的 package.zip 时，包会当场变空。
func copyFile(src, dst string) error {
	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()
	if srcInfo, statErr := source.Stat(); statErr == nil {
		if dstInfo, dstErr := os.Stat(dst); dstErr == nil && os.SameFile(srcInfo, dstInfo) {
			return errors.New("目标就是源文件本身，不能就地覆盖")
		}
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	target, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer target.Close()
	_, err = io.Copy(target, source)
	return err
}

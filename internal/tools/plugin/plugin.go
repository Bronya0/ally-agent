// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
//
// Package plugin 是插件系统的纯算法层：清单解析与校验、主机白名单匹配、插件目录
// 扫描、包内路径解析。它刻意不依赖 internal/app（不碰 *App、ConfigState、工作区
// 授权），所以可以被编排层与测试自由引用。
//
// 边界：安全解包**不在这里**——编排层复用既有 extractZip（见 internal/app/
// biz_update.go），本包只做解包前的静态校验（条目数、体积、路径形状），保证
// 安全解压实现全局只有一份。
package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// 插件目录布局与固定文件名。改变其中任何一个都会同时影响：安装目录、导出包、
// 资源 URL（/plugins/<id>/<entry>）与前端 pluginHost.mjs，所以全部收口在这里。
const (
	ManifestName  = "plugin.json"
	PackageName   = "package.zip"
	DataName      = "data.json"
	DirName       = "plugins"
	BackupDirName = ".old"
	ModePrefix    = "plugin:"
)

// DataPath 是插件数据文件的路径。数据刻意不住在插件目录里：删除插件时可以保留
// 数据、重装后接着用，导出时再按需把它打回包里。点开头的目录不会被目录扫描当成
// 插件（Discover）。
func DataPath(root, id string) string {
	return filepath.Join(root, ".data", id+".json")
}

// ModeKey 是一级菜单 key 与「当前页面」标识的唯一拼法（前端不允许自己拼）。
func ModeKey(id string) string { return ModePrefix + id }

// ModeID 从菜单 key 反解插件 id；不是插件页时返回空串。
func ModeID(mode string) string {
	mode = strings.TrimSpace(mode)
	if !strings.HasPrefix(mode, ModePrefix) {
		return ""
	}
	id := strings.TrimPrefix(mode, ModePrefix)
	if !ValidID(id) {
		return ""
	}
	return id
}

// 包体积上限：插件只承载前端资源，比自更新包的限额更严。
const (
	MaxPackageEntries   = 512
	MaxPackageBytes     = 32 << 20 // 解压后总字节
	MaxPackageFileBytes = 8 << 20  // 单个文件
	MaxManifestBytes    = 64 << 10
	MaxStoreBytes       = 1 << 20 // 插件自己的 data.json
)

// 工作区权限级别。
const (
	WorkspaceNone  = "none"
	WorkspaceRead  = "read"
	WorkspaceWrite = "write"
)

// MenuItem 是插件声明的侧栏一级菜单项。
type MenuItem struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Icon  string `json:"icon,omitempty"`
}

// Permissions 是插件声明的能力边界。默认全不给：没有声明的域名与工作区写级别
// 一律拒绝（见 HostAllowed 与编排层的文件能力）。
type Permissions struct {
	HTTP      []string `json:"http,omitempty"`
	Workspace string   `json:"workspace,omitempty"`
}

// Manifest 是 plugin.json 的结构。
// MenuGroup 是插件声明的侧栏分组（一级菜单）。多个插件声明同一个 key 时，侧栏会把
// 它们合到同一个一级菜单下——插件之间不需要互相知道，也就没有耦合；同一个组里
// title / icon 以先声明者为准（插件各自独立，宿主不做汇总），所以同组插件应当写成一样。
type MenuGroup struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Icon  string `json:"icon,omitempty"`
}

type Manifest struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Version     string      `json:"version"`
	Author      string      `json:"author,omitempty"`
	Description string      `json:"description,omitempty"`
	Entry       string      `json:"entry"`
	Menu        []MenuItem  `json:"menu"`
	Permissions Permissions `json:"permissions"`
	// KeepAlive 决定插件页切走时是保留还是销毁（前端 v-show / v-if）。用指针是为了区分
	// “没写”（默认保留）与“显式写 false”（切走销毁）——布尔零值分不出这两种。
	KeepAlive *bool `json:"keepAlive,omitempty"`
	// Group 可选：声明后该插件的页面归到这个一级菜单下；不写就留在侧栏顶层。
	Group *MenuGroup `json:"group,omitempty"`
}

var (
	idPattern      = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,48}$`)
	versionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	hostPattern    = regexp.MustCompile(`^(\*\.)?[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)
	iconPattern    = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]{0,31}$`)
	// cacheStampPattern 是资源 URL 里缓存戳段的形状（前端 pluginHost.mjs 按同一规则生成）：
	// `@` + 字母数字与 -_，刻意不含点，`..` 之类不用单独防。
	cacheStampPattern = regexp.MustCompile(`^@[A-Za-z0-9_-]{1,64}$`)
)

// ValidID 报告插件 id 是否合法。id 会直接当目录名用，所以字符集必须窄到不可能
// 出现路径分隔符、点开头或空段。
func ValidID(id string) bool {
	return idPattern.MatchString(strings.TrimSpace(id))
}

// IdentityKey 是判「同一个插件」的比较键：目录名、清单 id、配置里的禁用名单三处共用它。
// 目录名可能是人手拷出来的大写形态、配置可能是人手改过的，各自 trim/lower 一遍就会分叉
// ——分叉的后果是禁用与删除静默对着一个不存在的目标操作。
func IdentityKey(id string) string { return strings.ToLower(strings.TrimSpace(id)) }

// ParseManifest 解析并校验清单。
func ParseManifest(data []byte) (Manifest, error) {
	if len(data) > MaxManifestBytes {
		return Manifest{}, fmt.Errorf("清单文件超过 %d 字节", MaxManifestBytes)
	}
	var m Manifest
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("解析 %s 失败：%w", ManifestName, err)
	}
	m.ID = strings.TrimSpace(m.ID)
	m.Name = strings.TrimSpace(m.Name)
	m.Version = strings.TrimSpace(m.Version)
	entry, err := NormalizeEntry(m.Entry)
	if err != nil {
		return Manifest{}, err
	}
	m.Entry = entry
	m.Permissions.HTTP = NormalizeHostPatterns(m.Permissions.HTTP)
	m.Permissions.Workspace = strings.ToLower(strings.TrimSpace(m.Permissions.Workspace))
	if m.Group != nil {
		m.Group.Key = strings.ToLower(strings.TrimSpace(m.Group.Key))
		m.Group.Title = strings.TrimSpace(m.Group.Title)
		m.Group.Icon = strings.TrimSpace(m.Group.Icon)
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// NormalizeEntry 把入口路径统一成包内斜杠相对路径。拒绝空值、绝对路径与跑出包的
// `../`——入口写错就应该直接报错，而不是被静默改写成另一个文件（那样作者永远
// 不会发现自己写错了）。
func NormalizeEntry(entry string) (string, error) {
	entry = strings.TrimSpace(strings.ReplaceAll(entry, "\\", "/"))
	if entry == "" {
		return "", errors.New("entry 不能为空")
	}
	if strings.HasPrefix(entry, "/") || filepath.IsAbs(entry) || filepath.VolumeName(entry) != "" {
		return "", fmt.Errorf("entry 必须是包内相对路径，当前是 %q", entry)
	}
	clean := path.Clean(entry)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("entry 不能引用包外路径，当前是 %q", entry)
	}
	return clean, nil
}

// Validate 校验清单字段。错误信息直接呈现给用户（管理页），所以用中文且说清原因。
func (m Manifest) Validate() error {
	if !ValidID(m.ID) {
		return errors.New("id 必须是 2-49 位小写字母/数字/._-，且以字母或数字开头")
	}
	if m.Name == "" {
		return errors.New("name 不能为空")
	}
	if !versionPattern.MatchString(m.Version) {
		return fmt.Errorf("version 必须是 X.Y.Z 形式，当前是 %q", m.Version)
	}
	if _, err := NormalizeEntry(m.Entry); err != nil {
		return err
	}
	if len(m.Menu) != 1 {
		return fmt.Errorf("v1 每个插件只支持一个页面，menu 必须恰好一项（当前 %d 项）", len(m.Menu))
	}
	item := m.Menu[0]
	if strings.TrimSpace(item.Title) == "" {
		return errors.New("menu[0].title 不能为空")
	}
	if item.Icon != "" && !iconPattern.MatchString(item.Icon) {
		return fmt.Errorf("menu[0].icon 只接受内置图标名（字母数字），当前是 %q", item.Icon)
	}
	if m.Group != nil {
		// 组 key 会当成菜单 key 的一部分（group:<key>），所以字符集与 id 同一套。
		if !ValidID(m.Group.Key) {
			return fmt.Errorf("group.key 必须是 2-49 位小写字母/数字/._-，且以字母或数字开头，当前是 %q", m.Group.Key)
		}
		if m.Group.Title == "" {
			return errors.New("group.title 不能为空")
		}
		if len([]rune(m.Group.Title)) > 24 {
			return errors.New("group.title 最长 24 个字（菜单栏放不下）")
		}
		if m.Group.Icon != "" && !iconPattern.MatchString(m.Group.Icon) {
			return fmt.Errorf("group.icon 只接受内置图标名（字母数字），当前是 %q", m.Group.Icon)
		}
	}
	// v1 的 host.files 只读：声明 write 直接拒绝，而不是给一个假权限（管理页会把它
	// 显示成“可写”，插件却调不到任何写 API）。真支持写之后再把 WorkspaceWrite 放回来。
	switch m.WorkspaceLevel() {
	case WorkspaceNone, WorkspaceRead:
	case WorkspaceWrite:
		return errors.New("v1 暂不支持 permissions.workspace=\"write\"，请改成 \"read\" 或 \"none\"")
	default:
		return fmt.Errorf("permissions.workspace 只能是 none/read，当前是 %q", m.Permissions.Workspace)
	}
	for _, raw := range m.Permissions.HTTP {
		if !hostPattern.MatchString(raw) {
			return fmt.Errorf("permissions.http 里的 %q 不是合法主机名（可用 *.example.com 通配）", raw)
		}
	}
	return nil
}

// WorkspaceLevel 返回归一后的工作区权限级别（空值即 none）。
func (m Manifest) WorkspaceLevel() string {
	level := strings.ToLower(strings.TrimSpace(m.Permissions.Workspace))
	if level == "" {
		return WorkspaceNone
	}
	return level
}

// KeepPageAlive 返回归一后的页面存活策略：没声明即 true（切走时保留页面）。
// 归一收口在这里，前端只用 PluginInfo.KeepAlive 这个已解析的值。
func (m Manifest) KeepPageAlive() bool {
	return m.KeepAlive == nil || *m.KeepAlive
}

// NormalizeHostPatterns 归一主机白名单：小写、去重、保序。非法模式保留原样，
// 由 Validate 报错（这里不静默丢弃，否则声明写错会变成"静默放行得更宽"）。
func NormalizeHostPatterns(patterns []string) []string {
	if len(patterns) == 0 {
		return nil
	}
	out := make([]string, 0, len(patterns))
	seen := map[string]bool{}
	for _, raw := range patterns {
		pattern := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(raw, ".")))
		if pattern == "" || seen[pattern] {
			continue
		}
		seen[pattern] = true
		out = append(out, pattern)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// NormalizeIDList 清洗配置里的禁用插件名单（小写、去重、保序、丢弃非法 id）。
func NormalizeIDList(ids []string) []string {
	if ids == nil {
		return nil
	}
	out := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, raw := range ids {
		id := IdentityKey(raw)
		if id == "" || seen[id] || !ValidID(id) {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// HostAllowed 报告 host 是否命中白名单。模式两种形态：`jira.corp.com` 精确主机，
// `*.corp.com` 匹配该域本身与它的任意子域。空名单 = 谁都不许（默认拒绝）。
//
// 这是主机判定的唯一实现：插件 HTTP 代理的请求目标与重定向目标都走它。
func HostAllowed(host string, patterns []string) bool {
	host = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(host, ".")))
	if host == "" {
		return false
	}
	for _, pattern := range patterns {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		if pattern == "" {
			continue
		}
		if base, ok := strings.CutPrefix(pattern, "*."); ok {
			if base != "" && (host == base || strings.HasSuffix(host, "."+base)) {
				return true
			}
			continue
		}
		if host == pattern {
			return true
		}
	}
	return false
}

// Installed 是一次目录扫描的结果。清单坏掉的插件也要报出来（Valid=false +
// LoadError），否则用户在管理页里既看不到也删不掉它。
type Installed struct {
	Manifest     Manifest
	Dir          string
	Valid        bool
	LoadError    string
	PackageBytes int64
	HasPackage   bool
	EntryBytes   int64
	UpdatedAt    time.Time
}

// PluginRoot 返回插件安装根目录（<appData>/plugins）。
func PluginRoot(appDataDir string) string {
	return filepath.Join(appDataDir, DirName)
}

// Discover 扫描插件根目录。跳过点开头的条目（含 .old 备份目录与临时目录），
// 结果按 id 排序保证列表稳定。
func Discover(root string) ([]Installed, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]Installed, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || strings.HasPrefix(name, ".") {
			continue
		}
		dir := filepath.Join(root, name)
		installed := Installed{Dir: dir, Valid: false}
		if info, statErr := os.Stat(dir); statErr == nil {
			installed.UpdatedAt = info.ModTime()
		}
		if pkg, statErr := os.Stat(filepath.Join(dir, PackageName)); statErr == nil && !pkg.IsDir() {
			installed.HasPackage = true
			installed.PackageBytes = pkg.Size()
		}
		data, readErr := os.ReadFile(filepath.Join(dir, ManifestName))
		if readErr != nil {
			installed.LoadError = fmt.Sprintf("读取 %s 失败：%v", ManifestName, readErr)
			out = append(out, installed)
			continue
		}
		manifest, parseErr := ParseManifest(data)
		if parseErr != nil {
			installed.LoadError = parseErr.Error()
			out = append(out, installed)
			continue
		}
		// 清单 id 必须与目录名**逐字符一致**（含大小写）。只按 IdentityKey 比（忽略大小写）
		// 留下的是半截支持：下游拼路径一律用清单 id（资产中间件还会额外 lower 一遍），而
		// 盘上是 `Foo-Bar` 这种人手拷出来的大写目录名——在大小写敏感的文件系统上，列表里
		// 判为可用、点进去存储/HTTP/资源全部落空（Windows/macOS 大小写不敏感，测不出来）。
		// 安装流程写出的目录恒等于清单 id，所以不一致只可能来自手工拷贝：报出来并给出改法。
		if manifest.ID != name {
			installed.LoadError = fmt.Sprintf("目录名 %s 与清单 id %s 不一致（必须逐字符相同，含大小写）：把目录改名为 %s（或删掉重新安装）", name, manifest.ID, manifest.ID)
			out = append(out, installed)
			continue
		}
		entryPath, entryErr := ResolveAsset(dir, manifest.Entry)
		if entryErr != nil {
			installed.LoadError = entryErr.Error()
			out = append(out, installed)
			continue
		}
		entryInfo, entryStatErr := os.Stat(entryPath)
		if entryStatErr != nil || entryInfo.IsDir() {
			installed.LoadError = fmt.Sprintf("入口文件 %s 不存在", manifest.Entry)
			out = append(out, installed)
			continue
		}
		installed.EntryBytes = entryInfo.Size()
		installed.Manifest = manifest
		installed.Valid = true
		out = append(out, installed)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Valid != out[j].Valid {
			return out[i].Valid
		}
		return out[i].Dir < out[j].Dir
	})
	return out, nil
}

// StripCacheStamp 去掉资源路径首段的缓存戳（`@<stamp>/…`），不是戳时返回 false。
//
// 戳住在**目录**位置是有意的：插件内部的相对 import 会自动继承它，于是重新导入后子模块
// 也拿到新的模块 URL（模块表按 URL 常驻，查询串版本继承不到，见 docs/plugin-system.md
// §4.1）。中间件只在这一段剥掉之后能找到一个真实文件时才按戳处理，所以插件里真叫
// `@foo/` 的目录不会被误剥。
func StripCacheStamp(rel string) (string, bool) {
	head, rest, found := strings.Cut(strings.TrimPrefix(strings.TrimSpace(rel), "/"), "/")
	if !found || rest == "" || !cacheStampPattern.MatchString(head) {
		return "", false
	}
	return rest, true
}

// ResolveAsset 把插件内的相对路径解析成绝对路径，并断言结果没有跑出插件目录。
// 两道关：词法路径（Clean 掉 `..`）与软链解析后的真实路径（目录里一个指向外部的
// 链接不能变成越权读取）。文件不存在不算错误——调用方按 404 处理。
func ResolveAsset(dir, rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", errors.New("资源路径为空")
	}
	slashed := strings.ReplaceAll(rel, "\\", "/")
	if path.IsAbs(slashed) || filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" {
		return "", fmt.Errorf("资源路径必须是插件内相对路径：%s", rel)
	}
	clean := strings.TrimPrefix(path.Clean("/"+slashed), "/")
	if clean == "" || clean == "." {
		return "", fmt.Errorf("资源路径为空：%s", rel)
	}
	base, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	target := filepath.Join(base, filepath.FromSlash(clean))
	if !PathWithin(base, target) {
		return "", fmt.Errorf("资源路径越出插件目录：%s", rel)
	}
	resolved, resolveErr := filepath.EvalSymlinks(target)
	if resolveErr != nil {
		return target, nil
	}
	resolvedBase, baseErr := filepath.EvalSymlinks(base)
	if baseErr != nil {
		resolvedBase = base
	}
	if !PathWithin(resolvedBase, resolved) {
		return "", fmt.Errorf("资源路径经链接指向插件目录之外：%s", rel)
	}
	return resolved, nil
}

// PathWithin 报告 target 是否等于 base 或位于 base 之下（两侧都必须是绝对路径）。
// 导出给编排层，让它在同一套语义下再断言一次（例如包内前缀拼出来的内容目录）。
func PathWithin(base, target string) bool {
	if target == base {
		return true
	}
	return strings.HasPrefix(target, base+string(os.PathSeparator))
}

// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package plugin

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// PackageInfo 是插件包的静态校验结果：清单 + 包内文件清单 + 体积。它只描述
// 「这个包长什么样」，不写任何文件——解包由编排层的 extractZip 完成。
type PackageInfo struct {
	Manifest Manifest
	// Prefix 是包里统一的外层目录（用户把文件夹整个打包时会多一层）。解包后按它
	// 取插件目录；为空表示清单就在包根。
	Prefix       string
	Files        []string
	TotalBytes   uint64
	PackageBytes int64
}

// InspectPackage 打开 zip 做静态校验：条目数与体积上限、路径形状（绝对路径 /
// `..` / 软链）、外层目录、清单合法性、入口文件存在。任一条不过就直接拒绝安装。
func InspectPackage(zipPath string) (PackageInfo, error) {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return PackageInfo{}, fmt.Errorf("打开插件包失败：%w", err)
	}
	defer reader.Close()

	if len(reader.File) == 0 {
		return PackageInfo{}, errors.New("插件包是空的")
	}
	if len(reader.File) > MaxPackageEntries {
		return PackageInfo{}, fmt.Errorf("插件包含 %d 个条目，超过上限 %d", len(reader.File), MaxPackageEntries)
	}
	info := PackageInfo{}
	if stat, statErr := os.Stat(zipPath); statErr == nil {
		info.PackageBytes = stat.Size()
	}
	names := make([]string, 0, len(reader.File))
	for _, file := range reader.File {
		name, nameErr := safeEntryName(file)
		if nameErr != nil {
			return PackageInfo{}, nameErr
		}
		if file.UncompressedSize64 > MaxPackageFileBytes {
			return PackageInfo{}, fmt.Errorf("条目 %s 解压后 %d 字节，超过单文件上限 %d", name, file.UncompressedSize64, MaxPackageFileBytes)
		}
		if info.TotalBytes+file.UncompressedSize64 > MaxPackageBytes {
			return PackageInfo{}, fmt.Errorf("插件包解压后超过上限 %d 字节", MaxPackageBytes)
		}
		info.TotalBytes += file.UncompressedSize64
		if !file.FileInfo().IsDir() {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return PackageInfo{}, errors.New("插件包里没有任何文件")
	}
	sort.Strings(names)
	info.Files = names

	manifest, prefix, err := loadManifestFromList(names, func(name string) ([]byte, error) {
		for _, file := range reader.File {
			entry, entryErr := safeEntryName(file)
			if entryErr != nil || entry != name {
				continue
			}
			handle, openErr := file.Open()
			if openErr != nil {
				return nil, openErr
			}
			defer handle.Close()
			return io.ReadAll(io.LimitReader(handle, MaxManifestBytes+1))
		}
		return nil, os.ErrNotExist
	})
	if err != nil {
		return PackageInfo{}, err
	}
	info.Manifest = manifest
	info.Prefix = prefix
	return info, nil
}

// InspectDir 校验一个已经展开的插件目录（用于「从目录安装」）。
func InspectDir(dir string) (PackageInfo, error) {
	base, err := filepath.Abs(dir)
	if err != nil {
		return PackageInfo{}, err
	}
	stat, err := os.Stat(base)
	if err != nil {
		return PackageInfo{}, fmt.Errorf("目录不存在：%w", err)
	}
	if !stat.IsDir() {
		return PackageInfo{}, errors.New("目标不是目录")
	}
	names := make([]string, 0, 16)
	var total uint64
	walkErr := filepath.WalkDir(base, func(current string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if current != base && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("不接受符号链接：%s", entry.Name())
		}
		rel, relErr := filepath.Rel(base, current)
		if relErr != nil {
			return relErr
		}
		if strings.HasPrefix(entry.Name(), ".") || entry.Name() == PackageName {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		if info.Size() > MaxPackageFileBytes {
			return fmt.Errorf("文件 %s 超过单文件上限 %d 字节", entry.Name(), MaxPackageFileBytes)
		}
		if total+uint64(info.Size()) > MaxPackageBytes {
			return fmt.Errorf("目录内容超过上限 %d 字节", MaxPackageBytes)
		}
		total += uint64(info.Size())
		names = append(names, filepath.ToSlash(rel))
		return nil
	})
	if walkErr != nil {
		return PackageInfo{}, walkErr
	}
	if len(names) == 0 {
		return PackageInfo{}, errors.New("目录里没有任何文件")
	}
	sort.Strings(names)
	manifest, prefix, err := loadManifestFromList(names, func(name string) ([]byte, error) {
		return os.ReadFile(filepath.Join(base, filepath.FromSlash(name)))
	})
	if err != nil {
		return PackageInfo{}, err
	}
	return PackageInfo{Manifest: manifest, Prefix: prefix, Files: names, TotalBytes: total}, nil
}

// VerifyContentSizes 按**盘上真实字节**复验一个已展开目录的条目数与体积。
//
// 为什么必须复验：zip 头里的 UncompressedSize64 是打包者写的数字，谎报一个很小的
// 值就能让 InspectPackage 的三条上限（条目数 / 单文件 / 总量）全部失效；真正落盘的是
// extractZip，而它用的是自更新那套宽松限额（单文件 1GB、条目 4096），且总量也是按声明
// 值累计的。少了这一道，一个声明尺寸很小的第三方包实际能撑爆磁盘。
// 从目录安装同理：InspectDir 验过之后、CopyTree 复制之前源目录还在变（TOCTOU）。
// 所以落位前按真实字节再验一次——这才是插件体积上限真正生效的地方。
func VerifyContentSizes(dir string) error {
	var (
		entries int
		total   uint64
	)
	err := filepath.WalkDir(dir, func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		// 只数文件：目录条目在解包时会建出来，但它不占体积，也不构成入口。
		entries++
		if entries > MaxPackageEntries {
			return fmt.Errorf("插件内容包含超过 %d 个文件", MaxPackageEntries)
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		if info.Size() < 0 {
			return fmt.Errorf("文件体积异常：%s", entry.Name())
		}
		size := uint64(info.Size())
		if size > MaxPackageFileBytes {
			return fmt.Errorf("文件 %s 超过单文件上限 %d 字节", entry.Name(), MaxPackageFileBytes)
		}
		if total+size > MaxPackageBytes {
			return fmt.Errorf("插件内容超过上限 %d 字节", MaxPackageBytes)
		}
		total += size
		return nil
	})
	if err != nil {
		return err
	}
	if entries == 0 {
		return errors.New("插件内容为空")
	}
	return nil
}

// safeEntryName 校验 zip 条目名并返回归一后的包内斜杠路径（目录条目以 `/` 结尾）。
func safeEntryName(file *zip.File) (string, error) {
	raw := file.Name
	if strings.TrimSpace(raw) == "" {
		return "", errors.New("插件包里存在空条目名")
	}
	if file.FileInfo().Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("插件包不接受符号链接：%s", raw)
	}
	if strings.Contains(raw, `\`) {
		return "", fmt.Errorf("插件包条目名不能包含反斜杠：%s", raw)
	}
	if path.IsAbs(raw) || filepath.VolumeName(raw) != "" {
		return "", fmt.Errorf("插件包条目不能是绝对路径：%s", raw)
	}
	clean := path.Clean(raw)
	for _, part := range strings.Split(clean, "/") {
		if part == ".." {
			return "", fmt.Errorf("插件包条目包含上级引用：%s", raw)
		}
	}
	// 读清单时按不带尾斜杠的名字匹配，这里统一去掉尾斜杠（目录条目由调用方按
	// file.FileInfo().IsDir() 过滤）。
	return strings.TrimSuffix(clean, "/"), nil
}

// loadManifestFromList 从包内文件清单里定位并解析 plugin.json，同时算出统一的外层
// 目录前缀。
func loadManifestFromList(names []string, read func(name string) ([]byte, error)) (Manifest, string, error) {
	for _, candidate := range []string{ManifestName, "." + "/" + ManifestName} {
		if !ContainsFile(names, candidate) {
			continue
		}
		data, err := read(candidate)
		if err != nil {
			return Manifest{}, "", fmt.Errorf("读取 %s 失败：%w", ManifestName, err)
		}
		manifest, err := ParseManifest(data)
		if err != nil {
			return Manifest{}, "", err
		}
		if err := entryPresent(names, "", manifest.Entry); err != nil {
			return Manifest{}, "", err
		}
		return manifest, "", nil
	}
	// 外层目录：所有文件共用一个顶层段，且清单在该段之下。
	prefix := commonTopDir(names)
	if prefix == "" {
		return Manifest{}, "", fmt.Errorf("插件包根目录里找不到 %s", ManifestName)
	}
	manifestName := prefix + "/" + ManifestName
	if !ContainsFile(names, manifestName) {
		return Manifest{}, "", fmt.Errorf("插件包根目录里找不到 %s（顶层目录 %s 下也没有）", ManifestName, prefix)
	}
	data, err := read(manifestName)
	if err != nil {
		return Manifest{}, "", fmt.Errorf("读取 %s 失败：%w", manifestName, err)
	}
	manifest, err := ParseManifest(data)
	if err != nil {
		return Manifest{}, "", err
	}
	if err := entryPresent(names, prefix, manifest.Entry); err != nil {
		return Manifest{}, "", err
	}
	return manifest, prefix, nil
}

// commonTopDir 返回所有条目共用的顶层目录名；不共用时为空串。
func commonTopDir(names []string) string {
	top := ""
	for _, name := range names {
		head, _, ok := strings.Cut(name, "/")
		if !ok {
			return ""
		}
		if top == "" {
			top = head
			continue
		}
		if top != head {
			return ""
		}
	}
	// 只要求是单个安全目录段：用户把 `Jira-Helper-1.0.0` 这样的文件夹整个打包很常见，
	// 按插件 id 规则卡会把正常包判成“找不到 plugin.json”（`..`、反斜杠与绝对路径已在
	// safeEntryName 里挡掉，拼出来的内容目录另有一道 PathWithin 断言）。
	if top == "" || top == "." || top == ".." {
		return ""
	}
	return top
}

// entryPresent 断言入口文件在包内（带上外层目录前缀）。
func entryPresent(names []string, prefix, entry string) error {
	full := entry
	if prefix != "" {
		full = prefix + "/" + entry
	}
	if !ContainsFile(names, full) {
		return fmt.Errorf("入口文件 %s 不在包里", full)
	}
	return nil
}

// ContainsFile 报告（已归一化的）包内文件清单里是否存在该路径（带可选的外层目录
// 前缀）。导出给编排层用：它是「包里到底有没有 data.json」的唯一判据。
func ContainsFile(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

// WritePackage 把一个已展开的插件目录重新打成 zip（导出用）。dataJSON 非 nil 时
// 作为 data.json 写进包里（「导出时包含数据」）；package.zip 与目录里的 data.json
// 自己都不打进去。
func WritePackage(writer io.Writer, dir string, dataJSON []byte) error {
	base, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	zipWriter := zip.NewWriter(writer)
	names := make([]string, 0, 32)
	walkErr := filepath.WalkDir(base, func(current string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if current != base && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel, relErr := filepath.Rel(base, current)
		if relErr != nil {
			return relErr
		}
		slashed := filepath.ToSlash(rel)
		// 只跳过包根那一份 data.json：插件自己的 assets/data.json 是正常内容，
		// 按文件名一刀切会让「源目录 ↔ 导出包」不一致（round-trip 就不成立了）。
		if slashed == PackageName || slashed == DataName || strings.HasPrefix(entry.Name(), ".") {
			return nil
		}
		names = append(names, slashed)
		return nil
	})
	if walkErr != nil {
		zipWriter.Close()
		return walkErr
	}
	sort.Strings(names)
	for _, name := range names {
		source, openErr := os.Open(filepath.Join(base, filepath.FromSlash(name)))
		if openErr != nil {
			zipWriter.Close()
			return openErr
		}
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0o644)
		target, createErr := zipWriter.CreateHeader(header)
		if createErr != nil {
			source.Close()
			zipWriter.Close()
			return createErr
		}
		if _, copyErr := io.Copy(target, source); copyErr != nil {
			source.Close()
			zipWriter.Close()
			return copyErr
		}
		source.Close()
	}
	if dataJSON != nil {
		header := &zip.FileHeader{Name: DataName, Method: zip.Deflate}
		header.SetMode(0o600)
		target, createErr := zipWriter.CreateHeader(header)
		if createErr != nil {
			zipWriter.Close()
			return createErr
		}
		if _, writeErr := target.Write(dataJSON); writeErr != nil {
			zipWriter.Close()
			return writeErr
		}
	}
	return zipWriter.Close()
}

// CopyTree 把插件目录递归复制到目标目录（「从目录安装」用）。符号链接一律跳过：
// 安装进来的内容必须是插件目录里真实存在的普通文件。
func CopyTree(src, dst string) error {
	base, err := filepath.Abs(src)
	if err != nil {
		return err
	}
	return filepath.WalkDir(base, func(current string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(base, current)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o700)
		}
		if entry.IsDir() {
			if strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o700)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		name := entry.Name()
		if name == PackageName || strings.HasPrefix(name, ".") {
			return nil
		}
		source, openErr := os.Open(current)
		if openErr != nil {
			return openErr
		}
		defer source.Close()
		target, createErr := os.OpenFile(filepath.Join(dst, rel), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if createErr != nil {
			return createErr
		}
		defer target.Close()
		_, copyErr := io.Copy(target, source)
		return copyErr
	})
}

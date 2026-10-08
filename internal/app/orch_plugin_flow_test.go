// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"os"
	"path/filepath"
	"testing"

	"ally-dev/internal/tools/plugin"
)

// demoPluginFixture 是示例插件的源码目录（同时是 internal/tools/plugin 的受测 fixture）。
var demoPluginFixture = filepath.Join("..", "tools", "plugin", "testdata", "demo-plugin")

// buildDemoPackage 把 fixture 打成与用户手上那份形态一致的 zip（条目在包根）。
func buildDemoPackage(t *testing.T, dest string) {
	t.Helper()
	handle, err := os.Create(dest)
	if err != nil {
		t.Fatal(err)
	}
	if err := plugin.WritePackage(handle, demoPluginFixture, nil); err != nil {
		t.Fatalf("打包示例插件失败：%v", err)
	}
	if err := handle.Close(); err != nil {
		t.Fatal(err)
	}
}

// 插件生命周期全流程：安装 → 列表 → 启停（含重启后仍然生效）→ 存储 → 覆盖升级 →
// 删除（保留数据 / 连数据删）→ 从工作区目录安装。这条路径就是用户在管理页里点出来的那条。
func TestPluginLifecycleFlow(t *testing.T) {
	redirectAppStateDir(t)
	app := NewApp()
	if err := app.ensureInitialized(); err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(t.TempDir(), "demo-toolkit.zip")
	buildDemoPackage(t, pkg)

	info, err := app.ImportPlugin(pkg)
	if err != nil {
		t.Fatalf("ImportPlugin: %v", err)
	}
	if info.ID != "demo-toolkit" || info.Entry != "index.js" || info.Mode != "plugin:demo-toolkit" {
		t.Fatalf("导入结果不对：%+v", info)
	}
	if !info.Valid || !info.Enabled {
		t.Fatalf("新装的插件应当是可用且启用的：%+v", info)
	}
	if !info.HasPackage || info.PackageBytes == 0 {
		t.Fatalf("原始包副本没留下来（导出会退化成重新打包）：%+v", info)
	}
	if info.Permissions.Workspace != plugin.WorkspaceRead || len(info.Permissions.HTTP) != 1 {
		t.Fatalf("权限摘要不对：%+v", info.Permissions)
	}

	list, err := app.ListPlugins()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "demo-toolkit" {
		t.Fatalf("列表不对：%+v", list)
	}

	// 启停：写进配置，重启后仍然生效（这是"重启即丢"最容易踩的地方）。
	if err := app.SetPluginEnabled("demo-toolkit", false); err != nil {
		t.Fatal(err)
	}
	cfg, err := app.getConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.DisabledPlugins) != 1 || cfg.DisabledPlugins[0] != "demo-toolkit" {
		t.Fatalf("配置里的禁用名单不对：%+v", cfg.DisabledPlugins)
	}
	reloaded := NewApp()
	if err := reloaded.ensureInitialized(); err != nil {
		t.Fatal(err)
	}
	reloadedList, err := reloaded.ListPlugins()
	if err != nil {
		t.Fatal(err)
	}
	if len(reloadedList) != 1 || reloadedList[0].Enabled {
		t.Fatalf("重启后禁用状态丢了：%+v", reloadedList)
	}
	if err := reloaded.SetPluginEnabled("demo-toolkit", true); err != nil {
		t.Fatal(err)
	}
	reloadedList, _ = reloaded.ListPlugins()
	if !reloadedList[0].Enabled {
		t.Fatalf("重新启用没生效：%+v", reloadedList)
	}

	// 存储：只接受合法 JSON，能读回来，且不住在插件目录里。
	if err := reloaded.PluginStoreSet("demo-toolkit", `{"token":"abc"}`); err != nil {
		t.Fatal(err)
	}
	raw, err := reloaded.PluginStoreGet("demo-toolkit")
	if err != nil || raw != `{"token":"abc"}` {
		t.Fatalf("store round-trip = %q, err = %v", raw, err)
	}
	if err := reloaded.PluginStoreSet("demo-toolkit", "not json"); err == nil {
		t.Fatal("非 JSON 的存储内容必须被拒")
	}
	if _, err := os.Stat(filepath.Join(plugin.PluginRoot(appDataDir()), "demo-toolkit", plugin.DataName)); !os.IsNotExist(err) {
		t.Fatal("插件数据不该住在插件目录里（否则删插件就没法保留数据）")
	}

	// 覆盖升级：保留数据，并留下上一版备份。
	if _, err := reloaded.ImportPlugin(pkg); err != nil {
		t.Fatalf("覆盖升级失败：%v", err)
	}
	if raw, err = reloaded.PluginStoreGet("demo-toolkit"); err != nil || raw != `{"token":"abc"}` {
		t.Fatalf("升级把数据弄丢了：%q err = %v", raw, err)
	}
	if _, err := os.Stat(filepath.Join(plugin.PluginRoot(appDataDir()), plugin.BackupDirName, "demo-toolkit")); err != nil {
		t.Fatalf("没有留下上一版备份：%v", err)
	}

	// 删除（保留数据）→ 重装后数据还在；purge 才真的删数据。
	if err := reloaded.DeletePlugin("demo-toolkit", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(plugin.PluginRoot(appDataDir()), "demo-toolkit")); !os.IsNotExist(err) {
		t.Fatal("插件目录没被删掉")
	}
	if _, err := reloaded.PluginStoreGet("demo-toolkit"); err == nil {
		t.Fatal("未安装时读存储应当报错")
	}
	if _, err := reloaded.ImportPlugin(pkg); err != nil {
		t.Fatal(err)
	}
	if raw, err = reloaded.PluginStoreGet("demo-toolkit"); err != nil || raw != `{"token":"abc"}` {
		t.Fatalf("重装后数据丢了：%q err = %v", raw, err)
	}
	if err := reloaded.DeletePlugin("demo-toolkit", true); err != nil {
		t.Fatal(err)
	}
	if raw, err = reloaded.PluginStoreGet("demo-toolkit"); err == nil {
		t.Fatalf("purge 之后不该还能读到数据：%q", raw)
	}

	// 从目录安装：位置不限（用户自己挑的目录），校验与 zip 路径完全同一套。
	// 工作区外的目录同样能装，但目录里必须真的有一个能通过校验的插件。
	workspace := t.TempDir()
	insideDir := filepath.Join(workspace, "plugins-src", "demo-plugin")
	if err := plugin.CopyTree(demoPluginFixture, insideDir); err != nil {
		t.Fatal(err)
	}
	outsideDir := filepath.Join(t.TempDir(), "elsewhere")
	if err := plugin.CopyTree(demoPluginFixture, outsideDir); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{insideDir, outsideDir} {
		dirInfo, err := reloaded.ImportPluginFromDir(source)
		if err != nil {
			t.Fatalf("从目录 %s 安装失败：%v", source, err)
		}
		if dirInfo.ID != "demo-toolkit" || !dirInfo.Valid {
			t.Fatalf("目录安装的结果不对：%+v", dirInfo)
		}
		// 目录安装来的插件没有原始包副本，导出会走重新打包（WritePackage 已单测覆盖）。
		if dirInfo.HasPackage {
			t.Fatalf("目录安装不该凭空多出原始包：%+v", dirInfo)
		}
	}

	// 选到「插件文件夹的父目录」时也要装上：InspectDir 记下的外层目录必须被按上，
	// 否则文件多嵌套一层、入口文件找不到，界面上只看到一行「不可用」（zip 路径一直
	// 按 Prefix 落位，目录路径曾经漏掉这一步）。
	parent := filepath.Join(t.TempDir(), "Jira-Helper-1.0.0")
	if err := plugin.CopyTree(demoPluginFixture, parent); err != nil {
		t.Fatal(err)
	}
	parentInfo, err := reloaded.ImportPluginFromDir(filepath.Dir(parent))
	if err != nil {
		t.Fatalf("从父目录安装失败：%v", err)
	}
	if !parentInfo.Valid || parentInfo.ID != "demo-toolkit" || parentInfo.Entry != "index.js" {
		t.Fatalf("外层目录没被按上，装出来的插件不对：%+v", parentInfo)
	}

	// 空目录、不是目录、没有清单的目录都必须被拒（校验强度不因来源而变）。
	empty := t.TempDir()
	if _, err := reloaded.ImportPluginFromDir(empty); err == nil {
		t.Fatal("空目录必须被拒")
	}
	if _, err := reloaded.ImportPluginFromDir(filepath.Join(outsideDir, "plugin.json")); err == nil {
		t.Fatal("文件路径必须被拒")
	}
}

// 勾了「不包含数据」的导出必须真的不含数据——包括“上次导入时包里带进来的那一份”。
// 这是凭据外泄路径：含数据导出 → 对方导入 → 对方默认导出，凭据一路流下去。
func TestPluginExportNeverLeaksPackagedData(t *testing.T) {
	redirectAppStateDir(t)
	app := NewApp()
	if err := app.ensureInitialized(); err != nil {
		t.Fatal(err)
	}

	pkg := filepath.Join(t.TempDir(), "with-data.zip")
	handle, err := os.Create(pkg)
	if err != nil {
		t.Fatal(err)
	}
	if err := plugin.WritePackage(handle, demoPluginFixture, []byte(`{"token":"secret"}`)); err != nil {
		t.Fatal(err)
	}
	if err := handle.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ImportPlugin(pkg); err != nil {
		t.Fatalf("ImportPlugin: %v", err)
	}

	dir := filepath.Join(plugin.PluginRoot(appDataDir()), "demo-toolkit")
	if _, err := os.Stat(filepath.Join(dir, plugin.DataName)); !os.IsNotExist(err) {
		t.Fatal("导入后插件目录里不该留着 data.json")
	}
	if raw, err := app.PluginStoreGet("demo-toolkit"); err != nil || raw != `{"token":"secret"}` {
		t.Fatalf("包里的数据没被采纳：%q err = %v", raw, err)
	}

	plain := filepath.Join(t.TempDir(), "plain.zip")
	if err := app.writePluginExport("demo-toolkit", dir, false, plain); err != nil {
		t.Fatalf("导出失败：%v", err)
	}
	info, err := plugin.InspectPackage(plain)
	if err != nil {
		t.Fatalf("导出的包无法再导入：%v", err)
	}
	if plugin.ContainsFile(info.Files, plugin.DataName) {
		t.Fatalf("默认导出竟然带上了数据：%v", info.Files)
	}

	withData := filepath.Join(t.TempDir(), "with-data-out.zip")
	if err := app.writePluginExport("demo-toolkit", dir, true, withData); err != nil {
		t.Fatal(err)
	}
	info, err = plugin.InspectPackage(withData)
	if err != nil {
		t.Fatal(err)
	}
	if !plugin.ContainsFile(info.Files, plugin.DataName) {
		t.Fatalf("勾了包含数据却没带上：%v", info.Files)
	}
}

// 清单坏掉的插件在管理页里看得到，就必须删得掉：否则用户只能手动去数据目录里翻。
func TestPluginDeleteWorksForBrokenManifest(t *testing.T) {
	redirectAppStateDir(t)
	app := NewApp()
	if err := app.ensureInitialized(); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(plugin.PluginRoot(appDataDir()), "broken-plugin")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, plugin.ManifestName), []byte("{ not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	list, err := app.ListPlugins()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Valid {
		t.Fatalf("坏插件必须被列出并标记为不可用：%+v", list)
	}
	if err := app.SetPluginEnabled("broken-plugin", false); err != nil {
		t.Fatalf("坏插件也要能启停（口径一致）：%v", err)
	}
	if err := app.DeletePlugin("broken-plugin", true); err != nil {
		t.Fatalf("坏插件必须能删掉：%v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("目录没被删掉")
	}
}

// 包里的 data.json 坏了，不该把整次导入判死：调用 adoptPackagedData 时插件本体已经
// 落位（旧版本已被顶替），抛回错误就变成「界面说导入失败、插件其实装好了」。坏数据按
// 「包里没带数据」降级——记日志、删掉它、保留现有数据。
func TestPluginImportSurvivesBrokenPackagedData(t *testing.T) {
	redirectAppStateDir(t)
	app := NewApp()
	if err := app.ensureInitialized(); err != nil {
		t.Fatal(err)
	}

	// 先装一份干净的，写进数据（这是“现有数据”，不能被坏包冲掉）。
	clean := filepath.Join(t.TempDir(), "clean.zip")
	buildDemoPackage(t, clean)
	if _, err := app.ImportPlugin(clean); err != nil {
		t.Fatalf("ImportPlugin: %v", err)
	}
	if err := app.PluginStoreSet("demo-toolkit", `{"token":"keep-me"}`); err != nil {
		t.Fatal(err)
	}

	// 再导入一个带坏 data.json 的包（不是合法 JSON）。
	broken := filepath.Join(t.TempDir(), "broken-data.zip")
	handle, err := os.Create(broken)
	if err != nil {
		t.Fatal(err)
	}
	if err := plugin.WritePackage(handle, demoPluginFixture, []byte("{ not json")); err != nil {
		t.Fatal(err)
	}
	if err := handle.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := app.ImportPlugin(broken)
	if err != nil {
		t.Fatalf("带坏数据的包不该让导入失败（插件本体是好的）：%v", err)
	}
	if !info.Valid {
		t.Fatalf("带坏数据的包装出来应当是可用插件：%+v", info)
	}

	// 坏数据不能留在插件目录里：下一次默认导出会把它带出去。
	dir := filepath.Join(plugin.PluginRoot(appDataDir()), "demo-toolkit")
	if _, err := os.Stat(filepath.Join(dir, plugin.DataName)); !os.IsNotExist(err) {
		t.Fatal("坏的 data.json 不该留在插件目录里")
	}
	// 现有数据必须还在（没被坏数据清掉）。
	if raw, err := app.PluginStoreGet("demo-toolkit"); err != nil || raw != `{"token":"keep-me"}` {
		t.Fatalf("坏数据把现有数据弄没了：%q err = %v", raw, err)
	}
}

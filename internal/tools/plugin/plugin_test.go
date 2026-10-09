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
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func writeZipFile(t *testing.T, zipPath string, files map[string]string) {
	t.Helper()
	handle, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	writer := zip.NewWriter(handle)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("create entry %s: %v", name, err)
		}
		if _, err := entry.Write([]byte(files[name])); err != nil {
			t.Fatalf("write entry %s: %v", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	if err := handle.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}
}

// manifestJSON 生成一份合法清单，fields 里的键值对会逐条替换（用于构造非法样例）。
func manifestJSON(id, entry string) string {
	return `{
  "id": "` + id + `",
  "name": "Jira Bug 管理",
  "version": "1.0.0",
  "author": "example",
  "entry": "` + entry + `",
  "menu": [{"key": "jira", "title": "Jira", "icon": "ApiOutlined"}],
  "permissions": {"http": ["jira.corp.com", "*.corp.com"], "workspace": "read"}
}`
}

func TestParseManifestAcceptsValidManifest(t *testing.T) {
	manifest, err := ParseManifest([]byte(manifestJSON("jira-helper", "index.js")))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if manifest.ID != "jira-helper" || manifest.Entry != "index.js" {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
	if manifest.WorkspaceLevel() != WorkspaceRead {
		t.Fatalf("workspace level = %q, want read", manifest.WorkspaceLevel())
	}
	if len(manifest.Permissions.HTTP) != 2 {
		t.Fatalf("host patterns = %v", manifest.Permissions.HTTP)
	}
}

// keepAlive 是插件页的存活策略：不写即保留页面（前端用 v-show 藏着），显式写 false
// 才切走销毁（前端从挂载列表里移除）。默认必须是 true——布尔零值会把“没写”当成
// “销毁”，那是一次静默的行为反转。
func TestManifestKeepPageAliveDefaultsToTrue(t *testing.T) {
	manifest, err := ParseManifest([]byte(manifestJSON("jira-helper", "index.js")))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if manifest.KeepAlive != nil {
		t.Fatalf("没写的字段不该被填上值：%v", *manifest.KeepAlive)
	}
	if !manifest.KeepPageAlive() {
		t.Fatal("没写 keepAlive 时必须按“保留页面”处理")
	}
}

func TestManifestKeepPageAliveOverride(t *testing.T) {
	cases := []struct {
		name string
		json string
		want bool
	}{
		{"显式 true", `{"id":"abc","name":"x","version":"1.0.0","entry":"index.js","menu":[{"key":"a","title":"a"}],"permissions":{},"keepAlive":true}`, true},
		{"显式 false", `{"id":"abc","name":"x","version":"1.0.0","entry":"index.js","menu":[{"key":"a","title":"a"}],"permissions":{},"keepAlive":false}`, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			manifest, err := ParseManifest([]byte(testCase.json))
			if err != nil {
				t.Fatalf("ParseManifest: %v", err)
			}
			if got := manifest.KeepPageAlive(); got != testCase.want {
				t.Fatalf("KeepPageAlive() = %v, want %v", got, testCase.want)
			}
		})
	}
}

// 侧栏分组：key 是身份（同 key 即同组，插件之间不需要知道对方存在），title/icon 只是展示。
// key 的大小写与空白必须在解析时归一，否则同一个组会因为写法不同被拆成两个一级菜单。
func TestManifestGroupNormalized(t *testing.T) {
	manifest, err := ParseManifest([]byte(`{"id":"abc","name":"x","version":"1.0.0","entry":"index.js","menu":[{"key":"a","title":"a"}],"permissions":{},"group":{"key":"  NSFocus ","title":" 公司 ","icon":"GlobalOutlined"}}`))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if manifest.Group == nil {
		t.Fatal("group 被丢弃了")
	}
	if manifest.Group.Key != "nsfocus" {
		t.Fatalf("group.key 未归一：%q", manifest.Group.Key)
	}
	if manifest.Group.Title != "公司" {
		t.Fatalf("group.title 未去空白：%q", manifest.Group.Title)
	}
	if manifest.Group.Icon != "GlobalOutlined" {
		t.Fatalf("group.icon = %q", manifest.Group.Icon)
	}
}

// 没写 group 时必须保持 nil：前端据此把页面放在侧栏顶层；凭空造一个分组会让所有
// 不带分组的插件误入同一个一级菜单。
func TestManifestGroupOptional(t *testing.T) {
	manifest, err := ParseManifest([]byte(manifestJSON("jira-helper", "index.js")))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if manifest.Group != nil {
		t.Fatalf("没写 group 却解析出了 %+v", *manifest.Group)
	}
}

func TestParseManifestRejectsBadManifests(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"大写 id", `{"id":"Jira","name":"x","version":"1.0.0","entry":"index.js","menu":[{"key":"k","title":"t"}],"permissions":{}}`},
		{"id 带路径分隔符", `{"id":"a/b","name":"x","version":"1.0.0","entry":"index.js","menu":[{"key":"k","title":"t"}],"permissions":{}}`},
		{"id 以点开头", `{"id":".hidden","name":"x","version":"1.0.0","entry":"index.js","menu":[{"key":"k","title":"t"}],"permissions":{}}`},
		{"版本号不合规", `{"id":"abc","name":"x","version":"1.0","entry":"index.js","menu":[{"key":"k","title":"t"}],"permissions":{}}`},
		{"缺 name", `{"id":"abc","version":"1.0.0","entry":"index.js","menu":[{"key":"k","title":"t"}],"permissions":{}}`},
		{"entry 越出目录", `{"id":"abc","name":"x","version":"1.0.0","entry":"../evil.js","menu":[{"key":"k","title":"t"}],"permissions":{}}`},
		{"menu 为空", `{"id":"abc","name":"x","version":"1.0.0","entry":"index.js","menu":[],"permissions":{}}`},
		{"menu 多于一项", `{"id":"abc","name":"x","version":"1.0.0","entry":"index.js","menu":[{"key":"a","title":"a"},{"key":"b","title":"b"}],"permissions":{}}`},
		{"menu title 为空", `{"id":"abc","name":"x","version":"1.0.0","entry":"index.js","menu":[{"key":"a","title":" "}],"permissions":{}}`},
		{"工作区级别非法", `{"id":"abc","name":"x","version":"1.0.0","entry":"index.js","menu":[{"key":"a","title":"a"}],"permissions":{"workspace":"rw"}}`},
		{"主机模式带协议", `{"id":"abc","name":"x","version":"1.0.0","entry":"index.js","menu":[{"key":"a","title":"a"}],"permissions":{"http":["https://a.com"]}}`},
		{"主机模式带路径", `{"id":"abc","name":"x","version":"1.0.0","entry":"index.js","menu":[{"key":"a","title":"a"}],"permissions":{"http":["a.com/x"]}}`},
		{"未知字段（拼错的键要报错而不是静默忽略）", `{"id":"abc","name":"x","version":"1.0.0","entry":"index.js","menu":[{"key":"a","title":"a"}],"permissions":{},"premissions":{}}`},
		{"keepAlive 类型不对（字符串不是布尔）", `{"id":"abc","name":"x","version":"1.0.0","entry":"index.js","menu":[{"key":"a","title":"a"}],"permissions":{},"keepAlive":"true"}`},
		{"group.key 非法（会当成菜单 key 用）", `{"id":"abc","name":"x","version":"1.0.0","entry":"index.js","menu":[{"key":"a","title":"a"}],"permissions":{},"group":{"key":"BAD KEY","title":"公司"}}`},
		{"group.title 为空白", `{"id":"abc","name":"x","version":"1.0.0","entry":"index.js","menu":[{"key":"a","title":"a"}],"permissions":{},"group":{"key":"nsfocus","title":"  "}}`},
		{"group.title 超过 24 字", `{"id":"abc","name":"x","version":"1.0.0","entry":"index.js","menu":[{"key":"a","title":"a"}],"permissions":{},"group":{"key":"nsfocus","title":"这是一个特别特别特别特别特别特别长的分组名字超过上限"}}`},
		{"不是 JSON", `not json`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := ParseManifest([]byte(testCase.json)); err == nil {
				t.Fatalf("期望报错但通过了：%s", testCase.json)
			}
		})
	}
}

func TestHostAllowed(t *testing.T) {
	patterns := []string{"jira.corp.com", "*.example.com", "*.corp.cn"}
	cases := []struct {
		host string
		want bool
	}{
		{"jira.corp.com", true},
		{"JIRA.CORP.COM", true},
		{"jira.corp.com.", true},
		{"evil.com", false},
		{"jira.corp.com.evil.com", false},
		{"api.example.com", true},
		{"example.com", true}, // 通配也覆盖裸域本身
		{"deep.api.example.com", true},
		{"notexample.com", false},
		{"a.corp.cn", true},
		{"", false},
	}
	for _, testCase := range cases {
		if got := HostAllowed(testCase.host, patterns); got != testCase.want {
			t.Errorf("HostAllowed(%q) = %v, want %v", testCase.host, got, testCase.want)
		}
	}
	if HostAllowed("jira.corp.com", nil) {
		t.Error("空白名单必须拒绝一切主机")
	}
}

func TestNormalizeHelpers(t *testing.T) {
	got := NormalizeIDList([]string{" Jira-Helper ", "jira-helper", "bad/id", "", "../x", "ok2"})
	want := []string{"jira-helper", "ok2"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("NormalizeIDList = %v, want %v", got, want)
	}
	if NormalizeIDList(nil) != nil {
		t.Fatal("nil 输入必须返回 nil（保持“字段没携带”语义）")
	}
	if hosts := NormalizeHostPatterns([]string{" A.com ", "a.com", ""}); strings.Join(hosts, ",") != "a.com" {
		t.Fatalf("NormalizeHostPatterns = %v", hosts)
	}
	if ModeID(ModeKey("jira-helper")) != "jira-helper" {
		t.Fatal("ModeKey/ModeID 必须互逆")
	}
	if ModeID("kb") != "" || ModeID("plugin:BAD") != "" {
		t.Fatal("非插件页 key 必须返回空串")
	}
}

// IdentityKey 是判「同一个插件」的唯一比较键：目录名、清单 id、配置名单三处共用。
func TestIdentityKeyIsTheSharedComparisonKey(t *testing.T) {
	if IdentityKey(" Jira-Helper ") != "jira-helper" {
		t.Fatalf("IdentityKey = %q, want jira-helper", IdentityKey(" Jira-Helper "))
	}
	if got := NormalizeIDList([]string{"Foo-Broken"}); len(got) != 1 || got[0] != "foo-broken" {
		t.Fatalf("禁用名单必须存归一后的键（与列表侧查找同一把尺子）：%v", got)
	}
}

func TestStripCacheStamp(t *testing.T) {
	cases := []struct {
		rel  string
		want string
		ok   bool
	}{
		{"@20261008T163100Z/index.js", "index.js", true},
		{"@stamp/assets/logo.svg", "assets/logo.svg", true},
		{"index.js", "", false},
		{"@/index.js", "", false},
		{"@bad.stamp/index.js", "", false},
		{"@stamp/", "", false},
		{"assets/@stamp/logo.svg", "", false},
	}
	for _, testCase := range cases {
		got, ok := StripCacheStamp(testCase.rel)
		if ok != testCase.ok || got != testCase.want {
			t.Errorf("StripCacheStamp(%q) = (%q, %v), want (%q, %v)", testCase.rel, got, ok, testCase.want, testCase.ok)
		}
	}
}

// 目录名与清单 id 不一致时必须报成不可用：下游所有查询（存储 / 代理 / 资源 / 删除）都按
// 目录名拼路径，只有 Discover 认清单 id——不一致就是一个「列表里可用、点进去打不开」的插件。
func TestDiscoverFlagsDirNameMismatch(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "demo-plugin")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestName), []byte(manifestJSON("other-id", "index.js")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte("export {}"), 0o600); err != nil {
		t.Fatal(err)
	}
	installed, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(installed) != 1 || installed[0].Valid || !strings.Contains(installed[0].LoadError, "不一致") {
		t.Fatalf("目录名与清单 id 不一致时必须判为不可用：%+v", installed)
	}
}

func TestResolveAssetStaysInsidePluginDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte("export {}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "a.css"), []byte("body{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	ok, err := ResolveAsset(dir, "sub/a.css")
	if err != nil {
		t.Fatalf("合法资源被拒：%v", err)
	}
	if filepath.Base(ok) != "a.css" {
		t.Fatalf("解析结果不对：%s", ok)
	}
	// `..` 被路径清洗吃掉后仍然落在插件目录内（相当于从根重新算），但绝不允许
	// 解析结果跑到目录之外。
	if resolved, err := ResolveAsset(dir, "../../etc/passwd"); err != nil {
		t.Fatalf("意外报错：%v", err)
	} else if !strings.HasPrefix(resolved, dir) {
		t.Fatalf("越界了：%s", resolved)
	}
	if _, err := ResolveAsset(dir, ""); err == nil {
		t.Fatal("空路径必须被拒")
	}
	if _, err := ResolveAsset(dir, string(filepath.Separator)+"etc"); err == nil {
		t.Fatal("绝对路径必须被拒")
	}

	// 符号链接逃逸：环境不支持创建链接时跳过（Windows 需要特权）。
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "escape.txt")
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), link); err != nil {
		t.Skipf("当前环境不支持符号链接：%v", err)
	}
	if _, err := ResolveAsset(dir, "escape.txt"); err == nil {
		t.Fatal("指向目录外的符号链接必须被拒")
	}
}

func TestInspectPackageAndInstallLayout(t *testing.T) {
	root := t.TempDir()
	zipPath := filepath.Join(root, "pkg.zip")
	writeZipFile(t, zipPath, map[string]string{
		ManifestName: manifestJSON("jira-helper", "index.js"),
		"index.js":   "export function mount() {}",
		"style.css":  "body{}",
	})
	info, err := InspectPackage(zipPath)
	if err != nil {
		t.Fatalf("InspectPackage: %v", err)
	}
	if info.Manifest.ID != "jira-helper" || info.Prefix != "" {
		t.Fatalf("unexpected info: %+v", info)
	}
	if len(info.Files) != 3 || info.TotalBytes == 0 {
		t.Fatalf("文件清单不对：%+v", info.Files)
	}

	// 用户把文件夹整个打包：多一层顶层目录也要能装。
	wrapped := filepath.Join(root, "wrapped.zip")
	writeZipFile(t, wrapped, map[string]string{
		"jira-helper/" + ManifestName: manifestJSON("jira-helper", "index.js"),
		"jira-helper/index.js":        "export function mount() {}",
	})
	info, err = InspectPackage(wrapped)
	if err != nil {
		t.Fatalf("带外层目录的包被拒：%v", err)
	}
	if info.Prefix != "jira-helper" {
		t.Fatalf("外层目录 = %q, want jira-helper", info.Prefix)
	}
}

func TestInspectPackageRejectsBadPackages(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		name  string
		files map[string]string
	}{
		{"缺清单", map[string]string{"index.js": "x"}},
		{"缺入口", map[string]string{ManifestName: manifestJSON("abc", "index.js")}},
		{"清单非法", map[string]string{ManifestName: `{"id":"ABC"}`, "index.js": "x"}},
		{"条目带上级引用", map[string]string{
			ManifestName: manifestJSON("abc", "index.js"),
			"index.js":   "x",
			"../evil.js": "x",
		}},
		{"条目是绝对路径", map[string]string{
			ManifestName:  manifestJSON("abc", "index.js"),
			"index.js":    "x",
			"/etc/passwd": "x",
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			zipPath := filepath.Join(root, testCase.name+".zip")
			writeZipFile(t, zipPath, testCase.files)
			if _, err := InspectPackage(zipPath); err == nil {
				t.Fatalf("期望报错但通过了：%s", testCase.name)
			}
		})
	}
}

func TestInspectDirAndCopyTree(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "jira-helper")
	if err := os.MkdirAll(filepath.Join(source, "assets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, ManifestName), []byte(manifestJSON("jira-helper", "index.js")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "index.js"), []byte("export function mount() {}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "assets", "logo.svg"), []byte("<svg/>"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := InspectDir(source)
	if err != nil {
		t.Fatalf("InspectDir: %v", err)
	}
	if info.Manifest.ID != "jira-helper" {
		t.Fatalf("id = %q", info.Manifest.ID)
	}

	// 从目录安装等价于一次 CopyTree：复制出来的目录必须能再被打包并重新校验通过。
	stage := filepath.Join(root, "stage")
	if err := CopyTree(source, stage); err != nil {
		t.Fatalf("CopyTree: %v", err)
	}
	exported := filepath.Join(root, "exported.zip")
	handle, err := os.Create(exported)
	if err != nil {
		t.Fatal(err)
	}
	if err := WritePackage(handle, stage, []byte(`{"token":"t"}`)); err != nil {
		t.Fatalf("WritePackage: %v", err)
	}
	handle.Close()
	roundTrip, err := InspectPackage(exported)
	if err != nil {
		t.Fatalf("导出的包无法再导入：%v", err)
	}
	if roundTrip.Manifest.ID != "jira-helper" {
		t.Fatalf("round-trip id = %q", roundTrip.Manifest.ID)
	}
	if !ContainsFile(roundTrip.Files, DataName) {
		t.Fatalf("带数据的包里缺 %s：%v", DataName, roundTrip.Files)
	}
}

// 演示插件既是示例也是 fixture：它必须始终能通过导入路径（InspectDir / InspectPackage），
// 否则用户在管理页里导入示例包时会直接吃报错。
func TestDemoFixtureRoundTripsThroughPackagePaths(t *testing.T) {
	fixture := filepath.Join("testdata", "demo-plugin")
	dirInfo, err := InspectDir(fixture)
	if err != nil {
		t.Fatalf("示例插件目录无法通过校验：%v", err)
	}
	if dirInfo.Manifest.ID != "demo-toolkit" || dirInfo.Manifest.Entry != "index.js" {
		t.Fatalf("示例插件清单不对劲：%+v", dirInfo.Manifest)
	}
	if _, err := ResolveAsset(fixture, dirInfo.Manifest.Entry); err != nil {
		t.Fatalf("入口无法按资源规则解析：%v", err)
	}

	// 导出路径（WritePackage）产出的包必须能被导入路径（InspectPackage）接受。
	zipPath := filepath.Join(t.TempDir(), "demo-toolkit.zip")
	handle, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := WritePackage(handle, fixture, nil); err != nil {
		t.Fatalf("WritePackage: %v", err)
	}
	if err := handle.Close(); err != nil {
		t.Fatal(err)
	}
	packaged, err := InspectPackage(zipPath)
	if err != nil {
		t.Fatalf("导出的示例包无法再导入：%v", err)
	}
	if packaged.Manifest.ID != dirInfo.Manifest.ID || packaged.Manifest.Entry != dirInfo.Manifest.Entry {
		t.Fatalf("round-trip 不一致：%+v", packaged.Manifest)
	}
}

func TestDiscoverSkipsHiddenAndReportsBroken(t *testing.T) {
	root := t.TempDir()
	good := filepath.Join(root, "good-plugin")
	if err := os.MkdirAll(good, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(good, ManifestName), []byte(manifestJSON("good-plugin", "index.js")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(good, "index.js"), []byte("export {}"), 0o600); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(root, "broken-plugin")
	if err := os.MkdirAll(broken, 0o700); err != nil {
		t.Fatal(err)
	}
	// 备份与数据目录都是点开头：绝不能被当成插件。
	for _, dir := range []string{filepath.Join(root, BackupDirName, "good-plugin"), filepath.Join(root, ".data")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, BackupDirName, "good-plugin", ManifestName), []byte(manifestJSON("good-plugin", "index.js")), 0o600); err != nil {
		t.Fatal(err)
	}

	installed, err := Discover(root)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(installed) != 2 {
		t.Fatalf("扫描到 %d 个插件，期望 2（点开头目录必须跳过）：%+v", len(installed), installed)
	}
	if !installed[0].Valid || installed[0].Manifest.ID != "good-plugin" {
		t.Fatalf("第一个应是合法插件：%+v", installed[0])
	}
	if installed[1].Valid || installed[1].LoadError == "" {
		t.Fatalf("坏插件必须报出原因：%+v", installed[1])
	}
}

func TestValidateRejectsWorkspaceWrite(t *testing.T) {
	// v1 的 host 没有任何写 API：声明 write 必须在校验阶段就被拒，而不是让管理页告诉
	// 用户“可写”而插件调不到东西。
	write := `{"id":"abc","name":"x","version":"1.0.0","entry":"index.js","menu":[{"key":"k","title":"t"}],"permissions":{"workspace":"write"}}`
	if _, err := ParseManifest([]byte(write)); err == nil {
		t.Fatal("workspace=write 必须被拒")
	}
	read := `{"id":"abc","name":"x","version":"1.0.0","entry":"index.js","menu":[{"key":"k","title":"t"}],"permissions":{"workspace":"read"}}`
	if _, err := ParseManifest([]byte(read)); err != nil {
		t.Fatalf("workspace=read 应当通过：%v", err)
	}
}

// 只有包根那一份 data.json 是“插件数据”；assets/data.json 是插件的正常内容，
// 按文件名一刀切会让源目录与导出的包不一致。
func TestWritePackageKeepsNestedDataJSON(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		ManifestName:       manifestJSON("abc", "index.js"),
		"index.js":         "export function mount() {}",
		"assets/data.json": `{"data":true}`,
		DataName:           `{"token":"secret"}`,
	} {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	zipPath := filepath.Join(t.TempDir(), "pkg.zip")
	handle, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := WritePackage(handle, dir, nil); err != nil {
		t.Fatalf("WritePackage: %v", err)
	}
	if err := handle.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := InspectPackage(zipPath)
	if err != nil {
		t.Fatalf("InspectPackage: %v", err)
	}
	if !ContainsFile(info.Files, "assets/data.json") {
		t.Fatalf("嵌套的 data.json 被丢掉了：%v", info.Files)
	}
	if ContainsFile(info.Files, DataName) {
		t.Fatalf("包根的数据文件不该进包：%v", info.Files)
	}
}

// 用户把 `Jira-Helper-1.0.0` 这样的文件夹整个打包很常见：外层目录名不按插件 id
// 规则卡（`..`/绝对路径已在 safeEntryName 挡掉）。
func TestInspectPackageAcceptsFolderNamedLikeVersion(t *testing.T) {
	zipPath := filepath.Join(t.TempDir(), "wrapped.zip")
	writeZipFile(t, zipPath, map[string]string{
		"Jira-Helper-1.0.0/" + ManifestName: manifestJSON("jira-helper", "index.js"),
		"Jira-Helper-1.0.0/index.js":        "export function mount() {}",
	})
	info, err := InspectPackage(zipPath)
	if err != nil {
		t.Fatalf("带版本号的外层目录应当能装：%v", err)
	}
	if info.Prefix != "Jira-Helper-1.0.0" {
		t.Fatalf("prefix = %q", info.Prefix)
	}
}

// 落位前的真实体积复验：zip 头里的 UncompressedSize64 是打包者写的数字，谎报一个小值
// 就能让 InspectPackage 的三条上限全部失效（真正落盘的是 extractZip，限额宽松得多）。
// VerifyContentSizes 是插件体积上限真正生效的地方，所以它必须按盘上真实字节判。
func TestVerifyContentSizesUsesRealBytes(t *testing.T) {
	// 正常内容：通过。
	ok := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ok, "assets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ok, ManifestName), []byte(manifestJSON("demo", "index.js")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ok, "index.js"), []byte("export {}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyContentSizes(ok); err != nil {
		t.Fatalf("正常内容被误拒：%v", err)
	}

	// 单个文件超上限：即使清单/包头说它很小，真实字节说了算。
	big := t.TempDir()
	if err := os.WriteFile(filepath.Join(big, "index.js"), []byte("export {}"), 0o600); err != nil {
		t.Fatal(err)
	}
	huge := filepath.Join(big, "huge.bin")
	handle, err := os.Create(huge)
	if err != nil {
		t.Fatal(err)
	}
	// 写超出上限 1 字节：用 Sparse 式的 Truncate 不占真磁盘，但 stat 出的体积是真的——
	// 复验判的就是 stat 出来的体积（解包产出的文件同样按它计）。
	if err := handle.Truncate(int64(MaxPackageFileBytes) + 1); err != nil {
		t.Fatal(err)
	}
	if err := handle.Close(); err != nil {
		t.Fatal(err)
	}
	if err := VerifyContentSizes(big); err == nil {
		t.Fatal("超上限的单文件必须被拒（按真实字节，不是按声明值）")
	}

	// 总体积超上限：多个文件各不超限，加起来超。
	many := t.TempDir()
	chunk := int64(MaxPackageBytes)/4 + 1
	for i := 0; i < 4; i++ {
		name := filepath.Join(many, fmt.Sprintf("part-%d.bin", i))
		part, err := os.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := part.Truncate(chunk); err != nil {
			t.Fatal(err)
		}
		if err := part.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := VerifyContentSizes(many); err == nil {
		t.Fatal("总体积超限必须被拒")
	}

	// 空目录不是插件内容。
	if err := VerifyContentSizes(t.TempDir()); err == nil {
		t.Fatal("空内容必须被拒")
	}
}

// 目录名与清单 id 必须逐字符一致（含大小写）。只按 IdentityKey 比会在大小写敏感的
// 文件系统上留下「列表里可用、点进去全打不开」的半截支持——下游拼路径一律用清单 id。
func TestDiscoverRejectsDirNameCaseMismatch(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Demo-Toolkit")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestName), []byte(manifestJSON("demo-toolkit", "index.js")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte("export {}"), 0o600); err != nil {
		t.Fatal(err)
	}
	installed, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(installed) != 1 || installed[0].Valid {
		t.Fatalf("大小写不一致的目录必须判为不可用：%+v", installed)
	}
	if !strings.Contains(installed[0].LoadError, "demo-toolkit") {
		t.Fatalf("错误里要说清该改成什么名字：%q", installed[0].LoadError)
	}
}

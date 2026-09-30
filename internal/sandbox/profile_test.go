// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

package sandbox

import (
	"strings"
	"testing"
)

// Seatbelt 取最后一条匹配的规则，因此 profile 里规则的先后顺序就是策略本身。
func TestSeatbeltProfileOrderCarriesThePolicy(t *testing.T) {
	profile := seatbeltProfile(
		[]string{"/w/space", "/dev"},
		[]policyPath{{path: "/w/space/sources", dir: true}},
		[]policyPath{{path: "/Users/u/.ally_agent", dir: true}},
		false,
	)

	if !strings.HasPrefix(profile, "(version 1)\n(allow default)\n") {
		t.Fatalf("profile must start from allow-default, got:\n%s", profile)
	}

	positions := map[string]int{
		"deny file-write*":       strings.Index(profile, "(deny file-write*)"),
		"allow workspace write":  strings.Index(profile, `(subpath "/w/space")`),
		"allow /dev write":       strings.Index(profile, `(subpath "/dev")`),
		"re-deny sources":        strings.Index(profile, `(deny file-write* (subpath "/w/space/sources"))`),
		"deny read credentials":  strings.Index(profile, `(deny file-read* (subpath "/Users/u/.ally_agent"))`),
		"deny network egress":    strings.Index(profile, "(deny network*)"),
		"allow file-write block": strings.Index(profile, "(allow file-write*"),
	}
	for name, idx := range positions {
		if idx < 0 {
			t.Fatalf("%s missing from profile:\n%s", name, profile)
		}
	}
	// 写白名单必须在 deny file-write* 之后（否则一律不可写），re-deny 与
	// deny-read 又必须排在其 allow 之后（否则被 allow default 覆盖）。
	if !(positions["deny file-write*"] < positions["allow file-write block"] &&
		positions["allow file-write block"] < positions["re-deny sources"] &&
		positions["re-deny sources"] < positions["deny read credentials"]) {
		t.Fatalf("rules are out of order:\n%s", profile)
	}
	if !(positions["allow workspace write"] < positions["re-deny sources"]) {
		t.Fatalf("workspace allow must precede the sources re-deny:\n%s", profile)
	}
}

func TestSeatbeltProfileNetworkSwitch(t *testing.T) {
	if profile := seatbeltProfile(nil, nil, nil, true); strings.Contains(profile, "network") {
		t.Fatalf("network must stay open when allowed:\n%s", profile)
	}
	if profile := seatbeltProfile(nil, nil, nil, false); !strings.Contains(profile, "(deny network*)") {
		t.Fatalf("network must be denied when disallowed:\n%s", profile)
	}
}

// 路径里的引号/反斜杠不能把规则撬出 profile 语法之外。
func TestSbplQuoteEscapesProfileSyntax(t *testing.T) {
	if got, want := sbplQuote(`/tmp/a"b\c`), `"/tmp/a\"b\\c"`; got != want {
		t.Fatalf("sbplQuote = %s, want %s", got, want)
	}
}

// 单个文件的禁写要用 literal：subpath 会连它下面（不存在的）路径一起拒掉，
// 而且读起来不像一条精确规则。
func TestSeatbeltDenyWriteUsesLiteralForFiles(t *testing.T) {
	profile := seatbeltProfile(nil, []policyPath{{path: "/w/space/a.txt"}}, nil, true)
	if !strings.Contains(profile, `(deny file-write* (literal "/w/space/a.txt"))`) {
		t.Fatalf("a file re-deny must be a literal rule:\n%s", profile)
	}
}

// bubblewrap 按参数顺序挂载、后挂的覆盖先挂的，所以顺序同样是策略。
func TestBwrapArgsMountOrderCarriesThePolicy(t *testing.T) {
	args := bwrapArgs(
		[]string{"/w/space"},
		[]policyPath{{path: "/w/space/sources", dir: true}},
		[]policyPath{
			{path: "/home/u/.ally_agent", dir: true},
			{path: "/home/u/.ally_agent/api.json"},
		},
		false,
	)
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--die-with-parent",
		"--unshare-pid",
		"--unshare-net",
		"--ro-bind / /",
		"--bind /w/space",
		"--ro-bind /w/space/sources",
		"--tmpfs /home/u/.ally_agent",
		"--ro-bind /dev/null /home/u/.ally_agent/api.json",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("%q missing from:\n%s", want, joined)
		}
	}

	order := []string{"--ro-bind / /", "--bind /w/space", "--ro-bind /w/space/sources", "--tmpfs /home/u/.ally_agent", "--remount-ro /home/u/.ally_agent"}
	previous := -1
	for _, want := range order {
		idx := strings.Index(joined, want)
		if idx <= previous {
			t.Fatalf("%q is out of mount order:\n%s", want, joined)
		}
		previous = idx
	}
}

// 被遮蔽的目录必须是只读的：只遮住读会留下一个可写空洞，命令写进去会成功、
// 退出码为 0，然后在沙箱退出时静静消失。
func TestBwrapMaskedDirsAreReadOnly(t *testing.T) {
	joined := strings.Join(bwrapArgs(nil, nil, []policyPath{{path: "/home/u/.ally_agent", dir: true}}, true), " ")
	if !strings.Contains(joined, "--tmpfs /home/u/.ally_agent --remount-ro /home/u/.ally_agent") {
		t.Fatalf("a masked directory must be remounted read-only:\n%s", joined)
	}
}

func TestBwrapArgsNetworkSwitch(t *testing.T) {
	if joined := strings.Join(bwrapArgs(nil, nil, nil, true), " "); strings.Contains(joined, "--unshare-net") {
		t.Fatalf("network must stay open when allowed: %s", joined)
	}
	if joined := strings.Join(bwrapArgs(nil, nil, nil, false), " "); !strings.Contains(joined, "--unshare-net") {
		t.Fatalf("network must be unshared when disallowed: %s", joined)
	}
}

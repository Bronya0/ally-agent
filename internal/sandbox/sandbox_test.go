// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

package sandbox

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSpecEnforce(t *testing.T) {
	if (Spec{}).Enforce() {
		t.Fatal("zero spec must not enforce")
	}
	if !(Spec{Mode: ModeEnforce}).Enforce() {
		t.Fatal("ModeEnforce must enforce")
	}
}

// 档位由接入开关与宿主策略共同决定，不是用户设置。断言刻意不去复述解析函数自己的
// 算法（那只能写出恒真用例），而是钉住三件各自独立的事：两个方向的对偶关系、档位
// 只有两种取值、以及接入时哪一族平台在强制。
func TestResolvedModeFollowsThePlatform(t *testing.T) {
	mode := ResolvedMode()
	forced := ModeForced()

	// 强制 ⇒ 真的 enforce；不强制 ⇒ 真的关闭。两侧分开断言，任一侧坏了都会响。
	if forced && mode != ModeEnforce {
		t.Fatalf("a platform that mandates confinement must resolve to enforce, got %q", mode)
	}
	if !forced && mode != ModeOff {
		t.Fatalf("a platform that does not mandate confinement must resolve to off, got %q", mode)
	}
	// 没有任何配置能影响档位：解析结果只可能落在这两个值上。
	if mode != ModeOff && mode != ModeEnforce {
		t.Fatalf("ResolvedMode() = %q, want off or enforce", mode)
	}
	// 沙箱没接入时宿主策略一概不生效：三个平台都只走安全围栏。
	if !Attached() {
		if mode != ModeOff || forced {
			t.Fatalf("a detached sandbox must resolve to off on every platform, got mode=%q forced=%v", mode, forced)
		}
		return
	}
	// 接入时 macOS 是这一版唯一强制沙箱的平台。钉住它是因为降级文案（Warning）里
	// 写死了 macOS：哪天别的平台也开始强制，那句话必须先改。
	if runtime.GOOS == "darwin" {
		if !forced || mode != ModeEnforce {
			t.Fatalf("darwin must mandate the sandbox when attached, got mode=%q forced=%v", mode, forced)
		}
		return
	}
	if forced || mode != ModeOff {
		t.Fatalf("%s must not mandate the sandbox, got mode=%q forced=%v", runtime.GOOS, mode, forced)
	}
}

// 只在「强制开启但后端用不了」时出声：其余情况本机本来就不跑沙箱，没有话可说。
func TestWarningSpeaksOnlyForAnUnavailableForcedPlatform(t *testing.T) {
	warning := Warning()
	if !ModeForced() || Available() {
		if warning != "" {
			t.Fatalf("Warning() = %q, want silence", warning)
		}
		return
	}
	if warning == "" {
		t.Fatal("a mandated sandbox with an unusable backend must warn")
	}
	if !strings.Contains(warning, UnavailableReason()) {
		t.Fatalf("warning must name the reason %q, got %q", UnavailableReason(), warning)
	}
}

// 模型看到的必须是可以照做的下一步，而不是一句权限错误：可写目录要列出来，
// 并且和命令围栏的报错用同一种写法（主工作区 / 附加工作区）。
func TestWriteDeniedHintListsWritableRootsAndNextStep(t *testing.T) {
	roots := []string{t.TempDir(), t.TempDir()}
	hint := WriteDeniedHint(roots)
	checks := []string{"原因：", "处理方式：", "主工作区", "附加工作区", "手动在终端执行"}
	for _, root := range roots {
		checks = append(checks, filepath.ToSlash(root))
	}
	for _, want := range checks {
		if !strings.Contains(hint, want) {
			t.Fatalf("hint must contain %q, got %q", want, hint)
		}
	}
}

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

func TestParseMode(t *testing.T) {
	cases := map[string]Mode{
		"":           ModeOff,
		"off":        ModeOff,
		"enforce":    ModeEnforce,
		"  ENFORCE ": ModeEnforce,
		"on":         ModeOff, // 未知取值只能落到关闭，绝不能意外打开沙箱
		"true":       ModeOff,
	}
	for configured, want := range cases {
		if got := ParseMode(configured); got != want {
			t.Fatalf("ParseMode(%q) = %q, want %q", configured, got, want)
		}
	}
}

func TestResolveModeFollowsPlatformCapability(t *testing.T) {
	if ResolveMode("off") != ModeOff {
		t.Fatal("off must stay off")
	}
	switch runtime.GOOS {
	case "darwin", "linux":
		if ResolveMode("enforce") != ModeEnforce {
			t.Fatal("darwin/linux must honour enforce")
		}
	default:
		if ResolveMode("enforce") != ModeOff {
			t.Fatalf("%s has no backend and must collapse to off", runtime.GOOS)
		}
	}
}

func TestWarningIsSilentWhenSandboxIsOff(t *testing.T) {
	for _, configured := range []string{"", "off", "on"} {
		if warning := Warning(configured); warning != "" {
			t.Fatalf("Warning(%q) = %q, want silence", configured, warning)
		}
	}
}

// 没有后端的平台上开启沙箱必须出声：否则开关看起来生效、实际什么都没做。
func TestWarningOnPlatformWithoutBackend(t *testing.T) {
	switch runtime.GOOS {
	case "darwin", "linux":
		t.Skip("darwin/linux have a backend; availability is probed there, not asserted here")
	}
	warning := Warning(string(ModeEnforce))
	if warning == "" {
		t.Fatalf("%s must warn when the sandbox is requested", runtime.GOOS)
	}
	if !strings.Contains(warning, UnavailableReason()) {
		t.Fatalf("warning must name the reason %q, got %q", UnavailableReason(), warning)
	}
}

// 用户明确要求沙箱时，失败信息必须说清"拒绝执行"而不是降级执行。
func TestUnavailableErrorExplainsFailClosed(t *testing.T) {
	err := UnavailableError()
	if err == nil {
		t.Fatal("UnavailableError must not be nil")
	}
	message := err.Error()
	if !strings.Contains(message, "fail-closed") {
		t.Fatalf("message must say the command was refused, got %q", message)
	}
	if !strings.Contains(message, UnavailableReason()) || !strings.Contains(message, UnavailableRemediation()) {
		t.Fatalf("message must carry the reason and the fix, got %q", message)
	}
}

// 平台塌缩成「关闭」时命令照常运行：提示不能搬出"会被拒绝执行"的说法，也不能
// 叫用户去关一个已经关掉的开关——那读起来像应用没应用自己的选择。
func TestWarningOnPlatformWithoutBackendNamesWhatStillGuards(t *testing.T) {
	switch runtime.GOOS {
	case "darwin", "linux":
		t.Skip("darwin/linux have a backend; the collapse path does not run there")
	}
	warning := Warning(string(ModeEnforce))
	if !strings.Contains(warning, "已按「关闭」处理") {
		t.Fatalf("warning must say the setting was folded to off, got %q", warning)
	}
	if strings.Contains(warning, "fail-closed") {
		t.Fatalf("commands keep running without a backend, so the warning must not promise a refusal, got %q", warning)
	}
	if !strings.Contains(warning, safetyFenceNotice()) {
		t.Fatalf("warning must name what still guards the command, got %q", warning)
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

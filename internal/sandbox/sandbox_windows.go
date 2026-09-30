// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

//go:build windows

package sandbox

import "os/exec"

// platformHasBackend reports that this build has no backend for the platform.
func platformHasBackend() bool { return false }

// platformWriteExtras is empty: no backend renders a writable surface here.
var platformWriteExtras []string

// Available reports that this build has no Windows backend. Windows would need a
// restricted token or an AppContainer plus ACL work on the workspace; until
// that exists the product setting collapses to off (ResolveMode) so the command
// tool keeps working exactly as it did before sandboxing existed.
func Available() bool { return false }

// UnavailableReason names why the backend cannot run.
func UnavailableReason() string {
	return "Windows 没有内置的操作系统级沙箱后端"
}

// UnavailableRemediation names the way out. The fence wording is shared with
// the warning path so the two surfaces cannot drift apart.
func UnavailableRemediation() string {
	return "在设置里把命令沙箱改回「关闭」。" + safetyFenceNotice()
}

// Wrap returns argv unchanged: there is no runner to prefix it with.
func Wrap(spec Spec, argv []string) ([]string, bool) { return argv, false }

// PrepareCommand is a no-op: with no confinement there is nothing to prepare.
func PrepareCommand(cmd *exec.Cmd) {}

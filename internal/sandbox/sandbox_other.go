// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

//go:build !darwin && !linux && !windows

package sandbox

import "os/exec"

// platformHasBackend reports that this build has no backend for the platform.
func platformHasBackend() bool { return false }

// platformForcedMode: 本平台不强制沙箱，所以解析结果恒为关闭（见 ResolvedMode）。
func platformForcedMode() (Mode, bool) { return ModeOff, false }

// platformWriteExtras is empty: no backend renders a writable surface here.
var platformWriteExtras []string

// Available reports that this build has no backend for the platform. As on
// Windows, ResolvedMode is off here so the command tool keeps working.
func Available() bool { return false }

// UnavailableReason names why the backend cannot run.
func UnavailableReason() string {
	return "当前平台没有操作系统级沙箱后端"
}

// Wrap returns argv unchanged: there is no runner to prefix it with.
func Wrap(spec Spec, argv []string) ([]string, bool) { return argv, false }

// PrepareCommand is a no-op: with no confinement there is nothing to prepare.
func PrepareCommand(cmd *exec.Cmd) {}

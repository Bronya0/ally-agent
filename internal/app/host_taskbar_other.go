// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

//go:build !windows

package app

// flashTaskbarWindowIfInactive is the Windows-only run-end attention cue;
// other platforms have no taskbar flash equivalent. 运行中不设任务栏
// 进度动画（TBPF_INDETERMINATE 跑马灯会被感知为持续闪烁）。
func flashTaskbarWindowIfInactive() {}

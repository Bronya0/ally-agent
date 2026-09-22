// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

//go:build windows

package app

import (
	"os/exec"
	"syscall"
)

// createNoWindow 即 CREATE_NO_WINDOW：子进程拿不到控制台，因此不会闪出黑框。
// 不能用 SysProcAttr.HideWindow 代替它——原因见 terminalCommand 注释。
const createNoWindow = 0x08000000

// terminalCommand 在 Windows 上于指定目录打开终端：优先 Windows Terminal
// （wt.exe -w new），未安装时回退到新开的 cmd.exe 窗口。
//
// 两个实测坑（2026-09-22，Windows 11 + Windows Terminal 1.24）：
//
//  1. 绝不能设 SysProcAttr.HideWindow。它给子进程的是
//     STARTF_USESHOWWINDOW + SW_HIDE，而这个“隐藏”会顺着激活链传给真正被
//     创建出来的终端窗口：实测 `wt.exe -w new -d <dir>` 在 HideWindow 下建出的
//     窗口 IsWindowVisible=False（用户看到的就是“点了没反应”），
//     CreateProcess 的 CREATE_NO_WINDOW 下则正常可见。想“不闪黑框”只能用它。
//  2. `wt.exe -d` 是把标签页交给“最近使用的那个” Windows Terminal 窗口；
//     若那个窗口处于最小化，新标签页落进去也不会把窗口带回来（实测窗口始终
//     minimized）——终端就在用户眼皮底下却看不见。故这里显式 `-w new`，
//     保证每次都新开一个可见窗口。
func terminalCommand(path string) *exec.Cmd {
	if _, err := exec.LookPath("wt.exe"); err == nil {
		cmd := exec.Command("wt.exe", "-w", "new", "-d", path)
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
		return cmd
	}
	// 未安装 Windows Terminal：新开一个 cmd.exe 窗口（start 负责让新窗口脱离
	// 本进程持有自己的控制台）。同样不能用 HideWindow：中间的 cmd.exe 宿主被
	// 隐藏后，start 建出的那个 cmd.exe 窗口会继承隐藏状态。
	cmd := exec.Command("cmd.exe", "/c", "start", "", "/D", path, "cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}
	return cmd
}

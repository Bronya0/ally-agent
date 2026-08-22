// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import "time"

// statTimesResult 是平台无关的 stat 时间集合（全部为本地时间）。
type statTimesResult struct {
	access time.Time
	change time.Time
	birth  time.Time
}

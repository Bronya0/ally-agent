// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import "fmt"

// FormatDurationMS renders a duration as whole seconds for anything a person
// reads: tool cards, sub-agent progress summaries, proxy and SSH checks, and
// scheduled-task summaries. Sub-second work reads "<1s" instead of a
// millisecond count, and a rounded minute rolls over rather than showing
// "60s". The desktop's fmtDur delegates here, so one number never reads "3.4s"
// in one place and "3400ms" in another.
func FormatDurationMS(ms int64) string {
	switch {
	case ms <= 0:
		return ""
	case ms < 1000:
		return "<1s"
	}
	secs := (ms + 500) / 1000
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	return fmt.Sprintf("%dm%02ds", secs/60, secs%60)
}

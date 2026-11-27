// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package command

import "testing"

func TestContainsGrepCommandUsesExecutablePositions(t *testing.T) {
	for _, tc := range []struct {
		command string
		blocked bool
	}{
		{"grep needle file.txt", true},
		{"printf needle | grep needle | grep other", true},
		{"env LC_ALL=C /usr/bin/grep needle file.txt", true},
		{"bash -c 'grep needle file.txt'", true},
		{"busybox grep needle file.txt", true},
		{"fgrep needle file.txt", true},
		{"egrep needle file.txt", true},
		{"grep.exe needle file.txt", true},
		{"rg -p --color never -e 'grep|rm' -- src", false},
		{"git log --grep=needle", false},
		{"printf 'grep needle file.txt'", false},
		{"cat <<'EOF'\ngrep needle file.txt\nEOF", false},
	} {
		t.Run(tc.command, func(t *testing.T) {
			if got := ContainsGrepCommand(tc.command); got != tc.blocked {
				t.Fatalf("blocked=%v, want %v", got, tc.blocked)
			}
		})
	}
}

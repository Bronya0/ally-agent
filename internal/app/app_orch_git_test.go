// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import "testing"

func TestParseGitStatusV2(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want GitStatus
	}{
		{
			name: "clean repo with upstream ahead/behind",
			out:  "# branch.oid abc123\x00# branch.head main\x00# branch.upstream origin/main\x00# branch.ab +2 -1\x00",
			want: GitStatus{IsRepo: true, Branch: "main", Ahead: 2, Behind: 1},
		},
		{
			name: "mixed worktree states plus untracked",
			out: "# branch.head main\x00" +
				"1 .M N... 1 2 3 4 5 file-mod.txt\x00" +
				"1 A. N... 1 2 3 4 5 file-add.txt\x00" +
				"1 D. N... 1 2 3 4 5 file-del.txt\x00" +
				"? file-new.txt\x00",
			want: GitStatus{IsRepo: true, Branch: "main", Modified: 1, Added: 2, Deleted: 1},
		},
		{
			name: "rename skips original path token",
			out: "# branch.head main\x00" +
				"2 R. N... 1 2 3 4 5 new-name\x00old-name\x00" +
				"1 .M N... 1 2 3 4 5 other.txt\x00",
			want: GitStatus{IsRepo: true, Branch: "main", Modified: 2},
		},
		{
			name: "detached head without upstream",
			out:  "# branch.oid abc\x00# branch.head (detached)\x00",
			want: GitStatus{IsRepo: true, Branch: "(detached)"},
		},
		{
			name: "empty output keeps repo flag with zero counts",
			out:  "",
			want: GitStatus{IsRepo: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseGitStatusV2(tt.out)
			if got != tt.want {
				t.Fatalf("parseGitStatusV2 = %#v, want %#v", got, tt.want)
			}
		})
	}
}

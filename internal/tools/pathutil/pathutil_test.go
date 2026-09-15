// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package pathutil

import "testing"

// TestVCSMetadataReasonBlocksDirectoryAndContent pins the single judgement the
// write, delete, and command guards share: .git itself and anything below it.
func TestVCSMetadataReasonBlocksDirectoryAndContent(t *testing.T) {
	blocked := []string{
		".git",
		"repo/.git",
		"repo/.git/config",
		"repo/.git/hooks/pre-commit",
		"/home/me/repo/.git/HEAD",
		"repo/.svn/entries",
		"repo/.hg/store",
	}
	for _, path := range blocked {
		got, reason := VCSMetadataReason(path)
		if !got || reason == "" {
			t.Fatalf("VCSMetadataReason(%q) = %v, %q; want blocked with a reason", path, got, reason)
		}
	}
}

// TestVCSMetadataReasonAllowsSimilarNames guards against over-blocking: ordinary
// dot-directories and files that merely start with ".git".
func TestVCSMetadataReasonAllowsSimilarNames(t *testing.T) {
	allowed := []string{
		".github/workflows/build.yml",
		"repo/.gitattributes",
		"notes/.gitkeep",
		"gitignore",
		"git.ts",
	}
	for _, path := range allowed {
		if got, reason := VCSMetadataReason(path); got {
			t.Fatalf("VCSMetadataReason(%q) = true, %q; want allowed", path, reason)
		}
	}
}

// TestCanonicalPathKeepsNamesOnNonWindowsHosts: elsewhere a trailing dot is part
// of the name, so the canonical form must leave it alone.
func TestCanonicalPathKeepsNamesOnNonWindowsHosts(t *testing.T) {
	if IsWindows {
		t.Skip("windows canonicalization is covered by the alias test")
	}
	if got := CanonicalPath("/ws/.git."); got != "/ws/.git." {
		t.Fatalf("CanonicalPath(/ws/.git.) = %q, want the name untouched", got)
	}
}

// TestCanonicalPathStripsWindowsTrailingDotsAndSpaces: Win32 drops trailing dots
// and spaces, so ".git." opens the real .git while filepath.Clean keeps the dot —
// the literal comparison the delete guard used to make was therefore bypassable.
func TestCanonicalPathStripsWindowsTrailingDotsAndSpaces(t *testing.T) {
	if !IsWindows {
		t.Skip("windows path aliasing")
	}
	cases := []struct{ in, want string }{
		{`C:\ws\.git.`, `C:\ws\.git`},
		{`C:\ws\.git `, `C:\ws\.git`},
		{`C:\ws\dir. \file.txt`, `C:\ws\dir\file.txt`},
		{`C:\ws\.git..\config`, `C:\ws\.git\config`},
		{`C:\ws\..\other`, `C:\other`},
	}
	for _, tc := range cases {
		if got := CanonicalPath(tc.in); got != tc.want {
			t.Fatalf("CanonicalPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if blocked, _ := VCSMetadataReason(`C:\ws\.git.`); !blocked {
		t.Fatal("the trailing-dot alias of .git must be recognized")
	}
}

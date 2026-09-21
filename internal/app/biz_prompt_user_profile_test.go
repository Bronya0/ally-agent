// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General Public
// License v3. See the LICENSE file for details.
package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeUserProfileFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), userProfileFileName)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestUserProfilePromptPartEmptyWithoutFile(t *testing.T) {
	if got := buildUserProfilePromptPart(filepath.Join(t.TempDir(), "missing", userProfileFileName)); got != "" {
		t.Fatalf("expected no block without a profile file, got %q", got)
	}
	// A file whose body is only a frontmatter block injects nothing: there is no
	// user content to carry.
	path := writeUserProfileFile(t, "---\ndescription: nothing here\n---\n\n")
	if got := buildUserProfilePromptPart(path); got != "" {
		t.Fatalf("expected no block for an empty body, got %q", got)
	}
}

func TestUserProfilePromptPartInjectsBodyWithoutFrontmatter(t *testing.T) {
	path := writeUserProfileFile(t, "---\ndescription: 用户资料\n---\n\n- 称呼：老唐\n- 语言：中文\n")
	got := buildUserProfilePromptPart(path)
	if !strings.Contains(got, `<user-profile priority="lower-than-core">`) || !strings.Contains(got, "End user profile.") {
		t.Fatalf("expected the lower-than-core wrapper, got %q", got)
	}
	for _, want := range []string{"- 称呼：老唐", "- 语言：中文"} {
		if !strings.Contains(got, want) {
			t.Fatalf("profile line %q missing from %q", want, got)
		}
	}
	if strings.Contains(got, "description:") {
		t.Fatalf("the frontmatter must not be injected, got %q", got)
	}
	if !strings.Contains(got, userProfileDisplayPath) {
		t.Fatalf("the rules must name the file the model is supposed to edit, got %q", got)
	}
}

func TestUserProfilePathFeedsThePromptPart(t *testing.T) {
	redirectAppStateDir(t)
	path := userProfilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("- 称呼：老唐\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := buildUserProfilePromptPart(userProfilePath())
	if !strings.Contains(got, "- 称呼：老唐") || !strings.Contains(got, "<user-profile") {
		t.Fatalf("the profile under the app data directory must feed the prompt block, got %q", got)
	}
}

func TestUserProfilePromptPartKeepsHeadUnderCap(t *testing.T) {
	var lines []string
	for i := 0; i < 40; i++ {
		lines = append(lines, fmt.Sprintf("- line %02d %s", i, strings.Repeat("字", 100)))
	}
	got := buildUserProfilePromptPart(writeUserProfileFile(t, strings.Join(lines, "\n")))
	if !strings.Contains(got, "- line 00 ") {
		t.Fatalf("the head of the profile must survive the trim, got %.80q", got)
	}
	if strings.Contains(got, "- line 39 ") {
		t.Fatalf("the tail must be trimmed past the cap, got %.80q", got)
	}
	if !strings.Contains(got, userProfileTruncatedMark) {
		t.Fatal("a trimmed profile block must say it was trimmed")
	}
	block := got[strings.Index(got, "<user-profile"):]
	if len(block) > userProfileMaxBytes+400 {
		t.Fatalf("injected block = %d bytes, want around the %d-byte cap", len(block), userProfileMaxBytes)
	}
}

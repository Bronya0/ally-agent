// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package edit

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestGenerateEditDiffPreviewFoldsUnchangedRuns: 大文件 + 两处分散变更时，
// span 内大段相同的行必须折叠为省略号，而不是整体标成删除+添加。
func TestGenerateEditDiffPreviewFoldsUnchangedRuns(t *testing.T) {
	var oldLines, newLines []string
	for i := 1; i <= 800; i++ {
		line := fmt.Sprintf("line %03d: const value = %d;", i, i)
		oldLines = append(oldLines, line)
		newLines = append(newLines, line)
	}
	newLines[1] = "line 002: const value = 999;"     // 第 2 行改一行
	newLines[789] = "line 790: const value = 12345;" // 第 790 行改一行

	diff := GenerateEditDiffPreview(strings.Join(oldLines, "\n"), strings.Join(newLines, "\n"), 1<<20)
	if diff == "" {
		t.Fatal("expected non-empty diff")
	}

	// 中间大段相同行应折叠为省略号，但保留头尾各 3 行上下文。
	if !strings.Contains(diff, "unchanged lines") {
		t.Fatalf("expected folded unchanged run, got:\n%s", diff)
	}
	if !strings.Contains(diff, "line 003") || !strings.Contains(diff, "line 789") {
		t.Fatalf("expected 3 context lines around each change, got:\n%s", diff)
	}
	// 中间更深处的行（如 500）不应出现。
	if strings.Contains(diff, "line 500") {
		t.Fatalf("deep unchanged middle lines must not appear in diff:\n%s", diff)
	}

	// 只有两处真实变化：2 个删除行 + 2 个添加行。
	removed := 0
	added := 0
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			removed++
		} else if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			added++
		}
	}
	if removed != 2 || added != 2 {
		t.Fatalf("expected 2 removed and 2 added lines, got %d removed %d added:\n%s", removed, added, diff)
	}
}

// TestGenerateEditDiffPreviewKeepsShortUnchangedRuns: 相同行少时不折叠，
// 仍以 context 行输出（保持上下文可读性）。
func TestGenerateEditDiffPreviewKeepsShortUnchangedRuns(t *testing.T) {
	oldText := "a\nb\nc\nd\ne"
	newText := "a\nb\nX\nd\ne"
	diff := GenerateEditDiffPreview(oldText, newText, 1<<20)
	if diff == "" {
		t.Fatal("expected non-empty diff")
	}
	if strings.Contains(diff, "unchanged lines") {
		t.Fatalf("short unchanged runs must not fold:\n%s", diff)
	}
	if !strings.Contains(diff, "-c") || !strings.Contains(diff, "+X") {
		t.Fatalf("expected c->X change, got:\n%s", diff)
	}
}

// TestGenerateEditDiffPreviewInlineLongLine: 超长单行（minified 文件）改中间
// 一个词时，diff 必须只展示改动片段而不是整行，且改动本身可见。
func TestGenerateEditDiffPreviewInlineLongLine(t *testing.T) {
	aaa := strings.Repeat("a", 5000)
	bbb := strings.Repeat("b", 5000)
	oldLine := aaa + "OLD" + bbb
	newLine := aaa + "NEW" + bbb

	diff := GenerateEditDiffPreview(oldLine, newLine, 1<<20)
	if diff == "" {
		t.Fatal("expected non-empty diff")
	}
	// 输出必须远小于原行长度（1 万字符），且变化区 OLD/NEW 可见。
	if len(diff) > 2048 {
		t.Fatalf("long-line diff must be bounded, got %d bytes", len(diff))
	}
	if !strings.Contains(diff, "OLD") || !strings.Contains(diff, "NEW") {
		t.Fatalf("changed middle must be visible, got:\n%s", diff)
	}
	// 未变化的公共前缀不能整段出现。
	if strings.Contains(diff, aaa) {
		t.Fatalf("unchanged long prefix must be clipped, got:\n%s", diff)
	}
}

// TestGenerateEditDiffPreviewInlineLongLineUTF8: 超长行内 diff 不得截断
// 多字节字符（中文），输出必须是合法 UTF-8。
func TestGenerateEditDiffPreviewInlineLongLineUTF8(t *testing.T) {
	oldLine := strings.Repeat("数据", 3000) + "旧值" + strings.Repeat("填充", 3000)
	newLine := strings.Repeat("数据", 3000) + "新值" + strings.Repeat("填充", 3000)

	diff := GenerateEditDiffPreview(oldLine, newLine, 1<<20)
	if diff == "" {
		t.Fatal("expected non-empty diff")
	}
	if !utf8.ValidString(diff) {
		t.Fatalf("diff must be valid UTF-8")
	}
	// 行级 diff + 超长行行内裁剪：变化片段（旧值/新值）必须可见，
	// 且行内裁剪不截断多字节字符。
	if !strings.Contains(diff, "旧值") || !strings.Contains(diff, "新值") {
		t.Fatalf("changed middle must be visible, got:\n%s", diff)
	}
}

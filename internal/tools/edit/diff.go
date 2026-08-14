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
	"unicode/utf8"

	"github.com/sergi/go-diff/diffmatchpatch"
)

type ChangedLineRange struct {
	FirstChangedLine int
	LastChangedLine  int
}

func ComputeChangedLineRange(original, result string) ChangedLineRange {
	if original == result {
		return ChangedLineRange{}
	}

	if len(original) == 0 {
		return ChangedLineRange{FirstChangedLine: 1, LastChangedLine: visibleLineCount(result)}
	}

	if strings.HasPrefix(result, original) && strings.HasSuffix(original, "\n") {
		return ChangedLineRange{
			FirstChangedLine: visibleLineCount(original) + 1,
			LastChangedLine:  visibleLineCount(result),
		}
	}

	firstDiff := 0
	minLen := len(original)
	if len(result) < minLen {
		minLen = len(result)
	}
	for firstDiff < minLen && original[firstDiff] == result[firstDiff] {
		firstDiff++
	}
	if firstDiff == minLen && len(original) == len(result) {
		return ChangedLineRange{}
	}

	lastOrig := len(original) - 1
	lastRes := len(result) - 1
	for lastOrig >= firstDiff && lastRes >= firstDiff && original[lastOrig] == result[lastRes] {
		lastOrig--
		lastRes--
	}

	// strings.Count uses an optimized scan (Rabin-Karp / byte cancellation)
	// that is measurably faster than a per-byte Go loop over large texts, and
	// it avoids allocating a slice like strings.Split would. Counting newlines
	// in the prefix is O(prefix) regardless, but the constant is much smaller.
	indexToLine := func(charIdx int, text string) int {
		if charIdx <= 0 {
			return 1
		}
		if charIdx > len(text) {
			charIdx = len(text)
		}
		return strings.Count(text[:charIdx], "\n") + 1
	}

	firstChangedLine := indexToLine(firstDiff+1, result)
	lastChangedLine := indexToLine(lastRes+1, result)

	return ChangedLineRange{FirstChangedLine: firstChangedLine, LastChangedLine: lastChangedLine}
}

const (
	maxReadRangeLines          = 10000
	changedLineContextLines    = 2
	changedLineMaxOutputLines  = 12
	changedLineTextBudgetBytes = 50 * 1024

	editDiffLCSLineProductLimit = 250000
	editDiffContextLines        = 3
	editDiffTruncatedLineLimit  = 80
	// unchangedRunFoldThreshold：span 内连续相同行超过该数量时折叠为一行
	// “… N unchanged lines …”，避免把大段未变化的行标成删除+添加。
	unchangedRunFoldThreshold = 4

	// 超长行行内 diff 阈值与显示预算：行字节数超过 longLineClipThreshold
	// 即触发裁剪（minified JSON / 生成文件等单行超长场景），公共前后缀各
	// 保留 inlineDiffContextRunes 个字符，变化区最多显示
	// inlineDiffMiddleMaxRunes 个字符，保证渲染行始终有界。
	// 512 字节覆盖普通代码行（长字符串字面量、注释、日志格式串），
	// 只有真正超长的行（生成/minified 内容）才被裁剪。
	longLineClipThreshold    = 512
	inlineDiffContextRunes   = 40
	inlineDiffMiddleMaxRunes = 160

	// go-diff 窗口字节预算：窗口文本总字节（含换行）超过该值时不跑 Myers
	// 字符级 diff（O(ND) 在超长行上可能卡顿），直接走线性截断路径。
	editDiffWindowMaxBytes = 512 * 1024
)

type readRangeRequest struct {
	StartLine     int
	EndLine       int
	LineCount     int
	ContextBefore int
	ContextAfter  int
}

type readPreviewResult struct {
	Content       string
	RawContent    string
	TotalLines    int
	StartLine     int
	EndLine       int
	NextStartLine int
	Truncated     bool
	RangeStatus   string
	EmptyRange    bool
}

func GenerateEditDiffPreview(oldContent, newContent string, maxBytes int) string {
	if oldContent == newContent {
		return ""
	}
	oldLines := contentLinesForDiff(oldContent)
	newLines := contentLinesForDiff(newContent)
	if len(oldLines) == 0 && len(newLines) == 0 {
		return ""
	}

	// 先定位变更跨度（公共前后缀相同行），只对窗口做 diff：
	// 窗口小时用 go-diff（Myers 行级）精确计算，窗口超大时走线性截断路径，
	// 保证大文件的耗时与内存都有界。
	oldChangeStart, oldChangeEnd, newChangeStart, newChangeEnd, ok := changedLineSpans(oldLines, newLines)
	if !ok {
		return ""
	}

	oldStart := maxInt(0, oldChangeStart-editDiffContextLines)
	oldEnd := minInt(len(oldLines), oldChangeEnd+editDiffContextLines)
	newStart := maxInt(0, newChangeStart-editDiffContextLines)
	newEnd := minInt(len(newLines), newChangeEnd+editDiffContextLines)

	// 窗口行数乘积和窗口文本字节都受限时才用 go-diff（Myers 字符级），
	// 否则走线性截断路径，保证大文件/超长行的耗时与内存都有界。
	var body []string
	windowBytes := windowTextBytes(oldLines[oldStart:oldEnd], newLines[newStart:newEnd])
	if (oldEnd-oldStart)*(newEnd-newStart) <= editDiffLCSLineProductLimit && windowBytes <= editDiffWindowMaxBytes {
		body = diffWindowToBody(oldLines[oldStart:oldEnd], newLines[newStart:newEnd], oldStart+1, newStart+1)
	} else {
		body = generateTruncatedEditDiffHunkBody(oldLines, newLines, oldStart, oldChangeStart, oldChangeEnd, oldEnd, newStart, newChangeStart, newChangeEnd, newEnd)
	}
	diff := formatUnifiedDiffHunk(oldStart+1, oldEnd-oldStart, newStart+1, newEnd-newStart, body)
	return truncateDiffText(diff, maxBytes)
}

// windowTextBytes 返回两个窗口按行拼成文本后的总字节数（含行间换行）。
func windowTextBytes(oldWindow, newWindow []string) int {
	n := 0
	for _, l := range oldWindow {
		n += len(l) + 1
	}
	for _, l := range newWindow {
		n += len(l) + 1
	}
	return n
}

func contentLinesForDiff(content string) []string {
	if content == "" {
		return []string{}
	}
	// 预分配行数容量，避免 split 过程中反复扩容；用 IndexByte 切分
	// （SIMD 优化）替代 strings.Split 的逐段查找，行为完全一致：
	// 以 \n 结尾时末尾空段不产生。
	lines := make([]string, 0, strings.Count(content, "\n")+1)
	start := 0
	for {
		idx := strings.IndexByte(content[start:], '\n')
		if idx < 0 {
			if start < len(content) {
				lines = append(lines, content[start:])
			}
			return lines
		}
		lines = append(lines, content[start:start+idx])
		start += idx + 1
	}
}

// inlineDiffPair renders a -/+ line pair. When either line exceeds the long
// line threshold, the pair is clipped to its changed region: common prefix and
// suffix are trimmed to short windows and the changed middle is capped, so a
// 1MB single-line edit shows only the edited part.
func inlineDiffPair(oldLine, newLine string) (string, string) {
	if len(oldLine) <= longLineClipThreshold && len(newLine) <= longLineClipThreshold {
		return "-" + oldLine, "+" + newLine
	}
	oldR := []rune(oldLine)
	newR := []rune(newLine)
	cp := 0
	for cp < len(oldR) && cp < len(newR) && oldR[cp] == newR[cp] {
		cp++
	}
	cs := 0
	for cs < len(oldR)-cp && cs < len(newR)-cp && oldR[len(oldR)-1-cs] == newR[len(newR)-1-cs] {
		cs++
	}
	prefix := clipTail(oldR[:cp], inlineDiffContextRunes)
	suffix := clipHead(oldR[len(oldR)-cs:], inlineDiffContextRunes)
	oldMid := clipHead(oldR[cp:len(oldR)-cs], inlineDiffMiddleMaxRunes)
	newMid := clipHead(newR[cp:len(newR)-cs], inlineDiffMiddleMaxRunes)
	return "-" + prefix + oldMid + suffix, "+" + prefix + newMid + suffix
}

// clipLongLine bounds an over-long diff body line (keeping its leading marker)
// to a head/tail window.
func clipLongLine(line string) string {
	if len(line) <= longLineClipThreshold {
		return line
	}
	marker := line[0]
	runes := []rune(line[1:])
	if len(runes) <= inlineDiffContextRunes*2 {
		return line
	}
	return string(marker) + string(runes[:inlineDiffContextRunes]) + "…" + string(runes[len(runes)-inlineDiffContextRunes:])
}

// clipHead keeps the first n runes, appending an ellipsis when clipped.
func clipHead(r []rune, n int) string {
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}

// clipTail keeps the last n runes, prepending an ellipsis when clipped.
func clipTail(r []rune, n int) string {
	if len(r) <= n {
		return string(r)
	}
	return "…" + string(r[len(r)-n:])
}

// splitDiffTextLines 把 diff 段文本按行拆分（去掉末尾空串，保留行内空行）。
func splitDiffTextLines(text string) []string {
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// countTextLines 统计文本行数（与 splitDiffTextLines 结果一致），
// 只做 O(n) 扫描，不分配行切片。
func countTextLines(text string) int {
	if text == "" {
		return 0
	}
	n := strings.Count(text, "\n")
	if !strings.HasSuffix(text, "\n") {
		n++
	}
	return n
}

// headLinesOf 返回文本前 n 行（不含换行符），不足 n 行时返回全部。
func headLinesOf(text string, n int) []string {
	out := make([]string, 0, n)
	start := 0
	for len(out) < n {
		idx := strings.IndexByte(text[start:], '\n')
		if idx < 0 {
			out = append(out, text[start:])
			return out
		}
		out = append(out, text[start:start+idx])
		start += idx + 1
	}
	return out
}

// tailLinesOf 返回文本后 n 行（不含换行符），不足 n 行时返回全部。
// 与 splitDiffTextLines 的末尾语义一致：末尾换行不产生空尾行。
func tailLinesOf(text string, n int) []string {
	s := text
	if strings.HasSuffix(s, "\n") {
		s = s[:len(s)-1]
	}
	// 从尾部往前找第 n 个换行，确定起始位置。
	pos := len(s)
	seen := 0
	for pos > 0 && seen < n {
		pos--
		if s[pos] == '\n' {
			seen++
			if seen == n {
				pos++
				break
			}
		}
	}
	out := make([]string, 0, n)
	start := pos
	for {
		idx := strings.IndexByte(s[start:], '\n')
		if idx < 0 {
			out = append(out, s[start:])
			return out
		}
		out = append(out, s[start:start+idx])
		start += idx + 1
	}
}

// diffWindowToBody 用 go-diff 对变更窗口做严格的行级 diff，转成 unified
// body 行。窗口按行拼成文本时末尾补一个换行，保证与按行切片行数一致
// （包括以空行结尾的窗口）。
//
// DiffLinesToChars 把每一行编码成一个字符，再对编码串做 Myers diff——
// 得到的 Equal/Delete/Insert 段都是完整行，不会把一行拆成多个字符片段
// （直接对原文 DiffMain 是字符级 diff，true→false 会碎成 -tru/+ fals/e）。
func diffWindowToBody(oldWindow, newWindow []string, oldStart, newStart int) []string {
	dmp := diffmatchpatch.New()
	oldText := strings.Join(oldWindow, "\n") + "\n"
	newText := strings.Join(newWindow, "\n") + "\n"
	chars1, chars2, lineArray := dmp.DiffLinesToChars(oldText, newText)
	diffs := dmp.DiffMain(chars1, chars2, false)
	// v1.4.0 的 DiffCharsToLines 返回新切片（不是就地修改），必须接收返回值。
	diffs = dmp.DiffCharsToLines(diffs, lineArray)
	return diffsToBody(diffs, oldStart, newStart)
}

// diffsToBody 把 go-diff 的 Diff 序列转成 unified diff body 行：
//   - Equal 段：行数超过 unchangedRunFoldThreshold 折叠为一行
//     “ … N unchanged lines …”，否则逐行输出 context；行号按行数前进。
//   - Delete/Insert 对：逐行配对做行内 diff（超长行裁剪），剩余单边行单独
//     输出；行号按各自行数前进。
func diffsToBody(diffs []diffmatchpatch.Diff, oldStart, newStart int) []string {
	body := make([]string, 0, len(diffs)*2+8)
	oldLine, newLine := oldStart, newStart
	i := 0
	for i < len(diffs) {
		d := diffs[i]
		switch d.Type {
		case diffmatchpatch.DiffEqual:
			// 先只数行数（O(n) 不分配）；折叠时只需头尾几行，
			// 避免对几十万行的大段相同内容做全量 split。
			n := countTextLines(d.Text)
			if n > unchangedRunFoldThreshold+2*editDiffContextLines {
				for _, l := range headLinesOf(d.Text, editDiffContextLines) {
					body = append(body, clipLongLine(" "+l))
				}
				body = append(body, fmt.Sprintf(" … %d unchanged lines …", n-2*editDiffContextLines))
				for _, l := range tailLinesOf(d.Text, editDiffContextLines) {
					body = append(body, clipLongLine(" "+l))
				}
			} else {
				for _, l := range splitDiffTextLines(d.Text) {
					body = append(body, clipLongLine(" "+l))
				}
			}
			oldLine += n
			newLine += n
			i++
		case diffmatchpatch.DiffDelete:
			dels := splitDiffTextLines(d.Text)
			i++
			var adds []string
			if i < len(diffs) && diffs[i].Type == diffmatchpatch.DiffInsert {
				adds = splitDiffTextLines(diffs[i].Text)
				i++
			}
			n := len(dels)
			if len(adds) < n {
				n = len(adds)
			}
			for k := 0; k < n; k++ {
				od, nd := inlineDiffPair(dels[k], adds[k])
				body = append(body, od, nd)
			}
			for _, l := range dels[n:] {
				body = append(body, clipLongLine("-"+l))
			}
			for _, l := range adds[n:] {
				body = append(body, clipLongLine("+"+l))
			}
			oldLine += len(dels)
			newLine += len(adds)
		case diffmatchpatch.DiffInsert:
			lines := splitDiffTextLines(d.Text)
			for _, l := range lines {
				body = append(body, clipLongLine("+"+l))
			}
			newLine += len(lines)
			i++
		}
	}
	return body
}

func changedLineSpans(oldLines, newLines []string) (int, int, int, int, bool) {
	prefix := 0
	for prefix < len(oldLines) && prefix < len(newLines) && oldLines[prefix] == newLines[prefix] {
		prefix++
	}

	oldSuffix := len(oldLines)
	newSuffix := len(newLines)
	for oldSuffix > prefix && newSuffix > prefix && oldLines[oldSuffix-1] == newLines[newSuffix-1] {
		oldSuffix--
		newSuffix--
	}

	if prefix == oldSuffix && prefix == newSuffix {
		return 0, 0, 0, 0, false
	}
	return prefix, oldSuffix, prefix, newSuffix, true
}

func formatUnifiedDiffHunk(oldStart, oldCount, newStart, newCount int, body []string) string {
	if oldStart < 1 {
		oldStart = 1
	}
	if newStart < 1 {
		newStart = 1
	}
	if len(body) == 0 {
		return ""
	}
	lines := make([]string, 0, len(body)+1)
	lines = append(lines, fmt.Sprintf("@@ -%d,%d +%d,%d @@", oldStart, oldCount, newStart, newCount))
	lines = append(lines, body...)
	return strings.Join(lines, "\n")
}

func generateTruncatedEditDiffHunkBody(oldLines, newLines []string, oldStart, oldChangeStart, oldChangeEnd, oldEnd, newStart, newChangeStart, newChangeEnd, newEnd int) []string {
	body := make([]string, 0, editDiffTruncatedLineLimit*2+editDiffContextLines*2+2)
	// 变更区之前的头部上下文（旧行）。
	for _, line := range oldLines[oldStart:oldChangeStart] {
		body = append(body, clipLongLine(" "+line))
	}

	removed := oldLines[oldChangeStart:oldChangeEnd]
	added := newLines[newChangeStart:newChangeEnd]
	// 真实删除/添加数用 multiset 差统计：span 内可能夹着大量未变化的行，
	// 不能拿 span 长度当变化量。
	spanRemoved, spanAdded := lineSetDeltaCounts(removed, added)

	// 折叠式扫描：相同行 run 折叠为一行省略号（以 context 行输出，行号连续，
	// 前端解析与 split 对齐都不受影响）；变化行成对输出 -/+。避免多簇分散
	// 变更时把 span 内大段未变化的行整体标成删除+添加。
	i, j := 0, 0
	shownRemoved, shownAdded := 0, 0
	runStartI := 0
	inRun := false
	flushRun := func() {
		if !inRun {
			return
		}
		n := i - runStartI
		if n > unchangedRunFoldThreshold+2*editDiffContextLines {
			for k := runStartI; k < runStartI+editDiffContextLines; k++ {
				body = append(body, clipLongLine(" "+removed[k]))
			}
			body = append(body, fmt.Sprintf(" … %d unchanged lines …", n-2*editDiffContextLines))
			for k := i - editDiffContextLines; k < i; k++ {
				body = append(body, clipLongLine(" "+removed[k]))
			}
		} else {
			for k := runStartI; k < i; k++ {
				body = append(body, clipLongLine(" "+removed[k]))
			}
		}
		inRun = false
	}
	for i < len(removed) && j < len(added) {
		if removed[i] == added[j] {
			if !inRun {
				runStartI = i
				inRun = true
			}
			i++
			j++
			continue
		}
		flushRun()
		if shownRemoved < editDiffTruncatedLineLimit {
			body = append(body, clipLongLine("-"+removed[i]))
			shownRemoved++
		}
		if shownAdded < editDiffTruncatedLineLimit {
			body = append(body, clipLongLine("+"+added[j]))
			shownAdded++
		}
		i++
		j++
	}
	flushRun()
	// 插入/删除不对称时的剩余行。
	for ; i < len(removed); i++ {
		if shownRemoved < editDiffTruncatedLineLimit {
			body = append(body, clipLongLine("-"+removed[i]))
			shownRemoved++
		}
	}
	for ; j < len(added); j++ {
		if shownAdded < editDiffTruncatedLineLimit {
			body = append(body, clipLongLine("+"+added[j]))
			shownAdded++
		}
	}

	// 有未显示的变化行时打标记（用真实统计，而非 span 长度）。
	if shownRemoved < spanRemoved || shownAdded < spanAdded {
		body = append(body, fmt.Sprintf(" [diff truncated: %d removed and %d added lines in span]", spanRemoved, spanAdded))
	}

	// 变更区之后的尾部上下文（新行）。
	for _, line := range newLines[newChangeEnd:newEnd] {
		body = append(body, clipLongLine(" "+line))
	}
	return body
}

func truncateDiffText(diff string, maxBytes int) string {
	if maxBytes <= 0 || len(diff) <= maxBytes {
		return diff
	}
	marker := "\n[diff truncated: omitted remaining diff output]\n"
	cut := maxBytes - len(marker)
	if cut < 0 {
		cut = 0
	}
	for cut > 0 && !utf8.ValidString(diff[:cut]) {
		cut--
	}
	return strings.TrimRight(diff[:cut], "\n") + marker
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// lineSetDeltaCounts returns the true number of removed and added lines
// between two line slices using a multiset (bag) difference. Identical lines
// cancel out; only the surplus on either side counts as a change.
//
// It is O(n+m) in time and space, making it suitable for large change spans
// where an O(n*m) LCS is infeasible. It is order-insensitive, so block moves
// or reordered repeated lines may be miscounted — but for the common case of
// scattered small edits inside a large span it is far more accurate than the
// previous remove-all/add-all approximation.
func lineSetDeltaCounts(oldLines, newLines []string) (removed, added int) {
	counts := make(map[string]int, len(oldLines)+len(newLines))
	for _, line := range oldLines {
		counts[line]++
	}
	for _, line := range newLines {
		counts[line]--
	}
	for _, delta := range counts {
		if delta > 0 {
			removed += delta
		} else if delta < 0 {
			added += -delta
		}
	}
	return removed, added
}

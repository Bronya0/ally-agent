package edit

import (
	"fmt"
	"strings"
	"unicode/utf8"
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

	if len(oldLines)*len(newLines) <= editDiffLCSLineProductLimit {
		// Reuse the line slices already built above; splitting both contents
		// again through generateEditDiff needlessly duplicates O(N) work.
		diff := strings.Join(computePrefixedDiffLines(oldLines, newLines), "\n")
		return truncateDiffText(diff, maxBytes)
	}

	diff := generateLocalizedEditDiff(oldLines, newLines)
	return truncateDiffText(diff, maxBytes)
}

func generateEditDiff(oldContent, newContent string) string {
	oldLines := contentLinesForDiff(oldContent)
	newLines := contentLinesForDiff(newContent)
	return strings.Join(computePrefixedDiffLines(oldLines, newLines), "\n")
}

func contentLinesForDiff(content string) []string {
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 1 && lines[0] == "" && content == "" {
		return []string{}
	}
	return lines
}

func computePrefixedDiffLines(oldLines, newLines []string) []string {
	m, n := len(oldLines), len(newLines)
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if oldLines[i-1] == newLines[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] > dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}

	var result []string
	i, j := m, n
	for i > 0 || j > 0 {
		if i > 0 && j > 0 && oldLines[i-1] == newLines[j-1] {
			result = append(result, " "+oldLines[i-1])
			i--
			j--
		} else if j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]) {
			result = append(result, "+"+newLines[j-1])
			j--
		} else {
			result = append(result, "-"+oldLines[i-1])
			i--
		}
	}

	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}

	return result
}

func generateLocalizedEditDiff(oldLines, newLines []string) string {
	oldChangeStart, oldChangeEnd, newChangeStart, newChangeEnd, ok := changedLineSpans(oldLines, newLines)
	if !ok {
		return ""
	}

	oldStart := maxInt(0, oldChangeStart-editDiffContextLines)
	oldEnd := minInt(len(oldLines), oldChangeEnd+editDiffContextLines)
	newStart := maxInt(0, newChangeStart-editDiffContextLines)
	newEnd := minInt(len(newLines), newChangeEnd+editDiffContextLines)
	oldWindow := oldLines[oldStart:oldEnd]
	newWindow := newLines[newStart:newEnd]

	if len(oldWindow)*len(newWindow) <= editDiffLCSLineProductLimit {
		body := computePrefixedDiffLines(oldWindow, newWindow)
		return formatUnifiedDiffHunk(oldStart+1, len(oldWindow), newStart+1, len(newWindow), body)
	}

	return generateTruncatedEditDiffHunk(oldLines, newLines, oldStart, oldChangeStart, oldChangeEnd, oldEnd, newStart, newChangeStart, newChangeEnd, newEnd)
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

func generateTruncatedEditDiffHunk(oldLines, newLines []string, oldStart, oldChangeStart, oldChangeEnd, oldEnd, newStart, newChangeStart, newChangeEnd, newEnd int) string {
	body := make([]string, 0, editDiffTruncatedLineLimit*2+editDiffContextLines*2+2)
	for _, line := range oldLines[oldStart:oldChangeStart] {
		body = append(body, " "+line)
	}

	removed := oldLines[oldChangeStart:oldChangeEnd]
	added := newLines[newChangeStart:newChangeEnd]
	// The change span is too large for an O(n*m) LCS, so count true
	// removed/added via a multiset difference instead of treating the whole
	// span as remove-all/add-all. This keeps stats accurate for scattered
	// small edits separated by thousands of unchanged lines.
	spanRemoved, spanAdded := lineSetDeltaCounts(removed, added)
	removedShown := minInt(len(removed), editDiffTruncatedLineLimit)
	addedShown := minInt(len(added), editDiffTruncatedLineLimit)
	for _, line := range removed[:removedShown] {
		body = append(body, "-"+line)
	}
	for _, line := range added[:addedShown] {
		body = append(body, "+"+line)
	}
	removedOmitted := len(removed) - removedShown
	addedOmitted := len(added) - addedShown
	if removedOmitted > 0 || addedOmitted > 0 {
		body = append(body, fmt.Sprintf(" [diff truncated: %d removed and %d added lines in span]", spanRemoved, spanAdded))
	}

	for _, line := range newLines[newChangeEnd:newEnd] {
		body = append(body, " "+line)
	}
	return formatUnifiedDiffHunk(oldStart+1, oldEnd-oldStart, newStart+1, newEnd-newStart, body)
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

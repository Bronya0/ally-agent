package main

import (
	"strconv"
	"strings"
	"testing"
)

// TestTruncatedDiffStatsAccurateForScatteredEdits mirrors the real-world case
// that caused stats to balloon: a large file (well above the LCS line-product
// limit) with two small edits separated by thousands of unchanged lines. The
// truncated localized-diff path must report the true +2 -2, not thousands.
func TestTruncatedDiffStatsAccurateForScatteredEdits(t *testing.T) {
	const totalLines = 3000
	oldLines := make([]string, totalLines)
	for i := range oldLines {
		oldLines[i] = "line " + strconv.Itoa(i)
	}
	newLines := append([]string(nil), oldLines...)
	newLines[49] = "line 49 CHANGED"
	newLines[2949] = "line 2949 CHANGED"
	oldContent := strings.Join(oldLines, "\n") + "\n"
	newContent := strings.Join(newLines, "\n") + "\n"

	diff := generateEditDiffPreview(oldContent, newContent, maxToolOutput)
	if diff == "" {
		t.Fatal("expected a non-empty diff")
	}
	added, removed := countEditDiffStats(diff, oldLines, newLines)
	// Two lines changed => 2 removed + 2 added. Reject the old remove-all /
	// add-all blow-up (which would be ~3000 each).
	if added != 2 || removed != 2 {
		t.Fatalf("expected +2 -2, got +%d -%d", added, removed)
	}
}

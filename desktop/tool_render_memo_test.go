package desktop

import "testing"

func TestDiffMemoReplacesRowsAndResetsExpansion(t *testing.T) {
	var memo diffMemo
	memo.get("-old\n+new")
	memo.extra = 120
	rows := memo.get("-before\n+after")
	if len(rows) != 1 || rows[0].right != "+after" || memo.extra != 0 {
		t.Fatalf("stale diff: %+v extra=%d", rows, memo.extra)
	}
}

// Collapsing a card must drop the rows it had shown, or every later collapse
// keeps the expanded node count.
func TestDiffExpansionResetsWhenCardCollapses(t *testing.T) {
	var memo diffMemo
	if memo.limit(true) != 12 {
		t.Fatal("compact limit")
	}
	memo.extra = 120
	if memo.limit(true) != 132 {
		t.Fatal("show more lost")
	}
	if memo.limit(false) != 120 || memo.extra != 0 {
		t.Fatal("opening card must reset expansion")
	}
	memo.extra = 240
	if memo.limit(true) != 12 || memo.extra != 0 {
		t.Fatal("collapsing card must reset expansion")
	}
}

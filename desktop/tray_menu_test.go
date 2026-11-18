package desktop

import "testing"

func TestAllyTrayMenuUsesSharedItems(t *testing.T) {
	items := (&desktop{}).trayMenuItems()
	if len(items) != 3 {
		t.Fatalf("got %d items", len(items))
	}
	for i, item := range items {
		if item.Key == "" || item.Label == "" || item.Icon == nil || item.OnClick == nil {
			t.Fatalf("item %d is incomplete: %+v", i, item)
		}
	}
	if !items[1].SeparatorBefore || items[1].NativeSeparatorBefore == nil || *items[1].NativeSeparatorBefore || !items[2].SeparatorBefore {
		t.Fatal("tray menu grouping changed")
	}
}

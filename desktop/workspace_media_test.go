package desktop

import "testing"

func TestMediaNavigation(t *testing.T) {
	for _, tc := range []struct{ index, count, delta, want int }{
		{0, 3, 1, 1}, {2, 3, 1, 0}, {0, 3, -1, 2}, {0, 1, 1, 0}, {-1, 3, 1, -1}, {0, 0, 1, -1},
	} {
		if got := mediaNextIndex(tc.index, tc.count, "顺序循环", tc.delta); got != tc.want {
			t.Fatalf("%+v: got %d", tc, got)
		}
	}
	for count := 2; count < 20; count++ {
		for index := 0; index < count; index++ {
			got := mediaNextIndex(index, count, "随机循环", 1)
			if got < 0 || got >= count || got == index {
				t.Fatalf("random next %d/%d: %d", index, count, got)
			}
		}
	}
}

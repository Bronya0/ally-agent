package desktop

import "testing"

func TestUpdateVersionDoesNotOfferSameOrOlderRelease(t *testing.T) {
	for _, tc := range []struct {
		latest, current string
		want            bool
	}{{"v1.27.0", "1.26.9", true}, {"v1.27.0", "1.27.0", false}, {"v1.26.0", "1.27.0", false}, {"v1.10.0", "1.9.0", true}, {"v1.27.0-beta", "1.26.0", false}, {"v1.27.0", "dev", true}} {
		if got := newerStableRelease(tc.latest, tc.current); got != tc.want {
			t.Errorf("%s -> %s: %v", tc.current, tc.latest, got)
		}
	}
}

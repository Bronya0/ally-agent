package desktop

import (
	"regexp"
	"strconv"
)

var stableReleasePattern = regexp.MustCompile(`^v?([0-9]+)\.([0-9]+)\.([0-9]+)$`)

func newerStableRelease(latest, current string) bool {
	target := stableReleasePattern.FindStringSubmatch(latest)
	if target == nil {
		return false
	}
	installed := stableReleasePattern.FindStringSubmatch(current)
	if installed == nil {
		return true
	}
	for i := 1; i <= 3; i++ {
		a, errA := strconv.ParseUint(target[i], 10, 64)
		b, errB := strconv.ParseUint(installed[i], 10, 64)
		if errA != nil || errB != nil {
			return false
		}
		if a != b {
			return a > b
		}
	}
	return false
}

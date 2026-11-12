// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"runtime/debug"
	"time"
)

// memorySoftLimit caps Go-managed memory. A session that decodes screenshots or
// opens a long history peaks far above its live set (about 130 MB with several
// conversations), and without a cap the runtime keeps that peak's spans for the
// rest of the run. It is a soft cap: the runtime collects earlier and more
// often instead of failing, and native memory such as GPU textures is not
// counted, so the process still shows more than this.
const memorySoftLimit = 320 << 20

func applyMemorySoftLimit() { debug.SetMemoryLimit(memorySoftLimit) }

// memoryReleaseInterval spaces repeated releases: each one stops the world and
// then hands back pages a visible window would touch again right away.
const memoryReleaseInterval = time.Minute

// releaseMemory hands unused Go heap back to the OS. Hiding, minimizing,
// closing or opening a tab, and starting a new session all call it, and their
// previews and bitmaps are released first so the release sees that garbage.
// The callers are user actions that can come in bursts, so at most one release
// runs per memoryReleaseInterval.
func (a *desktop) releaseMemory() {
	if a.closing {
		return
	}
	now := time.Now()
	if !a.memoryReleasedAt.IsZero() && now.Sub(a.memoryReleasedAt) < memoryReleaseInterval {
		return
	}
	a.memoryReleasedAt = now
	// Off the UI thread: the release stops the world for a few milliseconds.
	go debug.FreeOSMemory()
}

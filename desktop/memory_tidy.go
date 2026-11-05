// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"log"
	"runtime"
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

const (
	// memoryTidyDelay lets the window stay out of sight first, so reopening it
	// does not touch pages this release just returned.
	memoryTidyDelay = 5 * time.Second
	// memoryTidyInterval spaces repeated tray toggles: each release costs a
	// collection and then soft faults when the pages are handed back.
	memoryTidyInterval = 2 * time.Minute
)

func applyMemorySoftLimit() { debug.SetMemoryLimit(memorySoftLimit) }

// scheduleMemoryTidy collects and releases once the window has been hidden or
// minimized for memoryTidyDelay. Call it on the UI thread.
func (a *desktop) scheduleMemoryTidy() {
	if a.win == nil || a.closing || a.memoryTidyTimer != nil {
		return
	}
	win := a.win
	a.memoryTidyTimer = time.AfterFunc(memoryTidyDelay, func() { win.Update(a.tidyMemory) })
}

// cancelMemoryTidy drops a pending tidy when the window comes back first.
func (a *desktop) cancelMemoryTidy() {
	if a.memoryTidyTimer != nil {
		a.memoryTidyTimer.Stop()
		a.memoryTidyTimer = nil
	}
}

// tidyMemory runs on the UI thread, which only checks the window state: the
// release itself stops the world for a few milliseconds and belongs off it.
// A window the user can see never pays for this.
func (a *desktop) tidyMemory() {
	a.memoryTidyTimer = nil
	if a.closing || a.win == nil || (!a.mainWindowHidden && !a.win.IsMinimized()) {
		return
	}
	if !a.memoryTidyAt.IsZero() && time.Since(a.memoryTidyAt) < memoryTidyInterval {
		return
	}
	a.memoryTidyAt = time.Now()
	go func() {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		debug.FreeOSMemory()
		runtime.ReadMemStats(&after)
		log.Printf("memory: heap %d MB -> %d MB after the window went away", before.HeapAlloc>>20, after.HeapAlloc>>20)
	}()
}

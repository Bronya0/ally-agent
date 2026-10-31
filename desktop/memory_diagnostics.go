// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/internal/diagnostics"
	"path/filepath"
)

// memoryCounts runs on the UI thread and passes only detached numbers to workers.
func (a *desktop) memoryCounts() map[string]int {
	counts := map[string]int{
		"tabs": len(a.tabs), "rememberedSessions": len(a.openSessions),
		"openFiles": len(a.workspaceFiles), "highlightEntries": len(highlightCache),
		"previewEntries": len(a.previews.entries), "subRuns": len(a.subRuns),
	}
	seen := map[*conversation]bool{}
	add := func(t *conversation) {
		if t == nil || seen[t] {
			return
		}
		seen[t] = true
		counts["conversations"]++
		counts["messages"] += len(t.Messages)
		counts["markdownOptions"] += len(t.MD)
		counts["attachmentImages"] += len(t.AttachmentImages)
		counts["pendingAttachmentImages"] += len(t.PendingAttachmentImages)
		if t.Busy {
			counts["busyConversations"]++
		}
	}
	for _, t := range a.tabs {
		add(t)
	}
	for _, t := range a.openSessions {
		add(t)
	}
	return counts
}

func (a *desktop) exportMemoryDiagnostics() {
	if a.memoryDiagnosticBusy {
		return
	}
	a.memoryDiagnosticBusy = true
	counts := a.memoryCounts()
	root := filepath.Join(a.core.AppDataDir(), "diagnostics")
	go func() {
		dir, err := diagnostics.Capture(root, counts)
		a.win.Update(func() {
			a.memoryDiagnosticBusy = false
			if err != nil {
				a.notifyError("内存诊断失败：" + err.Error())
				return
			}
			a.notifySuccess("内存诊断已导出：" + dir)
			a.job(func() error { return a.core.OpenPathInFileManager(dir) })
		})
	}()
}

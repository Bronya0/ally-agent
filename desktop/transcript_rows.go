// SPDX-License-Identifier: GPL-3.0-only
package desktop

import "github.com/egoist/mygo/ui"

// MyGo keeps registered handle bindings until the window closes and visits
// them each frame. Reuse the handles of rows leaving this page instead of
// registering one for every prompt/search row in an all-day conversation.
func pruneTranscriptHandles(t *conversation, start, end int) {
	st := t.uiState()
	for index, handle := range st.PromptHandles {
		if index >= start && index < end {
			continue
		}
		delete(st.PromptHandles, index)
		// A searched prompt shares its handle with the prompt navigator.
		if st.Search.handles[index] == handle {
			delete(st.Search.handles, index)
		}
		if handle != nil {
			st.transcriptHandlePool = append(st.transcriptHandlePool, handle)
		}
	}
	for index, handle := range st.Search.handles {
		if index >= start && index < end {
			continue
		}
		delete(st.Search.handles, index)
		if handle != nil {
			st.transcriptHandlePool = append(st.transcriptHandlePool, handle)
		}
	}
}

func takeTranscriptHandle(st *chatUI) *ui.Handle {
	if n := len(st.transcriptHandlePool); n > 0 {
		handle := st.transcriptHandlePool[n-1]
		st.transcriptHandlePool[n-1] = nil
		st.transcriptHandlePool = st.transcriptHandlePool[:n-1]
		return handle
	}
	return &ui.Handle{}
}

// rowKind is the one answer to "who draws message i". The transcript renderer,
// the history pager and restored scroll offsets all ask it, so a message that
// another row already shows can never claim a slot of its own: an empty slot
// still costs the transcript column its row gap, which is what left a blank
// band under a batch of sub-agents.
type rowKind int

const (
	// rowSkipped: another row draws it, or it has nothing to draw. No container.
	rowSkipped rowKind = iota
	// rowPlain: the message draws its own row.
	rowPlain
	// rowReadGroup: the message heads a run of read/grep/list/command calls
	// that starts with a read-like tool.
	rowReadGroup
	// rowCommandGroup: the same mixed tool run when it starts with a command.
	rowCommandGroup
)

// transcriptRows answers "who draws what" in one pass over the messages, so a
// frame does not rescan the list per row.
type transcriptRows struct {
	t *conversation
	// children maps a sub-agent card's call id to the rows it draws, in
	// transcript order.
	children map[string][]int
	// nested marks the sub-agent rows a parent's tool card already draws.
	nested map[int]bool
}

// newTranscriptRows collects the rows that belong to another row. A sub-agent
// child is drawn by the card whose call id it names, or by the card whose
// result reports its agent id.
func newTranscriptRows(t *conversation) *transcriptRows {
	r := &transcriptRows{t: t}
	cards := map[string]bool{}
	agents := map[string]string{}
	for _, m := range t.Messages {
		if toolKind(str(m, "name")) != "subagent" {
			continue
		}
		if id := str(m, "toolCallId"); id != "" {
			cards[id] = true
		}
		if id := str(toolResult(m), "agentId"); id != "" {
			agents[id] = str(m, "toolCallId")
		}
	}
	if len(cards) == 0 && len(agents) == 0 {
		return r
	}
	for i, m := range t.Messages {
		parent := str(m, "parentToolCallId")
		if !cards[parent] {
			parent = agents[str(m, "subId")]
		}
		if parent == "" {
			continue
		}
		if r.children == nil {
			r.children, r.nested = map[string][]int{}, map[int]bool{}
		}
		r.children[parent] = append(r.children[parent], i)
		r.nested[i] = true
	}
	return r
}

// childrenOf lists, in transcript order, the rows the card for callID draws.
func (r *transcriptRows) childrenOf(callID string) []int {
	if callID == "" {
		return nil
	}
	return r.children[callID]
}

// isToolRecord reports whether m records a tool call.
func isToolRecord(m map[string]any) bool {
	return str(m, "role") == "tool_call" || str(m, "toolCallId") != ""
}

// foldsInto reports whether message i is drawn by an earlier row: a folded
// follower of the run led by i-1, or a sub-agent row drawn inside its parent's
// tool card. Such a message opens no history block either, so a page never
// begins on a row that draws nothing.
func (r *transcriptRows) foldsInto(i int) bool {
	if i <= 0 || i >= len(r.t.Messages) {
		return false
	}
	if r.nested[i] {
		return true
	}
	m, prev := r.t.Messages[i], r.t.Messages[i-1]
	if !isToolRecord(m) || !isToolRecord(prev) {
		return false
	}
	return isFoldableToolGroupItem(prev) && isFoldableToolGroupItem(m)
}

// kindAt reports how message i appears in the window that starts at start: a
// run whose head sits before the window keeps its group card on its first
// visible member.
func (r *transcriptRows) kindAt(i, start int) rowKind {
	m := r.t.Messages[i]
	if r.nested[i] {
		return rowSkipped
	}
	switch role := str(m, "role"); {
	case role == "assistant":
		if _, ok := m["welcome"]; ok {
			return rowPlain
		}
		// assistantMessage draws nothing without text and without attachments.
		if str(m, "content") == "" && messageAttachmentCount(m) == 0 {
			return rowSkipped
		}
		return rowPlain
	case role == "user", role == "notice", role == "subagent":
		return rowPlain
	case isToolRecord(m):
		if i > start && r.foldsInto(i) {
			return rowSkipped
		}
		switch {
		case isFoldableReadTool(m):
			return rowReadGroup
		case isFoldableCommand(m):
			return rowCommandGroup
		}
		return rowPlain
	}
	if str(m, "content") == "" {
		return rowSkipped
	}
	return rowPlain
}

// startsBlock reports whether message i opens a history block. A page boundary
// may only land on a row that actually draws.
func (r *transcriptRows) startsBlock(i int) bool {
	return i == 0 || (!r.foldsInto(i) && r.kindAt(i, i-1) != rowSkipped)
}

// roundStatsIfAny keeps a round's statistics when its row is skipped: the
// numbers belong to the transcript, not to the message that happens to sit
// there.
func (a *desktop) roundStatsIfAny(c *ui.Context, t *conversation, i int, visible bool) {
	st := t.uiState()
	if idx, ok := st.RoundAt[i]; ok {
		a.roundStatsRow(c, t, idx, st.Round[idx], visible)
	}
}

// historyKey identifies one answer to "which rows open a history block": the
// transcript's length, and a revision that moves when a row changes what it
// shows without that length changing (a first token, a generated image).
type historyKey struct {
	rows int
	rev  uint64
}

// markHistory records that a row changed in place, so the block starts cached
// for the transcript are worked out again on the next frame.
func (t *conversation) markHistory() { t.historyRev++ }

// cachedTranscriptRows answers "who draws what" for the current transcript.
// It shares blockStarts' revision key: both walk the whole message list, and a
// frame asks for both (the window and the cards' children lookup).
func (t *conversation) cachedTranscriptRows() *transcriptRows {
	key := historyKey{rows: len(t.Messages), rev: t.historyRev}
	return t.historyRows.get(key, func(historyKey) *transcriptRows {
		return newTranscriptRows(t)
	})
}

// blockStarts answers which rows open a history block: a page may only begin on
// a row that draws. The grouping rule lives in foldsInto/startsBlock, so a page
// never starts on a row another row already draws. One frame asks more than
// once (the window, the older-page count) and every answer walks the whole
// transcript, so the answer stays until the transcript changes. The slice is
// borrowed until the next history change; callers read it and never append.
func (t *conversation) blockStarts() []int {
	key := historyKey{rows: len(t.Messages), rev: t.historyRev}
	return t.historyStarts.get(key, func(historyKey) []int {
		rows := t.cachedTranscriptRows()
		// Reuse the index while a conversation grows. Folded tool records often
		// outnumber visible blocks, so reserving one slot per message on every
		// append wastes both live capacity and transient allocations.
		starts := t.historyStarts.value[:0]
		if cap(starts) == 0 || cap(starts) > 4*(len(t.Messages)+1)+256 {
			starts = make([]int, 0, min(len(t.Messages)+1, 256))
		}
		for i := range t.Messages {
			if rows.startsBlock(i) {
				starts = append(starts, i)
			}
		}
		return append(starts, len(t.Messages))
	})
}

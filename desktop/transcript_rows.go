// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"slices"

	"github.com/egoist/mygo/ui"
)

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
	// Parent identities are retained so an appended child can be attached without
	// rescanning old records. Only IDs/indices, never message or result maps.
	cards              map[string]bool
	agents             map[string]string
	parentUses         map[string]bool
	agentUses          map[string]bool
	users, suggestions []int
	welcome            bool
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
	for _, m := range t.Messages {
		r.addParent(m)
	}
	r.attachTail(0)
	return r
}

func (r *transcriptRows) addParent(m map[string]any) {
	if toolKind(str(m, "name")) != "subagent" {
		return
	}
	if id := str(m, "toolCallId"); id != "" {
		if r.cards == nil {
			r.cards = map[string]bool{}
		}
		r.cards[id] = true
	}
	if id := str(toolResult(m), "agentId"); id != "" {
		if r.agents == nil {
			r.agents = map[string]string{}
		}
		r.agents[id] = str(m, "toolCallId")
	}
}

func (r *transcriptRows) attachTail(start int) {
	for i := start; i < len(r.t.Messages); i++ {
		m := r.t.Messages[i]
		if id := str(m, "parentToolCallId"); id != "" {
			if r.parentUses == nil {
				r.parentUses = map[string]bool{}
			}
			r.parentUses[id] = true
		}
		if id := str(m, "subId"); id != "" {
			if r.agentUses == nil {
				r.agentUses = map[string]bool{}
			}
			r.agentUses[id] = true
		}
		if str(m, "role") == "user" {
			r.users = append(r.users, i)
		}
		if toolKind(str(m, "name")) == "suggest" {
			r.suggestions = append(r.suggestions, i)
		}
		if _, ok := m["welcome"]; ok {
			r.welcome = true
		}
		parent := str(m, "parentToolCallId")
		if !r.cards[parent] {
			parent = r.agents[str(m, "subId")]
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
}

func (r *transcriptRows) roundStatsIndex(index int) int {
	next, _ := slices.BinarySearch(r.users, index+1)
	end := len(r.t.Messages)
	if next < len(r.users) {
		end = r.users[next]
	}
	n, _ := slices.BinarySearch(r.suggestions, end)
	if n > 0 && r.suggestions[n-1] > index {
		return r.suggestions[n-1]
	}
	return index
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

// kindAt reports how message i appears: a run of folded tool calls keeps one
// group card, on its head, so every other member of the run draws nothing.
func (r *transcriptRows) kindAt(i int) rowKind {
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
		if r.foldsInto(i) {
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

// startsBlock reports whether message i opens a history block: only a row that
// actually draws starts one.
func (r *transcriptRows) startsBlock(i int) bool {
	return r.kindAt(i) != rowSkipped
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

// The owner changes when a new parent can claim earlier child rows. An append
// may reuse the block prefix only when its ownership view also survived.
type historyBlocksKey struct {
	historyKey
	owner *transcriptRows
}

// markHistory records that a row changed in place, so the block starts cached
// for the transcript are worked out again on the next frame.
func (t *conversation) markHistory() { t.historyRev++ }

// A new, unindexed row is already covered by the length key. An in-place
// change to an indexed row still invalidates both complete views.
func (t *conversation) markHistoryAt(index int) {
	if (t.historyRows.ready && index < t.historyRows.key.rows) || (t.historyStarts.ready && index < t.historyStarts.key.rows) {
		t.markHistory()
	}
}

// cachedTranscriptRows answers "who draws what" for the current transcript.
// It shares blockStarts' revision key. Appends extend both views; ownership
// changes still rebuild the complete transcript before publishing page bounds.
func (t *conversation) cachedTranscriptRows() *transcriptRows {
	key := historyKey{rows: len(t.Messages), rev: t.historyRev}
	return t.historyRows.get(key, func(historyKey) *transcriptRows {
		previous := &t.historyRows
		if previous.ready && previous.key.rev == key.rev && previous.key.rows < key.rows {
			// A new parent may claim children that arrived first, or replace an
			// agent-ID mapping. Keep the complete two-pass rule for that case.
			needsRebuild := false
			for _, m := range t.Messages[previous.key.rows:] {
				if toolKind(str(m, "name")) == "subagent" {
					if previous.value.parentUses[str(m, "toolCallId")] || previous.value.agentUses[str(toolResult(m), "agentId")] {
						needsRebuild = true
						break
					}
				}
			}
			if !needsRebuild {
				for _, m := range t.Messages[previous.key.rows:] {
					previous.value.addParent(m)
				}
				previous.value.attachTail(previous.key.rows)
				return previous.value
			}
		}
		return newTranscriptRows(t)
	})
}

// blockStarts answers which rows open a history block: a page may only begin on
// a row that draws. The grouping rule lives in foldsInto/startsBlock, so a page
// never starts on a row another row already draws. One frame asks more than
// once (the window, the older-page count), so the answer stays until the
// transcript changes and ordinary appends reuse its prefix. The slice is
// borrowed until the next history change; callers read it and never append.
func (t *conversation) blockStarts() []int {
	rows := t.cachedTranscriptRows()
	key := historyBlocksKey{historyKey{rows: len(t.Messages), rev: t.historyRev}, rows}
	return t.historyStarts.get(key, func(historyBlocksKey) []int {
		// Reuse the index while a conversation grows. Folded tool records often
		// outnumber visible blocks, so reserving one slot per message on every
		// append wastes both live capacity and transient allocations.
		starts := t.historyStarts.value[:0]
		from := 0
		previous := &t.historyStarts
		if previous.ready && previous.key.rev == key.rev && previous.key.owner == rows && previous.key.rows < key.rows {
			// Remove the previous end sentinel, then judge only the appended
			// records. foldsInto still reads the preceding record at the seam.
			starts = previous.value[:len(previous.value)-1]
			from = previous.key.rows
		}
		if cap(starts) == 0 || cap(starts) > 4*(len(t.Messages)+1)+256 {
			starts = make([]int, 0, min(len(t.Messages)+1, 256))
		}
		for i := from; i < len(t.Messages); i++ {
			if rows.startsBlock(i) {
				starts = append(starts, i)
			}
		}
		return append(starts, len(t.Messages))
	})
}

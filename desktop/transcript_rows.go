// SPDX-License-Identifier: GPL-3.0-only
package desktop

import "github.com/egoist/mygo/ui"

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
	// rowReadGroup: the message heads a run of read/grep/list calls.
	rowReadGroup
	// rowCommandGroup: the message heads a run of command calls.
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
	return (isFoldableReadTool(prev) && isFoldableReadTool(m)) || (isFoldableCommand(prev) && isFoldableCommand(m))
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

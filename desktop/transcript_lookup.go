// SPDX-License-Identifier: GPL-3.0-only
package desktop

// Only IDs and row numbers are indexed, never result maps or output buffers.
// Appends index the new tail; rewrites release the index with the old rows.
// All access, like Messages itself, belongs to the UI thread.
type transcriptLookup struct {
	rows   int
	tools  map[string]int
	agents map[string]int
}

func (t *conversation) resetTranscriptLookup() { t.messageLookup = transcriptLookup{} }

func (t *conversation) indexTranscriptTail() {
	index := &t.messageLookup
	if index.rows > len(t.Messages) {
		t.resetTranscriptLookup()
	}
	for i := index.rows; i < len(t.Messages); i++ {
		m := t.Messages[i]
		if id := str(m, "toolCallId"); id != "" {
			if index.tools == nil {
				index.tools = make(map[string]int)
			}
			index.tools[id] = i
		}
		if str(m, "role") == "subagent" {
			if id := str(m, "subId"); id != "" {
				if index.agents == nil {
					index.agents = make(map[string]int)
				}
				index.agents[id] = i
			}
		}
	}
	index.rows = len(t.Messages)
}

func (t *conversation) toolCallIndex(id string) int {
	if id == "" {
		return -1
	}
	t.indexTranscriptTail()
	if i, ok := t.messageLookup.tools[id]; ok {
		if i >= len(t.Messages) || str(t.Messages[i], "toolCallId") != id {
			t.resetTranscriptLookup()
			return t.toolCallIndex(id)
		}
		return i
	}
	return -1
}

func (t *conversation) subagentIndex(id string) int {
	if id == "" {
		return -1
	}
	t.indexTranscriptTail()
	if i, ok := t.messageLookup.agents[id]; ok {
		if i >= len(t.Messages) || str(t.Messages[i], "role") != "subagent" || str(t.Messages[i], "subId") != id {
			t.resetTranscriptLookup()
			return t.subagentIndex(id)
		}
		return i
	}
	return -1
}

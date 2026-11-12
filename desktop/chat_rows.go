// SPDX-License-Identifier: GPL-3.0-only
package desktop

import "slices"

// welcomeRow is the row the welcome card draws on: it holds no message.
const welcomeRow = -1

// chatRow is one row of the transcript list: a message, which draws itself and
// the statistics of the round it ends, or the welcome card.
type chatRow struct {
	message int
}

// chatRowsKey identifies one answer to "which rows does the transcript draw":
// the transcript's length and in-place revision, and how many rounds carry
// statistics.
type chatRowsKey struct {
	historyKey
	rounds int
}

// chatRows answers which rows the transcript draws, in order: one per history
// block, one per row a round's statistics land on when the block that ends it
// is drawn by another row, and the welcome card while the transcript has none.
// The list builds only the rows in view, so this one pass is all a whole
// transcript costs a frame; the answer stays until the transcript or the round
// statistics change.
func (t *conversation) chatRows() []chatRow {
	st := t.uiState()
	key := chatRowsKey{historyKey{len(t.Messages), t.historyRev}, len(st.Round)}
	return st.chatRowsMemo.get(key, func(chatRowsKey) []chatRow {
		// The previous answer's capacity is reused as blockStarts reuses its
		// own: a conversation appends far more often than it changes shape.
		rows := st.chatRowsMemo.value[:0]
		if len(t.Messages) == 0 || !hasWelcomeMessage(t) {
			rows = append(rows, chatRow{message: welcomeRow})
		}
		starts := t.blockStarts()
		for _, index := range starts[:len(starts)-1] {
			rows = append(rows, chatRow{message: index})
		}
		for _, target := range t.placeRoundStats(st) {
			rows = append(rows, chatRow{message: target})
		}
		slices.SortFunc(rows, func(a, b chatRow) int { return a.message - b.message })
		return slices.Compact(rows)
	})
}

// placeRoundStats puts each completed round's statistics on the row that draws
// them, returning those row indices. Every round is placed once for the whole
// transcript; the list builds only the ones in view.
func (t *conversation) placeRoundStats(st *chatUI) []int {
	if st.RoundAt == nil {
		st.RoundAt = make(map[int]int, len(st.Round))
	} else {
		clear(st.RoundAt)
	}
	if len(st.Round) == 0 {
		return nil
	}
	transcript := t.cachedTranscriptRows()
	indices := st.roundIndices.get(roundIndexKey{t.historyRev, len(st.Round)}, func(roundIndexKey) []int {
		indices := st.roundIndices.value[:0]
		if cap(indices) > 4*len(st.Round)+256 {
			indices = nil
		}
		for index := range st.Round {
			indices = append(indices, index)
		}
		slices.Sort(indices)
		return indices
	})
	targets := make([]int, 0, len(indices))
	for _, index := range indices {
		stat := st.Round[index]
		if stat == nil || !stat.Done {
			continue
		}
		target := transcript.roundStatsIndex(index)
		st.RoundAt[target] = index
		targets = append(targets, target)
	}
	return targets
}

// rowOfMessage returns the row that draws message i, or the row that draws the
// block it folds into, and -1 while the transcript has no rows.
func (t *conversation) rowOfMessage(i int) int {
	rows := t.chatRows()
	if len(rows) == 0 {
		return -1
	}
	// The first row past the message draws the block it belongs to.
	at, found := slices.BinarySearchFunc(rows, chatRow{message: i}, func(row, want chatRow) int {
		return row.message - want.message
	})
	switch {
	case found:
		return at
	case at >= len(rows):
		return len(rows) - 1
	case at == 0:
		return 0
	}
	return at - 1
}

// followLatest parks a transcript at its newest message: a restored history, or
// one replaced by a summary, would otherwise keep the place it had.
func followLatest(t *conversation) {
	st := t.uiState()
	st.Follow, st.FollowPending = true, false
	st.List.FollowEnd = true
	st.List.ScrollToEnd()
}

// builtRange is the span of messages the transcript's list built, and whether
// it built the welcome card, so the caches keyed by message stay as small as
// the viewport.
type builtRange struct {
	ready   bool
	lo, hi  int
	welcome bool
}

func (r *builtRange) cover(row chatRow) {
	if row.message == welcomeRow {
		r.welcome = true
		return
	}
	if !r.ready {
		r.ready, r.lo, r.hi = true, row.message, row.message
		return
	}
	r.lo, r.hi = min(r.lo, row.message), max(r.hi, row.message)
}

func (r builtRange) holds(message int) bool {
	return r.ready && message >= r.lo && message <= r.hi
}

// chatBuildMargin is how many messages beyond the ones the list built keep
// their parsed text, so scrolling back a little does not parse them again.
const chatBuildMargin = 8

// pruneTranscriptOutsideView drops what is keyed by message but no longer
// drawn: parsed Markdown, row handles, command details and search spans. The
// list builds a few rows either side of the viewport, so this is what keeps a
// long conversation's live memory near the size of its screen.
func pruneTranscriptOutsideView(t *conversation, st *chatUI) {
	if !st.built.ready {
		return
	}
	lo, hi := st.built.lo-chatBuildMargin, st.built.hi+chatBuildMargin+1
	pruneTranscriptHandles(t, lo, hi)
	releaseCommandDetailsOutsideWindow(t, lo, hi)
	for index := range t.MD {
		if index < lo || index >= hi {
			delete(t.MD, index)
			delete(t.MDCodeBlock, index)
		}
	}
	for key := range st.Search.codeCache {
		if key.Message < lo || key.Message >= hi {
			delete(st.Search.codeCache, key)
		}
	}
	for key := range st.Search.spanCache {
		if key.Message < lo || key.Message >= hi {
			delete(st.Search.spanCache, key)
		}
	}
}

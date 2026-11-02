// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

// These values belong to a widget, never to persisted messages or a global
// cache: every frame rebuilds the tree, so a memo pays a derivation once per
// change and is dropped together with the widget that holds it.
//
// memoOf keeps one derived value while its key stays the same. The kind of key
// differs per use (raw arguments, a log, a diff), so it stays generic instead of
// each card growing its own copy of the compare-and-derive dance.
type memoOf[K comparable, V any] struct {
	ready bool
	key   K
	value V
}

func (m *memoOf[K, V]) get(key K, derive func(K) V) V {
	if !m.ready || m.key != key {
		m.key, m.value, m.ready = key, derive(key), true
	}
	return m.value
}

// memoAt attaches a memo to e under name, so it lives as long as e does.
func memoAt[V any](e ui.Element, name string) *V {
	return ui.Local(e, name, func() V {
		var zero V
		return zero
	})
}

// The memos the tool cards and source windows keep, named after the value they
// hold so a call site says what it caches.
type (
	argsMemo = memoOf[string, map[string]any]
	textMemo = memoOf[string, string]
	rowsMemo = memoOf[string, []splitDiffRow]
	lineMemo = memoOf[lineKey, string]
)

// lineKey addresses the number column of a source window: the text it numbers
// and the first number it starts at.
type lineKey struct {
	text  string
	start int
}

func toolCardKey(t *conversation, index int, message map[string]any) string {
	if key := str(message, "toolCallId"); key != "" {
		return key
	}
	return fmt.Sprintf("%s-%d", t.ID, index)
}

// diffMemo holds the split rows of one diff beside how many rows "show more" has
// revealed, since that button outlives neither the card nor the source.
type diffMemo struct {
	rows    rowsMemo
	extra   int
	compact bool
}

// get keeps the rows of one diff. A new diff also drops whatever "show more"
// had opened on the old one.
func (m *diffMemo) get(source string) []splitDiffRow {
	if m.rows.ready && m.rows.key == source {
		return m.rows.value
	}
	m.extra = 0
	return m.rows.get(source, splitDiffPairs)
}

// splitDiffPairs cuts a diff into the aligned row pairs both views draw.
func splitDiffPairs(source string) []splitDiffRow {
	lines := strings.Split(source, "\n")
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	return splitDiffRows(diffVisibleLines(lines, 0, false))
}

func (m *diffMemo) limit(compact bool) int {
	if m.compact != compact {
		m.extra = 0
		m.compact = compact
	}
	if compact {
		return 12 + m.extra
	}
	return 120 + m.extra
}

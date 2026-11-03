// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
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
	argsMemo        = memoOf[string, map[string]any]
	textMemo        = memoOf[string, string]
	displayRowsMemo = memoOf[string, []diffDisplayLine]
	lineMemo        = memoOf[lineKey, string]
)

// lineKey addresses the number column of a source window: the text it numbers
// and the first number it starts at.
type lineKey struct {
	text  string
	start int
}

type commandOutputKey struct{ output, stdout, stderr, text, raw string }

func commandKey(result map[string]any) commandOutputKey {
	return commandOutputKey{str(result, "output"), str(result, "stdout"), str(result, "stderr"), str(result, "text"), str(result, "_text")}
}

func commandOutputFromKey(key commandOutputKey) string {
	parts := make([]string, 0, 5)
	for _, s := range [...]string{key.output, key.stdout, key.stderr, key.text, key.raw} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n")
}

func toolCardKey(t *conversation, index int, message map[string]any) string {
	if key := str(message, "toolCallId"); key != "" {
		return key
	}
	return widgetKey(t.ID, "", index)
}

// diffMemo holds derived diff rows beside how many lines "show more" has
// revealed, since that button outlives neither the card nor the source.
type diffMemo struct {
	displayRows displayRowsMemo
	source      string
	sourceReady bool
	extra       int
	compact     bool
}

func (m *diffMemo) noteSource(source string) {
	if !m.sourceReady || m.source != source {
		m.source = source
		m.sourceReady = true
		m.extra = 0
	}
}

// display keeps the single-column rows rendered by tool cards.
func (m *diffMemo) display(source string) []diffDisplayLine {
	m.noteSource(source)
	return m.displayRows.get(source, func(source string) []diffDisplayLine {
		return singleColumnDiffRows(splitDiffPairs(source))
	})
}

// splitDiffPairs groups adjacent removed/added lines into pairs, retaining their
// source order so the card can flatten them back into a unified single column.
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

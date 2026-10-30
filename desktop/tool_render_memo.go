package desktop

import (
	"fmt"
	"strings"
)

// These values belong to a widget, never to persisted messages or a global cache.
type toolArgsMemo struct {
	source string
	value  map[string]any
}

func (m *toolArgsMemo) get(source string) map[string]any {
	if m.value == nil || m.source != source {
		m.source, m.value = source, parseToolArgs(source)
	}
	return m.value
}
func toolCardKey(t *conversation, index int, message map[string]any) string {
	if key := str(message, "toolCallId"); key != "" {
		return key
	}
	return fmt.Sprintf("%s-%d", t.ID, index)
}

type diffMemo struct {
	source  string
	rows    []splitDiffRow
	extra   int
	compact bool
}

func (m *diffMemo) get(source string) []splitDiffRow {
	if m.rows == nil || m.source != source {
		lines := strings.Split(source, "\n")
		for i := range lines {
			lines[i] = strings.TrimSuffix(lines[i], "\r")
		}
		m.source, m.rows, m.extra = source, splitDiffRows(diffVisibleLines(lines, 0, false)), 0
	}
	return m.rows
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

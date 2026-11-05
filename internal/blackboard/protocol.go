// SPDX-License-Identifier: GPL-3.0-only
package blackboard

import (
	"encoding/xml"
	"io"
	"strconv"
	"strings"
)

type protocolBlock struct {
	start, end int
	message    Message
	valid      bool
}

// Intercept examines ONLY a model's visible assistant text. Callers must never
// pass reasoning, tool arguments or tool results. Messages must occupy their own
// lines (up to three leading spaces); adjacent XML blocks on a line are allowed.
// Successful publications alone are removed, leaving all other bytes intact.
// The existing streaming adapters assemble text before this runs, so split XML
// tags and entities are parsed once, only after the model turn succeeds.
func Intercept(text string, publish func(Message) bool) string {
	if publish == nil || !strings.Contains(text, "<msg") {
		return text
	}
	var out strings.Builder
	var markdown markdownState
	position, kept := 0, 0
	for position < len(text) {
		lineEnd := nextLine(text, position)
		line := text[position:lineEnd]
		markdown.rest = text[lineEnd:]
		if markdown.blockLine(line) {
			position = lineEnd
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if markdown.ticks == 0 && !markdown.comment && indent <= 3 && startsMessage(line[indent:]) {
			blocks, resume, complete := protocolBlocks(text, position+indent)
			if complete {
				for _, block := range blocks {
					if block.valid && publish(block.message) {
						out.WriteString(text[kept:block.start])
						kept = block.end
					}
				}
			}
			// Also skip the body of rejected/malformed XML, rather than treating
			// nested example tags as independent messages.
			position = nextLine(text, resume)
			continue
		}
		markdown.inline(line)
		position = lineEnd
	}
	if kept == 0 {
		return text
	}
	out.WriteString(text[kept:])
	return out.String()
}

func startsMessage(text string) bool {
	if !strings.HasPrefix(text, "<msg") || len(text) < 5 {
		return false
	}
	switch text[4] {
	case ' ', '\t', '\r', '\n', '>', '/':
		return true
	}
	return false
}

func nextLine(text string, start int) int {
	if newline := strings.IndexByte(text[start:], '\n'); newline >= 0 {
		return start + newline + 1
	}
	return len(text)
}

func protocolBlocks(text string, start int) ([]protocolBlock, int, bool) {
	var blocks []protocolBlock
	for {
		message, length, valid, err := decodeMessage(text[start:])
		if err != nil {
			return nil, recoverMessageEnd(text, start), false
		}
		end := start + length
		blocks = append(blocks, protocolBlock{start: start, end: end, message: message, valid: valid})
		tail := end
		for tail < len(text) && (text[tail] == ' ' || text[tail] == '\t' || text[tail] == '\r') {
			tail++
		}
		if tail == len(text) || text[tail] == '\n' {
			return blocks, tail, true
		}
		if !startsMessage(text[tail:]) {
			return nil, tail, false // Inline prose/examples are not the protocol.
		}
		start = tail
	}
}

// A malformed block is retained, but its body is not recursively interpreted.
// This is error recovery only; all accepted blocks go through encoding/xml.
func recoverMessageEnd(text string, start int) int {
	for search := start; search < len(text); {
		index := strings.Index(text[search:], "</msg")
		if index < 0 {
			break
		}
		index += search
		tail := index + len("</msg")
		for tail < len(text) && (text[tail] == ' ' || text[tail] == '\t' || text[tail] == '\r' || text[tail] == '\n') {
			tail++
		}
		if tail < len(text) && text[tail] == '>' {
			return tail + 1
		}
		search = tail
	}
	return len(text)
}

func decodeMessage(text string) (message Message, length int, valid bool, err error) {
	if len(text) > MaxMessageBytes {
		text = text[:MaxMessageBytes]
	}
	decoder := xml.NewDecoder(strings.NewReader(text)) // Strict XML, no custom entities/DTDs.
	token, err := decoder.Token()
	if err != nil {
		return message, 0, false, err
	}
	root, ok := token.(xml.StartElement)
	if !ok || root.Name.Local != "msg" {
		return message, 0, false, io.ErrUnexpectedEOF
	}
	valid = root.Name.Space == ""
	seen := make(map[string]bool, len(root.Attr))
	for _, attribute := range root.Attr {
		key := attribute.Name.Local
		if attribute.Name.Space != "" || seen[key] {
			valid = false
		}
		seen[key] = true
		switch key {
		case "to":
			message.To = attribute.Value
		case "type":
			message.Type = attribute.Value
		case "reply_to":
			seq, parseErr := strconv.ParseInt(attribute.Value, 10, 64)
			if parseErr != nil || seq <= 0 {
				valid = false
			} else {
				message.ReplyTo = &seq
			}
		default: // Includes from, seq, xmlns and all undeclared attributes.
			valid = false
		}
	}
	var body strings.Builder
	depth := 1
	for {
		token, err = decoder.Token()
		if err != nil {
			return message, int(decoder.InputOffset()), false, err
		}
		switch token := token.(type) {
		case xml.StartElement:
			depth++
			valid = false // Body is text/CDATA, not nested protocol or markup.
		case xml.EndElement:
			depth--
			if depth == 0 {
				message.Body = strings.TrimSpace(body.String())
				valid = valid && validPayload(message)
				return message, int(decoder.InputOffset()), valid, nil
			}
		case xml.CharData:
			if depth == 1 {
				body.Write(token)
			}
		default:
			valid = false // No comments, processing instructions or directives.
		}
	}
}

// A small Markdown lexer keeps protocol-looking examples inert. XML parsing is
// deliberately separate: fenced/indented/inline code and HTML code/comments are
// excluded before any XML decoder or publisher sees their contents.
type markdownContainer struct {
	quote  bool
	indent int
}

type markdownState struct {
	fence           byte
	fenceWidth      int
	containers      []markdownContainer
	fenceContainers []markdownContainer
	ticks           int
	comment         bool
	htmlCode        string
	htmlDepth       int
	htmlTag         bool
	htmlEnd         bool
	htmlQuote       byte
	rest            string // Everything after the line being scanned.
}

func (m *markdownState) blockLine(line string) bool {
	if m.htmlCode != "" {
		m.inline(line)
		return true
	}
	if m.fence != 0 {
		content, _, contained := continueContainers(line, m.fenceContainers)
		if contained || strings.TrimSpace(line) == "" {
			trimmed := strings.TrimLeft(content, " ")
			width := prefixRun(trimmed, m.fence)
			if len(content)-len(trimmed) <= 3 && width >= m.fenceWidth && strings.TrimSpace(trimmed[width:]) == "" {
				m.fence = 0
			}
			return true
		}
		// A fenced block cannot outlive its list item or block quote.
		m.fence = 0
	}
	if !m.comment && m.ticks == 0 {
		content, containers := markdownContainers(line, m.containers)
		m.containers = containers
		trimmed := strings.TrimLeft(content, " ")
		if len(content)-len(trimmed) <= 3 && len(trimmed) > 0 && (trimmed[0] == '`' || trimmed[0] == '~') {
			width := prefixRun(trimmed, trimmed[0])
			if width >= 3 && (trimmed[0] == '~' || !strings.Contains(trimmed[width:], "`")) {
				m.fence, m.fenceWidth = trimmed[0], width
				m.fenceContainers = containers
				return true
			}
		}
	}
	trimmed := strings.TrimLeft(line, " ")
	return len(line)-len(trimmed) >= 4 || strings.HasPrefix(trimmed, "\t")
}

// Only container prefixes are stripped, never the body of a fenced block. In
// particular, list-looking example text cannot open or close another container.
func continueContainers(line string, containers []markdownContainer) (string, int, bool) {
	for i, container := range containers {
		spaces, columns := markdownIndent(line)
		if container.quote {
			if columns > 3 || spaces == len(line) || line[spaces] != '>' {
				return line, i, false
			}
			line = line[spaces+1:]
			if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
				line = line[1:]
			}
		} else {
			if columns < container.indent {
				return line, i, false
			}
			columns, spaces = 0, 0
			for columns < container.indent {
				if line[spaces] == '\t' {
					columns += 4 - columns%4
				} else {
					columns++
				}
				spaces++
			}
			line = strings.Repeat(" ", columns-container.indent) + line[spaces:]
		}
	}
	return line, len(containers), true
}

func markdownContainers(line string, previous []markdownContainer) (string, []markdownContainer) {
	if strings.TrimSpace(line) == "" {
		return line, previous
	}
	content, continued, _ := continueContainers(line, previous)
	containers := append([]markdownContainer(nil), previous[:continued]...)
	for {
		spaces, columns := markdownIndent(content)
		if columns > 3 || spaces == len(content) {
			return content, containers
		}
		tail := content[spaces:]
		if tail[0] == '>' {
			containers = append(containers, markdownContainer{quote: true})
			content = tail[1:]
			if len(content) > 0 && (content[0] == ' ' || content[0] == '\t') {
				content = content[1:]
			}
			continue
		}
		marker := 0
		if tail[0] == '-' || tail[0] == '+' || tail[0] == '*' {
			marker = 1
		} else {
			for marker < len(tail) && marker < 9 && tail[marker] >= '0' && tail[marker] <= '9' {
				marker++
			}
			if marker == 0 || marker == len(tail) || (tail[marker] != '.' && tail[marker] != ')') {
				return content, containers
			}
			marker++
		}
		if marker == len(tail) || (tail[marker] != ' ' && tail[marker] != '\t') {
			return content, containers
		}
		padding, width := markdownIndentAt(tail[marker:], columns+marker)
		if width > 4 {
			padding, width = markdownIndentAt(tail[marker:marker+1], columns+marker)
		}
		containers = append(containers, markdownContainer{indent: columns + marker + width})
		content = tail[marker+padding:]
	}
}

func markdownIndent(text string) (bytes, columns int) {
	return markdownIndentAt(text, 0)
}

func markdownIndentAt(text string, startColumn int) (bytes, width int) {
	columns := startColumn
	for bytes < len(text) {
		switch text[bytes] {
		case ' ':
			columns++
		case '\t':
			columns += 4 - columns%4
		default:
			return bytes, columns - startColumn
		}
		bytes++
	}
	return bytes, columns - startColumn
}

func prefixRun(text string, character byte) int {
	i := 0
	for i < len(text) && text[i] == character {
		i++
	}
	return i
}

// finishLine ends the state that only a closing marker can extend: a code span's
// backticks and an HTML comment. With no such marker left below, the run was
// literal text, and carrying it would silently hide every later line.
func (m *markdownState) finishLine() {
	if m.ticks != 0 && !strings.Contains(m.rest, strings.Repeat("`", m.ticks)) {
		m.ticks = 0
	}
	if m.comment && !strings.Contains(m.rest, "-->") {
		m.comment = false
	}
}

func (m *markdownState) inline(line string) {
	for i := 0; i < len(line); {
		if m.htmlCode != "" {
			i = m.html(line, i)
			continue
		}
		if m.comment {
			end := strings.Index(line[i:], "-->")
			if end < 0 {
				break // The rest of this line is inside the comment.
			}
			i += end + 3
			m.comment = false
			continue
		}
		if line[i] == '\\' {
			i += 2
			continue
		}
		if line[i] == '`' {
			width := prefixRun(line[i:], '`')
			if m.ticks == 0 {
				m.ticks = width
			} else if m.ticks == width {
				m.ticks = 0
			}
			i += width
			continue
		}
		if m.ticks == 0 && strings.HasPrefix(line[i:], "<!--") {
			m.comment = true
			i += 4
			continue
		}
		if m.ticks == 0 && line[i] == '<' {
			for _, tag := range [...]string{"pre", "code", "script", "style", "textarea"} {
				if htmlTagPrefix(line[i:], "<"+tag) {
					m.htmlCode, m.htmlDepth, m.htmlTag = tag, 1, true
					i += len(tag) + 1
					break
				}
			}
			if m.htmlCode != "" {
				continue
			}
		}
		i++
	}
	m.finishLine()
}

func htmlTagPrefix(text, prefix string) bool {
	if len(text) <= len(prefix) || !strings.EqualFold(text[:len(prefix)], prefix) {
		return false
	}
	switch text[len(prefix)] {
	case ' ', '\t', '\r', '\n', '\f', '>', '/':
		return true
	}
	return false
}

// tagClosesOnLine reports whether the tag whose "<" starts the text ends on this
// same line, quotes included. A tag that never closes here is literal code.
func tagClosesOnLine(text string) bool {
	var quote byte
	for i := 1; i < len(text); i++ {
		switch {
		case quote != 0:
			if text[i] == quote {
				quote = 0
			}
		case text[i] == '\'' || text[i] == '"':
			quote = text[i]
		case text[i] == '>':
			return true
		}
	}
	return false
}

// Keep tag/attribute state across lines: a quoted closing-tag example in an
// opening attribute must not release the surrounding HTML code block.
func (m *markdownState) html(line string, i int) int {
	for i < len(line) && m.htmlCode != "" {
		if m.htmlTag {
			switch {
			case m.htmlQuote != 0:
				if line[i] == m.htmlQuote {
					m.htmlQuote = 0
				}
			case line[i] == '\'' || line[i] == '"':
				m.htmlQuote = line[i]
			case line[i] == '>':
				m.htmlTag = false
				if m.htmlEnd {
					m.htmlDepth--
					m.htmlEnd = false
					if m.htmlDepth == 0 {
						m.htmlCode = ""
					}
				}
			}
			i++
			continue
		}
		if m.comment {
			end := strings.Index(line[i:], "-->")
			if end < 0 {
				return len(line)
			}
			i += end + 3
			m.comment = false
			continue
		}
		if line[i] == '<' {
			if (m.htmlCode == "pre" || m.htmlCode == "code") && strings.HasPrefix(line[i:], "<!--") {
				m.comment = true
				i += 4
				continue
			}
			if htmlTagPrefix(line[i:], "</"+m.htmlCode) {
				i += len(m.htmlCode) + 2
				m.htmlTag, m.htmlEnd = true, true
				continue
			}
			if m.htmlCode == "pre" || m.htmlCode == "code" {
				if htmlTagPrefix(line[i:], "<"+m.htmlCode) {
					m.htmlDepth++
					i += len(m.htmlCode) + 1
					m.htmlTag = true
					continue
				}
				// Other tags' attributes can also contain a literal closing tag, but
				// this state may only outlive the line when the tag really ends here:
				// "<b" in code is literal, and holding it open ate the real closing
				// tag on the next line, hiding the rest of the turn.
				name := i + 1
				if name < len(line) && line[name] == '/' {
					name++
				}
				if name < len(line) && asciiLetter(line[name]) && tagClosesOnLine(line[i:]) {
					m.htmlTag = true
				}
			}
		}
		i++
	}
	return i
}

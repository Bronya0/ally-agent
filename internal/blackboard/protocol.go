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
type markdownState struct {
	fence      byte
	fenceWidth int
	ticks      int
	comment    bool
	htmlCode   string
}

func (m *markdownState) blockLine(line string) bool {
	trimmed := strings.TrimLeft(line, " ")
	indent := len(line) - len(trimmed)
	if m.htmlCode != "" {
		if strings.Contains(strings.ToLower(line), "</"+m.htmlCode+">") {
			m.htmlCode = ""
		}
		return true
	}
	if m.fence != 0 {
		if indent <= 3 && prefixRun(trimmed, m.fence) >= m.fenceWidth && strings.TrimSpace(trimmed[prefixRun(trimmed, m.fence):]) == "" {
			m.fence = 0
		}
		return true
	}
	if indent >= 4 || strings.HasPrefix(trimmed, "\t") {
		return true
	}
	if !m.comment && m.ticks == 0 && len(trimmed) > 0 && (trimmed[0] == '`' || trimmed[0] == '~') {
		width := prefixRun(trimmed, trimmed[0])
		if width >= 3 && (trimmed[0] == '~' || !strings.Contains(trimmed[width:], "`")) {
			m.fence, m.fenceWidth = trimmed[0], width
			return true
		}
	}
	return false
}

func prefixRun(text string, character byte) int {
	i := 0
	for i < len(text) && text[i] == character {
		i++
	}
	return i
}

func (m *markdownState) inline(line string) {
	for i := 0; i < len(line); {
		if m.comment {
			end := strings.Index(line[i:], "-->")
			if end < 0 {
				return
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
			lower := strings.ToLower(line[i:])
			for _, tag := range [...]string{"pre", "code", "script", "style", "textarea"} {
				opening := "<" + tag
				if strings.HasPrefix(lower, opening) && len(lower) > len(opening) && (lower[len(opening)] == '>' || lower[len(opening)] == ' ' || lower[len(opening)] == '\t') && !strings.Contains(lower, "</"+tag+">") {
					m.htmlCode = tag
					return
				}
			}
		}
		i++
	}
}

// SPDX-License-Identifier: GPL-3.0-only
package blackboard

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	MaxNameLength   = 64
	NamePattern     = "^[A-Za-z][A-Za-z0-9_.-]*$"
	MaxMessageBytes = 64 * 1024
)

// Message is collaboration data, not an instruction or a shared chat history.
// Seq and From are always assigned by the runtime.
type Message struct {
	Seq     int64
	From    string
	To      string
	Type    string
	ReplyTo *int64
	Body    string
}

// Blackboard belongs to one runChat. The append-only log and each agent's global
// scan position share a lock; no per-agent inbox or persistent state is needed.
// Fields stay private so callers cannot mutate a snapshot behind the lock.
type Blackboard struct {
	mu       sync.Mutex
	messages []Message
	nextSeq  int64
	cursors  map[string]int64
	nextName int64
	closed   bool
}

func New() *Blackboard {
	return &Blackboard{nextSeq: 1, nextName: 1, cursors: make(map[string]int64)}
}

func ValidName(name string) bool {
	if len(name) == 0 || len(name) > MaxNameLength || !asciiLetter(name[0]) {
		return false
	}
	for i := 1; i < len(name); i++ {
		c := name[i]
		if !asciiLetter(c) && !(c >= '0' && c <= '9') && c != '_' && c != '.' && c != '-' {
			return false
		}
	}
	return true
}

func asciiLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

func validType(kind string) bool {
	switch kind {
	case "finding", "question", "answer", "decision", "blocker", "handoff":
		return true
	}
	return false
}

// Register reserves a name for the whole run, even while its agent is queued or
// after it finishes. Empty names receive a collision-free agent-N identity.
func (b *Blackboard) Register(name string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return "", errors.New("blackboard is closed")
	}
	if name == "" {
		for {
			name = fmt.Sprintf("agent-%d", b.nextName)
			b.nextName++
			if _, exists := b.cursors[name]; !exists {
				break
			}
		}
	} else if !ValidName(name) {
		return "", fmt.Errorf("invalid blackboard agent name %q", name)
	}
	if _, exists := b.cursors[name]; exists {
		return "", fmt.Errorf("blackboard agent name %q is already used in this run; choose a different name", name)
	}
	b.cursors[name] = 0 // A late-starting agent can still read earlier messages.
	return name, nil
}

func (b *Blackboard) Names() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	names := make([]string, 0, len(b.cursors))
	for name := range b.cursors {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func validPayload(message Message) bool {
	body := strings.TrimSpace(message.Body)
	return (message.To == "*" || ValidName(message.To)) && validType(message.Type) &&
		body != "" && len(body) <= MaxMessageBytes && utf8.ValidString(body) &&
		(message.ReplyTo == nil || *message.ReplyTo > 0)
}

// Publish accepts a named destination even before it is registered: the log
// retains it for a later delegate in this run. Unknown names never cross runs.
func (b *Blackboard) Publish(from string, message Message) bool {
	if !validPayload(message) {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return false
	}
	if _, registered := b.cursors[from]; !registered {
		return false
	}
	if message.ReplyTo != nil && (*message.ReplyTo <= 0 || *message.ReplyTo >= b.nextSeq) {
		return false
	}
	message.Seq = b.nextSeq
	message.From = from
	message.Body = strings.TrimSpace(message.Body)
	message = cloneMessage(message)
	b.nextSeq++
	b.messages = append(b.messages, message)
	return true
}

// Read snapshots the unread suffix without acknowledging deliverable messages.
// scanned is a GLOBAL scan position, including messages for other agents and
// self-broadcasts. An irrelevant suffix can be acknowledged immediately because
// it contains nothing this agent could lose on a failed request.
func (b *Blackboard) Read(name string) (messages []Message, scanned int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	cursor, registered := b.cursors[name]
	if b.closed || !registered {
		return nil, 0
	}
	scanned = b.nextSeq - 1
	for _, message := range b.messages[cursor:] {
		if message.From != name && (message.To == name || message.To == "*") {
			messages = append(messages, cloneMessage(message))
		}
	}
	if len(messages) == 0 {
		b.cursors[name] = scanned
	}
	return messages, scanned
}

// Ack is called only after a successful model turn containing the snapshot.
// Failed requests may redeliver the same Seq; they cannot lose unread messages.
func (b *Blackboard) Ack(name string, scanned int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	cursor, registered := b.cursors[name]
	if !b.closed && registered && scanned > cursor && scanned < b.nextSeq {
		b.cursors[name] = scanned
	}
}

// Close must follow the caller's child-execution barrier. It releases every
// message and identity, and makes a retained reference inert.
func (b *Blackboard) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	b.messages = nil
	b.cursors = nil
	b.nextSeq = 0
	b.nextName = 0
}

func cloneMessage(message Message) Message {
	if message.ReplyTo != nil {
		seq := *message.ReplyTo
		message.ReplyTo = &seq
	}
	return message
}

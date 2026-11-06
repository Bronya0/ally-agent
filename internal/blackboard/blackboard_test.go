// SPDX-License-Identifier: GPL-3.0-only
package blackboard

import (
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func registerAgents(t *testing.T, board *Blackboard, names ...string) {
	t.Helper()
	for _, name := range names {
		if got, err := board.Register(name); err != nil || got != name {
			t.Fatalf("Register(%q) = %q, %v", name, got, err)
		}
	}
}

func publish(t *testing.T, board *Blackboard, from, to, kind, body string, reply *int64) {
	t.Helper()
	if !board.Publish(from, Message{To: to, Type: kind, Body: body, ReplyTo: reply}) {
		t.Fatalf("Publish(%s -> %s, %s) failed", from, to, kind)
	}
}

func sequences(messages []Message) []int64 {
	var out []int64
	for _, message := range messages {
		out = append(out, message.Seq)
	}
	return out
}

func TestBlackboardRoutingBroadcastIncrementAndReply(t *testing.T) {
	t.Parallel()
	board := New()
	registerAgents(t, board, "agent-a", "agent-b", "agent-c")
	publish(t, board, "agent-a", "agent-b", "finding", "JWT is supported", nil)
	publish(t, board, "agent-a", "*", "decision", "Migration is ready", nil)
	publish(t, board, "agent-a", "agent-c", "handoff", "C-only work", nil)

	b, scanned := board.Read("agent-b")
	if !reflect.DeepEqual(sequences(b), []int64{1, 2}) || scanned != 3 {
		t.Fatalf("B = %+v, scanned %d", b, scanned)
	}
	c, cScanned := board.Read("agent-c")
	if !reflect.DeepEqual(sequences(c), []int64{2, 3}) || cScanned != 3 {
		t.Fatalf("C = %+v, scanned %d", c, cScanned)
	}
	if own, scanned := board.Read("agent-a"); len(own) != 0 || scanned != 3 {
		t.Fatalf("sender received its own messages: %+v, scanned %d", own, scanned)
	}
	board.Ack("agent-b", scanned)
	if again, _ := board.Read("agent-b"); len(again) != 0 {
		t.Fatalf("B received acknowledged messages again: %+v", again)
	}
	if stillUnread, _ := board.Read("agent-c"); !reflect.DeepEqual(sequences(stillUnread), []int64{2, 3}) {
		t.Fatalf("B's cursor affected C: %+v", stillUnread)
	}
	replyTo := int64(1)
	publish(t, board, "agent-b", "agent-a", "answer", "JWT implementation confirmed", &replyTo)
	reply, scanned := board.Read("agent-a")
	if len(reply) != 1 || reply[0].Seq != 4 || reply[0].From != "agent-b" || reply[0].ReplyTo == nil || *reply[0].ReplyTo != 1 || scanned != 4 {
		t.Fatalf("reply = %+v, scanned %d", reply, scanned)
	}
}

func TestBlackboardLateReceiverAndUniqueNames(t *testing.T) {
	t.Parallel()
	board := New()
	registerAgents(t, board, "agent-a", "agent-1")
	publish(t, board, "agent-a", "agent-b", "finding", "Stored before B exists", nil)
	if unread, scan := board.Read("agent-b"); len(unread) != 0 || scan != 0 {
		t.Fatalf("unregistered read = %+v, %d", unread, scan)
	}
	registerAgents(t, board, "agent-b")
	if unread, scan := board.Read("agent-b"); len(unread) != 1 || unread[0].Seq != 1 || scan != 1 {
		t.Fatalf("late B lost the earlier message: %+v, %d", unread, scan)
	}
	if _, err := board.Register("agent-a"); err == nil {
		t.Fatal("duplicate name was accepted")
	}
	if name, err := board.Register(""); err != nil || name != "agent-2" {
		t.Fatalf("automatic name = %q, %v", name, err)
	}
	if !reflect.DeepEqual(board.Names(), []string{"agent-1", "agent-2", "agent-a", "agent-b"}) {
		t.Fatalf("names = %v", board.Names())
	}
	for _, invalid := range []string{"*", "a b", "../a", "中文", "1agent", strings.Repeat("a", MaxNameLength+1)} {
		if _, err := board.Register(invalid); err == nil {
			t.Errorf("invalid name %q was accepted", invalid)
		}
	}
}

func TestBlackboardGlobalScanAndFailedDelivery(t *testing.T) {
	t.Parallel()
	board := New()
	registerAgents(t, board, "agent-a", "agent-b", "agent-c")
	publish(t, board, "agent-a", "agent-c", "finding", "irrelevant to B", nil)
	if unread, scanned := board.Read("agent-b"); len(unread) != 0 || scanned != 1 || board.cursors["agent-b"] != 1 {
		t.Fatalf("irrelevant suffix was not scanned: %+v, %d, cursor %d", unread, scanned, board.cursors["agent-b"])
	}
	publish(t, board, "agent-a", "agent-b", "question", "new B question", nil)
	first, scanned := board.Read("agent-b")
	// No Ack: an empty/rejected/interrupted model response cannot consume it.
	retry, retryScan := board.Read("agent-b")
	if !reflect.DeepEqual(first, retry) || scanned != 2 || retryScan != 2 {
		t.Fatalf("retry lost/changed delivery: %+v -> %+v", first, retry)
	}
	publish(t, board, "agent-a", "agent-b", "finding", "arrived during the request", nil)
	board.Ack("agent-b", scanned)
	board.Ack("agent-b", 1)     // A stale acknowledgment cannot rewind the cursor.
	board.Ack("agent-b", 1<<62) // Nor can it consume a future suffix.
	remaining, scanned := board.Read("agent-b")
	if !reflect.DeepEqual(sequences(remaining), []int64{3}) || scanned != 3 {
		t.Fatalf("ack consumed a later publication: %+v, %d", remaining, scanned)
	}
}

func TestBlackboardPayloadAndRuntimeIdentity(t *testing.T) {
	t.Parallel()
	board := New()
	registerAgents(t, board, "agent-a", "agent-b")
	for _, kind := range []string{"finding", "question", "answer", "decision", "blocker", "handoff"} {
		publish(t, board, "agent-a", "agent-b", kind, " helpful data ", nil)
	}
	zero, negative, future := int64(0), int64(-1), int64(99)
	for _, message := range []Message{
		{To: "*", Type: "progress", Body: "no"},
		{To: "bad target", Type: "finding", Body: "no"},
		{To: "", Type: "finding", Body: "no"},
		{To: "*", Type: "finding", Body: " \n\t"},
		{To: "*", Type: "finding", Body: string([]byte{0xff})},
		{To: "*", Type: "finding", Body: strings.Repeat("x", MaxMessageBytes+1)},
		{To: "*", Type: "answer", Body: "no", ReplyTo: &zero},
		{To: "*", Type: "answer", Body: "no", ReplyTo: &negative},
		{To: "*", Type: "answer", Body: "no", ReplyTo: &future},
	} {
		if board.Publish("agent-a", message) {
			t.Errorf("invalid payload was accepted: %+v", message)
		}
	}
	if board.Publish("unregistered", Message{To: "*", Type: "finding", Body: "spoof"}) {
		t.Fatal("unregistered sender was accepted")
	}
	if !board.Publish("agent-a", Message{From: "spoofed", Seq: 99, To: "agent-b", Type: "finding", Body: "runtime owns identity"}) {
		t.Fatal("valid runtime publication failed")
	}
	messages, _ := board.Read("agent-b")
	if len(messages) != 7 || messages[6].From != "agent-a" || messages[6].Seq != 7 || messages[0].Body != "helpful data" {
		t.Fatalf("identity/sequence/body = %+v", messages)
	}
}

func TestBlackboardSnapshotsDoNotAliasReplyPointers(t *testing.T) {
	t.Parallel()
	board := New()
	registerAgents(t, board, "agent-a", "agent-b")
	publish(t, board, "agent-a", "agent-b", "question", "question", nil)
	reply := int64(1)
	published, ok := board.PublishMessage("agent-b", Message{To: "agent-a", Type: "answer", Body: "answer", ReplyTo: &reply})
	if !ok || published.Seq != 2 || published.From != "agent-b" {
		t.Fatalf("published snapshot = %+v, accepted=%v", published, ok)
	}
	*published.ReplyTo = 77
	reply = 99
	first, _ := board.Read("agent-a")
	if *first[0].ReplyTo != 1 {
		t.Fatal("stored reply aliased the caller's pointer")
	}
	*first[0].ReplyTo = 88
	second, _ := board.Read("agent-a")
	if *second[0].ReplyTo != 1 {
		t.Fatal("snapshot mutation changed the stored message")
	}
}

func TestBlackboardConcurrentPublishAndIndependentReaders(t *testing.T) {
	t.Parallel()
	const senders, perSender, readers = 12, 150, 3
	board := New()
	for sender := 0; sender < senders; sender++ {
		registerAgents(t, board, fmt.Sprintf("sender-%d", sender))
	}
	for reader := 0; reader < readers; reader++ {
		registerAgents(t, board, fmt.Sprintf("reader-%d", reader))
	}
	done := make(chan struct{})
	var reading sync.WaitGroup
	seen := make([]map[int64]Message, readers)
	for reader := 0; reader < readers; reader++ {
		reading.Add(1)
		go func(reader int) {
			defer reading.Done()
			name := fmt.Sprintf("reader-%d", reader)
			seen[reader] = make(map[int64]Message)
			for {
				messages, scanned := board.Read(name)
				for _, message := range messages {
					if _, duplicate := seen[reader][message.Seq]; duplicate {
						t.Errorf("%s received sequence %d twice", name, message.Seq)
					}
					seen[reader][message.Seq] = message
				}
				board.Ack(name, scanned)
				select {
				case <-done:
					if len(messages) == 0 {
						return
					}
				default:
				}
				runtime.Gosched()
			}
		}(reader)
	}
	var writing sync.WaitGroup
	for sender := 0; sender < senders; sender++ {
		writing.Add(1)
		go func(sender int) {
			defer writing.Done()
			name := fmt.Sprintf("sender-%d", sender)
			for index := 0; index < perSender; index++ {
				publish(t, board, name, "*", "finding", fmt.Sprintf("%s:%d:完整内容", name, index), nil)
			}
		}(sender)
	}
	writing.Wait()
	close(done)
	reading.Wait()
	want := senders * perSender
	if len(board.messages) != want || board.nextSeq != int64(want+1) {
		t.Fatalf("log length %d, next sequence %d, want %d/%d", len(board.messages), board.nextSeq, want, want+1)
	}
	bodies := make(map[string]bool)
	for index, message := range board.messages {
		if message.Seq != int64(index+1) || bodies[message.Body] || !strings.HasPrefix(message.Body, message.From+":") || !strings.HasSuffix(message.Body, ":完整内容") {
			t.Fatalf("overwritten/torn message at %d: %+v", index, message)
		}
		bodies[message.Body] = true
	}
	for reader, messages := range seen {
		if len(messages) != want {
			t.Errorf("reader %d got %d/%d messages", reader, len(messages), want)
		}
	}
}

func TestBlackboardReservationLifecycleRetainsQueuedDelivery(t *testing.T) {
	t.Parallel()
	board := New()
	registerAgents(t, board, "sender")
	if name, err := board.Reserve("worker"); err != nil || name != "worker" {
		t.Fatalf("Reserve = %q, %v", name, err)
	}
	if !reflect.DeepEqual(board.Names(), []string{"sender", "worker"}) {
		t.Fatalf("queued peer is not visible: %v", board.Names())
	}
	if _, err := board.Register("worker"); err == nil {
		t.Fatal("pending reservation was reused before release")
	}
	publish(t, board, "sender", "worker", "finding", "queued delivery", nil)
	board.ReleaseReservation("worker")
	board.ReleaseReservation("worker")
	if err := board.StartReserved("worker"); err == nil {
		t.Fatal("released reservation could be started")
	}
	if _, err := board.Reserve("worker"); err != nil {
		t.Fatalf("never-started identity could not be retried: %v", err)
	}
	if err := board.StartReserved("worker"); err != nil {
		t.Fatal(err)
	}
	if err := board.StartReserved("worker"); err == nil {
		t.Fatal("reservation was claimed twice")
	}
	board.ReleaseReservation("worker")
	if _, err := board.Register("worker"); err == nil {
		t.Fatal("started identity was released and reused")
	}
	if messages, _ := board.Read("worker"); len(messages) != 1 || messages[0].Body != "queued delivery" {
		t.Fatalf("retry lost queued messages: %+v", messages)
	}
	board.Close()
	if _, err := board.Reserve("later"); err == nil {
		t.Fatal("closed board accepted a reservation")
	}
	board.ReleaseReservation("worker")
}

func TestBlackboardReservationStartAndReleaseAreAtomic(t *testing.T) {
	t.Parallel()
	for i := 0; i < 100; i++ {
		board := New()
		if _, err := board.Reserve("worker"); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		var startErr error
		wg.Add(2)
		go func() { defer wg.Done(); startErr = board.StartReserved("worker") }()
		go func() { defer wg.Done(); board.ReleaseReservation("worker") }()
		wg.Wait()
		if startErr == nil {
			if _, err := board.Register("worker"); err == nil {
				t.Fatal("release removed a concurrently started identity")
			}
		} else if _, err := board.Register("worker"); err != nil {
			t.Fatalf("released identity was left occupied: %v", err)
		}
	}
}

func TestBlackboardCloseReleasesRunState(t *testing.T) {
	t.Parallel()
	board := New()
	registerAgents(t, board, "agent-a", "agent-b")
	publish(t, board, "agent-a", "agent-b", "finding", "old run", nil)
	board.Close()
	board.Close() // Cleanup is idempotent.
	if board.messages != nil || board.cursors != nil || board.reserved != nil || board.nextSeq != 0 || len(board.Names()) != 0 {
		t.Fatal("closed board retained messages or cursor state")
	}
	if messages, _ := board.Read("agent-b"); len(messages) != 0 {
		t.Fatal("closed board delivered old messages")
	}
	if _, err := board.Register("agent-c"); err == nil || board.Publish("agent-a", Message{To: "*", Type: "finding", Body: "late"}) {
		t.Fatal("retained reference remained active")
	}
	next := New()
	registerAgents(t, next, "agent-a", "agent-b")
	if messages, scanned := next.Read("agent-b"); len(messages) != 0 || scanned != 0 {
		t.Fatal("new run inherited old messages")
	}
	publish(t, next, "agent-a", "agent-b", "finding", "new run", nil)
	if messages, _ := next.Read("agent-b"); messages[0].Seq != 1 {
		t.Fatal("new run did not reset the sequence")
	}
}

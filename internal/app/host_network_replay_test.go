package app

import (
	"bytes"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNetworkReplayCursorAndOverflow(t *testing.T) {
	sink, err := newNetworkEventSink("127.0.0.1:0", "secret", 2)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		sink.Emit("run:stream", map[string]any{"sessionId": "s"})
	}
	req := httptest.NewRequest("GET", "/events?since=0", nil)
	if _, reset := sink.replayRequest(req); !reset {
		t.Fatal("covered history must require resync")
	}
	req = httptest.NewRequest("GET", "/events", nil)
	req.Header.Set("Last-Event-ID", fmt.Sprintf("%s:1", sink.instance))
	since, reset := sink.replayRequest(req)
	if since != 2 || reset {
		t.Fatalf("resume cursor=%d reset=%v", since, reset)
	}
	req.Header.Set("Last-Event-ID", "previous-process:1")
	if _, reset := sink.replayRequest(req); !reset {
		t.Fatal("restart must require resync")
	}
	events, _ := sink.ring.since(2)
	var out bytes.Buffer
	if err := writeSSE(&out, events[0]); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), fmt.Sprintf("id: %s:2", sink.instance)) {
		t.Fatal(out.String())
	}
	sub := sink.subscribe()
	defer sink.unsubscribe(sub)
	for i := 0; i < subscriberQueueSize+1; i++ {
		sink.Emit("run:done", nil)
	}
	select {
	case <-sub.overflow:
	default:
		t.Fatal("dropping even the final event must signal resync")
	}
}

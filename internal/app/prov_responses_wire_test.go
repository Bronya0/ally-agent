// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestResponsesSSEFramingPreservesMultilineEvents(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		t.Run(strings.ReplaceAll(newline, "\r", "CR"), func(t *testing.T) {
			wire := strings.Join([]string{
				": heartbeat", "event: response.output_text.delta",
				`data: {"type":"response.output_text.delta",`,
				`data: "delta":"hello"}`, "", ": next heartbeat", "",
				`data: {"type":"response.completed","response":{}}`, "", "",
			}, newline)
			stream := &openAIResponsesSSEStream{decoder: newResponsesSSEDecoder(&http.Response{Body: io.NopCloser(strings.NewReader(wire))})}
			defer stream.Close()
			if !stream.Next() {
				t.Fatalf("missing multiline event: %v", stream.Err())
			}
			event, raw, err := stream.Event()
			if err != nil || event.Type != "response.output_text.delta" || event.Delta != "hello" || !strings.Contains(string(raw), "\n") {
				t.Fatalf("multiline event lost: %+v %q %v", event, raw, err)
			}
			if !stream.Next() {
				t.Fatalf("missing terminal event: %v", stream.Err())
			}
			event, _, err = stream.Event()
			if err != nil || event.Type != "response.completed" {
				t.Fatalf("terminal event lost: %+v %v", event, err)
			}
			if stream.Next() || stream.Err() != nil {
				t.Fatalf("unexpected stream tail: %v", stream.Err())
			}
		})
	}
}

func TestResponsesSSEFramingKeepsUntypedErrorsAndLargePayloads(t *testing.T) {
	// Image/argument deltas exceed bufio.Scanner's default 64 KiB limit.
	large := strings.Repeat("x", 128<<10)
	wire := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"" + large + "\"}\n\ndata: {\"message\":\"429 rate limited\"}\n\n"
	stream := &openAIResponsesSSEStream{decoder: newResponsesSSEDecoder(&http.Response{Body: io.NopCloser(strings.NewReader(wire))})}
	defer stream.Close()
	if !stream.Next() {
		t.Fatalf("large event rejected: %v", stream.Err())
	}
	event, _, err := stream.Event()
	if err != nil || event.Delta != large {
		t.Fatalf("large payload truncated: %d %v", len(event.Delta), err)
	}
	if !stream.Next() {
		t.Fatalf("untyped error swallowed: %v", stream.Err())
	}
	event, raw, err := stream.Event()
	if err != nil || event.Type != "" || !strings.Contains(untypedResponsesErrorMessage(raw), "429 rate limited") {
		t.Fatalf("untyped error lost: %s %v", raw, err)
	}
}

func TestResponsesUnknownEventPayloadDoesNotFailStream(t *testing.T) {
	stream := auditStream("data: {\"type\":\"response.future.delta\",\"delta\":{\"text\":\"ignored\"},\"response\":{\"usage\":\"future format\"}}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{}}\n\n", false)
	app := &App{reasoningStash: newReasoningStash()}
	result, _, err := app.consumeOpenAIResponsesStream(ConfigState{}, buildOpenAIResponsesRequest(ConfigState{}, "test-model", nil, nil, nil), stream, nil)
	if err != nil || result == nil || result.Content != "hello" {
		t.Fatalf("unknown event interrupted the response: %+v %v", result, err)
	}
}

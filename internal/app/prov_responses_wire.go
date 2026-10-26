// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	openai "github.com/sashabaranov/go-openai"
)

// The community SDK's common output item omits encrypted reasoning and image
// results. Decode those fields at the provider boundary; retain native history
// separately as RawMessage so new provider fields survive replay unchanged.
type responsesOutputItem struct {
	openai.ResponseOutputItem
	EncryptedContent string `json:"encrypted_content,omitempty"`
	Result           string `json:"result,omitempty"`
}

func responsesEventItem(raw []byte) responsesOutputItem {
	var event struct {
		Item responsesOutputItem `json:"item"`
	}
	_ = json.Unmarshal(raw, &event)
	return event.Item
}

type responsesTextPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type responsesReasoningWireItem struct {
	Type             string              `json:"type"`
	ID               string              `json:"id"`
	Summary          []responsesTextPart `json:"summary"`
	Content          []responsesTextPart `json:"content,omitempty"`
	EncryptedContent string              `json:"encrypted_content,omitempty"`
	Status           string              `json:"status,omitempty"`
}

func responsesErrorParam(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	raw, _ := json.Marshal(value)
	return string(raw)
}

func responsesTerminalImages(raw []byte) []responsesOutputItem {
	var event struct {
		Response struct {
			Output []responsesOutputItem `json:"output"`
		} `json:"response"`
	}
	_ = json.Unmarshal(raw, &event)
	var images []responsesOutputItem
	for _, item := range event.Response.Output {
		if item.Type == "image_generation_call" {
			images = append(images, item)
		}
	}
	return images
}

// Frame SSE independently of the SDK's single-data-line reader. Responses
// gateways can emit multiline events, comment heartbeats and untyped errors;
// all must reach the community SDK event decoder without losing raw JSON.
type responsesSSEFrame struct {
	Type string
	Data []byte
}

type responsesSSEDecoder struct {
	body    io.ReadCloser
	scanner *bufio.Scanner
	event   responsesSSEFrame
	err     error
	done    bool
}

const maxResponsesSSEEventBytes = 32 << 20

func newResponsesSSEDecoder(response *http.Response) *responsesSSEDecoder {
	if response == nil || response.Body == nil {
		return nil
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(nil, maxResponsesSSEEventBytes)
	return &responsesSSEDecoder{body: response.Body, scanner: scanner}
}

func (d *responsesSSEDecoder) Next() bool {
	if d.done || d.err != nil {
		return false
	}
	var event responsesSSEFrame
	for d.scanner.Scan() {
		line := d.scanner.Bytes()
		if len(line) == 0 {
			if len(event.Data) > 0 {
				d.event = event
				return true
			}
			event.Type = ""
			continue
		}
		name, value, _ := bytes.Cut(line, []byte(":"))
		value = bytes.TrimPrefix(value, []byte(" "))
		switch string(name) {
		case "event":
			event.Type = string(value)
		case "data":
			if len(event.Data)+len(value)+1 > maxResponsesSSEEventBytes {
				d.err = errors.New("Responses SSE event exceeds size limit")
				return false
			}
			event.Data = append(event.Data, value...)
			event.Data = append(event.Data, '\n')
		}
	}
	d.done = true
	d.err = d.scanner.Err()
	// Match the previous decoder: only a blank line commits an SSE frame.
	return false
}

func (d *responsesSSEDecoder) Event() responsesSSEFrame { return d.event }
func (d *responsesSSEDecoder) Err() error               { return d.err }
func (d *responsesSSEDecoder) Close() error             { return d.body.Close() }

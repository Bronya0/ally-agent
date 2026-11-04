// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"fmt"
	"strings"

	"ally-dev/internal/blackboard"
	openai "github.com/sashabaranov/go-openai"
)

type runBlackboardContextKey struct{}
type blackboardReservationContextKey struct{}
type blackboardParticipantContextKey struct{}

type blackboardParticipant struct {
	board *blackboard.Blackboard
	name  string
}

func newRunBlackboard(ctx context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	board := blackboard.New()
	ctx = context.WithValue(ctx, runBlackboardContextKey{}, board)
	return ctx, func() {
		// executeStagedToolBatch drains its workers; executeDelegate and each
		// child's own tool batches are synchronous. runChat reaches this cleanup
		// only after ALL child executions return, including on cancellation.
		cancel()
		board.Close()
	}
}

func runBlackboard(ctx context.Context) *blackboard.Blackboard {
	board, _ := ctx.Value(runBlackboardContextKey{}).(*blackboard.Blackboard)
	return board
}

func currentBlackboardParticipant(ctx context.Context) (blackboardParticipant, bool) {
	participant, ok := ctx.Value(blackboardParticipantContextKey{}).(blackboardParticipant)
	return participant, ok && participant.board != nil && participant.board == runBlackboard(ctx)
}

// Reserve the whole batch before concurrency/step-stage gates, so agents know
// queued peers' identities as well as running peers. This does not start a child,
// change its arguments or bypass the normal argument/availability checks.
func (a *App) reserveBlackboardDelegates(ctx context.Context, cfg ConfigState, calls []openai.ToolCall, conflicts map[int]error) (map[int]string, map[int]error) {
	board := runBlackboard(ctx)
	if board == nil {
		return nil, conflicts
	}
	for _, call := range calls {
		if _, err := toolCallStage(call); err != nil {
			return nil, conflicts // The existing stage gate rejects this entire batch.
		}
	}
	var names map[int]string
	for i, call := range calls {
		name := normalizeToolName(call.Function.Name)
		if (name != "subagent" && name != "agent_delegate") || conflicts[i] != nil || builtinToolDisabled(ctx, cfg, name) {
			continue
		}
		var req AgentDelegateRequest
		if err, _ := decodeToolArguments(name, []byte(call.Function.Arguments), &req); err != nil || strings.TrimSpace(req.Task) == "" {
			continue
		}
		identity, err := board.Register(req.Name)
		if err != nil {
			if conflicts == nil {
				conflicts = make(map[int]error)
			}
			conflicts[i] = codedToolError("E_BAD_ARGS", err)
			continue
		}
		if names == nil {
			names = make(map[int]string)
		}
		names[i] = identity
	}
	return names, conflicts
}

func withBlackboardReservation(ctx context.Context, name string) context.Context {
	if name == "" {
		return ctx
	}
	return context.WithValue(ctx, blackboardReservationContextKey{}, name)
}

func startBlackboardDelegate(ctx context.Context, name string) (context.Context, string, error) {
	board := runBlackboard(ctx)
	if board == nil {
		return ctx, name, nil // Scheduled/direct delegates do not share a chat board.
	}
	identity, _ := ctx.Value(blackboardReservationContextKey{}).(string)
	if identity == "" {
		var err error
		identity, err = board.Register(name)
		if err != nil {
			return ctx, "", err
		}
	}
	ctx = context.WithValue(ctx, blackboardReservationContextKey{}, "")
	ctx = context.WithValue(ctx, blackboardParticipantContextKey{}, blackboardParticipant{board: board, name: identity})
	return ctx, identity, nil
}

func blackboardIdentityMessage(ctx context.Context) string {
	participant, ok := currentBlackboardParticipant(ctx)
	if !ok {
		return ""
	}
	var peers []string
	for _, name := range participant.board.Names() {
		if name != participant.name {
			peers = append(peers, name)
		}
	}
	return fmt.Sprintf("## Blackboard Identity\nYour name in this run is %s (case-sensitive). Registered peers, which may be queued or finished: %s. Other named peers may join later in this run. Use these names or * as message destinations; never set from or seq.", participant.name, strings.Join(peers, ", "))
}

// Only the request copy carries delivery data. Neither tool-result history nor
// the system prompt is rewritten, and an empty suffix changes no request bytes.
func blackboardRequestMessages(messages []openai.ChatCompletionMessage, incoming []blackboard.Message) []openai.ChatCompletionMessage {
	if len(incoming) == 0 {
		return messages
	}
	var text strings.Builder
	text.WriteString("[Agent Blackboard Messages]\n\n")
	for _, message := range incoming {
		fmt.Fprintf(&text, "[%d] %s -> %s (%s", message.Seq, message.From, message.To, message.Type)
		if message.ReplyTo != nil {
			fmt.Fprintf(&text, ", reply_to=%d", *message.ReplyTo)
		}
		text.WriteString(")\n")
		text.WriteString(message.Body)
		text.WriteString("\n\n")
	}
	text.WriteString("[End Blackboard Messages]")
	request := append(make([]openai.ChatCompletionMessage, 0, len(messages)+1), messages...)
	// Prefer the most recent tool-result chain, even if an image or budget
	// notice follows it. Never attach to a tool result from an older model turn.
	for i := len(request) - 1; i >= 0; i-- {
		if request[i].Role == openai.ChatMessageRoleAssistant {
			break
		}
		if request[i].Role == openai.ChatMessageRoleTool && request[i].ToolCallID != "" {
			request[i].Content += "\n\n" + text.String()
			return request
		}
	}
	return append(request, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: text.String()})
}

// All child model calls (normal turns, retries and forced reports) use this
// wrapper. Acknowledgment waits for a valid nonempty turn, not merely an HTTP
// connection or a partial stream, so failed deliveries remain available.
func (a *App) streamSubagentModelResponse(ctx context.Context, cfg ConfigState, model string, messages []openai.ChatCompletionMessage, tools []openai.Tool, onEvent func(modelStreamEvent)) (*modelStreamResult, error) {
	participant, connected := currentBlackboardParticipant(ctx)
	request := messages
	var scanned int64
	if connected {
		var incoming []blackboard.Message
		incoming, scanned = participant.board.Read(participant.name)
		request = blackboardRequestMessages(messages, incoming)
	}
	response, err := a.streamModelResponse(ctx, cfg, model, request, tools, onEvent)
	if connected && err == nil && modelResponseStopError(cfg, response) == nil && emptyModelResponseError(response) == nil {
		participant.board.Ack(participant.name, scanned)
	}
	return response, err
}

func interceptSubagentBlackboard(ctx context.Context, content string) string {
	participant, connected := currentBlackboardParticipant(ctx)
	if !connected {
		return content
	}
	return blackboard.Intercept(content, func(message blackboard.Message) bool {
		return participant.board.Publish(participant.name, message)
	})
}

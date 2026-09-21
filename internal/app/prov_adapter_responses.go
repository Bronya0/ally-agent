// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"ally-dev/internal/tools/schemautil"
	"ally-dev/internal/tools/toolcall"

	oa "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/ssestream"
	oaresp "github.com/openai/openai-go/v3/responses"
	legacyopenai "github.com/sashabaranov/go-openai"
)

func (a *App) streamOpenAIResponses(ctx context.Context, cfg ConfigState, model string, messages []legacyopenai.ChatCompletionMessage, tools []legacyopenai.Tool, onEvent func(modelStreamEvent)) (*modelStreamResult, error) {
	// Replay the reasoning items captured from every tool-loop turn of this
	// session: OpenAI requires them around function calls, and under
	// store=false the encrypted_content is the only carrier. Each turn's items
	// are replayed right before that turn's own function_call outputs — on
	// every request, not just the trailing turn's — so the model resumes its
	// reasoning instead of starting over (quality) and the input prefix stays
	// byte-identical across agent steps (prompt cache).
	replayKey := reasoningReplayKey(cfg, model)
	replay := a.reasoningStash.get(replayKey)
	body := buildOpenAIResponsesRequest(cfg, model, messages, tools, replay)
	// Ask for encrypted reasoning so stateless replay stays possible.
	body.Include = append(body.Include, oaresp.ResponseIncludableReasoningEncryptedContent)

	maxRetries := effectiveLLMRetries(cfg)
	result, emitted, err := a.openAIResponsesStreamAttempt(ctx, cfg, body, onEvent)
	// 只重试"尚未产出任何输出"的失败:建流失败,或消费阶段在产出内容前
	// 失败(流内 error/response.failed 事件等)。此时重试无重复输出风险;
	// 已产出内容的中断交给上层 runChat 做整轮重试。
	for attempt := 1; err != nil && !emitted && ctx.Err() == nil && attempt <= maxRetries && shouldRetryLLMError(err); attempt++ {
		wait := llmRetryDelay(attempt)
		emitLLMRetryEvent(onEvent, attempt, maxRetries, err, wait)
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		result, emitted, err = a.openAIResponsesStreamAttempt(ctx, cfg, body, onEvent)
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

// openAIResponsesStreamAttempt 执行一次完整的 Responses 建流与消费。第二个
// 返回值报告本次尝试是否已产出输出(文本/推理/工具调用/图片任一非空),
// 调用方据此判定适配器内重试是否安全(无输出则重试不会造成重复)。
func (a *App) openAIResponsesStreamAttempt(ctx context.Context, cfg ConfigState, body oaresp.ResponseNewParams, onEvent func(modelStreamEvent)) (*modelStreamResult, bool, error) {
	stream, err := newOpenAIResponsesSSEStream(ctx, cfg, body)
	if err != nil {
		return nil, false, err
	}
	defer stream.Close()

	var assistant strings.Builder
	var reasoning strings.Builder
	// reasoningItems captures completed reasoning items (with encrypted
	// content) for replay on the next request of this tool loop.
	reasoningItems := []responsesReasoningItem{}
	// toolItemIDs maps the call id of every function call of this response to the
	// item id (fc_…) the model emitted for it, for the pair-preserving replay of
	// the captured reasoning items (see responseInputItemParamOfFunctionCall).
	toolItemIDs := map[string]string{}
	toolCalls := []legacyopenai.ToolCall{}
	toolIndexByOutput := map[int64]int{}
	toolIndexByItemID := map[string]int{}
	toolEventGate := newModelToolCallEventGate(func(event modelStreamEvent) {
		emitModelStreamEvent(onEvent, event)
	})
	var usage *modelUsage
	finalOutputText := ""
	var streamErr error
	gotTerminalEvent := false
	// stopReason is the Responses equivalent of a finish_reason: it is derived
	// from the terminal event so the run reports truncation / filtering the same
	// way the Chat and Anthropic adapters do (see modelResponseStopError).
	stopReason := ""
	images := []modelImage{}
	imageIndexes := map[string]int{}
	emitImage := func(id, b64 string, partial bool) {
		id = strings.TrimSpace(id)
		b64 = strings.TrimSpace(b64)
		if id == "" || b64 == "" {
			return
		}
		img := modelImage{ID: id, DataURL: "data:image/png;base64," + b64, MimeType: "image/png", Partial: partial}
		if idx, ok := imageIndexes[id]; ok {
			images[idx] = img
		} else {
			imageIndexes[id] = len(images)
			images = append(images, img)
		}
		emitModelStreamEvent(onEvent, modelStreamEvent{Image: &img})
	}
	hasOutput := func() bool {
		return assistant.Len() > 0 || reasoning.Len() > 0 || len(toolCalls) > 0 || len(images) > 0
	}

	for stream.Next() {
		event, rawEvent, err := stream.Event()
		if err != nil {
			if isIncompleteStreamJSON(err) && len(toolCalls) == 0 && (assistant.Len() > 0 || reasoning.Len() > 0) {
				break
			}
			return nil, hasOutput(), wrapLLMStreamEventDecode(fmt.Errorf("decode responses stream event: %w", err))
		}
		switch event.Type {
		case "response.output_text.delta":
			ev := event.AsResponseOutputTextDelta()
			if ev.Delta != "" {
				assistant.WriteString(ev.Delta)
				emitModelStreamEvent(onEvent, modelStreamEvent{ContentDelta: ev.Delta})
			}
		case "response.reasoning_summary_text.delta":
			ev := event.AsResponseReasoningSummaryTextDelta()
			if ev.Delta != "" {
				reasoning.WriteString(ev.Delta)
				emitModelStreamEvent(onEvent, modelStreamEvent{ReasoningDelta: ev.Delta})
			}
		case "response.reasoning_text.delta":
			// Raw reasoning text, streamed when the request asks for it instead
			// of (or in addition to) the summarized form (pi
			// openai-responses-shared.ts:623-632 handles both).
			ev := event.AsResponseReasoningTextDelta()
			if ev.Delta != "" {
				reasoning.WriteString(ev.Delta)
				emitModelStreamEvent(onEvent, modelStreamEvent{ReasoningDelta: ev.Delta})
			}
		case "response.reasoning_summary_part.done":
			// Summary parts are separate paragraphs: pi closes each one with a
			// blank line (openai-responses-shared.ts:613-622) instead of letting
			// consecutive parts run together in the thinking panel. A separator is
			// only written when there is something to separate, so an empty part
			// cannot turn into the response's only "output".
			if reasoning.Len() > 0 {
				reasoning.WriteString("\n\n")
				emitModelStreamEvent(onEvent, modelStreamEvent{ReasoningDelta: "\n\n"})
			}
		case "response.refusal.delta":
			// A refusal is user-visible answer text: dropping it left the panel
			// empty even though the model answered.
			ev := event.AsResponseRefusalDelta()
			if ev.Delta != "" {
				assistant.WriteString(ev.Delta)
				emitModelStreamEvent(onEvent, modelStreamEvent{ContentDelta: ev.Delta})
			}
		case "response.output_item.added":
			ev := event.AsResponseOutputItemAdded()
			if ev.Item.Type == "function_call" {
				idx := ensureResponsesToolCall(&toolCalls, toolIndexByOutput, toolIndexByItemID, ev.OutputIndex, ev.Item.ID)
				updateToolCallFromResponsesItem(&toolCalls[idx], ev.Item)
				captureResponsesToolItemID(toolItemIDs, toolCalls[idx], ev.Item)
				toolEventGate.emit(modelStreamEvent{ToolCalls: toolCalls})
			}
		case "response.function_call_arguments.delta":
			ev := event.AsResponseFunctionCallArgumentsDelta()
			idx := ensureResponsesToolCall(&toolCalls, toolIndexByOutput, toolIndexByItemID, ev.OutputIndex, ev.ItemID)
			toolCalls[idx].Function.Arguments += ev.Delta
			toolEventGate.emit(modelStreamEvent{ToolCalls: toolCalls})
		case "response.function_call_arguments.done":
			ev := event.AsResponseFunctionCallArgumentsDone()
			idx := ensureResponsesToolCall(&toolCalls, toolIndexByOutput, toolIndexByItemID, ev.OutputIndex, ev.ItemID)
			// The done event carries the authoritative full arguments, but a
			// non-compliant relay may omit the field: an empty overwrite would
			// wipe the accumulated deltas and the tool would silently run with
			// "{}". Keep the accumulated value instead (kimi requires the field
			// outright, openai-responses.ts:973-980; pi falls back to the
			// terminal item, openai-responses-shared.ts:711). A non-empty final
			// value always wins, exactly like pi's overwrite.
			if ev.Arguments != "" {
				toolCalls[idx].Function.Arguments = ev.Arguments
			}
			toolEventGate.emit(modelStreamEvent{ToolCalls: toolCalls})
		case "response.output_item.done":
			ev := event.AsResponseOutputItemDone()
			if ev.Item.Type == "function_call" {
				idx := ensureResponsesToolCall(&toolCalls, toolIndexByOutput, toolIndexByItemID, ev.OutputIndex, ev.Item.ID)
				updateToolCallFromResponsesItem(&toolCalls[idx], ev.Item)
				captureResponsesToolItemID(toolItemIDs, toolCalls[idx], ev.Item)
				toolEventGate.emit(modelStreamEvent{ToolCalls: toolCalls})
			} else if ev.Item.Type == "reasoning" {
				// Capture the completed reasoning item (its encrypted_content is
				// only complete at output_item.done) for stateless replay on the
				// next request of this tool loop. Every documented field is kept so
				// the next request can send back a copy of the item the API
				// produced.
				if captured, ok := captureResponsesReasoningItem(ev.Item.AsReasoning()); ok {
					reasoningItems = append(reasoningItems, captured)
				}
			} else if ev.Item.Type == "image_generation_call" {
				imageCall := ev.Item.AsImageGenerationCall()
				emitImage(imageCall.ID, imageCall.Result, false)
			}
		case "response.image_generation_call.partial_image":
			ev := event.AsResponseImageGenerationCallPartialImage()
			emitImage(ev.ItemID, ev.PartialImageB64, true)
		case "response.completed":
			gotTerminalEvent = true
			stopReason = "stop"
			ev := event.AsResponseCompleted()
			usage = modelUsageFromResponses(ev.Response.Usage)
			// The openai-go Responses union decoder currently drops the nested
			// response object when decoding a stream event. Read usage from the
			// original event JSON so cached input tokens are not lost.
			if rawUsage := modelUsageFromResponsesEvent(rawEvent); rawUsage != nil {
				usage = rawUsage
			}
			finalOutputText = ev.Response.OutputText()
			// Some compatible Responses gateways omit output_text.delta and only
			// include the final text in response.completed. Forward the missing
			// suffix so the frontend does not end on a blank assistant message.
			if finalOutputText != "" {
				seen := assistant.String()
				missing := ""
				if seen == "" {
					missing = finalOutputText
				} else if strings.HasPrefix(finalOutputText, seen) {
					missing = finalOutputText[len(seen):]
				}
				if missing != "" {
					assistant.WriteString(missing)
					emitModelStreamEvent(onEvent, modelStreamEvent{ContentDelta: missing})
				}
			}
			for _, item := range ev.Response.Output {
				if item.Type == "image_generation_call" {
					imageCall := item.AsImageGenerationCall()
					emitImage(imageCall.ID, imageCall.Result, false)
				}
			}
		case "error":
			ev := event.AsError()
			// code/message/param formatting matches kimi-code
			// (openai-responses.ts:228-236, 1004-1012) so the text keeps the
			// markers ("insufficient_quota", "429", "rate_limit") the retry and
			// key classifiers match on, instead of degrading to ": message".
			streamErr = errors.New("OpenAI Responses stream error: " + responsesErrorText(ev.Code, ev.Message, ev.Param))
		case "response.failed":
			ev := event.AsResponseFailed()
			code := string(ev.Response.Error.Code)
			message := ev.Response.Error.Message
			if strings.TrimSpace(code) == "" && strings.TrimSpace(message) == "" {
				// No error object: fall back to incomplete_details.reason the way
				// kimi (openai-responses.ts:323-350) and pi
				// (openai-responses.ts:742-752) do.
				if reason := ev.Response.IncompleteDetails.Reason; reason != "" {
					code = "response.incomplete"
					message = string(reason)
				}
			}
			streamErr = errors.New("OpenAI Responses response.failed: " + responsesErrorText(code, message, ""))
		case "response.incomplete":
			gotTerminalEvent = true
			ev := event.AsResponseIncomplete()
			usage = modelUsageFromResponses(ev.Response.Usage)
			if rawUsage := modelUsageFromResponsesEvent(rawEvent); rawUsage != nil {
				usage = rawUsage
			}
			finalOutputText = ev.Response.OutputText()
			if finalOutputText != "" {
				seen := assistant.String()
				missing := ""
				if seen == "" {
					missing = finalOutputText
				} else if strings.HasPrefix(finalOutputText, seen) {
					missing = finalOutputText[len(seen):]
				}
				if missing != "" {
					assistant.WriteString(missing)
					emitModelStreamEvent(onEvent, modelStreamEvent{ContentDelta: missing})
				}
			}
			for _, item := range ev.Response.Output {
				if item.Type == "image_generation_call" {
					imageCall := item.AsImageGenerationCall()
					emitImage(imageCall.ID, imageCall.Result, false)
				}
			}
			reason := ev.Response.IncompleteDetails.Reason
			// "max_output_tokens" is the Responses API equivalent of
			// finish_reason: "length". It is a normal terminal state of the
			// response rather than a broken stream, so it is reported through
			// StopReason — modelResponseStopError turns that into the same
			// actionable Max Tokens message the other two adapters produce —
			// instead of as a stream error that would be retried.
			switch reason {
			case "":
			case "max_output_tokens":
				stopReason = "length"
			case "content_filter":
				stopReason = "content_filter"
				streamErr = errors.New("OpenAI Responses was stopped by the content filter")
			default:
				streamErr = fmt.Errorf("response incomplete: %s", reason)
			}
		default:
			// Unknown-but-typed events stay ignored (kimi: "unknown future event
			// types carry no data we currently consume"). Only payloads WITHOUT a
			// type field are examined: some gateways forward their error object
			// that way ({"message":"..."}), which the SDK decodes with an empty
			// Type — dropping it silently ended runs with a misleading "stream
			// ended without terminal event". Kimi surfaces these and unpacks the
			// nested gateway JSON (openai-responses.ts:884-892, 270-321).
			if event.Type == "" {
				if msg := untypedResponsesErrorMessage(rawEvent); msg != "" {
					streamErr = errors.New(msg)
				}
			}
		}
		if streamErr != nil {
			break
		}
	}
	if err := stream.Err(); err != nil {
		return nil, hasOutput(), err
	}
	if streamErr != nil {
		return nil, hasOutput(), streamErr
	}
	if !gotTerminalEvent {
		return nil, hasOutput(), errors.New("stream ended without terminal event")
	}
	// Record this response's reasoning items as one turn of the session
	// ledger, keyed by the turn's tool-call ids: every later request replays
	// them before that turn's own function_call outputs. Done here (not in
	// streamOpenAIResponses) so adapter-internal retries rewrite it per
	// attempt. A response without reasoning items records no turn; earlier
	// turns keep theirs.
	if len(reasoningItems) > 0 && len(toolCalls) > 0 {
		a.reasoningStash.appendTurn(reasoningReplayKey(cfg, string(body.Model)), reasoningTurn{
			callIDs:        toolCallIDsOf(toolCalls),
			responses:      reasoningItems,
			responsesItems: toolItemIDs,
		})
	}
	content := assistant.String()
	if content == "" && finalOutputText != "" {
		content = finalOutputText
	}
	return &modelStreamResult{
		Content:          content,
		Reasoning:        reasoning.String(),
		ReasoningPresent: len(reasoningItems) > 0,
		ToolCalls:        normalizeToolCalls(toolCalls),
		Images:           images,
		Usage:            usage,
		StopReason:       stopReason,
	}, hasOutput(), nil
}

// responsesErrorText formats a Responses error the way kimi-code does
// (openai-responses.ts:228-236): `code: message (param: X)` with placeholders
// for missing fields, so the text never degrades to ": msg" and always keeps
// the code markers the retry/key classifiers match on.
func responsesErrorText(code, message, param string) string {
	code = strings.TrimSpace(code)
	message = strings.TrimSpace(message)
	if code == "" {
		code = "unknown"
	}
	if message == "" {
		message = "no message"
	}
	if p := strings.TrimSpace(param); p != "" {
		return fmt.Sprintf("%s: %s (param: %s)", code, message, p)
	}
	return fmt.Sprintf("%s: %s", code, message)
}

// untypedResponsesErrorMessage extracts an error from a Responses stream
// payload that carries no "type" field (some gateways forward their error
// object that way). A nested gateway error embedded as JSON after the literal
// "received error while streaming:" marker is unpacked first so its code
// (numeric codes included) reaches the retry classifiers. Returns "" when the
// payload is not an error shape.
func untypedResponsesErrorMessage(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var payload struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	if payload.Type != "" || strings.TrimSpace(payload.Message) == "" {
		return ""
	}
	code, message, param := "", payload.Message, ""
	if c, m, p, ok := parseNestedGatewayStreamError(payload.Message); ok {
		code, message, param = c, m, p
	}
	return "OpenAI Responses stream error: " + responsesErrorText(code, message, param)
}

// parseNestedGatewayStreamError unpacks the JSON object some gateways append
// after the literal "received error while streaming:" marker (kimi-code
// openai-responses.ts:270-302). Numeric codes are stringified so markers like
// "429" survive. ok is false when the remainder is not a JSON object carrying
// a message.
func parseNestedGatewayStreamError(message string) (code, msg, param string, ok bool) {
	const marker = "received error while streaming:"
	idx := strings.Index(message, marker)
	if idx < 0 {
		return "", "", "", false
	}
	var nested struct {
		Code    any    `json:"code"`
		Message string `json:"message"`
		Param   string `json:"param"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(message[idx+len(marker):])), &nested); err != nil {
		return "", "", "", false
	}
	if strings.TrimSpace(nested.Message) == "" {
		return "", "", "", false
	}
	switch v := nested.Code.(type) {
	case string:
		code = v
	case float64:
		code = fmt.Sprintf("%v", v)
	}
	return code, nested.Message, nested.Param, true
}

// openAIResponsesPromptCacheKey keeps cache routing session-local without
// exposing the caller-provided session ID to the provider.
func openAIResponsesPromptCacheKey(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(sessionID))
	return fmt.Sprintf("ally:%x", sum[:16])
}

func buildOpenAIResponsesRequest(cfg ConfigState, model string, messages []legacyopenai.ChatCompletionMessage, tools []legacyopenai.Tool, replay *sessionReasoningPayload) oaresp.ResponseNewParams {
	instructions, inputItems := buildOpenAIResponsesInput(messages, replay)
	cacheKey := strings.TrimSpace(cfg.responsesPromptCacheKey)
	body := oaresp.ResponseNewParams{
		Model:           oaresp.ResponsesModel(model),
		Input:           oaresp.ResponseNewParamsInputUnion{OfInputItemList: inputItems},
		MaxOutputTokens: oa.Int(int64(clampResponsesMaxOutputTokens(cfg.MaxTokens))),
	}
	// Prompt caching runs in the provider's implicit mode: it places the
	// breakpoint at the end of the latest eligible message — the newest user or
	// tool-response message — which is exactly the growing prefix of a tool loop,
	// so no explicit breakpoint marker is needed anywhere in the input. Explicit
	// mode is the opposite of what an agent loop wants (it *disables* the implicit
	// breakpoint, and a request without its own marker then uses no caching at
	// all), which is why pi only selects it to turn caching off. No retention
	// field is sent anywhere: the provider default stays in place.
	// Store and ParallelToolCalls are OpenAI-official fields that
	// compatible gateways may reject with 400 ("unsupported field").
	// Gate them behind the official-endpoint check so relays stay happy.
	if isOfficialOpenAIEndpoint(cfg) {
		body.ParallelToolCalls = oa.Bool(true)
		body.Store = oa.Bool(false)
	}
	// Thinking strength for the Responses API (reasoning.effort): "off" arrives as
	// effort "none" (DeepSeek documents "none" as 关闭思考模式). The SDK type is
	// string-backed, so the normalized selection is sent unchanged, including
	// xhigh and max.
	reasoningWire := reasoningWireForAdapter(cfg, apiFormatOpenAIResponses, cfg.ReasoningEffort)
	if reasoningWire.Effort != "" && modelSupportsReasoningEffort(cfg, model) {
		body.Reasoning = oa.ReasoningParam{Effort: oa.ReasoningEffort(reasoningWire.Effort)}
		if reasoningWire.Effort != reasoningEffortOffWireValue {
			// Pair reasoning.effort with summary:"auto" — both kimi-code
			// (openai-responses.ts:1116-1120) and pi (openai-responses.ts:319-335)
			// do: without an explicit summary request the Responses API emits no
			// reasoning_summary_text deltas, leaving the thinking panel empty even
			// though the model reasoned. With thinking off there is nothing to
			// summarize, so the summary request is left out.
			body.Reasoning.Summary = oa.ReasoningSummaryAuto
		}
	}
	if strings.TrimSpace(instructions) != "" {
		body.Instructions = oa.String(instructions)
	}
	body.Tools = convertToolsToOpenAIResponses(tools)
	if len(tools) > 0 && supportsOpenAIResponsesImageGeneration(cfg) {
		body.Tools = append(body.Tools, oaresp.ToolUnionParam{
			OfImageGeneration: &oaresp.ToolImageGenerationParam{OutputFormat: "png"},
		})
	}
	if len(body.Tools) > 0 {
		body.ToolChoice = oaresp.ResponseNewParamsToolChoiceUnion{OfToolChoiceMode: oa.Opt(oaresp.ToolChoiceOptionsAuto)}
	}
	// Codex sends the stable session key on every Responses request, including
	// custom compatible endpoints. Only the explicit breakpoint/options below
	// are restricted to the official GPT-5.6 route because older gateways may
	// reject those newer fields.
	if cacheKey != "" {
		body.PromptCacheKey = oa.String(cacheKey)
	}
	return body
}

func supportsOpenAIResponsesImageGeneration(cfg ConfigState) bool {
	return isOfficialOpenAIEndpoint(cfg)
}

type openAIResponsesSSEStream struct {
	decoder ssestream.Decoder
}

// openAIResponsesMinOutputTokens is the documented floor for max_output_tokens:
// the Responses API rejects smaller values, so a small Max Tokens setting is
// raised instead of failing the request (pi: OPENAI_RESPONSES_MIN_OUTPUT_TOKENS,
// openai-responses.ts Math.max(options.maxTokens, 16)).
const openAIResponsesMinOutputTokens = 16

func clampResponsesMaxOutputTokens(maxTokens int) int {
	if maxTokens < openAIResponsesMinOutputTokens {
		return openAIResponsesMinOutputTokens
	}
	return maxTokens
}

// ensureResponsesReasoningSummaries guarantees the summary key on replayed
// reasoning items. The SDK declares summary as required but tags it omitzero, so
// an item captured without summary text would drop the key entirely — and the
// API only accepts an exact copy of the reasoning item it produced, key
// included. An empty array is a valid replay value
// (openai-go ResponseReasoningItemParam: json:"summary,omitzero" api:"required").
func ensureResponsesReasoningSummaries(body map[string]any) bool {
	input, ok := body["input"].([]any)
	if !ok {
		return false
	}
	changed := false
	for _, rawItem := range input {
		item, ok := rawItem.(map[string]any)
		if !ok || item["type"] != "reasoning" {
			continue
		}
		if _, exists := item["summary"]; exists {
			continue
		}
		item["summary"] = []any{}
		changed = true
	}
	return changed
}

func newOpenAIResponsesSSEStream(ctx context.Context, cfg ConfigState, body oaresp.ResponseNewParams) (*openAIResponsesSSEStream, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	var requestBody map[string]any
	if err := json.Unmarshal(payload, &requestBody); err != nil {
		return nil, err
	}
	requestBody["stream"] = true
	// Replayed reasoning items must carry their required summary key; the SDK
	// drops it when it is empty (see ensureResponsesReasoningSummaries).
	ensureResponsesReasoningSummaries(requestBody)
	payload, err = json.Marshal(requestBody)
	if err != nil {
		return nil, err
	}

	endpoint := strings.TrimRight(baseURLForAPIFormat(cfg), "/") + "/responses"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if apiKey := strings.TrimSpace(cfg.APIKey); apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("User-Agent", effectiveUserAgent(cfg))
	for key, value := range sessionAffinityHeaders(cfg) {
		req.Header.Set(key, value)
	}
	// 自定义头在适配器内置头之后应用，可覆盖 Authorization / User-Agent。
	applyCustomHeaders(req, cfg)

	resp, err := proxyHTTPClient(cfg, true, 0).Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxToolOutput))
		msg := strings.TrimSpace(string(raw))
		// 错误信息必须携带状态码(resp.Status 形如 "429 Too Many Requests"):
		// 重试/切换分类(shouldRetryLLMError 等)基于 "429"/"too many requests"
		// 等关键词做字符串匹配。部分中转返回体只有 "Rate exceeded." 之类的
		// 文案、不含状态码,丢弃状态码会让限流错误绕过所有重试机制直接失败。
		if msg == "" {
			msg = resp.Status
		} else {
			msg = resp.Status + ": " + msg
		}
		return nil, fmt.Errorf("responses request failed: %s", msg)
	}
	resp.Body = newIdleTimeoutReader(resp.Body, defaultStreamIdleTimeout)
	decoder := ssestream.NewDecoder(resp)
	if decoder == nil {
		resp.Body.Close()
		return nil, errors.New("responses stream was empty")
	}
	return &openAIResponsesSSEStream{decoder: decoder}, nil
}

func responseInputItemParamOfFunctionCallOutput(callID, output string) oaresp.ResponseInputItemUnionParam {
	p := oaresp.ResponseInputItemParamOfFunctionCallOutput(output)
	if p.OfFunctionCallOutput != nil && strings.TrimSpace(callID) != "" {
		p.OfFunctionCallOutput.CallID = oa.String(callID)
	}
	return p
}

func (s *openAIResponsesSSEStream) Next() bool {
	if s == nil || s.decoder == nil {
		return false
	}
	return s.decoder.Next()
}

func (s *openAIResponsesSSEStream) Event() (oaresp.ResponseStreamEventUnion, []byte, error) {
	var event oaresp.ResponseStreamEventUnion
	if s == nil || s.decoder == nil {
		return event, nil, io.EOF
	}
	raw := bytes.TrimSpace(s.decoder.Event().Data)
	if len(raw) == 0 || bytes.Equal(raw, []byte("[DONE]")) {
		return event, raw, nil
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		return event, raw, err
	}
	return event, raw, nil
}

func (s *openAIResponsesSSEStream) Err() error {
	if s == nil || s.decoder == nil {
		return nil
	}
	return s.decoder.Err()
}

func (s *openAIResponsesSSEStream) Close() error {
	if s == nil || s.decoder == nil {
		return nil
	}
	return s.decoder.Close()
}

// responsesTurnItemsComplete reports whether every tool call of the turn has
// its captured Responses item id. OpenAI pairs a replayed reasoning item with
// the id of the item that follows it: when a follower id is unknown the pair
// cannot be completed, and replaying the items would risk the documented
// "reasoning item was provided without its required following item"
// rejection — so the whole turn replays without ids, exactly like a fresh
// turn.
func responsesTurnItemsComplete(turn *reasoningTurn, calls []legacyopenai.ToolCall) bool {
	if turn == nil || len(turn.responses) == 0 {
		return false
	}
	for _, call := range calls {
		key := toolcall.ForResponsesCall(toolcall.Effective(call.ID, call.Function.Name))
		if strings.TrimSpace(turn.responsesItems[key]) == "" {
			return false
		}
	}
	return true
}

func buildOpenAIResponsesInput(messages []legacyopenai.ChatCompletionMessage, replay *sessionReasoningPayload) (string, oaresp.ResponseInputParam) {
	systemParts := []string{}
	input := oaresp.ResponseInputParam{}
	hasSeenTurn := false
	for _, m := range messages {
		role := strings.TrimSpace(m.Role)
		switch role {
		case legacyopenai.ChatMessageRoleSystem:
			text := messageText(m)
			if text == "" {
				continue
			}
			if !hasSeenTurn {
				systemParts = append(systemParts, text)
			} else {
				// Preserve mid-conversation system turns (compaction summaries,
				// steering prompts) at their chronological position using the
				// Responses API developer role.
				input = append(input, oaresp.ResponseInputItemParamOfMessage(text, oaresp.EasyInputMessageRoleDeveloper))
			}
		case legacyopenai.ChatMessageRoleUser, legacyopenai.ChatMessageRoleAssistant:
			hasSeenTurn = true
			if role == legacyopenai.ChatMessageRoleAssistant && len(m.ToolCalls) > 0 {
				if text := messageText(m); text != "" {
					input = append(input, oaresp.ResponseInputItemParamOfMessage(text, oaresp.EasyInputMessageRoleAssistant))
				}
				// The turn's captured reasoning items replay right before its
				// function_call outputs — on every request that still carries
				// the turn, so the input prefix stays byte-identical across
				// agent steps. The all-or-nothing guard keeps a reasoning item
				// from being sent without its follower item id.
				turn := replay.turnForAny(toolCallIDsOf(m.ToolCalls))
				if responsesTurnItemsComplete(turn, m.ToolCalls) {
					for _, item := range turn.responses {
						input = append(input, responsesReasoningInputItem(item))
					}
					for _, call := range m.ToolCalls {
						input = append(input, responseInputItemParamOfFunctionCall(call, turn.responsesItems))
					}
				} else {
					for _, call := range m.ToolCalls {
						input = append(input, responseInputItemParamOfFunctionCall(call, nil))
					}
				}
				continue
			}
			if len(m.MultiContent) > 0 {
				content := openAIResponsesContentFromMulti(m)
				if len(content) > 0 {
					input = append(input, oaresp.ResponseInputItemParamOfMessage(content, oaresp.EasyInputMessageRole(role)))
				}
			} else if text := strings.TrimSpace(m.Content); text != "" {
				input = append(input, oaresp.ResponseInputItemParamOfMessage(text, oaresp.EasyInputMessageRole(role)))
			}
		case legacyopenai.ChatMessageRoleTool:
			hasSeenTurn = true
			if strings.TrimSpace(m.ToolCallID) != "" {
				input = append(input, responseInputItemParamOfFunctionCallOutput(toolcall.ForResponsesCall(m.ToolCallID), m.Content))
			}
		}
	}
	return strings.Join(systemParts, "\n\n"), input
}

func responseInputItemParamOfFunctionCall(call legacyopenai.ToolCall, toolItemIDs map[string]string) oaresp.ResponseInputItemUnionParam {
	args := strings.TrimSpace(call.Function.Arguments)
	if args == "" {
		args = "{}"
	}
	callID := toolcall.ForResponsesCall(toolcall.Effective(call.ID, call.Function.Name))
	p := oaresp.ResponseInputItemParamOfFunctionCall(args, callID, call.Function.Name)
	// A replayed reasoning item is paired with its follower BY ID: sending the
	// reasoning item's id back without the id of the function_call it precedes
	// is rejected ("Item 'rs_…' of type 'reasoning' was provided without its
	// required following item"), which is why pi keeps the fc_* item id for the
	// same model (openai-responses-shared.ts:247-292). The id is only replayed
	// when it came from the current model's last response (model-scoped stash),
	// so a foreign or stale id can never be sent.
	if p.OfFunctionCall != nil {
		if id := strings.TrimSpace(toolItemIDs[callID]); id != "" && toolcall.IsSafeResponsesItemID(id) {
			p.OfFunctionCall.ID = oa.String(id)
		}
	}
	return p
}

func openAIResponsesContentFromMulti(m legacyopenai.ChatCompletionMessage) oaresp.ResponseInputMessageContentListParam {
	content := oaresp.ResponseInputMessageContentListParam{}
	hasMultiText := false
	for _, part := range m.MultiContent {
		if part.Type == legacyopenai.ChatMessagePartTypeText && strings.TrimSpace(part.Text) != "" {
			hasMultiText = true
			break
		}
	}
	if !hasMultiText && strings.TrimSpace(m.Content) != "" {
		content = append(content, oaresp.ResponseInputContentParamOfInputText(m.Content))
	}
	for _, part := range m.MultiContent {
		switch part.Type {
		case legacyopenai.ChatMessagePartTypeText:
			if strings.TrimSpace(part.Text) != "" {
				content = append(content, oaresp.ResponseInputContentParamOfInputText(part.Text))
			}
		case legacyopenai.ChatMessagePartTypeImageURL:
			if part.ImageURL != nil && validImageDataURL(part.ImageURL.URL) {
				img := oaresp.ResponseInputContentParamOfInputImage(oaresp.ResponseInputImageDetailAuto)
				if img.OfInputImage != nil {
					img.OfInputImage.ImageURL = oa.String(part.ImageURL.URL)
				}
				content = append(content, img)
			}
		}
	}
	return content
}

func convertToolsToOpenAIResponses(tools []legacyopenai.Tool) []oaresp.ToolUnionParam {
	out := make([]oaresp.ToolUnionParam, 0, len(tools))
	for _, tool := range tools {
		if tool.Function == nil {
			continue
		}
		params := schemautil.Map(tool.Function.Parameters)
		next := oaresp.ToolParamOfFunction(tool.Function.Name, params, false)
		if next.OfFunction != nil && strings.TrimSpace(tool.Function.Description) != "" {
			next.OfFunction.Description = oa.String(tool.Function.Description)
		}
		out = append(out, next)
	}
	return out
}

// ensureResponsesToolCall resolves the tool call a Responses event belongs to. The
// item id is the call's identity — a function_call item is never renamed — and the
// output index is only a locator for the events that carry no item id (an
// arguments delta without item_id). The index must therefore never win over a
// differing item id, which is what the original lookup did: a relay that reuses
// one output_index for two items had the second item merged into the first call,
// and the item table was poisoned with the second id pointing at the first call.
func ensureResponsesToolCall(toolCalls *[]legacyopenai.ToolCall, byOutput map[int64]int, byItemID map[string]int, outputIndex int64, itemID string) int {
	itemID = strings.TrimSpace(itemID)
	if itemID != "" {
		if idx, ok := byItemID[itemID]; ok {
			// Keep an alias for an index this item has not been seen on yet, but
			// never overwrite the newest start binding: an id-less event has to keep
			// resolving to the call that most recently started on that index.
			if _, bound := byOutput[outputIndex]; !bound {
				byOutput[outputIndex] = idx
			}
			return idx
		}
		// An index whose call has no item id yet belongs to this event: the relay
		// opened the call without one and names it now. An index whose call already
		// has an item id is a different item reusing that index.
		if idx, ok := byOutput[outputIndex]; ok && !responsesSlotHasItemID(byItemID, idx) {
			byItemID[itemID] = idx
			return idx
		}
	} else if idx, ok := byOutput[outputIndex]; ok {
		return idx
	}
	idx := len(*toolCalls)
	*toolCalls = append(*toolCalls, legacyopenai.ToolCall{Type: legacyopenai.ToolTypeFunction})
	byOutput[outputIndex] = idx
	if itemID != "" {
		byItemID[itemID] = idx
	}
	return idx
}

// responsesSlotHasItemID reports whether an already resolved call was named by an
// item id. The item table holds one entry per call in the response, so a scan
// keeps the caller from maintaining a third lookup table.
func responsesSlotHasItemID(byItemID map[string]int, slot int) bool {
	for _, mapped := range byItemID {
		if mapped == slot {
			return true
		}
	}
	return false
}

// captureResponsesToolItemID records the item id (fc_…) the model assigned to a
// function call, keyed by the call id its result is replayed with. A replayed
// reasoning item must be paired with the id of the item that follows it, so the
// next request needs this id (see responseInputItemParamOfFunctionCall).
func captureResponsesToolItemID(ids map[string]string, call legacyopenai.ToolCall, item oaresp.ResponseOutputItemUnion) {
	if ids == nil || item.ID == "" || item.CallID == "" {
		return
	}
	// Only ids that can be echoed back verbatim are kept
	// (toolcall.IsSafeResponsesItemID), and the key is the same sanitized call id the
	// replay path looks up, so a compound or over-long call id still pairs with
	// its item id.
	if !toolcall.IsSafeResponsesItemID(item.ID) {
		return
	}
	ids[toolcall.ForResponsesCall(toolcall.Effective(call.ID, call.Function.Name))] = item.ID
}

func updateToolCallFromResponsesItem(call *legacyopenai.ToolCall, item oaresp.ResponseOutputItemUnion) {
	if item.CallID != "" {
		call.ID = item.CallID
	} else if call.ID == "" {
		call.ID = item.ID
	}
	call.Type = legacyopenai.ToolTypeFunction
	if item.Name != "" {
		call.Function.Name = item.Name
	}
	if args := strings.TrimSpace(item.Arguments.OfString); args != "" {
		call.Function.Arguments = args
	}
}

// modelUsageFromResponses maps the Responses usage counters. input_tokens is
// inclusive of the cached and cache-write subsets (pi: openai-responses-shared
// derives input = input_tokens - cached - cache_write), so the uncached share —
// the denominator of the cache hit rate — is derived from input - cached instead
// of being read from cache_write_tokens, which would under-count the miss side
// and inflate the hit rate.
func modelUsageFromResponses(usage oaresp.ResponseUsage) *modelUsage {
	return modelUsageFromResponseTokenCounts(
		usage.InputTokens,
		usage.OutputTokens,
		usage.InputTokensDetails.CachedTokens,
		0,
		usage.InputTokensDetails.CacheWriteTokens,
	)
}

func modelUsageFromResponseTokenCounts(input, output, hit, miss, write int64) *modelUsage {
	if input <= 0 && output <= 0 && hit <= 0 && miss <= 0 {
		return nil
	}
	if input < 0 {
		input = 0
	}
	if output < 0 {
		output = 0
	}
	if hit < 0 {
		hit = 0
	}
	if miss < 0 {
		miss = 0
	}
	if write < 0 {
		write = 0
	}
	if input <= 0 {
		input = hit + miss
	}
	if miss <= 0 && input > hit {
		miss = input - hit
	}
	return &modelUsage{
		PromptTokens:     int(input),
		CompletionTokens: int(output),
		CacheHitTokens:   int(hit),
		CacheMissTokens:  int(miss),
		CacheWriteTokens: int(write),
	}
}

type responsesUsageTokenDetails struct {
	CachedTokens     int64 `json:"cached_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
}

type responsesUsageWire struct {
	InputTokens           int64                       `json:"input_tokens"`
	PromptTokens          int64                       `json:"prompt_tokens"`
	OutputTokens          int64                       `json:"output_tokens"`
	CompletionTokens      int64                       `json:"completion_tokens"`
	InputTokensDetails    *responsesUsageTokenDetails `json:"input_tokens_details"`
	PromptTokensDetails   *responsesUsageTokenDetails `json:"prompt_tokens_details"`
	CachedInputTokens     int64                       `json:"cached_input_tokens"`
	CacheReadInputTokens  int64                       `json:"cache_read_input_tokens"`
	CacheWriteInputTokens int64                       `json:"cache_write_input_tokens"`
	PromptCacheHitTokens  int64                       `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens int64                       `json:"prompt_cache_miss_tokens"`
	CachedTokens          int64                       `json:"cached_tokens"`
}

// modelUsageFromResponsesEvent parses usage from a Responses JSON payload:
// either the response.completed event carrying a nested response object, or a
// bare usage object. It intentionally uses small local structs instead of the
// SDK's response union because that union currently loses the nested response
// object during stream-event decoding. The wire struct carries fallback field
// names so OpenAI-Responses-compatible relays (cached/cache_read/prompt_cache_*)
// are all recognized.
func modelUsageFromResponsesEvent(raw []byte) *modelUsage {
	var payload struct {
		Response *struct {
			Usage *responsesUsageWire `json:"usage"`
		} `json:"response"`
		Usage *responsesUsageWire `json:"usage"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &payload) != nil {
		return nil
	}
	usage := payload.Usage
	if payload.Response != nil && payload.Response.Usage != nil {
		usage = payload.Response.Usage
	}
	if usage == nil {
		return nil
	}
	input := usage.InputTokens
	if input <= 0 {
		input = usage.PromptTokens
	}
	output := usage.OutputTokens
	if output <= 0 {
		output = usage.CompletionTokens
	}
	hit := int64(0)
	if usage.InputTokensDetails != nil {
		hit = usage.InputTokensDetails.CachedTokens
	}
	if hit <= 0 && usage.PromptTokensDetails != nil {
		hit = usage.PromptTokensDetails.CachedTokens
	}
	if hit <= 0 {
		for _, candidate := range []int64{
			usage.CachedInputTokens,
			usage.CacheReadInputTokens,
			usage.PromptCacheHitTokens,
			usage.CachedTokens,
		} {
			if candidate > 0 {
				hit = candidate
				break
			}
		}
	}
	// The cache-write counter is recorded as an observation on top of the derived
	// miss (see modelUsage.CacheWriteTokens), read with the same field-name
	// fallbacks as the hit side because compatible relays spell it differently.
	write := int64(0)
	if usage.InputTokensDetails != nil {
		write = usage.InputTokensDetails.CacheWriteTokens
	}
	if write <= 0 && usage.PromptTokensDetails != nil {
		write = usage.PromptTokensDetails.CacheWriteTokens
	}
	if write <= 0 {
		write = usage.CacheWriteInputTokens
	}
	miss := usage.PromptCacheMissTokens
	if input <= 0 && miss <= 0 {
		if usage.InputTokensDetails != nil {
			miss = usage.InputTokensDetails.CacheWriteTokens
		}
		if miss <= 0 && usage.PromptTokensDetails != nil {
			miss = usage.PromptTokensDetails.CacheWriteTokens
		}
		if miss <= 0 {
			miss = usage.CacheWriteInputTokens
		}
	}
	return modelUsageFromResponseTokenCounts(input, output, hit, miss, write)
}

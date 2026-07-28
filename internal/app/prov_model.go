package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	oa "github.com/openai/openai-go"
	"github.com/openai/openai-go/packages/ssestream"
	oaresp "github.com/openai/openai-go/responses"
	legacyopenai "github.com/sashabaranov/go-openai"
)

type modelUsage struct {
	PromptTokens     int
	CompletionTokens int
}

type modelStreamEvent struct {
	ContentDelta   string
	ReasoningDelta string
	ToolCalls      []legacyopenai.ToolCall
	Image          *modelImage
	Retry          *modelRetryInfo
}

type modelImage struct {
	ID       string
	DataURL  string
	MimeType string
	Partial  bool
}

// modelRetryInfo 描述一次 LLM 请求重试,前端据此显示重试状态。
type modelRetryInfo struct {
	Attempt     int    // 第几次重试,从 1 开始
	MaxAttempts int    // 最大重试次数(不含首次)
	Error       string // 触发重试的错误信息
	WaitMS      int    // 重试前等待毫秒数
}

// shouldRetryLLMError 判断错误是否值得重试(429/5xx/瞬时网络错误)。
// context.Canceled / DeadlineExceeded 不重试。
func shouldRetryLLMError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "429") || strings.Contains(msg, "too many requests") || strings.Contains(msg, "rate limit") {
		return true
	}
	if strings.Contains(msg, "status code: 5") || strings.Contains(msg, "500 ") || strings.Contains(msg, "502 ") || strings.Contains(msg, "503 ") || strings.Contains(msg, "504 ") ||
		strings.Contains(msg, "bad gateway") || strings.Contains(msg, "service unavailable") || strings.Contains(msg, "gateway timeout") || strings.Contains(msg, "internal server error") {
		return true
	}
	if strings.Contains(msg, "timeout") || strings.Contains(msg, "timed out") ||
		strings.Contains(msg, "eof") || strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "broken pipe") || strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "connection refused") || strings.Contains(msg, "temporarily unavailable") {
		return true
	}
	return false
}

// llmRetryDelay 返回第 attempt 次重试(从 1 开始)前的退避时间。
// 500ms / 1s / 2s / 4s...,上限 10s。
func llmRetryDelay(attempt int) time.Duration {
	d := time.Duration(1<<uint(attempt-1)) * 500 * time.Millisecond
	if d > 10*time.Second {
		d = 10 * time.Second
	}
	return d
}

// emitLLMRetryEvent 通过 onEvent 通知调用方发生了一次重试。
func emitLLMRetryEvent(onEvent func(modelStreamEvent), attempt, maxAttempts int, err error, wait time.Duration) {
	if onEvent == nil || err == nil {
		return
	}
	emitModelStreamEvent(onEvent, modelStreamEvent{Retry: &modelRetryInfo{
		Attempt:     attempt,
		MaxAttempts: maxAttempts,
		Error:       err.Error(),
		WaitMS:      int(wait.Milliseconds()),
	}})
}

// effectiveLLMRetries 返回有效的最大重试次数。
func effectiveLLMRetries(cfg ConfigState) int {
	if cfg.LLMRetries > 0 {
		return cfg.LLMRetries
	}
	return defaultLLMRetries
}

type modelStreamResult struct {
	Content      string
	Reasoning    string
	ToolCalls    []legacyopenai.ToolCall
	Images       []modelImage
	Usage        *modelUsage
	StopReason   string
	StopSequence string
}

func (a *App) completeModelText(ctx context.Context, cfg ConfigState, model string, messages []legacyopenai.ChatCompletionMessage, maxTokens int) (string, error) {
	next := cfg
	next.MaxTokens = maxTokens
	result, err := a.streamModelResponse(ctx, next, model, messages, nil, nil)
	if err != nil {
		return "", err
	}
	if err := modelResponseStopError(next, result); err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Content), nil
}

func (a *App) streamModelResponse(ctx context.Context, cfg ConfigState, model string, messages []legacyopenai.ChatCompletionMessage, tools []legacyopenai.Tool, onEvent func(modelStreamEvent)) (*modelStreamResult, error) {
	cfg.APIFormat = normalizeAPIFormat(cfg.APIFormat)
	if strings.TrimSpace(model) == "" {
		model = cfg.Model
	}
	if strings.TrimSpace(model) == "" {
		return nil, errors.New("model is required")
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("API key is required")
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = defaultMaxTokensForAPIFormat(cfg.APIFormat)
	}
	switch cfg.APIFormat {
	case apiFormatOpenAIResponses:
		return a.streamOpenAIResponses(ctx, cfg, model, messages, tools, onEvent)
	case apiFormatAnthropicMessages:
		return a.streamAnthropicMessages(ctx, cfg, model, messages, tools, onEvent)
	default:
		return a.streamOpenAIChat(ctx, cfg, model, messages, tools, onEvent)
	}
}

func emitModelStreamEvent(onEvent func(modelStreamEvent), event modelStreamEvent) {
	if onEvent != nil {
		onEvent(event)
	}
}

// partialTagMatch returns the length of the suffix of s that is a prefix of tag.
// For example, if tag is "<sink>" then partialTagMatch("abc<sin", tag) returns 4
// because "<sin" is both a prefix of tag and a suffix of s.
// This is used to detect tags that may be split across streaming chunks.
func partialTagMatch(s, tag string) int {
	maxLen := len(s)
	if len(tag) < maxLen {
		maxLen = len(tag)
	}
	for n := maxLen; n > 0; n-- {
		if strings.HasPrefix(tag, s[len(s)-n:]) {
			return n
		}
	}
	return 0
}

func (a *App) streamOpenAIChat(ctx context.Context, cfg ConfigState, model string, messages []legacyopenai.ChatCompletionMessage, tools []legacyopenai.Tool, onEvent func(modelStreamEvent)) (*modelStreamResult, error) {
	clientCfg := legacyopenai.DefaultConfig(cfg.APIKey)
	clientCfg.BaseURL = baseURLForAPIFormat(cfg)
	clientCfg.HTTPClient = httpClientWithUserAgent(cfg, true, 0)
	client := legacyopenai.NewClientWithConfig(clientCfg)

	streamReq := legacyopenai.ChatCompletionRequest{
		Model:         model,
		Messages:      messages,
		StreamOptions: &legacyopenai.StreamOptions{IncludeUsage: true},
	}
	// Route the token limit to the field the target provider accepts. Both
	// fields are `omitempty`, so only the selected one is serialized — never
	// both. "auto" and "max_tokens" use the legacy field (broadest
	// compatibility across DeepSeek/GLM/Qwen/Kimi/vLLM/Ollama/OpenRouter/…);
	// "max_completion_tokens" targets official OpenAI o-series / newer GPT
	// models that reject `max_tokens` with a "please use max_completion_tokens"
	// error.
	if normalizeTokenParam(cfg.TokenParam) == tokenParamMaxCompletionTokens {
		streamReq.MaxCompletionTokens = cfg.MaxTokens
	} else {
		streamReq.MaxTokens = cfg.MaxTokens
	}
	if len(tools) > 0 {
		streamReq.Tools = tools
		// Do not set ToolChoice. The default is "auto" for OpenAI and all
		// compatible gateways, so omitting the field is equivalent. Some
		// gateways (e.g. OpenCode Go forwarding to DeepSeek) trip schema
		// validation on extra fields, so sending the default value adds risk
		// with no benefit.
		// ParallelToolCalls is also intentionally omitted for the same reason.
	}

	var stream *legacyopenai.ChatCompletionStream
	var err error
	maxRetries := effectiveLLMRetries(cfg)
	for attempt := 0; attempt <= maxRetries; attempt++ {
		stream, err = client.CreateChatCompletionStream(ctx, streamReq)
		if err != nil && strings.Contains(strings.ToLower(err.Error()), "stream_options") {
			streamReq.StreamOptions = nil
			stream, err = client.CreateChatCompletionStream(ctx, streamReq)
		}
		if err == nil || ctx.Err() != nil {
			break
		}
		if attempt < maxRetries && shouldRetryLLMError(err) {
			wait := llmRetryDelay(attempt + 1)
			emitLLMRetryEvent(onEvent, attempt+1, maxRetries, err, wait)
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			continue
		}
		break
	}
	if err != nil {
		return nil, err
	}
	defer stream.Close()

	var assistant strings.Builder
	var reasoning strings.Builder
	var reasoningState struct {
		tag      string
		openTag  string
		closeTag string
		inTag    bool
		partial  string
	}
	if cfg.ReasoningTag != "" && cfg.ReasoningTag != "reasoning_content" {
		reasoningState.tag = cfg.ReasoningTag
		reasoningState.openTag = "<" + cfg.ReasoningTag + ">"
		reasoningState.closeTag = "</" + cfg.ReasoningTag + ">"
	}
	toolCalls := []legacyopenai.ToolCall{}
	var usage *modelUsage
	for {
		raw, err := stream.RecvRaw()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 {
			continue
		}
		var resp legacyopenai.ChatCompletionStreamResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			if isIncompleteChatStreamJSON(err) && len(toolCalls) == 0 && (assistant.Len() > 0 || reasoning.Len() > 0) {
				break
			}
			return nil, fmt.Errorf("decode chat stream event: %w", err)
		}
		if resp.Usage != nil {
			usage = modelUsageFromLegacy(resp.Usage)
		}
		if len(resp.Choices) == 0 {
			continue
		}
		delta := resp.Choices[0].Delta
		if reasoningState.tag != "" {
			// Parse content-level reasoning tags embedded in delta.Content
			// (e.g. <sink>...</sink> or any configured <tag>...</tag>).
			text := delta.Content
			if reasoningState.partial != "" {
				text = reasoningState.partial + text
				reasoningState.partial = ""
			}
			remaining := text
			for len(remaining) > 0 {
				if reasoningState.inTag {
					// Look for close tag.
					idx := strings.Index(remaining, reasoningState.closeTag)
					if idx >= 0 {
						reasoning.WriteString(remaining[:idx])
						emitModelStreamEvent(onEvent, modelStreamEvent{ReasoningDelta: remaining[:idx]})
						remaining = remaining[idx+len(reasoningState.closeTag):]
						reasoningState.inTag = false
					} else {
						// Check if remaining ends with a partial close tag.
						overlap := partialTagMatch(remaining, reasoningState.closeTag)
						if overlap > 0 {
							reasoning.WriteString(remaining[:len(remaining)-overlap])
							emitModelStreamEvent(onEvent, modelStreamEvent{ReasoningDelta: remaining[:len(remaining)-overlap]})
							reasoningState.partial = remaining[len(remaining)-overlap:]
							remaining = ""
						} else {
							reasoning.WriteString(remaining)
							emitModelStreamEvent(onEvent, modelStreamEvent{ReasoningDelta: remaining})
							remaining = ""
						}
					}
				} else {
					// Look for open tag.
					idx := strings.Index(remaining, reasoningState.openTag)
					if idx >= 0 {
						if idx > 0 {
							assistant.WriteString(remaining[:idx])
							emitModelStreamEvent(onEvent, modelStreamEvent{ContentDelta: remaining[:idx]})
						}
						remaining = remaining[idx+len(reasoningState.openTag):]
						reasoningState.inTag = true
					} else {
						// Check if remaining ends with a partial open tag.
						overlap := partialTagMatch(remaining, reasoningState.openTag)
						if overlap > 0 && overlap < len(reasoningState.openTag) {
							assistant.WriteString(remaining[:len(remaining)-overlap])
							emitModelStreamEvent(onEvent, modelStreamEvent{ContentDelta: remaining[:len(remaining)-overlap]})
							reasoningState.partial = remaining[len(remaining)-overlap:]
							remaining = ""
						} else {
							assistant.WriteString(remaining)
							emitModelStreamEvent(onEvent, modelStreamEvent{ContentDelta: remaining})
							remaining = ""
						}
					}
				}
			}
		} else {
			// Original behavior: use delta.Content and delta.ReasoningContent separately.
			if delta.Content != "" {
				assistant.WriteString(delta.Content)
				emitModelStreamEvent(onEvent, modelStreamEvent{ContentDelta: delta.Content})
			}
			if delta.ReasoningContent != "" {
				reasoning.WriteString(delta.ReasoningContent)
				emitModelStreamEvent(onEvent, modelStreamEvent{ReasoningDelta: delta.ReasoningContent})
			}
		}
		if len(delta.ToolCalls) > 0 {
			mergeToolCallDeltas(&toolCalls, delta.ToolCalls)
			emitModelStreamEvent(onEvent, modelStreamEvent{ToolCalls: cloneToolCalls(toolCalls)})
		}
	}

	// Flush any residual partial tag content left in the streaming parser.
	if reasoningState.tag != "" && reasoningState.partial != "" {
		if reasoningState.inTag {
			reasoning.WriteString(reasoningState.partial)
			emitModelStreamEvent(onEvent, modelStreamEvent{ReasoningDelta: reasoningState.partial})
		} else {
			assistant.WriteString(reasoningState.partial)
			emitModelStreamEvent(onEvent, modelStreamEvent{ContentDelta: reasoningState.partial})
		}
	}

	return &modelStreamResult{
		Content:   assistant.String(),
		Reasoning: reasoning.String(),
		ToolCalls: normalizeToolCalls(toolCalls),
		Usage:     usage,
	}, nil
}

func isIncompleteChatStreamJSON(err error) bool {
	return isIncompleteStreamJSON(err)
}

func isIncompleteStreamJSON(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unexpected end of json input") || strings.Contains(msg, "unexpected eof")
}

func (a *App) streamOpenAIResponses(ctx context.Context, cfg ConfigState, model string, messages []legacyopenai.ChatCompletionMessage, tools []legacyopenai.Tool, onEvent func(modelStreamEvent)) (*modelStreamResult, error) {
	instructions, inputItems := buildOpenAIResponsesInput(messages)
	body := oaresp.ResponseNewParams{
		Model:             oaresp.ResponsesModel(model),
		Input:             oaresp.ResponseNewParamsInputUnion{OfInputItemList: inputItems},
		MaxOutputTokens:   oa.Int(int64(cfg.MaxTokens)),
		ParallelToolCalls: oa.Bool(true),
		Store:             oa.Bool(false),
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

	var stream *openAIResponsesSSEStream
	var err error
	maxRetries := effectiveLLMRetries(cfg)
	for attempt := 0; attempt <= maxRetries; attempt++ {
		stream, err = newOpenAIResponsesSSEStream(ctx, cfg, body)
		if err == nil || ctx.Err() != nil {
			break
		}
		if attempt < maxRetries && shouldRetryLLMError(err) {
			wait := llmRetryDelay(attempt + 1)
			emitLLMRetryEvent(onEvent, attempt+1, maxRetries, err, wait)
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			continue
		}
		break
	}
	if err != nil {
		return nil, err
	}
	defer stream.Close()

	var assistant strings.Builder
	var reasoning strings.Builder
	toolCalls := []legacyopenai.ToolCall{}
	toolIndexByOutput := map[int64]int{}
	toolIndexByItemID := map[string]int{}
	var usage *modelUsage
	finalOutputText := ""
	var streamErr error
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

	for stream.Next() {
		event, err := stream.Event()
		if err != nil {
			if isIncompleteStreamJSON(err) && len(toolCalls) == 0 && (assistant.Len() > 0 || reasoning.Len() > 0) {
				break
			}
			return nil, fmt.Errorf("decode responses stream event: %w", err)
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
		case "response.output_item.added":
			ev := event.AsResponseOutputItemAdded()
			if ev.Item.Type == "function_call" {
				idx := ensureResponsesToolCall(&toolCalls, toolIndexByOutput, toolIndexByItemID, ev.OutputIndex, ev.Item.ID)
				updateToolCallFromResponsesItem(&toolCalls[idx], ev.Item)
				emitModelStreamEvent(onEvent, modelStreamEvent{ToolCalls: cloneToolCalls(toolCalls)})
			}
		case "response.function_call_arguments.delta":
			ev := event.AsResponseFunctionCallArgumentsDelta()
			idx := ensureResponsesToolCall(&toolCalls, toolIndexByOutput, toolIndexByItemID, ev.OutputIndex, ev.ItemID)
			toolCalls[idx].Function.Arguments += ev.Delta
			emitModelStreamEvent(onEvent, modelStreamEvent{ToolCalls: cloneToolCalls(toolCalls)})
		case "response.function_call_arguments.done":
			ev := event.AsResponseFunctionCallArgumentsDone()
			idx := ensureResponsesToolCall(&toolCalls, toolIndexByOutput, toolIndexByItemID, ev.OutputIndex, ev.ItemID)
			toolCalls[idx].Function.Arguments = ev.Arguments
			emitModelStreamEvent(onEvent, modelStreamEvent{ToolCalls: cloneToolCalls(toolCalls)})
		case "response.output_item.done":
			ev := event.AsResponseOutputItemDone()
			if ev.Item.Type == "function_call" {
				idx := ensureResponsesToolCall(&toolCalls, toolIndexByOutput, toolIndexByItemID, ev.OutputIndex, ev.Item.ID)
				updateToolCallFromResponsesItem(&toolCalls[idx], ev.Item)
				emitModelStreamEvent(onEvent, modelStreamEvent{ToolCalls: cloneToolCalls(toolCalls)})
			} else if ev.Item.Type == "image_generation_call" {
				imageCall := ev.Item.AsImageGenerationCall()
				emitImage(imageCall.ID, imageCall.Result, false)
			}
		case "response.image_generation_call.partial_image":
			ev := event.AsResponseImageGenerationCallPartialImage()
			emitImage(ev.ItemID, ev.PartialImageB64, true)
		case "response.completed":
			ev := event.AsResponseCompleted()
			usage = modelUsageFromResponses(ev.Response.Usage)
			finalOutputText = ev.Response.OutputText()
			for _, item := range ev.Response.Output {
				if item.Type == "image_generation_call" {
					imageCall := item.AsImageGenerationCall()
					emitImage(imageCall.ID, imageCall.Result, false)
				}
			}
		case "error":
			ev := event.AsError()
			streamErr = fmt.Errorf("%s: %s", strings.TrimSpace(ev.Code), strings.TrimSpace(ev.Message))
		case "response.failed":
			ev := event.AsResponseFailed()
			if ev.Response.Error.Message != "" {
				streamErr = fmt.Errorf("%s: %s", ev.Response.Error.Code, ev.Response.Error.Message)
			} else {
				streamErr = errors.New("response failed")
			}
		case "response.incomplete":
			ev := event.AsResponseIncomplete()
			if ev.Response.IncompleteDetails.Reason != "" {
				streamErr = fmt.Errorf("response incomplete: %s", ev.Response.IncompleteDetails.Reason)
			}
		}
		if streamErr != nil {
			break
		}
	}
	if err := stream.Err(); err != nil {
		return nil, err
	}
	if streamErr != nil {
		return nil, streamErr
	}
	content := assistant.String()
	if content == "" && finalOutputText != "" {
		content = finalOutputText
	}
	return &modelStreamResult{
		Content:   content,
		Reasoning: reasoning.String(),
		ToolCalls: normalizeToolCalls(toolCalls),
		Images:    images,
		Usage:     usage,
	}, nil
}

func supportsOpenAIResponsesImageGeneration(cfg ConfigState) bool {
	if cfg.grillMode {
		return false
	}
	base := strings.ToLower(strings.TrimRight(baseURLForAPIFormat(cfg), "/"))
	return base == defaultOpenAIResponsesURL || strings.HasPrefix(base, defaultOpenAIResponsesURL+"/")
}

type openAIResponsesSSEStream struct {
	decoder ssestream.Decoder
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

	resp, err := proxyHTTPClient(cfg, true, 0).Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxToolOutput))
		msg := strings.TrimSpace(string(raw))
		if msg == "" {
			msg = resp.Status
		}
		return nil, fmt.Errorf("responses request failed: %s", msg)
	}
	decoder := ssestream.NewDecoder(resp)
	if decoder == nil {
		resp.Body.Close()
		return nil, errors.New("responses stream was empty")
	}
	return &openAIResponsesSSEStream{decoder: decoder}, nil
}

func (s *openAIResponsesSSEStream) Next() bool {
	if s == nil || s.decoder == nil {
		return false
	}
	return s.decoder.Next()
}

func (s *openAIResponsesSSEStream) Event() (oaresp.ResponseStreamEventUnion, error) {
	var event oaresp.ResponseStreamEventUnion
	if s == nil || s.decoder == nil {
		return event, io.EOF
	}
	raw := bytes.TrimSpace(s.decoder.Event().Data)
	if len(raw) == 0 || bytes.Equal(raw, []byte("[DONE]")) {
		return event, nil
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		return event, err
	}
	return event, nil
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

func (a *App) streamAnthropicMessages(ctx context.Context, cfg ConfigState, model string, messages []legacyopenai.ChatCompletionMessage, tools []legacyopenai.Tool, onEvent func(modelStreamEvent)) (*modelStreamResult, error) {
	baseURL := baseURLForAPIFormat(cfg)
	// 关闭 SDK 内置重试,改用本模块统一的重试循环以便发出 run:retry 事件。
	client := anthropic.NewClient(
		anthropicoption.WithAPIKey(cfg.APIKey),
		anthropicoption.WithBaseURL(baseURL),
		anthropicoption.WithMaxRetries(0),
		anthropicoption.WithHTTPClient(httpClientWithUserAgent(cfg, true, 0)),
	)

	system, anthropicMessages := buildAnthropicMessages(messages)
	if len(anthropicMessages) == 0 {
		anthropicMessages = append(anthropicMessages, anthropic.NewUserMessage(anthropic.NewTextBlock("")))
	}
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(model),
		Messages:  anthropicMessages,
		MaxTokens: int64(cfg.MaxTokens),
	}
	if system != "" {
		params.System = []anthropic.TextBlockParam{{Text: system}}
	}
	if len(tools) > 0 {
		params.Tools = convertToolsToAnthropic(tools)
	}
	if baseURL == defaultAnthropicMessagesURL {
		params.CacheControl = anthropic.CacheControlEphemeralParam{TTL: anthropic.CacheControlEphemeralTTLTTL5m}
	}

	maxRetries := effectiveLLMRetries(cfg)
	var assistant strings.Builder
	var reasoning strings.Builder
	toolCalls := []legacyopenai.ToolCall{}
	toolIndexByBlock := map[int64]int{}
	var usage *modelUsage
	var stopReason string
	var stopSequence string

	for attempt := 0; attempt <= maxRetries; attempt++ {
		assistant.Reset()
		reasoning.Reset()
		toolCalls = toolCalls[:0]
		toolIndexByBlock = map[int64]int{}
		usage = nil
		stopReason = ""
		stopSequence = ""
		stream := client.Messages.NewStreaming(ctx, params)
		gotAnyEvent := false
		for stream.Next() {
			gotAnyEvent = true
			event := stream.Current()
			switch event.Type {
			case "message_start":
				ev := event.AsMessageStart()
				usage = modelUsageFromAnthropic(ev.Message.Usage)
			case "message_delta":
				ev := event.AsMessageDelta()
				usage = mergeAnthropicUsage(usage, ev.Usage)
				stopReason = string(ev.Delta.StopReason)
				stopSequence = ev.Delta.StopSequence
			case "content_block_start":
				ev := event.AsContentBlockStart()
				block := ev.ContentBlock
				switch block.Type {
				case "text":
					if block.Text != "" {
						assistant.WriteString(block.Text)
						emitModelStreamEvent(onEvent, modelStreamEvent{ContentDelta: block.Text})
					}
				case "thinking":
					if block.Thinking != "" {
						reasoning.WriteString(block.Thinking)
						emitModelStreamEvent(onEvent, modelStreamEvent{ReasoningDelta: block.Thinking})
					}
				case "tool_use":
					idx := len(toolCalls)
					toolIndexByBlock[ev.Index] = idx
					args := ""
					if block.Input != nil {
						if raw, err := json.Marshal(block.Input); err == nil && string(raw) != "null" && string(raw) != "{}" {
							args = string(raw)
						}
					}
					toolCalls = append(toolCalls, legacyopenai.ToolCall{
						ID:   block.ID,
						Type: legacyopenai.ToolTypeFunction,
						Function: legacyopenai.FunctionCall{
							Name:      block.Name,
							Arguments: args,
						},
					})
					emitModelStreamEvent(onEvent, modelStreamEvent{ToolCalls: cloneToolCalls(toolCalls)})
				}
			case "content_block_delta":
				ev := event.AsContentBlockDelta()
				delta := ev.Delta
				switch delta.Type {
				case "text_delta":
					if delta.Text != "" {
						assistant.WriteString(delta.Text)
						emitModelStreamEvent(onEvent, modelStreamEvent{ContentDelta: delta.Text})
					}
				case "thinking_delta":
					if delta.Thinking != "" {
						reasoning.WriteString(delta.Thinking)
						emitModelStreamEvent(onEvent, modelStreamEvent{ReasoningDelta: delta.Thinking})
					}
				case "input_json_delta":
					if idx, ok := toolIndexByBlock[ev.Index]; ok {
						toolCalls[idx].Function.Arguments += delta.PartialJSON
						emitModelStreamEvent(onEvent, modelStreamEvent{ToolCalls: cloneToolCalls(toolCalls)})
					}
				}
			}
		}
		streamErr := stream.Err()
		stream.Close()
		if streamErr == nil {
			break
		}
		// 已经 emit 过事件的内容不可重试(会重复输出),直接返回错误。
		// 仅在尚未开始流输出时重试。
		if !gotAnyEvent && ctx.Err() == nil && attempt < maxRetries && shouldRetryLLMError(streamErr) {
			wait := llmRetryDelay(attempt + 1)
			emitLLMRetryEvent(onEvent, attempt+1, maxRetries, streamErr, wait)
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			continue
		}
		return nil, streamErr
	}
	for i := range toolCalls {
		if strings.TrimSpace(toolCalls[i].Function.Arguments) == "" {
			toolCalls[i].Function.Arguments = "{}"
		}
	}
	return &modelStreamResult{
		Content:      assistant.String(),
		Reasoning:    reasoning.String(),
		ToolCalls:    normalizeToolCalls(toolCalls),
		Usage:        usage,
		StopReason:   stopReason,
		StopSequence: stopSequence,
	}, nil
}

func anthropicStopReasonError(reason string) error {
	switch strings.TrimSpace(reason) {
	case "", "end_turn", "tool_use", "stop_sequence":
		return nil
	case "max_tokens":
		return errors.New("Anthropic response reached the Max Tokens limit; increase Max Tokens or shorten the conversation")
	case "refusal":
		return errors.New("Anthropic refused the request")
	case "pause_turn":
		return errors.New("Anthropic paused the turn, which is not supported by the current client-tool workflow")
	case "model_context_window_exceeded":
		return errors.New("Anthropic stopped because the model context window was exceeded")
	default:
		return fmt.Errorf("Anthropic stopped with unsupported reason %q", reason)
	}
}

func modelResponseStopError(cfg ConfigState, result *modelStreamResult) error {
	if result == nil || normalizeAPIFormat(cfg.APIFormat) != apiFormatAnthropicMessages {
		return nil
	}
	return anthropicStopReasonError(result.StopReason)
}

func buildOpenAIResponsesInput(messages []legacyopenai.ChatCompletionMessage) (string, oaresp.ResponseInputParam) {
	systemParts := []string{}
	input := oaresp.ResponseInputParam{}
	for _, m := range messages {
		role := strings.TrimSpace(m.Role)
		switch role {
		case legacyopenai.ChatMessageRoleSystem:
			if text := messageText(m); text != "" {
				systemParts = append(systemParts, text)
			}
		case legacyopenai.ChatMessageRoleUser, legacyopenai.ChatMessageRoleAssistant:
			if role == legacyopenai.ChatMessageRoleAssistant && len(m.ToolCalls) > 0 {
				if text := messageText(m); text != "" {
					input = append(input, oaresp.ResponseInputItemParamOfMessage(text, oaresp.EasyInputMessageRoleAssistant))
				}
				for _, call := range m.ToolCalls {
					input = append(input, responseInputItemParamOfFunctionCall(call))
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
			if strings.TrimSpace(m.ToolCallID) != "" {
				input = append(input, oaresp.ResponseInputItemParamOfFunctionCallOutput(m.ToolCallID, m.Content))
			}
		}
	}
	return strings.Join(systemParts, "\n\n"), input
}

func responseInputItemParamOfFunctionCall(call legacyopenai.ToolCall) oaresp.ResponseInputItemUnionParam {
	item := oaresp.ResponseInputItemParamOfFunctionCall(call.Function.Arguments, effectiveToolCallID(call), call.Function.Name)
	if item.OfFunctionCall != nil {
		item.OfFunctionCall.ID = oa.String(effectiveResponsesFunctionCallItemID(call))
	}
	return item
}

func effectiveResponsesFunctionCallItemID(call legacyopenai.ToolCall) string {
	if strings.TrimSpace(call.ID) != "" {
		return "fc_" + strings.TrimPrefix(call.ID, "fc_")
	}
	if strings.TrimSpace(call.Function.Name) != "" {
		return "fc_" + call.Function.Name
	}
	return "fc_unknown"
}

func openAIResponsesContentFromMulti(m legacyopenai.ChatCompletionMessage) oaresp.ResponseInputMessageContentListParam {
	content := oaresp.ResponseInputMessageContentListParam{}
	if strings.TrimSpace(m.Content) != "" {
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

func buildAnthropicMessages(messages []legacyopenai.ChatCompletionMessage) (string, []anthropic.MessageParam) {
	systemParts := []string{}
	out := []anthropic.MessageParam{}
	for i := 0; i < len(messages); i++ {
		m := messages[i]
		switch m.Role {
		case legacyopenai.ChatMessageRoleSystem:
			if text := messageText(m); text != "" {
				systemParts = append(systemParts, text)
			}
		case legacyopenai.ChatMessageRoleUser:
			blocks := anthropicBlocksFromMessage(m)
			if len(blocks) > 0 {
				out = append(out, anthropic.NewUserMessage(blocks...))
			}
		case legacyopenai.ChatMessageRoleAssistant:
			blocks := []anthropic.ContentBlockParamUnion{}
			if text := messageText(m); text != "" {
				blocks = append(blocks, anthropic.NewTextBlock(text))
			}
			for _, call := range m.ToolCalls {
				blocks = append(blocks, anthropic.NewToolUseBlock(effectiveToolCallID(call), decodeToolArguments(call.Function.Arguments), call.Function.Name))
			}
			if len(blocks) > 0 {
				out = append(out, anthropic.NewAssistantMessage(blocks...))
			}
		case legacyopenai.ChatMessageRoleTool:
			blocks := []anthropic.ContentBlockParamUnion{}
			for i < len(messages) && messages[i].Role == legacyopenai.ChatMessageRoleTool {
				toolMsg := messages[i]
				if strings.TrimSpace(toolMsg.ToolCallID) != "" {
					blocks = append(blocks, anthropic.NewToolResultBlock(toolMsg.ToolCallID, toolMsg.Content, anthropicToolResultIsError(toolMsg.Content)))
				}
				i++
			}
			i--
			if len(blocks) > 0 {
				out = append(out, anthropic.NewUserMessage(blocks...))
			}
		}
	}
	return strings.Join(systemParts, "\n\n"), out
}

func anthropicToolResultIsError(content string) bool {
	var result struct {
		OK *bool `json:"ok"`
	}
	if err := json.Unmarshal([]byte(content), &result); err != nil || result.OK == nil {
		return false
	}
	return !*result.OK
}

func anthropicBlocksFromMessage(m legacyopenai.ChatCompletionMessage) []anthropic.ContentBlockParamUnion {
	blocks := []anthropic.ContentBlockParamUnion{}
	if strings.TrimSpace(m.Content) != "" {
		blocks = append(blocks, anthropic.NewTextBlock(m.Content))
	}
	for _, part := range m.MultiContent {
		switch part.Type {
		case legacyopenai.ChatMessagePartTypeText:
			if strings.TrimSpace(part.Text) != "" {
				blocks = append(blocks, anthropic.NewTextBlock(part.Text))
			}
		case legacyopenai.ChatMessagePartTypeImageURL:
			if part.ImageURL == nil {
				continue
			}
			mediaType, data, ok := splitImageDataURL(part.ImageURL.URL)
			if !ok {
				continue
			}
			blocks = append(blocks, anthropic.NewImageBlock(anthropic.Base64ImageSourceParam{
				MediaType: anthropic.Base64ImageSourceMediaType(mediaType),
				Data:      data,
			}))
		}
	}
	return blocks
}

func convertToolsToOpenAIResponses(tools []legacyopenai.Tool) []oaresp.ToolUnionParam {
	out := make([]oaresp.ToolUnionParam, 0, len(tools))
	for _, tool := range tools {
		if tool.Function == nil {
			continue
		}
		params := schemaMap(tool.Function.Parameters)
		next := oaresp.ToolParamOfFunction(tool.Function.Name, params, false)
		if next.OfFunction != nil && strings.TrimSpace(tool.Function.Description) != "" {
			next.OfFunction.Description = oa.String(tool.Function.Description)
		}
		out = append(out, next)
	}
	return out
}

func convertToolsToAnthropic(tools []legacyopenai.Tool) []anthropic.ToolUnionParam {
	out := make([]anthropic.ToolUnionParam, 0, len(tools))
	for _, tool := range tools {
		if tool.Function == nil {
			continue
		}
		t := anthropic.ToolParam{
			Name:        tool.Function.Name,
			InputSchema: anthropicInputSchema(schemaMap(tool.Function.Parameters)),
		}
		if strings.TrimSpace(tool.Function.Description) != "" {
			t.Description = anthropic.String(tool.Function.Description)
		}
		out = append(out, anthropic.ToolUnionParam{OfTool: &t})
	}
	return out
}

func schemaMap(value any) map[string]any {
	if value == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	if m, ok := value.(map[string]any); ok {
		return m
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return m
}

func anthropicInputSchema(schema map[string]any) anthropic.ToolInputSchemaParam {
	result := anthropic.ToolInputSchemaParam{}
	if props, ok := schema["properties"]; ok {
		result.Properties = props
	} else {
		result.Properties = map[string]any{}
	}
	result.Required = stringSliceFromAny(schema["required"])
	result.ExtraFields = map[string]any{}
	for key, value := range schema {
		switch key {
		case "type", "properties", "required":
			continue
		default:
			result.ExtraFields[key] = value
		}
	}
	if len(result.ExtraFields) == 0 {
		result.ExtraFields = nil
	}
	return result
}

func stringSliceFromAny(value any) []string {
	switch v := value.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func ensureResponsesToolCall(toolCalls *[]legacyopenai.ToolCall, byOutput map[int64]int, byItemID map[string]int, outputIndex int64, itemID string) int {
	if idx, ok := byOutput[outputIndex]; ok {
		if itemID != "" {
			byItemID[itemID] = idx
		}
		return idx
	}
	if itemID != "" {
		if idx, ok := byItemID[itemID]; ok {
			byOutput[outputIndex] = idx
			return idx
		}
	}
	idx := len(*toolCalls)
	*toolCalls = append(*toolCalls, legacyopenai.ToolCall{Type: legacyopenai.ToolTypeFunction})
	byOutput[outputIndex] = idx
	if itemID != "" {
		byItemID[itemID] = idx
	}
	return idx
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
	if item.Arguments != "" {
		call.Function.Arguments = item.Arguments
	}
}

func normalizeToolCalls(toolCalls []legacyopenai.ToolCall) []legacyopenai.ToolCall {
	out := cloneToolCalls(toolCalls)
	for i := range out {
		if out[i].Type == "" {
			out[i].Type = legacyopenai.ToolTypeFunction
		}
		if strings.TrimSpace(out[i].Function.Arguments) == "" {
			out[i].Function.Arguments = "{}"
		}
	}
	return out
}

func cloneToolCalls(toolCalls []legacyopenai.ToolCall) []legacyopenai.ToolCall {
	if len(toolCalls) == 0 {
		return nil
	}
	out := make([]legacyopenai.ToolCall, len(toolCalls))
	copy(out, toolCalls)
	return out
}

func effectiveToolCallID(call legacyopenai.ToolCall) string {
	if strings.TrimSpace(call.ID) != "" {
		return call.ID
	}
	if strings.TrimSpace(call.Function.Name) != "" {
		return "call_" + call.Function.Name
	}
	return "call_unknown"
}

func decodeToolArguments(args string) any {
	args = strings.TrimSpace(args)
	if args == "" {
		return map[string]any{}
	}
	var decoded any
	if err := json.Unmarshal([]byte(args), &decoded); err == nil && decoded != nil {
		return decoded
	}
	return map[string]any{"_raw": args}
}

func messageText(m legacyopenai.ChatCompletionMessage) string {
	if strings.TrimSpace(m.Content) != "" {
		return m.Content
	}
	if len(m.MultiContent) > 0 {
		return textFromMultiContent(m.MultiContent)
	}
	return ""
}

func splitImageDataURL(value string) (string, string, bool) {
	if !validImageDataURL(value) {
		return "", "", false
	}
	prefix, data, ok := strings.Cut(value, ",")
	if !ok {
		return "", "", false
	}
	mediaType := strings.TrimPrefix(prefix, "data:")
	mediaType = strings.TrimSuffix(mediaType, ";base64")
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	switch mediaType {
	case "image/png", "image/jpeg", "image/jpg", "image/webp", "image/gif":
		if mediaType == "image/jpg" {
			mediaType = "image/jpeg"
		}
		return mediaType, data, strings.TrimSpace(data) != ""
	default:
		return "", "", false
	}
}

func modelUsageFromLegacy(usage *legacyopenai.Usage) *modelUsage {
	if usage == nil {
		return nil
	}
	return &modelUsage{
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
	}
}

func modelUsageFromResponses(usage oaresp.ResponseUsage) *modelUsage {
	if usage.InputTokens <= 0 && usage.OutputTokens <= 0 {
		return nil
	}
	return &modelUsage{
		PromptTokens:     int(usage.InputTokens),
		CompletionTokens: int(usage.OutputTokens),
	}
}

func modelUsageFromAnthropic(usage anthropic.Usage) *modelUsage {
	input := usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens
	output := usage.OutputTokens
	if input <= 0 && output <= 0 {
		return nil
	}
	return &modelUsage{PromptTokens: int(input), CompletionTokens: int(output)}
}

func mergeAnthropicUsage(current *modelUsage, usage anthropic.MessageDeltaUsage) *modelUsage {
	if current == nil {
		current = &modelUsage{}
	}
	if usage.OutputTokens > 0 {
		current.CompletionTokens = int(usage.OutputTokens)
	}
	return current
}

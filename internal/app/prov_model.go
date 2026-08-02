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
	CacheHitTokens   int
	CacheMissTokens  int
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
	Attempt     int // 第几次重试,从 1 开始
	MaxAttempts int // 最大重试次数。单 key 路径=重试次数(不含首次);
	// 多 key 路径=总尝试次数上限(含首次,即 maxMultiKeyAttempts)
	Error     string // 触发重试的错误信息
	WaitMS    int    // 重试前等待毫秒数
	KeyIndex  int    // 失败/切换涉及的 key 序号(0 基),0 表示未知(单 key 或适配器内重试)
	TotalKeys int    // key 池总数,0 表示未知
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

// isAuthKeyError 判断错误是否属于认证/配额类(key 本身失效),这类错误重试
// 同一 key 无意义,应切换或直接失败。匹配覆盖常见变体:HTTP 状态码
// (401/403)、OpenAI/Anthropic 错误码(invalid_api_key、insufficient_quota、
// permission_error 等)以及常见文案(invalid api key、unauthorized、
// forbidden、quota、credential 等)。
func isAuthKeyError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "401") || strings.Contains(msg, "402") || strings.Contains(msg, "403") ||
		strings.Contains(msg, "invalid api key") || strings.Contains(msg, "invalid_api_key") ||
		strings.Contains(msg, "invalid-api-key") || strings.Contains(msg, "invalid key") ||
		strings.Contains(msg, "api key") || strings.Contains(msg, "api_key") ||
		strings.Contains(msg, "unauthorized") || strings.Contains(msg, "authentication failed") ||
		strings.Contains(msg, "not authorized") || strings.Contains(msg, "permission denied") ||
		strings.Contains(msg, "permission") || strings.Contains(msg, "forbidden") ||
		strings.Contains(msg, "insufficient_quota") || strings.Contains(msg, "insufficient_balance") ||
		strings.Contains(msg, "quota") || strings.Contains(msg, "payment required") ||
		strings.Contains(msg, "access denied") || strings.Contains(msg, "credential") {
		return true
	}
	return false
}

// shouldFailoverKey 判断错误是否值得切换到下一个 key。包含瞬时错误
// (429/5xx/网络)以及认证/配额类错误(401/403/invalid api key/quota)——
// 后者重试同一 key 无意义,但多 key 场景下应立即切换。
func shouldFailoverKey(err error) bool {
	return isAuthKeyError(err) || shouldRetryLLMError(err)
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
	emitLLMRetryEventForKey(onEvent, attempt, maxAttempts, err, wait, 0, 0)
}

// emitLLMRetryEventForKey 在重试事件中附加 key 序号与池大小,前端据此显示
// 当前使用第几个 key。keyIndex 为 0 基;未知时传 0,0。
func emitLLMRetryEventForKey(onEvent func(modelStreamEvent), attempt, maxAttempts int, err error, wait time.Duration, keyIndex, totalKeys int) {
	if onEvent == nil || err == nil {
		return
	}
	emitModelStreamEvent(onEvent, modelStreamEvent{Retry: &modelRetryInfo{
		Attempt:     attempt,
		MaxAttempts: maxAttempts,
		Error:       err.Error(),
		WaitMS:      int(wait.Milliseconds()),
		KeyIndex:    keyIndex,
		TotalKeys:   totalKeys,
	}})
}

// effectiveLLMRetries 返回有效的最大重试次数。多 key 模式下 noAdapterRetry
// 为 true,适配器内不做退避重试,由 streamModelResponse 的外层循环统一承担
// 重试与故障切换,避免重试次数随 key 数翻倍。
func effectiveLLMRetries(cfg ConfigState) int {
	if cfg.noAdapterRetry {
		return 0
	}
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
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = defaultMaxTokensForAPIFormat(cfg.APIFormat)
	}
	keys := resolveKeyPool(cfg)
	if len(keys) == 0 {
		return nil, errors.New("API key is required")
	}
	// 单 key 快速路径:完全保持原有的适配器内重试行为。
	if len(keys) == 1 {
		return a.streamModelResponseWithKey(ctx, cfg, model, messages, tools, onEvent)
	}
	// 多 key:固定优先级故障转移 + 冷却。每次尝试从第一个可用(不在冷却)
	// 的 key 开始;失败后按错误类别记录冷却(认证/配额 60s,瞬时 10s)并顺延
	// 到下一个,直到成功或全部失败。总尝试次数有上限(maxMultiKeyAttempts),
	// 且通过 noAdapterRetry 关闭适配器内退避重试,由本循环统一承担重试与
	// 轮换,避免 N 个 key × 适配器重试组合爆炸。
	// 已发射任何流事件(文本/推理/工具调用/图片)后禁止切换,避免重复输出
	// ——与适配器内 mid-stream 不重试的既有约定一致。
	maxAttempts := len(keys)
	if maxAttempts > maxMultiKeyAttempts {
		maxAttempts = maxMultiKeyAttempts
	}
	var lastErr error
	emitted := false
	wrappedOnEvent := func(e modelStreamEvent) {
		if e.ContentDelta != "" || e.ReasoningDelta != "" || e.ToolCalls != nil || e.Image != nil {
			emitted = true
		}
		if onEvent != nil {
			onEvent(e)
		}
	}
	for attempt := 0; attempt < maxAttempts; attempt++ {
		idx := a.firstUsableKeyIndex(cfg, keys)
		key := keys[idx]
		if a.isKeyCoolingDown(cfg, key) {
			// firstUsableKeyIndex 只在全部 key 冷却时返回冷却中的 key。
			if lastErr == nil {
				lastErr = fmt.Errorf("all API keys are cooling down, try again later")
			}
			break
		}
		callCfg := cfg
		callCfg.APIKey = key
		callCfg.noAdapterRetry = true // 外层循环统一处理重试与轮换
		result, err := a.streamModelResponseWithKey(ctx, callCfg, model, messages, tools, wrappedOnEvent)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !emitted && shouldFailoverKey(err) {
			cooldown := keyTransientCooldownDuration
			wait := time.Duration(0)
			if isAuthKeyError(err) {
				cooldown = keyAuthCooldownDuration
			} else if shouldRetryLLMError(err) {
				// 瞬时错误(429/5xx/网络):切换前短暂退避,避免多个 key
				// 同时打向同一故障端点,也避免连续 8 次尝试没有间隔。
				// 最后一次尝试后没有下一次切换,不再白等退避。
				if attempt+1 < maxAttempts {
					wait = llmRetryDelay(attempt + 1)
					select {
					case <-time.After(wait):
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}
			}
			a.recordKeyFailure(cfg, key, cooldown)
			// 无论是否还有备用 key 都发出事件(含最后一个 key 失败),前端显示
			// "第 N 个 Key 失败",避免用户只看到泛化错误。
			emitLLMRetryEventForKey(onEvent, attempt+1, maxAttempts, err, wait, idx, len(keys))
			continue
		}
		return nil, err
	}
	return nil, lastErr
}

// streamModelResponseWithKey 按 apiFormat 分发到具体适配器,key 已由调用方
// 写入 cfg.APIKey。
func (a *App) streamModelResponseWithKey(ctx context.Context, cfg ConfigState, model string, messages []legacyopenai.ChatCompletionMessage, tools []legacyopenai.Tool, onEvent func(modelStreamEvent)) (*modelStreamResult, error) {
	switch cfg.APIFormat {
	case apiFormatOpenAIResponses:
		return a.streamOpenAIResponses(ctx, cfg, model, messages, tools, onEvent)
	case apiFormatAnthropicMessages:
		return a.streamAnthropicMessages(ctx, cfg, model, messages, tools, onEvent)
	default:
		return a.streamOpenAIChat(ctx, cfg, model, messages, tools, onEvent)
	}
}

// keyAuthCooldownDuration 是认证/配额类错误(401/403/invalid key/quota)后
// 的冷却窗口:key 本身已失效,短时间重试无意义。
const keyAuthCooldownDuration = 60 * time.Second

// keyTransientCooldownDuration 是瞬时错误(429/5xx/网络)后的冷却窗口。比
// 认证错误短,避免端点短暂故障时把整个 key 池冷却 60 秒(fail-fast 但快速自愈)。
const keyTransientCooldownDuration = 10 * time.Second

// maxMultiKeyAttempts 是单次请求在多 key 模式下最多尝试的总次数(含故障切换)。
// 与 effectiveLLMRetries 解耦,防止 N 个 key × 适配器重试组合爆炸。
const maxMultiKeyAttempts = 8

// keyCooldownID 是冷却记录的键,按 endpoint+key 隔离。
func keyCooldownID(cfg ConfigState, key string) string {
	return baseURLForAPIFormat(cfg) + "\x00" + key
}

// firstUsableKeyIndex 返回 key 池中第一个不在冷却的 key 序号(从 0 开始,
// 即最高优先级;冷却期内的低优先级 key 不会越过高优先级被选中)。
// 全部冷却中返回 0,调用方循环会跳过所有冷却 key。
func (a *App) firstUsableKeyIndex(cfg ConfigState, keys []string) int {
	a.keyStateMu.Lock()
	defer a.keyStateMu.Unlock()
	for i, key := range keys {
		id := keyCooldownID(cfg, key)
		until, ok := a.keyCooldowns[id]
		if !ok || time.Now().After(until) {
			if ok {
				delete(a.keyCooldowns, id)
			}
			return i
		}
	}
	return 0
}

// isKeyCoolingDown 报告 key 是否处于冷却窗口;过期记录被惰性清理。
func (a *App) isKeyCoolingDown(cfg ConfigState, key string) bool {
	id := keyCooldownID(cfg, key)
	a.keyStateMu.Lock()
	defer a.keyStateMu.Unlock()
	until, ok := a.keyCooldowns[id]
	if !ok {
		return false
	}
	if time.Now().Before(until) {
		return true
	}
	delete(a.keyCooldowns, id)
	return false
}

// recordKeyFailure 将 key 置入冷却窗口,窗口长度由错误类别决定(认证/配额
// 60s,瞬时错误 10s)。
func (a *App) recordKeyFailure(cfg ConfigState, key string, cooldown time.Duration) {
	a.keyStateMu.Lock()
	a.keyCooldowns[keyCooldownID(cfg, key)] = time.Now().Add(cooldown)
	a.keyStateMu.Unlock()
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
	// Thinking strength: only send reasoning_effort when the user explicitly
	// picked a level. The normalized selection is sent unchanged, including
	// xhigh and max; "auto" (the default) sends nothing.
	if effort := reasoningEffortForAdapter(apiFormatOpenAIChat, cfg.ReasoningEffort); effort != "" {
		streamReq.ReasoningEffort = effort
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
			usage = modelUsageFromLegacy(resp.Usage, raw)
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
	// Thinking strength for the Responses API (reasoning.effort). The SDK
	// type is string-backed, so the normalized selection is sent unchanged,
	// including xhigh and max.
	if effort := reasoningEffortForAdapter(apiFormatOpenAIResponses, cfg.ReasoningEffort); effort != "" {
		body.Reasoning = oa.ReasoningParam{Effort: oa.ReasoningEffort(effort)}
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
		event, rawEvent, err := stream.Event()
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
			// The openai-go Responses union decoder currently drops the nested
			// response object when decoding a stream event. Read usage from the
			// original event JSON so cached input tokens are not lost.
			if rawUsage := modelUsageFromResponsesEvent(rawEvent); rawUsage != nil {
				usage = rawUsage
			}
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
	// Thinking strength for Anthropic: only output_config.effort. Anthropic
	// has no reasoning_effort parameter; effort is the equivalent control and
	// is only sent when the user explicitly picked a level ("auto" keeps the
	// provider default). Thinking itself is NOT enabled here: adaptive/enabled
	// thinking would emit thinking blocks whose signatures must be replayed
	// verbatim in the next tool turn, and buildAnthropicMessages cannot do
	// that losslessly yet (see AGENTS.md). On models where adaptive thinking
	// is already the default (Opus 4.6+/Sonnet 4.6+/Claude 5), effort alone
	// tunes that existing thinking; on older models the parameter is either
	// ignored or rejected, which the user opted into by picking a level.
	if effort := reasoningEffortForAdapter(apiFormatAnthropicMessages, cfg.ReasoningEffort); effort != "" {
		params.OutputConfig = anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffort(effort)}
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

// modelUsageFromLegacy maps the OpenAI-compatible usage struct. DeepSeek returns
// cache counters as top-level prompt_cache_{hit,miss}_tokens (not parsed by the
// sashabaranov struct), so raw is re-scanned to recover them when present;
// OpenAI/MiMo carry them nested under prompt_tokens_details.cached_tokens.
func modelUsageFromLegacy(usage *legacyopenai.Usage, raw []byte) *modelUsage {
	if usage == nil {
		return nil
	}
	hit, miss := 0, 0
	if usage.PromptTokensDetails != nil {
		hit = usage.PromptTokensDetails.CachedTokens
	}
	// DeepSeek 顶层字段（sashabaranov 不解析，从原始 JSON 补取）
	if len(raw) > 0 {
		var ds struct {
			Usage *struct {
				PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens"`
				PromptCacheMissTokens int `json:"prompt_cache_miss_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(raw, &ds) == nil && ds.Usage != nil {
			if ds.Usage.PromptCacheHitTokens > 0 {
				hit = ds.Usage.PromptCacheHitTokens
			}
			if ds.Usage.PromptCacheMissTokens > 0 {
				miss = ds.Usage.PromptCacheMissTokens
			}
		}
	}
	if miss == 0 && hit > 0 && usage.PromptTokens > hit {
		miss = usage.PromptTokens - hit
	}
	return &modelUsage{
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		CacheHitTokens:   hit,
		CacheMissTokens:  miss,
	}
}

func modelUsageFromResponses(usage oaresp.ResponseUsage) *modelUsage {
	if usage.InputTokens <= 0 && usage.OutputTokens <= 0 {
		return nil
	}
	input := int(usage.InputTokens)
	hit := int(usage.InputTokensDetails.CachedTokens)
	miss := input - hit
	if miss < 0 {
		miss = 0
	}
	return &modelUsage{
		PromptTokens:     input,
		CompletionTokens: int(usage.OutputTokens),
		CacheHitTokens:   hit,
		CacheMissTokens:  miss,
	}
}

// modelUsageFromResponsesEvent parses usage from the raw response.completed
// event. This intentionally uses small local structs instead of the SDK's
// response union because that union currently loses the nested response object
// during stream-event decoding.
func modelUsageFromResponsesEvent(raw []byte) *modelUsage {
	var payload struct {
		Response *struct {
			Usage *struct {
				InputTokens        int64 `json:"input_tokens"`
				InputTokensDetails *struct {
					CachedTokens int64 `json:"cached_tokens"`
				} `json:"input_tokens_details"`
				OutputTokens int64 `json:"output_tokens"`
			} `json:"usage"`
		} `json:"response"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &payload) != nil || payload.Response == nil || payload.Response.Usage == nil {
		return nil
	}
	usage := payload.Response.Usage
	input := int(usage.InputTokens)
	hit := 0
	if usage.InputTokensDetails != nil {
		hit = int(usage.InputTokensDetails.CachedTokens)
	}
	if input <= 0 && usage.OutputTokens <= 0 {
		return nil
	}
	miss := input - hit
	if miss < 0 {
		miss = 0
	}
	return &modelUsage{
		PromptTokens:     input,
		CompletionTokens: int(usage.OutputTokens),
		CacheHitTokens:   hit,
		CacheMissTokens:  miss,
	}
}

func modelUsageFromAnthropic(usage anthropic.Usage) *modelUsage {
	input := usage.InputTokens + usage.CacheCreationInputTokens + usage.CacheReadInputTokens
	output := usage.OutputTokens
	if input <= 0 && output <= 0 {
		return nil
	}
	return &modelUsage{
		PromptTokens:     int(input),
		CompletionTokens: int(output),
		CacheHitTokens:   int(usage.CacheReadInputTokens),
		CacheMissTokens:  int(usage.InputTokens + usage.CacheCreationInputTokens),
	}
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

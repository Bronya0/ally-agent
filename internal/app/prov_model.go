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
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"ally-dev/internal/tools/schemautil"
	"ally-dev/internal/tools/toolcall"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	oa "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/ssestream"
	oaresp "github.com/openai/openai-go/v3/responses"
	legacyopenai "github.com/sashabaranov/go-openai"
)

type modelUsage struct {
	PromptTokens     int
	CompletionTokens int
	CacheHitTokens   int
	CacheMissTokens  int
	// CacheWriteTokens is what the provider reports as written to its prompt cache
	// — Anthropic cache_creation_input_tokens, OpenAI Responses
	// cache_write_tokens. It is an observation, not a third bucket: on the native
	// protocols the written tokens are already inside miss, so the hit rate stays
	// Σhit/Σ(hit+miss). It exists to price extended retention (pi reports the same
	// split as usage.cacheWrite / cacheWrite1h, ai/src/types.ts:386-389). Chat
	// Completions reports no cache-write counter, so it stays 0 there.
	CacheWriteTokens int
}

var errEmptyModelResponse = errors.New("empty model response")

// errChatStreamNoFinishReason 表示流结束了却没收到任何显式终止标记
// (finish_reason 或 `[DONE]`)。半截流必须按失败处理:已产出的内容由上层
// 整轮重试（丢弃半截响应）接管,而不是当作完整回合执行。
var errChatStreamNoFinishReason = errors.New("stream ended without finish_reason")

type modelStreamEvent struct {
	ContentDelta   string
	ReasoningDelta string
	// ReasoningPresentDelta marks a provider that explicitly emitted an
	// (possibly empty) reasoning field, so the replay layer can distinguish
	// "no thinking" from "thinking present but empty".
	ReasoningPresentDelta bool
	ToolCalls             []legacyopenai.ToolCall
	Image                 *modelImage
	Retry                 *modelRetryInfo
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
	MaxAttempts int // 最大重试次数(不含首次)。所有路径统一取自"LLM 请求
	// 重试次数"设置(effectiveLLMRetries):单 key 适配器内退避重试、
	// 多 key 故障转移、runChat 流中断整轮重试共用同一预算。
	Error     string // 触发重试的错误信息
	WaitMS    int    // 重试前等待毫秒数
	KeyIndex  int    // 失败/切换涉及的 key 序号(0 基),0 表示未知(单 key 或适配器内重试)
	TotalKeys int    // key 池总数,0 表示未知
}

// llmErrorKind 是 LLM 请求错误的语义分类，单一定义点。适配器拿到 HTTP 状态
// 码/错误码时就地归类（typed 路径，见 classifyLLMError），provider 文案不可控
// 时才落到关键词匹配（fallback 路径）。shouldRetryLLMError / isAuthKeyError /
// isProvider400Error 都消费这个枚举，不再各自维护一份关键词黑/白清单、也不再
// 靠注释承诺同步。
type llmErrorKind int

const (
	llmErrorKindUnknown          llmErrorKind = iota
	llmErrorKindRateLimited                   // 429/限流:瞬时,重试同一 key 意义有限但无害
	llmErrorKindAuth                          // 401/403/认证类:重试同一 key 无意义
	llmErrorKindBilling                       // 402/配额耗尽:需人工处理,key 级长冷却
	llmErrorKindContextTooLong                // 上下文超长:确定性失败,重试必然同样失败
	llmErrorKindModelNotFound                 // 404/模型不存在:确定性失败
	llmErrorKindDeterministic400              // 其它 400:上下文毒化,sanitize 后可恢复
)

// llmStreamEventDecodeError 标记流式事件 JSON 解析失败(流内 SSE 事件截断或
// 形状损坏)。原实现靠 Go encoding/json 的报错文案 "Expecting ',' delimiter"
// 恰好出现在错误链里误判为 provider 400 触发 sanitize;现在适配器在 decode
// 失败处包上本错误,isProvider400Error 直接判型。
type llmStreamEventDecodeError struct{ err error }

func (e *llmStreamEventDecodeError) Error() string { return e.err.Error() }
func (e *llmStreamEventDecodeError) Unwrap() error { return e.err }

func wrapLLMStreamEventDecode(err error) error {
	if err == nil {
		return nil
	}
	return &llmStreamEventDecodeError{err: err}
}

// providerHTTPStatusCode 从常见 provider SDK 错误类型中提取 HTTP 状态码，
// 同时消除 isProvider400Error / 分类函数里三份重复的 errors.As 链。
func providerHTTPStatusCode(err error) (int, bool) {
	var legacyReqErr *legacyopenai.RequestError
	if errors.As(err, &legacyReqErr) {
		return legacyReqErr.HTTPStatusCode, true
	}
	var oaErr *oa.Error
	if errors.As(err, &oaErr) {
		return oaErr.StatusCode, true
	}
	var anthropicErr *anthropic.Error
	if errors.As(err, &anthropicErr) {
		return anthropicErr.StatusCode, true
	}
	return 0, false
}

// classifyLLMError 把错误归入 llmErrorKind。typed 状态码优先；状态码不可得
// 时才走关键词匹配（provider/中继文案不可控，关键词表只作为最终兑底保留，
// 且只在这一处存在）。context 取消/超时返回 Unknown——它是调用层的控制流，
// 不是 LLM 错误语义。
func classifyLLMError(err error) llmErrorKind {
	if err == nil {
		return llmErrorKindUnknown
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return llmErrorKindUnknown
	}
	var decodeErr *llmStreamEventDecodeError
	if errors.As(err, &decodeErr) {
		// 流内事件损坏:产出零输出时上层可重试,但不是 provider 400——
		// sanitize 历史救不了网络流。文案兜底也不得把它归类为 400。
		return llmErrorKindUnknown
	}
	msg := strings.ToLower(err.Error())
	if status, ok := providerHTTPStatusCode(err); ok {
		switch {
		case status == 401 || status == 403:
			return llmErrorKindAuth
		case status == 402:
			return llmErrorKindBilling
		case status == 404:
			return llmErrorKindModelNotFound
		case status == 429:
			// OpenAI and Moonshot (Kimi) return HTTP 429 for account balance/quota exhaustion
			// (insufficient_quota, exceeded_current_quota_error, insufficient balance).
			// Such errors are fatal account issues, not transient rate limits, and must not be retried.
			if llmErrorTextMatchesAny(msg, llmBillingMarkers) {
				return llmErrorKindBilling
			}
			return llmErrorKindRateLimited
		case status == 400:
			// 400 的细分必须在 typed 分支里完成：关键词阶段在本分支之后，而这里
			// 会 return，否则上下文超长/模型不存在永远分不到自己的枚举（注释曾
			// 说交给关键词阶段补充，实际上走不到）。重试策略不受影响——
			// shouldRetryLLMError 对这三种都返回 false；但 overflow 恢复与
			// sanitize 以外的处理依赖这个细分。
			if llmContextTooLongPattern.MatchString(msg) {
				return llmErrorKindContextTooLong
			}
			if llmErrorTextMatchesAny(msg, llmModelNotFoundMarkers) {
				return llmErrorKindModelNotFound
			}
			return llmErrorKindDeterministic400
		}
	}
	if llmErrorTextMatchesAny(msg, llmBillingMarkers) {
		return llmErrorKindBilling
	}
	if llmErrorTextMatchesAny(msg, llmRateLimitMarkers) {
		return llmErrorKindRateLimited
	}
	if llmErrorTextMatchesAny(msg, llmAuthMarkers) {
		return llmErrorKindAuth
	}
	if llmContextTooLongPattern.MatchString(msg) {
		return llmErrorKindContextTooLong
	}
	if llmErrorTextMatchesAny(msg, llmModelNotFoundMarkers) {
		return llmErrorKindModelNotFound
	}
	return llmErrorKindUnknown
}

// llmErrorTextMatchesAny reports whether msg contains any of the markers.
func llmErrorTextMatchesAny(msg string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// 关键词兑底表(全部小写)。这是 provider 文案匹配的唯一一份清单:原实现里
// shouldRetryLLMError 与 isAuthKeyError 各持一份且靠注释承诺人工同步,新文案
// 只需要加到这里。
var (
	llmAbortTextMarkers = []string{
		"context deadline exceeded", "context canceled", "context was canceled",
	}
	llmBillingMarkers = []string{
		"402", "insufficient_quota", "insufficient_balance", "payment required",
		"exceeded_current_quota_error", "check your account balance", "recharge your account",
		"please recharge", "account is in arrears", "account in arrears",
	}
	llmRateLimitMarkers = []string{
		"429", "too many requests", "rate limit", "rate exceeded", "rate_limit",
	}
	llmAuthMarkers = []string{
		"401", "403", "invalid api key", "invalid_api_key", "invalid-api-key",
		"invalid key", "api key", "api_key", "unauthorized", "authentication failed",
		"not authorized", "permission denied", "permission", "forbidden",
		"access denied", "credential",
	}
	llmModelNotFoundMarkers = []string{
		"model not found", "no such model", "does not exist", "not_found",
		"status code: 404", "404 not found",
	}
)

// llmContextTooLongPattern 识别"上下文超长"。按 pi 的做法用正则而不是纯子串
// (utils/overflow.ts:37-60):同一件事各厂商措辞差异很大（“prompt is too long”、
// “request_too_large”、“input token count … exceeds the maximum”、“maximum
// prompt length is 8192”、“reduce the length of the messages”），子串表每漏一个
// 变体，代价就是拿同一个超大请求白重试 LLMRetries 次（退避 0.5s→10s）后才报错。
// 文本在 classifyLLMError 里已小写化，所以模式统一小写。
var llmContextTooLongPattern = regexp.MustCompile(strings.Join([]string{
	`prompt is too long`,                                    // Anthropic token overflow
	`request_too_large`,                                     // Anthropic request-size overflow (HTTP 413)
	`request too large`,                                     // 兼容旧表的空格写法
	`input is too long for requested model`,                 // Amazon Bedrock
	`exceeds (?:the )?(?:model'?s )?maximum context length`, // OpenAI / LiteLLM
	`exceeds the context window`,                            // OpenAI (Completions & Responses)
	`input token count.*exceeds the maximum`,                // Google Gemini
	`maximum prompt length is \d+`,                          // xAI Grok
	`reduce the length of the messages`,                     // Groq
	`context[_ ]length`,                                     // 旧表: context length / context_length
	`maximum context`,                                       // 旧表
	`context window`,                                        // 旧表
	`too many input tokens`,                                 // 旧表
	`maximum_prompt_size`,                                   // 旧表
	`input length`,                                          // 旧表
}, "|"))

// shouldRetryLLMError 判断错误是否值得重试。
// 策略是"默认重试 + 明确排除确定性失败":中转/服务商的瞬时错误文案千奇百怪
// ("Rate exceeded"、"Service temporarily overloaded"、...),白名单每遇到一个
// 新文案就漏一个、直接中断整个会话;分类错误的代价只是多等几次有上限的退避。
// 分类本身已收敛到 classifyLLMError 的单一枚举。
func shouldRetryLLMError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errEmptyModelResponse) {
		return true
	}
	switch classifyLLMError(err) {
	case llmErrorKindUnknown:
		// 取消/超时的文本形态(中转或 SDK 把 context 错误转成纯字符串、丢失
		// 错误链)不可重试:请求已被上游放弃,重试只会浪费时间。其余未知
		// 情形(网络抖动/中转自定义文案/未知错误)默认按瞬时错误重试。
		msg := strings.ToLower(err.Error())
		return !llmErrorTextMatchesAny(msg, llmAbortTextMarkers)
	case llmErrorKindRateLimited:
		return true
	case llmErrorKindAuth, llmErrorKindBilling, llmErrorKindContextTooLong, llmErrorKindModelNotFound:
		return false
	case llmErrorKindDeterministic400:
		// 400 可能是可修复的上下文毒化(runChat sanitize 路径接管),不能
		// 简单当作 "确定性失败不重试" 也不当瞬时错误盲重——交由调用方
		// sanitize 决定;此处按不可直接重试处理,与旧行为一致(旧实现的
		// 400 文案不在任何重试/非重试清单里,默认重试过,但 sanitize 优先
		// 于重试且不占预算,行为不变)。
		return false
	}
	return false
}

func emptyModelResponseError(result *modelStreamResult) error {
	if result == nil {
		return errEmptyModelResponse
	}
	if strings.TrimSpace(result.Content) == "" && strings.TrimSpace(result.Reasoning) == "" && len(result.ToolCalls) == 0 && len(result.Images) == 0 {
		return errEmptyModelResponse
	}
	return nil
}

// isAuthKeyError 判断错误是否属于认证/配额类(key 本身失效):这类错误重试同一
// key 无意义,应切换或直接失败(配 key 池的长冷却)。
//
// 判定完全交给 classifyLLMError 的单一枚举(HTTP 状态码优先,provider 文案关键词
// 兜底),这里只做“该分类是否 key 级故障”的映射:认证与计费是,限流/未知不是。
// 旧实现在这里另持一份裸数字子串匹配("402"/"401"/"403"/"429"),报文里任何位置
// 出现这些数字——输入长度 4021、请求 id req_402…——都会把健康 key 判为失效并冻结
// 30 分钟,而 429 与计费文案的歧义也要和 shouldRetryLLMError 各维护一遍。
func isAuthKeyError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	switch classifyLLMError(err) {
	case llmErrorKindAuth, llmErrorKindBilling:
		return true
	default:
		return false
	}
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
	Content   string
	Reasoning string
	// ReasoningPresent reports that the provider explicitly produced a
	// reasoning payload this response — including an explicitly EMPTY one
	// (DeepSeek V4 emits reasoning_content:"" on obvious tool calls). Empty
	// reasoning must still be replayed as a present field by thinking-mode
	// providers, so "" + true and "" + false carry different replay duties.
	ReasoningPresent bool
	ToolCalls        []legacyopenai.ToolCall
	Images           []modelImage
	Usage            *modelUsage
	StopReason       string
	StopSequence     string
}

func (a *App) completeModelText(ctx context.Context, cfg ConfigState, model string, messages []legacyopenai.ChatCompletionMessage, maxTokens int) (string, error) {
	text, _, err := a.completeModelTextWithUsage(ctx, cfg, model, messages, maxTokens)
	return text, err
}

// completeModelTextWithUsage is completeModelText plus the provider-reported
// token usage, so non-chat flows (compaction, connectivity tests) can account
// the call in the workspace/token statistics with the same fidelity as the
// chat loop. Callers that ignore usage keep the old single-value signature.
func (a *App) completeModelTextWithUsage(ctx context.Context, cfg ConfigState, model string, messages []legacyopenai.ChatCompletionMessage, maxTokens int) (string, *modelUsage, error) {
	next := cfg
	next.MaxTokens = maxTokens
	// Like normal chat, stream the response and capture all content/reasoning deltas
	var contentBuilder strings.Builder
	var reasoningBuilder strings.Builder
	result, err := a.streamModelResponse(ctx, next, model, messages, nil, func(event modelStreamEvent) {
		if event.ContentDelta != "" {
			contentBuilder.WriteString(event.ContentDelta)
		}
		if event.ReasoningDelta != "" {
			reasoningBuilder.WriteString(event.ReasoningDelta)
		}
	})
	if err != nil {
		return "", nil, err
	}
	if err := modelResponseStopError(next, result); err != nil {
		return "", nil, err
	}
	content := strings.TrimSpace(result.Content)
	if content == "" {
		content = strings.TrimSpace(contentBuilder.String())
	}
	if content == "" {
		content = strings.TrimSpace(result.Reasoning)
	}
	if content == "" {
		content = strings.TrimSpace(reasoningBuilder.String())
	}
	if content == "" && len(result.ToolCalls) > 0 {
		// The model decided to call tools (e.g. attempting to read files or call ask/done)
		// instead of outputting direct text. Extract argument text or summarize tool calls.
		var toolCallParts []string
		for _, tc := range result.ToolCalls {
			if strings.TrimSpace(tc.Function.Arguments) != "" {
				toolCallParts = append(toolCallParts, tc.Function.Arguments)
			}
		}
		content = strings.TrimSpace(strings.Join(toolCallParts, "\n"))
	}
	if content == "" {
		details := fmt.Sprintf("result.Content=%q, builder=%q, reasoning=%q, rBuilder=%q, stopReason=%q, toolCalls=%d",
			result.Content, contentBuilder.String(), result.Reasoning, reasoningBuilder.String(), result.StopReason, len(result.ToolCalls))
		log.Printf("[compaction] debug: empty summary! %s", details)
		return "", nil, fmt.Errorf("compaction returned empty summary (%s)", details)
	}
	return content, result.Usage, nil
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
		result, err := a.streamModelResponseWithKey(ctx, cfg, model, messages, tools, onEvent)
		if err != nil {
			err = wrapProviderRequestError(err)
		}
		return result, err
	}
	// 多 key:固定优先级故障转移 + 冷却。每次尝试从第一个可用(不在冷却)
	// 的 key 开始;失败后按错误类别记录冷却(认证/配额 30min,瞬时 10s)并顺延
	// 到下一个,直到成功或全部失败。重试预算统一由"LLM 请求重试次数"设置
	// 决定(单一来源):总重试次数上限 = effectiveLLMRetries(cfg),不再按
	// key 池大小截断,且通过 noAdapterRetry 关闭适配器内退避重试,由本循环
	// 统一承担重试与轮换,避免 N 个 key × 适配器重试组合爆炸。
	// 已发射任何流事件(文本/推理/工具调用/图片)后禁止切换,避免重复输出
	// ——与适配器内 mid-stream 不重试的既有约定一致。
	llmRetries := effectiveLLMRetries(cfg) // 此处 cfg 未设置 noAdapterRetry,返回用户设置/默认值
	startedAllCooling := a.isKeyCoolingDown(cfg, keys[a.firstUsableKeyIndex(cfg, keys)])
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
	retries := 0
	probedAllCooling := false
	// 防御性迭代上限:正常路径请求次数 ≤ 首次 + N 次重试 + 1 次探测;
	// 余量容纳并发调用延长冷却导致的少量纯等待轮次,杜绝无限循环。
	maxIterations := llmRetries + 2*len(keys) + 2
	for iter := 0; iter < maxIterations; iter++ {
		idx := a.firstUsableKeyIndex(cfg, keys)
		key := keys[idx]
		isProbe := false
		if a.isKeyCoolingDown(cfg, key) {
			// firstUsableKeyIndex 只在全部 key 冷却时返回冷却中的 key。
			// 冷却只是本地启发式,不能替代服务端裁决:仍用最早到期的 key
			// 探测一次,避免错误分类(如把限流 429 误判为配额失效)把整个
			// key 池冻结到冷却结束、只能重启恢复。
			if !probedAllCooling {
				probedAllCooling = true
				isProbe = true
				idx = a.earliestCooldownKeyIndex(cfg, keys)
				key = keys[idx]
			} else if !startedAllCooling {
				// 本次调用内 key 已全部失败进入冷却:等待最早到期的瞬时
				// 冷却(≤10s)过去后继续用满重试预算——限流类错误通常等待
				// 即可恢复。长冷却(认证/配额 30min)重试同样失败,不等待。
				wait, ok := a.earliestCooldownWait(cfg, keys)
				if !ok || wait > keyTransientCooldownDuration {
					break
				}
				select {
				case <-time.After(wait):
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				idx = a.firstUsableKeyIndex(cfg, keys) // 冷却到期的 key 已可用
				key = keys[idx]
			} else {
				// 起始即全部冷却(上次调用留下的冷却记录):只做一次有界
				// 探测,失败立即返回真实的服务端错误,不把用户困在等待里。
				break
			}
		}
		callCfg := cfg
		callCfg.APIKey = key
		callCfg.noAdapterRetry = true // 外层循环统一处理重试与轮换
		result, err := a.streamModelResponseWithKey(ctx, callCfg, model, messages, tools, wrappedOnEvent)
		if err == nil {
			return result, nil
		}
		err = wrapProviderRequestError(err)
		lastErr = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !(!emitted && shouldFailoverKey(err)) {
			return nil, err
		}
		cooldown := keyTransientCooldownDuration
		if isAuthKeyError(err) {
			cooldown = keyAuthCooldownDuration
		}
		a.recordKeyFailure(cfg, key, cooldown)
		if isProbe {
			// 探测失败不算用户设置语义内的重试,不发重试事件;是否继续
			// 由下一轮的冷却分支决定(等待冷却或终止)。
			continue
		}
		if retries >= llmRetries {
			// 预算用尽:最后的失败不再发"重试"事件,失败由 run:error
			// 呈现——与单 key 适配器路径的语义一致。
			break
		}
		retries++
		wait := time.Duration(0)
		if !probedAllCooling {
			// 瞬时错误(429/5xx/网络):切换前短暂退避,避免多个 key 同时
			// 打向同一故障端点。全部冷却后的等待由上面的冷却分支承担,
			// 不再叠加退避。
			wait = llmRetryDelay(retries)
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		emitLLMRetryEventForKey(onEvent, retries, llmRetries, err, wait, idx, len(keys))
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("all API keys are cooling down, try again later")
	}
	return nil, lastErr
}

// providerRequestError 重新格式化 go-openai 的 RequestError,消除库在错误体
// 没有 message 字段时输出的 "%!s(<nil>)" 伪影(部分中转返回
// {"object":"error",...} 之类没有 message 的错误体)。保留原始错误链,字符串
// 形态与库保持一致("error, status code: ... 429 ..."),重试/切换分类
// (shouldFailoverKey 等)基于这些关键字,不受包装影响。
type providerRequestError struct {
	inner error
	msg   string
}

func (e *providerRequestError) Error() string { return e.msg }
func (e *providerRequestError) Unwrap() error { return e.inner }

// providerErrorMessageFromBody 尽力从错误响应体提取人类可读信息:顶层
// message、error.message,或短非 JSON 体的原文预览。
func providerErrorMessageFromBody(body []byte) string {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return ""
	}
	var top struct {
		Message string          `json:"message"`
		Error   json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &top) == nil {
		if top.Message != "" {
			return top.Message
		}
		var inner struct {
			Message string `json:"message"`
		}
		if top.Error != nil && json.Unmarshal(top.Error, &inner) == nil && inner.Message != "" {
			return inner.Message
		}
		return ""
	}
	// 非 JSON 体:短则原文预览,长则截断。
	if len(trimmed) <= 200 {
		return trimmed
	}
	return trimmed[:200] + "..."
}

// wrapProviderRequestError 把 Err 为 nil 的 RequestError 重新包成可读消息;
// 其他错误原样返回(nil 也原样返回)。
func wrapProviderRequestError(err error) error {
	if err == nil {
		return nil
	}
	var reqErr *legacyopenai.RequestError
	if !errors.As(err, &reqErr) || reqErr.Err != nil {
		return err
	}
	msg := providerErrorMessageFromBody(reqErr.Body)
	if msg == "" {
		msg = "(服务商未返回错误说明)"
	}
	return &providerRequestError{
		inner: err,
		msg: fmt.Sprintf("error, status code: %d, status: %s, message: %s, body: %s",
			reqErr.HTTPStatusCode, reqErr.HTTPStatus, msg, string(reqErr.Body)),
	}
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
const keyAuthCooldownDuration = 30 * time.Minute

// keyTransientCooldownDuration 是瞬时错误(429/5xx/网络)后的冷却窗口。比
// 认证错误短,避免端点短暂故障时把整个 key 池冷却 30 分钟(fail-fast 但快速自愈)。
const keyTransientCooldownDuration = 10 * time.Second

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

// recordKeyFailure 将 key 置入冷却窗口,窗口长度由错误类别决定(认证/计费
// 30min,瞬时错误 10s)。
func (a *App) recordKeyFailure(cfg ConfigState, key string, cooldown time.Duration) {
	a.keyStateMu.Lock()
	a.keyCooldowns[keyCooldownID(cfg, key)] = time.Now().Add(cooldown)
	a.keyStateMu.Unlock()
}

// earliestCooldownKeyIndex 返回冷却到期最早的 key 序号。仅在全部 key 都在
// 冷却时由多 key 循环调用,用于挑选"最接近恢复"的 key 做一次探测。
func (a *App) earliestCooldownKeyIndex(cfg ConfigState, keys []string) int {
	a.keyStateMu.Lock()
	defer a.keyStateMu.Unlock()
	best := 0
	var bestUntil time.Time
	for i, key := range keys {
		until, ok := a.keyCooldowns[keyCooldownID(cfg, key)]
		if !ok {
			return i // 不在冷却的 key(防御性:正常不会走到这里)
		}
		if i == 0 || until.Before(bestUntil) {
			bestUntil = until
			best = i
		}
	}
	return best
}

// earliestCooldownWait 返回最早到期的冷却剩余等待时间,以及是否存在冷却
// 记录。仅在全部 key 都在冷却时由多 key 循环调用,用于决定等待重试或
// 终止(长冷却如认证 30min 不值得等待)。
func (a *App) earliestCooldownWait(cfg ConfigState, keys []string) (time.Duration, bool) {
	a.keyStateMu.Lock()
	defer a.keyStateMu.Unlock()
	best := time.Duration(0)
	found := false
	for _, key := range keys {
		until, ok := a.keyCooldowns[keyCooldownID(cfg, key)]
		if !ok {
			continue
		}
		remaining := time.Until(until)
		if remaining < 0 {
			remaining = 0
		}
		if !found || remaining < best {
			best = remaining
			found = true
		}
	}
	return best, found
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

// openAIReasoningModelPattern matches OpenAI reasoning-series model names that
// require max_completion_tokens instead of max_tokens. It mirrors the anchored
// patterns of kimi-code (/^o\d(?:$|[-.])/ and /^gpt-5(?:$|[-.])/,
// openai-legacy.ts:130-133): a family prefix without a version boundary
// ("gpt-50", "o1preview") must NOT match, while every o-digit series ("o2",
// "o5", ...) must. Provider-routing prefixes such as "openai/o3-mini" are
// stripped first (pi routes by endpoint/model catalog instead of the name;
// stripping keeps the predicate correct for gateway-style model ids).
var openAIReasoningModelPattern = regexp.MustCompile(`^(?:o\d|gpt-5)(?:$|[-.])`)

func isOpenAIReasoningModelName(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(m, "/"); i >= 0 && i+1 < len(m) {
		m = m[i+1:]
	}
	return openAIReasoningModelPattern.MatchString(m)
}

// modelSupportsReasoningEffort reports whether the reasoning-effort parameter may
// be sent for this model. pi gates every thinking/effort branch on the model
// catalog's reasoning flag and only writes the field when the model declares
// reasoning support (openai-completions.ts:864-935) — the official endpoints
// reject an effort value the target model does not accept. Ally has no catalog
// flag, so the positive signals are the model name (the OpenAI o*/gpt-5*
// families) and a configured reasoning tag: a non-empty tag means the model was
// observed to stream a reasoning field, which only reasoning-capable models do.
// Unknown combinations stay unset instead of risking a rejected request.
func modelSupportsReasoningEffort(cfg ConfigState, model string) bool {
	return isOpenAIReasoningModelName(model) || strings.TrimSpace(cfg.ReasoningTag) != ""
}

func shouldUseMaxCompletionTokens(param, model string) bool {
	switch normalizeTokenParam(param) {
	case tokenParamMaxCompletionTokens:
		return true
	case tokenParamMaxTokens:
		return false
	default:
		return isOpenAIReasoningModelName(model)
	}
}

func (a *App) streamOpenAIChat(ctx context.Context, cfg ConfigState, model string, messages []legacyopenai.ChatCompletionMessage, tools []legacyopenai.Tool, onEvent func(modelStreamEvent)) (*modelStreamResult, error) {
	clientCfg := legacyopenai.DefaultConfig(cfg.APIKey)
	clientCfg.BaseURL = baseURLForAPIFormat(cfg)
	clientCfg.HTTPClient = modelHTTPClient(cfg, true, 0)
	// Request rewrite: the Chat adapter always installs the rewriting transport,
	// because two of its normalizations apply to every request — `content` must
	// be a present key on tool-call/tool messages (see
	// chatRequestRewriteTransport) and OpenRouter-style sticky-session headers
	// are attached there. The reasoning backfill (an explicit, possibly empty
	// reasoning field on every assistant message) is written for every endpoint
	// except the official OpenAI API, and only while the selected level leaves
	// thinking on: without it DeepSeek V4 / Kimi K3 reject a request whose
	// assistant messages omit the field, while "off" has nothing to hand back
	// (chatReasoningBackfillKey).
	replayKey := reasoningReplayKey(cfg, model)
	var turnDetails map[string]json.RawMessage
	if payload := a.reasoningStash.get(replayKey); payload != nil {
		turnDetails = payload.chatDetailsByCallID()
	}
	reasoningKey := chatReasoningBackfillKey(cfg)
	// "off" reaches a compatible endpoint as the stop-thinking field the body
	// rewrite adds below; the official endpoint spells it as an effort value
	// instead (see reasoningWireForAdapter).
	reasoningWire := reasoningWireForAdapter(cfg, apiFormatOpenAIChat, cfg.ReasoningEffort)
	disableThinking := reasoningWire.DisableThinking && modelSupportsReasoningEffort(cfg, model)
	streamDone := &sseDoneWatcher{}
	base := modelHTTPClient(cfg, true, 0)
	rt := base.Transport
	if rt == nil {
		rt = http.DefaultTransport
	}
	base.Transport = &chatRequestRewriteTransport{
		base:            rt,
		reasoningKey:    reasoningKey,
		turnDetails:     turnDetails,
		headers:         sessionAffinityHeaders(cfg),
		promptCacheKey:  openAIChatPromptCacheKey(cfg),
		streamDone:      streamDone,
		disableThinking: disableThinking,
	}
	clientCfg.HTTPClient = base
	client := legacyopenai.NewClientWithConfig(clientCfg)

	streamReq := legacyopenai.ChatCompletionRequest{
		Model:         model,
		Messages:      messages,
		StreamOptions: &legacyopenai.StreamOptions{IncludeUsage: true},
	}
	// Route the token limit to the field the target provider accepts. Both
	// fields are `omitempty`, so only the selected one is serialized — never
	// both. "auto" automatically routes to max_completion_tokens for OpenAI
	// o-series and newer models (which reject max_tokens with a 400 error),
	// and uses max_tokens for other models. Explicit "max_tokens" or
	// "max_completion_tokens" overrides auto-detection.
	if shouldUseMaxCompletionTokens(cfg.TokenParam, model) {
		streamReq.MaxCompletionTokens = cfg.MaxTokens
	} else {
		streamReq.MaxTokens = cfg.MaxTokens
	}
	// Thinking strength: send an effort value only when a level was picked AND
	// the model accepts the parameter. The normalized selection is sent
	// unchanged — xhigh and max are declared values of the OpenAI SDK enum
	// (shared.ReasoningEffortXhigh / ReasoningEffortMax) — while "auto" and
	// non-reasoning models send nothing (see modelSupportsReasoningEffort and
	// reasoningWireForAdapter).
	if reasoningWire.Effort != "" && modelSupportsReasoningEffort(cfg, model) {
		streamReq.ReasoningEffort = reasoningWire.Effort
	}
	if len(tools) > 0 {
		streamReq.Tools = normalizeToolsForOpenAIChat(tools)
	}
	// Note: Do NOT set streamReq.ToolChoice = "none" when len(tools) == 0.
	// The official OpenAI Chat Completions API specification (and compatible
	// gateways like Azure, Groq, DeepSeek) strictly rejects requests with
	// HTTP 400 ("'tool_choice' cannot be set without 'tools'") when tools is
	// omitted or empty. When no tools are provided, models cannot execute tools anyway.

	maxRetries := effectiveLLMRetries(cfg)
	result, emitted, err := a.openAIChatStreamAttempt(ctx, cfg, client, streamReq, streamDone, onEvent)
	// 只重试"尚未产出任何输出"的失败:建流失败,或消费阶段在产出内容前
	// 失败(中转常以 HTTP 200 建流,再以流内 {"error":...} 事件返回 529
	// overloaded 之类瞬时错误,错误信息只有文案、不带状态码)。此时重试
	// 无重复输出风险;已产出内容的中断交给上层 runChat 做整轮重试。
	for attempt := 1; err != nil && !emitted && ctx.Err() == nil && attempt <= maxRetries && shouldRetryLLMError(err); attempt++ {
		wait := llmRetryDelay(attempt)
		emitLLMRetryEvent(onEvent, attempt, maxRetries, err, wait)
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		result, emitted, err = a.openAIChatStreamAttempt(ctx, cfg, client, streamReq, streamDone, onEvent)
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

// openAIChatStreamAttempt 执行一次完整的 OpenAI Chat 建流与消费。第二个
// 返回值报告本次尝试是否已产出输出(assistant/reasoning/toolCalls 任一非空),
// 调用方据此判定适配器内重试是否安全(无输出则重试不会造成重复)。
func (a *App) openAIChatStreamAttempt(ctx context.Context, cfg ConfigState, client *legacyopenai.Client, streamReq legacyopenai.ChatCompletionRequest, streamDone *sseDoneWatcher, onEvent func(modelStreamEvent)) (*modelStreamResult, bool, error) {
	// The terminator watcher is owned by the caller and therefore survives this
	// attempt's retries. A `[DONE]` that a failed attempt read off the wire must
	// not be inherited by the next one: the retry's connection can be cut
	// mid-stream, and a stale terminator would make that truncated response pass
	// the {finish_reason, [DONE]} check and be persisted as a complete turn.
	streamDone.reset()
	stream, err := client.CreateChatCompletionStream(ctx, streamReq)
	if err != nil && isStreamOptionsRejectedError(err) {
		streamReq.StreamOptions = nil
		log.Printf("[llm] gateway rejected stream_options; retrying without it: %v", err)
		stream, err = client.CreateChatCompletionStream(ctx, streamReq)
	}
	if err != nil {
		return nil, false, err
	}
	defer stream.Close()

	var assistant strings.Builder
	var reasoning strings.Builder
	// mergedDetails accumulates OpenRouter-style reasoning_details across the
	// stream so the next request of this tool loop can replay them.
	mergedDetails := []map[string]any{}
	// reasoningPresent: the provider explicitly emitted a reasoning field
	// (possibly empty). go-openai unmarshals an absent field and an explicit
	// empty string identically, so this only turns true once non-empty
	// reasoning text arrives; the wire field for the next request is filled by
	// the rewrite transport instead (see chatReasoningBackfillKey).
	reasoningPresent := false
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
	// toolAcc 把流式增量归入对应调用;它必须在整个流期间保持存活,否则只带
	// index、不带 id 的增量无法跨块匹配。id 表先由对话历史播种:中转跨回合复用
	// 同一个 id 时会被改写成唯一 id,而不是让下一个请求带上重复的 tool_call id。
	toolAcc := newToolCallAccumulator(nil)
	toolAcc.seedConversation(streamReq.Messages)
	toolCalls := []legacyopenai.ToolCall{}
	toolEventGate := newModelToolCallEventGate(func(event modelStreamEvent) {
		emitModelStreamEvent(onEvent, event)
	})
	var usage *modelUsage
	gotFinishReason := false
	stopReason := ""
	hasOutput := func() bool {
		return assistant.Len() > 0 || reasoning.Len() > 0 || len(toolCalls) > 0
	}
	for {
		raw, err := stream.RecvRaw()
		if errors.Is(err, io.EOF) {
			// 终止判定与另外两个适配器一致:必须收到显式终止标记。go-openai 把
			// `[DONE]` 与"连接在帧边界被切断"归一成同一个 io.EOF,所以这里同时
			// 接受真实的 `[DONE]`(由 sseDoneReadCloser 记录)与 finish_reason;
			// 两者都没有就是半截流。已产出内容时返回 hasOutput()=true,适配器内
			// 不再重试（避免重复输出）,由上层 runChat 整轮重试。
			if !gotFinishReason && (streamDone == nil || !streamDone.Done()) {
				return nil, hasOutput(), errChatStreamNoFinishReason
			}
			break
		}
		if err != nil {
			// 流内错误事件(如中转的 529 overloaded):错误信息只有文案、
			// 不带状态码。是否重试由调用方按"是否已产出输出"统一判定。
			return nil, hasOutput(), err
		}
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 {
			continue
		}
		var resp legacyopenai.ChatCompletionStreamResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, hasOutput(), wrapLLMStreamEventDecode(fmt.Errorf("decode chat stream event: %w", err))
		}
		if resp.Usage != nil {
			usage = modelUsageFromLegacy(resp.Usage, raw)
		} else if len(resp.Choices) > 0 && len(raw) > 0 {
			if choiceUsage := extractChoiceUsageFromRaw(raw); choiceUsage != nil {
				usage = modelUsageFromLegacy(choiceUsage, raw)
			}
		}
		if len(resp.Choices) == 0 {
			continue
		}
		delta := resp.Choices[0].Delta
		if resp.Choices[0].FinishReason != "" {
			gotFinishReason = true
			stopReason = string(resp.Choices[0].FinishReason)
		}
		// Structured reasoning (OpenRouter reasoning_details, vLLM reasoning)
		// arrives in every chunk: the text feeds the thinking panel, the detail
		// objects are merged for replay on the next request of this loop.
		streamReasoningText, streamReasoningDetails := parseStreamReasoning(raw)
		for _, detail := range streamReasoningDetails {
			mergedDetails = appendChatReasoningDetail(mergedDetails, detail)
		}
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
			reasoningText := delta.ReasoningContent
			if reasoningText == "" {
				reasoningText = streamReasoningText
			}
			if reasoningText != "" {
				reasoning.WriteString(reasoningText)
				reasoningPresent = true
				emitModelStreamEvent(onEvent, modelStreamEvent{ReasoningDelta: reasoningText})
			}
		}
		if len(delta.ToolCalls) > 0 {
			toolCalls = toolAcc.merge(delta.ToolCalls)
			toolEventGate.emit(modelStreamEvent{ToolCalls: toolCalls})
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

	// Record this response's OpenRouter-style reasoning details as one turn of
	// the session ledger, keyed by the turn's tool-call ids: every later
	// request replays them on this turn's own assistant message, so the
	// message prefix stays byte-identical across agent steps (the key is
	// model-scoped). A response without details records no turn; earlier
	// turns keep theirs.
	if detailsJSON := chatReasoningDetailsJSON(mergedDetails); len(detailsJSON) > 0 && len(toolCalls) > 0 {
		a.reasoningStash.appendTurn(reasoningReplayKey(cfg, streamReq.Model), reasoningTurn{
			callIDs:     toolCallIDsOf(toolCalls),
			chatDetails: detailsJSON,
		})
	}

	if notes := toolAcc.diagnostics(); len(notes) > 0 {
		log.Printf("[toolcall] %s", strings.Join(notes, "; "))
	}

	return &modelStreamResult{
		Content:          assistant.String(),
		Reasoning:        reasoning.String(),
		ReasoningPresent: reasoningPresent || reasoning.Len() > 0,
		ToolCalls:        normalizeToolCalls(toolCalls),
		Usage:            usage,
		StopReason:       stopReason,
	}, hasOutput(), nil
}

// isStreamOptionsRejectedError 判定建流失败是否因为网关不支持 stream_options
// 参数。这类网关在 400 报错文本里转述被拒绝的参数名；判定收紧为：400 语义
// + 文本提及该字段名。纯文本命中而没有任何 400 语义时不再降级，避免把无关
// 报错误判为参数被拒。文本来自 provider/中继，无法完全脱离字符串匹配。
func isStreamOptionsRejectedError(err error) bool {
	if err == nil || !isProvider400Error(err) {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "stream_options")
}

func isIncompleteStreamJSON(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unexpected end of json input") || strings.Contains(msg, "unexpected eof")
}

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

// openAIChatPromptCacheKey returns the session-sticky prompt_cache_key for a
// Chat Completions request, or "" when the field must not be sent. pi attaches
// it only when the request goes to api.openai.com (openai-completions.ts:810-814)
// — the official endpoint is the one place the field is documented, and a
// compatible gateway may answer an unknown top-level parameter with 400. The
// value is the same hashed session key the Responses adapter uses, so no raw
// session id leaves the client.
func openAIChatPromptCacheKey(cfg ConfigState) string {
	if !isOfficialOpenAIEndpoint(cfg) {
		return ""
	}
	return strings.TrimSpace(cfg.responsesPromptCacheKey)
}

// isOfficialOpenAIEndpoint reports whether the request targets the official
// OpenAI API. It is the single identity check for the request fields only OpenAI
// serves — prompt_cache_key / store / image_generation / explicit prompt-cache
// options — because compatible gateways reject fields they do not know.
func isOfficialOpenAIEndpoint(cfg ConfigState) bool {
	base := strings.ToLower(strings.TrimRight(baseURLForAPIFormat(cfg), "/"))
	return base == openAIOfficialAPIBaseURL || strings.HasPrefix(base, openAIOfficialAPIBaseURL+"/")
}

func isOpenRouterEndpoint(cfg ConfigState) bool {
	return strings.Contains(strings.ToLower(baseURLForAPIFormat(cfg)), "openrouter")
}

// sessionAffinityHeaders returns the sticky-routing headers that keep one
// conversation on the same upstream so prompt / KV cache hits survive across the
// requests of a tool loop. Only OpenRouter is handled: pi maps
// sessionAffinityFormat "openrouter" to x-session-id and uses provider-specific
// names elsewhere (openai-completions.ts), which Ally does not model — an unknown
// header would be meaningless to those endpoints anyway. The value is the same
// hashed session key used as the prompt-cache key, so no raw session id leaves
// the client.
func sessionAffinityHeaders(cfg ConfigState) map[string]string {
	key := strings.TrimSpace(cfg.responsesPromptCacheKey)
	if key == "" || !isOpenRouterEndpoint(cfg) {
		return nil
	}
	return map[string]string{"x-session-id": key}
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

func (a *App) streamAnthropicMessages(ctx context.Context, cfg ConfigState, model string, messages []legacyopenai.ChatCompletionMessage, tools []legacyopenai.Tool, onEvent func(modelStreamEvent)) (*modelStreamResult, error) {
	baseURL := baseURLForAPIFormat(cfg)
	// 关闭 SDK 内置重试,改用本模块统一的重试循环以便发出 run:retry 事件。
	clientOptions := []anthropicoption.RequestOption{
		anthropicoption.WithAPIKey(cfg.APIKey),
		anthropicoption.WithBaseURL(baseURL),
		anthropicoption.WithMaxRetries(0),
		anthropicoption.WithHTTPClient(modelHTTPClient(cfg, true, 0)),
	}
	for key, value := range sessionAffinityHeaders(cfg) {
		clientOptions = append(clientOptions, anthropicoption.WithHeader(key, value))
	}
	client := anthropic.NewClient(clientOptions...)

	replay := a.reasoningStash.get(reasoningReplayKey(cfg, model))
	system, anthropicMessages := buildAnthropicMessages(messages, replay, model)
	if len(anthropicMessages) == 0 || anthropicMessages[0].Role != anthropic.MessageParamRoleUser {
		anthropicMessages = append([]anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock("..."))}, anthropicMessages...)
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
	// Prompt-cache breakpoints: one on the last tool definition, one on the last
	// system block, one on the last content block of the last real message.
	// Supported by official Anthropic and Anthropic-compatible reverse
	// proxies/gateways; the ttl is pinned to the provider default (5m).
	markAnthropicPromptCacheBreakpoints(&params)
	// Thinking configuration for Anthropic:
	// - For adaptive models (Claude 4.6+/5+): thinking: { type: "adaptive" } and output_config.effort.
	// - For budget models (Claude 3.7 Sonnet): thinking: { type: "enabled", budget_tokens: N } without output_config.effort
	//   (passing output_config.effort on 3.7 causes a 400 error).
	// - For effort "off": thinking: { type: "disabled" }.
	thinkingEnabled := configureAnthropicThinking(&params, model, cfg.ReasoningEffort, cfg.MaxTokens)

	maxRetries := effectiveLLMRetries(cfg)
	var assistant strings.Builder
	var reasoning strings.Builder
	// thinkingBlocks captures this response's thinking/redacted_thinking
	// blocks in order, with signatures, for replay on the next request.
	thinkingBlocks := []anthropicThinkingBlock{}
	// blockThinkingIdx maps the stream content-block index of a thinking
	// block to its index in thinkingBlocks, so thinking_delta and
	// signature_delta events append to the right block.
	blockThinkingIdx := map[int64]int{}
	toolCalls := []legacyopenai.ToolCall{}
	toolIndexByBlock := map[int64]int{}
	toolEventGate := newModelToolCallEventGate(func(event modelStreamEvent) {
		emitModelStreamEvent(onEvent, event)
	})
	var usage *modelUsage
	// usageState carries the raw Anthropic counters across message_start /
	// message_delta events (see anthropicUsageState).
	usageState := &anthropicUsageState{}
	var stopReason string
	var stopSequence string

	for attempt := 0; attempt <= maxRetries; attempt++ {
		assistant.Reset()
		reasoning.Reset()
		thinkingBlocks = thinkingBlocks[:0]
		blockThinkingIdx = map[int64]int{}
		toolCalls = toolCalls[:0]
		toolIndexByBlock = map[int64]int{}
		usage = nil
		usageState = &anthropicUsageState{}
		stopReason = ""
		stopSequence = ""
		// Anthropic requires the interleaved-thinking beta for extended thinking
		// to continue across tool calls inside one turn (pi:
		// anthropic-messages.ts betas, kimi: anthropic/requester.ts baseline).
		// Adaptive-thinking models interleave by default, so the flag is only
		// sent for budget-based models.
		streamOptions := []anthropicoption.RequestOption{}
		if beta := anthropicInterleavedThinkingBeta(thinkingEnabled, len(tools) > 0, model); beta != "" {
			streamOptions = append(streamOptions, anthropicoption.WithHeader("anthropic-beta", beta))
		}
		stream := client.Messages.NewStreaming(ctx, params, streamOptions...)
		for stream.Next() {
			event := stream.Current()
			switch event.Type {
			case "message_start":
				ev := event.AsMessageStart()
				usageState.mergeStart(ev.Message.Usage)
			case "message_delta":
				ev := event.AsMessageDelta()
				usageState.mergeDelta(ev.Usage)
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
					blockThinkingIdx[ev.Index] = len(thinkingBlocks)
					thinkingBlocks = append(thinkingBlocks, anthropicThinkingBlock{})
					if block.Thinking != "" {
						reasoning.WriteString(block.Thinking)
						emitModelStreamEvent(onEvent, modelStreamEvent{ReasoningDelta: block.Thinking})
					}
				case "redacted_thinking":
					blockThinkingIdx[ev.Index] = len(thinkingBlocks)
					thinkingBlocks = append(thinkingBlocks, anthropicThinkingBlock{Data: block.Data})
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
					toolEventGate.emit(modelStreamEvent{ToolCalls: toolCalls})
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
						if idx, ok := blockThinkingIdx[ev.Index]; ok {
							thinkingBlocks[idx].Thinking += delta.Thinking
						}
						emitModelStreamEvent(onEvent, modelStreamEvent{ReasoningDelta: delta.Thinking})
					}
				case "signature_delta":
					if idx, ok := blockThinkingIdx[ev.Index]; ok {
						thinkingBlocks[idx].Signature += delta.Signature
					}
				case "input_json_delta":
					if idx, ok := toolIndexByBlock[ev.Index]; ok {
						toolCalls[idx].Function.Arguments += delta.PartialJSON
						toolEventGate.emit(modelStreamEvent{ToolCalls: toolCalls})
					}
				}
			}
		}
		streamErr := stream.Err()
		stream.Close()
		if streamErr == nil && strings.TrimSpace(stopReason) != "" {
			break
		}
		if streamErr == nil {
			streamErr = errors.New("stream ended without terminal event")
		}
		// 适配器只重试"尚未产出任何输出"的失败(与 streamOpenAIChat /
		// streamOpenAIResponses 的重试守卫一致):无输出则重试不会造成
		// 重复;已经产生内容的中断交给上层 runChat 做整轮重试,避免把
		// 半截输出拼进下一次请求。
		if assistant.Len() == 0 && reasoning.Len() == 0 && len(toolCalls) == 0 &&
			ctx.Err() == nil && attempt < maxRetries && shouldRetryLLMError(streamErr) {
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
	usage = usageState.modelUsage()
	for i := range toolCalls {
		if strings.TrimSpace(toolCalls[i].Function.Arguments) == "" {
			toolCalls[i].Function.Arguments = "{}"
		}
	}
	// Record this response's thinking blocks as one turn of the session
	// ledger, keyed by the turn's tool-call ids: every later request replays
	// them at the front of this turn's own assistant message, so the prefix
	// stays byte-identical across agent steps. Non-thinking responses record
	// no turn; earlier turns keep theirs.
	if len(thinkingBlocks) > 0 && len(toolCalls) > 0 {
		a.reasoningStash.appendTurn(reasoningReplayKey(cfg, model), reasoningTurn{
			callIDs:   toolCallIDsOf(toolCalls),
			anthropic: thinkingBlocks,
		})
	}
	return &modelStreamResult{
		Content:          assistant.String(),
		Reasoning:        reasoning.String(),
		ReasoningPresent: len(thinkingBlocks) > 0,
		ToolCalls:        normalizeToolCalls(toolCalls),
		Usage:            usage,
		StopReason:       stopReason,
		StopSequence:     stopSequence,
	}, nil
}

// anthropicInterleavedThinkingBeta returns the beta flag Anthropic requires for
// extended thinking to continue between tool calls inside one assistant turn. pi
// sends interleaved-thinking-2025-05-14 for any thinking request on a
// non-adaptive model (anthropic-messages.ts betas); Ally limits it to tool turns,
// which is where interleaving can actually happen, and never sends it to
// adaptive-thinking models, which interleave by default.
func anthropicInterleavedThinkingBeta(thinkingEnabled, hasTools bool, model string) string {
	if !thinkingEnabled || !hasTools || isAnthropicAdaptiveThinkingModel(model) {
		return ""
	}
	return "interleaved-thinking-2025-05-14"
}

func isAnthropicAdaptiveThinkingModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return strings.Contains(m, "sonnet-4.6") || strings.Contains(m, "sonnet-4-6") ||
		strings.Contains(m, "opus-4.6") || strings.Contains(m, "opus-4-6") ||
		strings.Contains(m, "opus-4.7") || strings.Contains(m, "opus-4-7") ||
		strings.Contains(m, "opus-4.8") || strings.Contains(m, "opus-4-8") ||
		strings.Contains(m, "sonnet-5") || strings.Contains(m, "opus-5") ||
		strings.Contains(m, "haiku-5") || strings.Contains(m, "fable") ||
		strings.Contains(m, "mythos") || strings.HasPrefix(m, "claude-5")
}

// configureAnthropicThinking applies the thinking configuration for the
// selected effort level and reports whether extended thinking is enabled for
// this request (the caller uses that to decide on the interleaved-thinking
// beta). It returns false for an explicit "off" and for "auto": auto sends no
// thinking field at all, so the model's own default applies.
func configureAnthropicThinking(params *anthropic.MessageNewParams, model, rawEffort string, maxTokens int) bool {
	raw := strings.ToLower(strings.TrimSpace(rawEffort))
	if normalizeReasoningEffort(raw) == reasoningEffortOff {
		params.Thinking = anthropic.ThinkingConfigParamUnion{OfDisabled: &anthropic.ThinkingConfigDisabledParam{Type: "disabled"}}
		return false
	}
	effort := normalizeReasoningEffort(rawEffort)
	if effort == "" || effort == reasoningEffortAuto {
		return false
	}
	// display: "summarized" is what makes the model stream its thinking text:
	// pi sets it explicitly on both branches so Opus 4.7 / Mythos behave like the
	// older Claude 4 models (anthropic-messages.ts:1127/1135/1147), and the SDK
	// documents summarized as the value that returns thinking normally instead of
	// a signature-only ("omitted") response.
	if isAnthropicAdaptiveThinkingModel(model) {
		params.Thinking = anthropic.ThinkingConfigParamUnion{OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{
			Type:    "adaptive",
			Display: anthropic.ThinkingConfigAdaptiveDisplaySummarized,
		}}
		params.OutputConfig = anthropic.OutputConfigParam{Effort: anthropic.OutputConfigEffort(effort)}
	} else {
		// Budget-tokens thinking for Claude 3.7 and non-adaptive models.
		// Platform docs: minimum budget_tokens is 1024, and budget_tokens must be
		// strictly less than max_tokens.
		budget := int64(4096)
		switch effort {
		case "low":
			budget = 1024
		case "medium":
			budget = 4096
		case "high", "xhigh", "max":
			budget = 32000
		}
		// The thinking budget and the visible answer share max_tokens, so a budget
		// is capped to leave the answer room that is always kept free (pi:
		// clampThinkingBudgetToAnswerRoom with MIN_ANSWER_TOKENS = 1024,
		// simple-options.ts:64-72). When the configured cap cannot satisfy both
		// the floor and the reserved answer room there is no valid
		// extended-thinking configuration, so thinking is left unset instead of
		// sending a request the API rejects.
		if maxTokens < minAnthropicThinkingBudget+minAnthropicAnswerTokens {
			log.Printf("[llm] max_tokens=%d is too small for Anthropic extended thinking; leaving thinking unset", maxTokens)
			return false
		}
		if room := int64(maxTokens - minAnthropicAnswerTokens); budget > room {
			budget = room
		}
		params.Thinking = anthropic.ThinkingConfigParamUnion{OfEnabled: &anthropic.ThinkingConfigEnabledParam{
			Type:         "enabled",
			BudgetTokens: budget,
			Display:      anthropic.ThinkingConfigEnabledDisplaySummarized,
		}}
	}
	return true
}

const (
	// minAnthropicThinkingBudget is Anthropic's documented floor for
	// budget_tokens (budget_tokens must additionally stay below max_tokens).
	minAnthropicThinkingBudget = 1024
	// minAnthropicAnswerTokens is the output room always kept for the visible
	// answer when a thinking budget shares the response ceiling (pi:
	// MIN_ANSWER_TOKENS, simple-options.ts:64).
	minAnthropicAnswerTokens = 1024
)

func anthropicStopReasonError(reason string, hasOutput bool) error {
	switch strings.TrimSpace(reason) {
	case "", "end_turn", "tool_use", "stop_sequence", "pause_turn":
		return nil
	case "max_tokens":
		return errors.New("Anthropic response reached the Max Tokens limit; increase Max Tokens or shorten the conversation")
	case "refusal":
		return errors.New("Anthropic refused the request")
	case "model_context_window_exceeded":
		return errors.New("Anthropic stopped because the model context window was exceeded")
	default:
		if hasOutput {
			return nil
		}
		return fmt.Errorf("Anthropic stopped with unsupported reason %q", reason)
	}
}

func modelResponseStopError(cfg ConfigState, result *modelStreamResult) error {
	if result == nil {
		return nil
	}
	hasOutput := strings.TrimSpace(result.Content) != "" || strings.TrimSpace(result.Reasoning) != "" || len(result.ToolCalls) > 0 || len(result.Images) > 0
	if normalizeAPIFormat(cfg.APIFormat) == apiFormatOpenAIChat {
		switch strings.TrimSpace(result.StopReason) {
		case "", "stop", "tool_calls", "function_call", "stop_sequence", "eos", "end_turn":
			return nil
		case "length":
			return errors.New("OpenAI-compatible response reached the Max Tokens limit; increase Max Tokens or shorten the conversation")
		case "content_filter":
			return errors.New("OpenAI-compatible response was stopped by the content filter")
		default:
			if hasOutput {
				return nil
			}
			return fmt.Errorf("OpenAI-compatible response stopped with unsupported reason %q", result.StopReason)
		}
	}
	if normalizeAPIFormat(cfg.APIFormat) == apiFormatOpenAIResponses {
		switch strings.TrimSpace(result.StopReason) {
		case "", "stop", "completed", "in_progress", "queued", "tool_calls":
			return nil
		case "length":
			return errors.New("OpenAI Responses reached the Max Tokens limit; increase Max Tokens or shorten the conversation")
		case "content_filter":
			return errors.New("OpenAI Responses was stopped by the content filter")
		default:
			if hasOutput {
				return nil
			}
			return fmt.Errorf("OpenAI Responses stopped with unsupported reason %q", result.StopReason)
		}
	}
	if normalizeAPIFormat(cfg.APIFormat) != apiFormatAnthropicMessages {
		return nil
	}
	return anthropicStopReasonError(result.StopReason, hasOutput)
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

func buildAnthropicMessages(messages []legacyopenai.ChatCompletionMessage, replay *sessionReasoningPayload, model string) (string, []anthropic.MessageParam) {
	systemParts := []string{}
	out := []anthropic.MessageParam{}

	appendMessage := func(role anthropic.MessageParamRole, blocks []anthropic.ContentBlockParamUnion) {
		if len(blocks) == 0 {
			return
		}
		// Anthropic Messages API requires roles to alternate strictly (user ⇄ assistant).
		// When multiple same-role messages appear consecutively (for example: user question ->
		// cancelledTurnMarker -> new user prompt, or tool results followed by user cancellation/input),
		// merge their content blocks into the preceding same-role message instead of emitting
		// adjacent same-role messages that fail Anthropic validation or get discarded by gateways.
		if len(out) > 0 && out[len(out)-1].Role == role {
			out[len(out)-1].Content = append(out[len(out)-1].Content, blocks...)
			return
		}
		if role == anthropic.MessageParamRoleUser {
			out = append(out, anthropic.NewUserMessage(blocks...))
		} else {
			out = append(out, anthropic.NewAssistantMessage(blocks...))
		}
	}

	hasSeenTurn := false
	for i := 0; i < len(messages); i++ {
		m := messages[i]
		switch m.Role {
		case legacyopenai.ChatMessageRoleSystem:
			text := messageText(m)
			if text == "" {
				continue
			}
			if !hasSeenTurn {
				systemParts = append(systemParts, text)
			} else {
				// Mid-conversation system turns (compaction summaries, steering reminders)
				// must not be hoisted into the top-level system prompt, which would corrupt
				// the primary prompt-cache breakpoint. Emit as a chronological <system>...</system>
				// block in a user message (merged with adjacent user messages to preserve alternating roles).
				wrapped := fmt.Sprintf("<system>\n%s\n</system>", text)
				appendMessage(anthropic.MessageParamRoleUser, []anthropic.ContentBlockParamUnion{
					anthropic.NewTextBlock(wrapped),
				})
			}
		case legacyopenai.ChatMessageRoleUser:
			hasSeenTurn = true
			blocks := anthropicBlocksFromMessage(m)
			appendMessage(anthropic.MessageParamRoleUser, blocks)
		case legacyopenai.ChatMessageRoleAssistant:
			hasSeenTurn = true
			blocks := []anthropic.ContentBlockParamUnion{}
			// The turn's captured thinking blocks replay at the front of its own
			// assistant message — on every request that still carries the turn —
			// so the request prefix stays byte-identical across agent steps.
			// Anthropic requires thinking blocks to precede the tool_use blocks
			// of the same message and accepts omitting them entirely, which is
			// why a turn without a ledger entry simply emits none. The ledger is
			// model-scoped: blocks captured from another model are never
			// replayed, because Anthropic validates the signature against the
			// model that produced it.
			if turn := replay.turnForAny(toolCallIDsOf(m.ToolCalls)); turn != nil {
				blocks = append(blocks, anthropicThinkingBlockParams(turn.anthropic, model)...)
			}
			if text := messageText(m); text != "" {
				blocks = append(blocks, anthropic.NewTextBlock(text))
			}
			for _, call := range m.ToolCalls {
				toolID := toolcall.ForAnthropic(toolcall.Effective(call.ID, call.Function.Name))
				blocks = append(blocks, anthropic.NewToolUseBlock(toolID, toolcall.DecodeArguments(call.Function.Arguments), call.Function.Name))
			}
			appendMessage(anthropic.MessageParamRoleAssistant, blocks)
		case legacyopenai.ChatMessageRoleTool:
			blocks := []anthropic.ContentBlockParamUnion{}
			for i < len(messages) && messages[i].Role == legacyopenai.ChatMessageRoleTool {
				toolMsg := messages[i]
				toolID := strings.TrimSpace(toolMsg.ToolCallID)
				if toolID == "" {
					toolID = "tool_call"
				}
				toolID = toolcall.ForAnthropic(toolID)
				blocks = append(blocks, anthropic.NewToolResultBlock(toolID, toolMsg.Content, anthropicToolResultIsError(toolMsg.Content)))
				i++
			}
			i--
			appendMessage(anthropic.MessageParamRoleUser, blocks)
		}
	}
	return strings.Join(systemParts, "\n\n"), out
}

// markAnthropicPromptCacheBreakpoints places explicit prompt-cache
// breakpoints: one on the last tool definition and one on the last system block
// (together they cache tools+system, reusable across runs while the header bytes
// stay stable), and one on the last content
// block of the last non-transient message (caches the stable request prefix
// so it grows incrementally across agent steps). Transient tail items such as
// <ally-context-budget> (currently disabled; see the commented call in
// runChat) are rebuilt every request and stay outside the cached prefix, so
// they can appear, change, or vanish without invalidating anything.
// Anthropic allows up to 4 breakpoints; 3 are used (the last tool, the last
// system block, the last non-transient block of the last message). The ttl is
// pinned to "5m", Anthropic's documented default, written out explicitly so
// every request carries the same breakpoint marker.
func markAnthropicPromptCacheBreakpoints(params *anthropic.MessageNewParams) {
	cc := anthropic.CacheControlEphemeralParam{TTL: anthropic.CacheControlEphemeralTTL("5m")}
	if len(params.Tools) > 0 {
		lastIdx := len(params.Tools) - 1
		if params.Tools[lastIdx].OfTool != nil {
			params.Tools[lastIdx].OfTool.CacheControl = cc
		}
	}
	if len(params.System) > 0 {
		params.System[len(params.System)-1].CacheControl = cc
	}
	for i := len(params.Messages) - 1; i >= 0; i-- {
		msg := params.Messages[i]
		for j := len(msg.Content) - 1; j >= 0; j-- {
			if anthropicBlockIsTransientInjection(msg.Content[j]) {
				continue
			}
			setAnthropicBlockCacheControl(msg.Content[j], cc)
			return
		}
	}
}

func anthropicBlockIsTransientInjection(block anthropic.ContentBlockParamUnion) bool {
	return block.OfText != nil && strings.HasPrefix(block.OfText.Text, "<ally-context-budget>")
}

func setAnthropicBlockCacheControl(block anthropic.ContentBlockParamUnion, cc anthropic.CacheControlEphemeralParam) {
	switch {
	case block.OfText != nil:
		block.OfText.CacheControl = cc
	case block.OfToolResult != nil:
		block.OfToolResult.CacheControl = cc
	case block.OfToolUse != nil:
		block.OfToolUse.CacheControl = cc
	case block.OfImage != nil:
		block.OfImage.CacheControl = cc
	}
}

func anthropicToolResultIsError(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return false
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(trimmed), &obj); err == nil && obj != nil {
		if okVal, exists := obj["ok"]; exists {
			if b, isBool := okVal.(bool); isBool {
				return !b
			}
		}
		if isErr, exists := obj["isError"]; exists {
			if b, isBool := isErr.(bool); isBool {
				return b
			}
		}
		if isErr, exists := obj["is_error"]; exists {
			if b, isBool := isErr.(bool); isBool {
				return b
			}
		}
		if _, hasErr := obj["error"]; hasErr {
			if okVal, hasOK := obj["ok"]; !hasOK || okVal == false {
				return true
			}
		}
		return false
	}
	lower := strings.ToLower(trimmed)
	return strings.HasPrefix(lower, "error:") ||
		strings.HasPrefix(lower, "mcp call failed:") ||
		strings.HasPrefix(lower, "unknown tool:") ||
		strings.HasPrefix(lower, "tool execution failed:") ||
		strings.HasPrefix(lower, "failed:") ||
		strings.HasPrefix(lower, "错误:") ||
		strings.HasPrefix(lower, "错误：") ||
		strings.HasPrefix(lower, "未知工具:") ||
		strings.HasPrefix(lower, "未知工具：") ||
		strings.HasPrefix(lower, "执行失败:") ||
		strings.HasPrefix(lower, "执行失败：") ||
		strings.HasPrefix(lower, "调用失败:") ||
		strings.HasPrefix(lower, "调用失败：")
}

func anthropicBlocksFromMessage(m legacyopenai.ChatCompletionMessage) []anthropic.ContentBlockParamUnion {
	blocks := []anthropic.ContentBlockParamUnion{}
	hasMultiText := false
	for _, part := range m.MultiContent {
		if part.Type == legacyopenai.ChatMessagePartTypeText && strings.TrimSpace(part.Text) != "" {
			hasMultiText = true
			break
		}
	}
	if !hasMultiText && strings.TrimSpace(m.Content) != "" {
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
		params := schemautil.Map(tool.Function.Parameters)
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
			InputSchema: anthropicInputSchema(schemautil.Map(tool.Function.Parameters)),
		}
		if strings.TrimSpace(tool.Function.Description) != "" {
			t.Description = anthropic.String(tool.Function.Description)
		}
		out = append(out, anthropic.ToolUnionParam{OfTool: &t})
	}
	return out
}

func normalizeToolsForOpenAIChat(tools []legacyopenai.Tool) []legacyopenai.Tool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]legacyopenai.Tool, len(tools))
	for i, t := range tools {
		out[i] = t
		if t.Function == nil {
			continue
		}
		// A function tool must always carry a parameters object: the field is
		// required by the API, and the Anthropic / Responses converters build the
		// same default through schemautil.Map. Routing all three adapters through
		// one path means a tool declared without parameters can never serialize
		// into a request that omits the key (schemautil.Map maps nil to
		// {"type":"object","properties":{}}).
		fn := *t.Function
		fn.Parameters = schemautil.Map(t.Function.Parameters)
		out[i].Function = &fn
	}
	return out
}

func anthropicInputSchema(schema map[string]any) anthropic.ToolInputSchemaParam {
	result := anthropic.ToolInputSchemaParam{
		// The Messages API requires input_schema.type = "object". The SDK field
		// would otherwise serialize empty, which gateways reject with 400
		// "tools.N.custom.input_schema.type: Field required".
		Type: "object",
	}
	if props, ok := schema["properties"]; ok {
		result.Properties = props
	} else {
		result.Properties = map[string]any{}
	}
	result.Required = schemautil.StringSlice(schema["required"])
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

// collapseRepeatedName folds a name that is an exact whole repetition of a
// shorter string (>= 2 folds of a >= 3-char unit) back to one fold — the
// artifact a relay that re-sends the function name in every streaming delta
// produced in histories written before toolcall.MergeRepeatedDelta existed.
// "http_request" repeated 7 times collapses back to "http_request"; real
// tool names (snake_case verbs, mcp__server__tool) are never whole-number
// repetitions, so the collapse cannot damage a legitimate name.
func collapseRepeatedName(name string) string {
	if len(name) < 6 {
		return name
	}
	// 只折叠“重复单元本身是已知工具名”的名字：中继重复发送产生的人工制品
	// （如 "http_request" x7）的重复单元必然是已知工具名，而一个恰好呈整周期
	// 重复形态的未知 MCP 工具名（其重复单元不在已知集合里）不会被误折。
	// 折出的单元若未知则原样返回，宁可放过不可误伤。
	for period := 3; period <= len(name)/2; period++ {
		if len(name)%period != 0 {
			continue
		}
		unit := name[:period]
		if name == strings.Repeat(unit, len(name)/period) && isKnownToolName(unit) {
			return unit
		}
	}
	return name
}

// knownBuiltinToolNames 缓存 chatTools() 里的内置工具名集合，用于历史加载
// 时识别由流式合并 bug 产生的未知工具名（如 "readlist_files"）。MCP 工具
// 名以 "mcp__" 开头，通过前缀检查识别，不需要在此集合中。
var (
	knownToolNamesOnce sync.Once
	knownToolNamesSet  map[string]bool
)

func knownBuiltinToolNames() map[string]bool {
	knownToolNamesOnce.Do(func() {
		tools := chatTools()
		knownToolNamesSet = make(map[string]bool, len(tools))
		for _, tool := range tools {
			if tool.Function != nil && tool.Function.Name != "" {
				knownToolNamesSet[tool.Function.Name] = true
			}
		}
	})
	return knownToolNamesSet
}

// mcpFunctionNamePrefix 是 MCP 工具的 sanitized OpenAI function name 前缀，
// 由 mcpToolFunctionName 生成。MCP 工具身份判定的唯一来源——判定处引用本
// 常量，不要散落字面量。
const mcpFunctionNamePrefix = "mcp__"

func isMcpToolFunctionName(name string) bool {
	return strings.HasPrefix(name, mcpFunctionNamePrefix)
}

// isKnownToolName 判断工具名是否是已知的内置工具或 MCP 工具。
func isKnownToolName(name string) bool {
	if isMcpToolFunctionName(name) {
		return true
	}
	return knownBuiltinToolNames()[name]
}

// isConcatenatedKnownToolNames 检测一个名字是否是两个已知工具名的拼接
// （如 "readlist_files" = "read" + "list_files"）。这种名字由服务商对多个
// tool_calls 使用相同 Index 导致的流式合并 bug 产生，不会是真实工具名。
func isConcatenatedKnownToolNames(name string) bool {
	for i := 1; i < len(name); i++ {
		if isKnownToolName(name[:i]) && isKnownToolName(name[i:]) {
			return true
		}
	}
	return false
}

// isProvider400Error 判断错误是否是服务商返回的 400 Bad Request。这通常意味
// 着上下文里有服务端校验无法通过的消息（截断参数、拼接工具名等），runChat
// 会先尝试 sanitize 修复上下文再重试，而不是直接中断会话。
// typed 状态码优先（providerHTTPStatusCode 消费三家 SDK 的错误类型）；关键
// 词只作为中继把状态码丢掉、仅在错误文本里转述 400 的兑底。流内 SSE 事件
// 解码失败（llmStreamEventDecodeError）不再靠 Go json 包报错文案误判——
// sanitize 历史救不了网络流损坏。
func isProvider400Error(err error) bool {
	if err == nil {
		return false
	}
	var decodeErr *llmStreamEventDecodeError
	if errors.As(err, &decodeErr) {
		return false
	}
	if status, ok := providerHTTPStatusCode(err); ok {
		return status == 400
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "status code: 400") ||
		strings.Contains(msg, "400 bad request")
}

func normalizeToolCalls(toolCalls []legacyopenai.ToolCall) []legacyopenai.ToolCall {
	out := cloneToolCalls(toolCalls)
	for i := range out {
		if out[i].Type == "" {
			out[i].Type = legacyopenai.ToolTypeFunction
		}
		out[i].Function.Name = collapseRepeatedName(out[i].Function.Name)
		if strings.TrimSpace(out[i].Function.Arguments) == "" {
			out[i].Function.Arguments = "{}"
			continue
		}
	}
	return out
}

// mintMissingToolCallIDs is runChat's backstop after the adapter returned:
// calls that still reach history without an id (a provider or relay that sent
// none) get a run-scoped one so every call stays pairable with its result, and
// a bare type is normalized. The stream-side identity authority is the
// accumulator's CallIDFoundry (id-first merging, dedup minting); this runs
// after it, on the exact slice that is appended to the history — which is also
// why a turn captured with all-empty ids into the reasoning ledger can never
// be matched again and is not stored (see reasoningTurn.callIDs).
func mintMissingToolCallIDs(runID string, toolCalls []legacyopenai.ToolCall) {
	for i := range toolCalls {
		if toolCalls[i].ID == "" {
			toolCalls[i].ID = fmt.Sprintf("call_%s_%d", runID, i)
		}
		if toolCalls[i].Type == "" {
			toolCalls[i].Type = legacyopenai.ToolTypeFunction
		}
	}
}

func cloneToolCalls(toolCalls []legacyopenai.ToolCall) []legacyopenai.ToolCall {
	if len(toolCalls) == 0 {
		return nil
	}
	out := make([]legacyopenai.ToolCall, len(toolCalls))
	copy(out, toolCalls)
	return out
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

func extractChoiceUsageFromRaw(raw []byte) *legacyopenai.Usage {
	var chunk struct {
		Choices []struct {
			Usage *legacyopenai.Usage `json:"usage"`
		} `json:"choices"`
	}
	if json.Unmarshal(raw, &chunk) == nil && len(chunk.Choices) > 0 && chunk.Choices[0].Usage != nil {
		return chunk.Choices[0].Usage
	}
	return nil
}

// modelUsageFromLegacy maps the OpenAI-compatible usage struct. DeepSeek returns
// cache counters as top-level prompt_cache_{hit,miss}_tokens (not parsed by the
// sashabaranov struct), and Moonshot (Kimi) returns top-level cached_tokens (or
// choices[0].usage.cached_tokens in streaming chunks), so raw is re-scanned to
// recover them when present; OpenAI/MiMo carry them nested under
// prompt_tokens_details.cached_tokens.
func modelUsageFromLegacy(usage *legacyopenai.Usage, raw []byte) *modelUsage {
	if usage == nil {
		return nil
	}
	hit, miss := 0, 0
	if usage.PromptTokensDetails != nil {
		hit = usage.PromptTokensDetails.CachedTokens
	}
	// DeepSeek 与 Moonshot 顶层字段（sashabaranov 不解析，从原始 JSON 补取）。
	// 支持顶层 usage 与 choices[0].usage（Moonshot 流式专有格式）。
	var extra struct {
		Usage *struct {
			PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens"`
			PromptCacheMissTokens int `json:"prompt_cache_miss_tokens"`
			CachedTokens          int `json:"cached_tokens"`
		} `json:"usage"`
		Choices []struct {
			Usage *struct {
				PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens"`
				PromptCacheMissTokens int `json:"prompt_cache_miss_tokens"`
				CachedTokens          int `json:"cached_tokens"`
			} `json:"usage"`
		} `json:"choices"`
	}
	if len(raw) > 0 && json.Unmarshal(raw, &extra) == nil {
		target := extra.Usage
		if target == nil && len(extra.Choices) > 0 {
			target = extra.Choices[0].Usage
		}
		if target != nil {
			if target.PromptCacheHitTokens > 0 || target.PromptCacheMissTokens > 0 {
				hit = target.PromptCacheHitTokens
				miss = target.PromptCacheMissTokens
			} else if target.CachedTokens > 0 {
				hit = target.CachedTokens
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

// anthropicUsageState accumulates the raw Anthropic usage counters across
// message_start / message_delta events. message_delta.usage fields are
// cumulative: a present field overwrites, an absent field keeps the previous
// value — never add (that would double count). Kimi-code overwrites each
// present field (anthropic.ts:872-888) and pi keeps an accumulator and
// recomputes (anthropic-messages.ts:716-743); keeping the raw counters here
// lets every field merge by presence even though the shared modelUsage only
// stores derived hit/miss totals. Presence uses the SDK's respjson.Field
// because a Go zero value cannot distinguish "0 tokens" from "field absent".
type anthropicUsageState struct {
	seen          bool
	input         int64
	output        int64
	cacheRead     int64
	cacheCreation int64
}

func (s *anthropicUsageState) mergeStart(usage anthropic.Usage) {
	if s == nil {
		return
	}
	s.seen = true
	s.input = usage.InputTokens
	s.output = usage.OutputTokens
	s.cacheRead = usage.CacheReadInputTokens
	s.cacheCreation = usage.CacheCreationInputTokens
}

func (s *anthropicUsageState) mergeDelta(usage anthropic.MessageDeltaUsage) {
	if s == nil {
		return
	}
	s.seen = true
	if usage.OutputTokens > 0 {
		s.output = usage.OutputTokens
	}
	if usage.JSON.InputTokens.Valid() {
		s.input = usage.InputTokens
	}
	if usage.JSON.CacheReadInputTokens.Valid() {
		s.cacheRead = usage.CacheReadInputTokens
	}
	if usage.JSON.CacheCreationInputTokens.Valid() {
		s.cacheCreation = usage.CacheCreationInputTokens
	}
}

func (s *anthropicUsageState) modelUsage() *modelUsage {
	if s == nil || !s.seen {
		return nil
	}
	input := s.input + s.cacheCreation + s.cacheRead
	if input <= 0 && s.output <= 0 {
		return nil
	}
	return &modelUsage{
		PromptTokens:     int(input),
		CompletionTokens: int(s.output),
		CacheHitTokens:   int(s.cacheRead),
		CacheMissTokens:  int(s.input + s.cacheCreation),
		CacheWriteTokens: int(s.cacheCreation),
	}
}

// toolCallAccumulator assembles streamed tool_calls deltas into whole calls. Its
// only job is to answer two questions per fragment: which call does this fragment
// belong to, and is it a new one? The answers come from one ordered rule set,
// strongest evidence first:
//
//  1. The id is the identity. A fragment that carries an id no open call owns
//     starts a new call (a call never changes its id), so two fragments that both
//     state an identity never merge unless their ids are equal. The stream index
//     is only a locator and must never be consulted first: it used to be the
//     primary key, and a relay that sends two calls under one index then had the
//     second call's identity swallowed by the index table — the two were welded
//     into one call with a spliced name and a corrupted argument string, and the
//     round was lost with a misleading "arguments were truncated" error.
//  2. For a fragment without an id, the index (or, when it carries no index at
//     all, the newest open call) locates the call it continues, and the tool name
//     decides whether it is in fact a different call: a name that continues the
//     accumulated one — equal, a prefix ("http_" -> "http_request"), or a
//     concatenation that spells a known tool name — is a continuation, while a
//     fragment that names a known tool on its own is a different call. That name
//     test is the only heuristic left, and it exists for relays that send neither
//     an id nor a usable index.
//  3. A fragment that states neither an id nor a name is a continuation: it is
//     located through the index or the newest open call, and with no call to
//     continue it is a stray and gets dropped instead of being appended as a
//     nameless call (providers reject those).
//
// Ids come from toolcall.CallIDFoundry, seeded from the conversation, so every call
// ends up with a unique id — even for a relay that reuses one id or sends none —
// and every call stays pairable with its result.
type toolCallAccumulator struct {
	calls     []legacyopenai.ToolCall
	byIndex   map[int]int
	byID      map[string]int
	synthetic map[int]bool
	foundry   *toolcall.CallIDFoundry
	notes     []string
}

// toolCallAction is what resolve decided for one fragment.
type toolCallAction int

const (
	// toolCallMerge folds the fragment into the resolved call.
	toolCallMerge toolCallAction = iota
	// toolCallAppend starts a new call for the fragment.
	toolCallAppend
	// toolCallDrop discards a stray continuation.
	toolCallDrop
	// toolCallAdoptID keeps the resolved call and replaces the id the foundry
	// minted with the provider's own id, sent after the call started.
	toolCallAdoptID
)

func newToolCallAccumulator(existing []legacyopenai.ToolCall) *toolCallAccumulator {
	acc := &toolCallAccumulator{
		calls:     cloneToolCalls(existing),
		byIndex:   map[int]int{},
		byID:      map[string]int{},
		synthetic: map[int]bool{},
		foundry:   toolcall.NewCallIDFoundry(),
	}
	for i := range acc.calls {
		acc.bind(i, acc.calls[i].ID, nil)
	}
	return acc
}

// seedConversation registers the ids the conversation already uses, so a relay
// that re-sends one of them (the common "call_0" per-response numbering) gets it
// rewritten instead of producing a request whose assistant messages share a
// tool_call id.
func (acc *toolCallAccumulator) seedConversation(messages []legacyopenai.ChatCompletionMessage) {
	for i := range messages {
		for _, call := range messages[i].ToolCalls {
			acc.foundry.Seed(call.ID)
		}
		acc.foundry.Seed(messages[i].ToolCallID)
	}
}

// diagnostics returns the notes recorded while assembling — rewritten duplicate
// ids, adopted late ids, dropped strays — as log lines for the adapter.
func (acc *toolCallAccumulator) diagnostics() []string {
	notes := make([]string, 0, len(acc.notes))
	for _, remap := range acc.foundry.Remaps() {
		notes = append(notes, fmt.Sprintf("rewrote duplicate tool call id %q to %q", remap.Raw, remap.Assigned))
	}
	return append(notes, acc.notes...)
}

// bind registers a slot under the fragment's stream index and under an id.
func (acc *toolCallAccumulator) bind(slot int, id string, index *int) {
	if index != nil {
		acc.byIndex[*index] = slot
	}
	if id = strings.TrimSpace(id); id != "" {
		acc.byID[id] = slot
	}
}

// resolve decides where one fragment belongs. It never mutates the accumulated
// calls; merge applies the outcome.
func (acc *toolCallAccumulator) resolve(delta legacyopenai.ToolCall) (int, toolCallAction) {
	id := strings.TrimSpace(delta.ID)
	name := strings.TrimSpace(delta.Function.Name)
	slotByID, hasID := -1, false
	if id != "" {
		slotByID, hasID = acc.byID[id]
	}
	slotByIndex, hasIndex := -1, false
	if delta.Index != nil {
		slotByIndex, hasIndex = acc.byIndex[*delta.Index]
	}
	// Rule 1: an id no open call owns starts a new call — unless the index still
	// points at a call that is waiting for the provider id, because some relays
	// send the id only after the call has already started.
	if id != "" && !hasID {
		if hasIndex && acc.synthetic[slotByIndex] {
			return slotByIndex, toolCallAdoptID
		}
		return 0, toolCallAppend
	}
	slot, ok := slotByID, hasID
	if !ok {
		slot, ok = slotByIndex, hasIndex
	}
	if !ok {
		// Rule 2/3: no id and no usable index — the newest open call is the one a
		// fragment belongs to, because a stream keeps a call's fragments adjacent.
		if len(acc.calls) == 0 {
			if id == "" && name == "" {
				acc.note("dropped a tool call fragment with no call to continue: " + describeToolCallFragment(delta))
				return 0, toolCallDrop
			}
			return 0, toolCallAppend
		}
		slot = len(acc.calls) - 1
	}
	// Rule 2: a fragment that names a tool the accumulated name is not continuing
	// belongs to a different call.
	if name != "" && !nameContinues(acc.calls[slot].Function.Name, name) {
		return 0, toolCallAppend
	}
	return slot, toolCallMerge
}

// merge appends one streamed batch and returns the accumulated calls. It must
// outlive the whole stream: index-only deltas can only be matched across chunks
// while the tables are kept alive.
func (acc *toolCallAccumulator) merge(deltas []legacyopenai.ToolCall) []legacyopenai.ToolCall {
	for _, delta := range deltas {
		slot, action := acc.resolve(delta)
		switch action {
		case toolCallAppend:
			acc.appendCall(delta)
		case toolCallDrop:
		case toolCallAdoptID:
			acc.adoptID(slot, delta.ID)
			acc.mergeInto(slot, delta)
		default:
			acc.mergeInto(slot, delta)
		}
	}
	return acc.calls
}

// appendCall starts a new call for a fragment that declares an identity.
func (acc *toolCallAccumulator) appendCall(delta legacyopenai.ToolCall) {
	id, synthetic := acc.foundry.Claim(delta.ID)
	call := legacyopenai.ToolCall{
		Type: delta.Type,
		ID:   id,
		Function: legacyopenai.FunctionCall{
			Name:      delta.Function.Name,
			Arguments: delta.Function.Arguments,
		},
	}
	if call.Type == "" {
		call.Type = legacyopenai.ToolTypeFunction
	}
	acc.calls = append(acc.calls, call)
	slot := len(acc.calls) - 1
	if synthetic {
		acc.synthetic[slot] = true
	}
	acc.bind(slot, id, delta.Index)
}

// adoptID replaces the id the foundry minted with the provider's own id for a
// call whose id arrived late.
func (acc *toolCallAccumulator) adoptID(slot int, rawID string) {
	id := strings.TrimSpace(rawID)
	if id == "" || slot < 0 || slot >= len(acc.calls) {
		return
	}
	delete(acc.synthetic, slot)
	acc.calls[slot].ID = id
	acc.bind(slot, id, nil)
	acc.note(fmt.Sprintf("adopted the tool call id %q that arrived after the call started", id))
}

// mergeInto folds one fragment into an existing call: names and ids dedupe
// through MergeRepeatedDelta (a relay that re-sends the full value must not
// produce "http_requesthttp_request"), argument chunks concatenate, and an exact
// duplicate argument chunk is skipped. A prefix-based replace is NOT safe for
// arguments: a legitimate continuation chunk can itself start with the
// accumulated prefix (nested JSON objects).
func (acc *toolCallAccumulator) mergeInto(slot int, delta legacyopenai.ToolCall) {
	current := &acc.calls[slot]
	if id := strings.TrimSpace(delta.ID); id != "" {
		current.ID = toolcall.MergeRepeatedDelta(current.ID, id)
	}
	if delta.Type != "" {
		current.Type = delta.Type
	}
	if name := delta.Function.Name; name != "" {
		current.Function.Name = toolcall.MergeRepeatedDelta(current.Function.Name, name)
	}
	if args := delta.Function.Arguments; args != "" && args != current.Function.Arguments {
		current.Function.Arguments += args
	}
	acc.bind(slot, current.ID, delta.Index)
}

// nameContinues reports whether a fragment's tool name belongs to the call that
// already accumulated `current`. Names arrive in pieces ("http_" then "request"),
// relays re-send the whole name with every chunk, and a relay that reuses one
// index for two calls names the second one outright: only that last case is a
// different call — the fragment names a tool that is known on its own while the
// accumulated name does not continue into it.
func nameContinues(current, delta string) bool {
	current = strings.TrimSpace(current)
	delta = strings.TrimSpace(delta)
	if current == "" || delta == "" {
		return true
	}
	if delta == current || strings.HasPrefix(delta, current) {
		return true
	}
	if isKnownToolName(current + delta) {
		return true
	}
	return !isKnownToolName(delta)
}

// note records a bounded diagnostic line for the adapter to log.
func (acc *toolCallAccumulator) note(message string) {
	if len(acc.notes) < 8 {
		acc.notes = append(acc.notes, message)
	}
}

// describeToolCallFragment renders a fragment for a diagnostic line without
// dumping an unbounded argument string.
func describeToolCallFragment(delta legacyopenai.ToolCall) string {
	index := "none"
	if delta.Index != nil {
		index = fmt.Sprintf("%d", *delta.Index)
	}
	return fmt.Sprintf("index=%s id=%q name=%q args=%q", index, delta.ID, delta.Function.Name, truncateRunes(delta.Function.Arguments, 40))
}

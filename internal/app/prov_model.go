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
	"strings"
	"sync"
	"time"

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
}

var errEmptyModelResponse = errors.New("empty model response")

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
	llmErrorKindUnknown llmErrorKind = iota
	llmErrorKindRateLimited        // 429/限流:瞬时,重试同一 key 意义有限但无害
	llmErrorKindAuth               // 401/403/认证类:重试同一 key 无意义
	llmErrorKindBilling            // 402/配额耗尽:需人工处理,key 级长冷却
	llmErrorKindContextTooLong     // 上下文超长:确定性失败,重试必然同样失败
	llmErrorKindModelNotFound      // 404/模型不存在:确定性失败
	llmErrorKindDeterministic400   // 其它 400:上下文毒化,sanitize 后可恢复
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
	if status, ok := providerHTTPStatusCode(err); ok {
		switch {
		case status == 401 || status == 403:
			return llmErrorKindAuth
		case status == 402:
			return llmErrorKindBilling
		case status == 404:
			return llmErrorKindModelNotFound
		case status == 429:
			return llmErrorKindRateLimited
		case status == 400:
			// 400 的细分(上下文超长/模型不存在文案)交给关键词阶段补充;
			// typed 只确认 "服务端拒绝了本请求"。
			return llmErrorKindDeterministic400
		}
	}
	msg := strings.ToLower(err.Error())
	if llmErrorTextMatchesAny(msg, llmBillingMarkers) {
		return llmErrorKindBilling
	}
	if llmErrorTextMatchesAny(msg, llmRateLimitMarkers) {
		return llmErrorKindRateLimited
	}
	if llmErrorTextMatchesAny(msg, llmAuthMarkers) {
		return llmErrorKindAuth
	}
	if llmErrorTextMatchesAny(msg, llmContextTooLongMarkers) {
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
	llmContextTooLongMarkers = []string{
		"context length", "context_length", "maximum context", "context window",
		"prompt is too long", "input length", "too many input tokens",
		"maximum_prompt_size", "request too large",
	}
	llmModelNotFoundMarkers = []string{
		"model not found", "no such model", "does not exist", "not_found",
		"status code: 404", "404 not found",
	}
)

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

// isAuthKeyError 判断错误是否属于认证/配额类(key 本身失效),这类错误重试
// 同一 key 无意义,应切换或直接失败。匹配覆盖常见变体:HTTP 状态码
// (401/403)、OpenAI/Anthropic 错误码(invalid_api_key、insufficient_quota、
// permission_error 等)以及常见文案(invalid api key、unauthorized、
// forbidden、credential 等)。
// 例外:429/限流类错误即使文案含 quota(阿里云 429 token-limit 的
// "You exceeded your current quota" 与 OpenAI 计费文案同形,实为分钟级
// 限流)也按瞬时错误处理,避免整个 key 池被 30 分钟冷却冻结;只有明确的
// 计费类标记才视为 key 级故障。
func isAuthKeyError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	msg := strings.ToLower(err.Error())
	// 明确的计费类错误(余额/配额耗尽,需人工处理)保持长冷却。
	if strings.Contains(msg, "402") || strings.Contains(msg, "insufficient_quota") ||
		strings.Contains(msg, "insufficient_balance") || strings.Contains(msg, "payment required") {
		return true
	}
	// 429/限流按定义是"请求过频"而非 key 失效,按瞬时错误短冷却。
	// 变体文案与 shouldRetryLLMError 保持一致,防止 "Rate exceeded" 类
	// 限流错误被下面的 quota 关键词误判为计费失效、触发 30 分钟长冷却。
	if strings.Contains(msg, "429") || strings.Contains(msg, "too many requests") ||
		strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "rate exceeded") || strings.Contains(msg, "rate_limit") {
		return false
	}
	if strings.Contains(msg, "401") || strings.Contains(msg, "403") ||
		strings.Contains(msg, "invalid api key") || strings.Contains(msg, "invalid_api_key") ||
		strings.Contains(msg, "invalid-api-key") || strings.Contains(msg, "invalid key") ||
		strings.Contains(msg, "api key") || strings.Contains(msg, "api_key") ||
		strings.Contains(msg, "unauthorized") || strings.Contains(msg, "authentication failed") ||
		strings.Contains(msg, "not authorized") || strings.Contains(msg, "permission denied") ||
		strings.Contains(msg, "permission") || strings.Contains(msg, "forbidden") ||
		strings.Contains(msg, "quota") ||
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
	} else {
		// Explicitly tell models not to call tools (prevents models from hallucinating tool calls during text-only completion)
		streamReq.ToolChoice = "none"
	}

	maxRetries := effectiveLLMRetries(cfg)
	result, emitted, err := a.openAIChatStreamAttempt(ctx, cfg, client, streamReq, onEvent)
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
		result, emitted, err = a.openAIChatStreamAttempt(ctx, cfg, client, streamReq, onEvent)
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

// openAIChatStreamAttempt 执行一次完整的 OpenAI Chat 建流与消费。第二个
// 返回值报告本次尝试是否已产出输出(assistant/reasoning/toolCalls 任一非空),
// 调用方据此判定适配器内重试是否安全(无输出则重试不会造成重复)。
func (a *App) openAIChatStreamAttempt(ctx context.Context, cfg ConfigState, client *legacyopenai.Client, streamReq legacyopenai.ChatCompletionRequest, onEvent func(modelStreamEvent)) (*modelStreamResult, bool, error) {
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
			if !gotFinishReason && assistant.Len() == 0 && reasoning.Len() == 0 && len(toolCalls) == 0 {
				return nil, false, errors.New("stream ended without finish_reason")
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
		}
		if len(resp.Choices) == 0 {
			continue
		}
		delta := resp.Choices[0].Delta
		if resp.Choices[0].FinishReason != "" {
			gotFinishReason = true
			stopReason = string(resp.Choices[0].FinishReason)
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
			if delta.ReasoningContent != "" {
				reasoning.WriteString(delta.ReasoningContent)
				emitModelStreamEvent(onEvent, modelStreamEvent{ReasoningDelta: delta.ReasoningContent})
			}
		}
		if len(delta.ToolCalls) > 0 {
			mergeToolCallDeltas(&toolCalls, delta.ToolCalls)
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

	return &modelStreamResult{
		Content:    assistant.String(),
		Reasoning:  reasoning.String(),
		ToolCalls:  normalizeToolCalls(toolCalls),
		Usage:      usage,
		StopReason: stopReason,
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
	body := buildOpenAIResponsesRequest(cfg, model, messages, tools)

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
		case "response.output_item.added":
			ev := event.AsResponseOutputItemAdded()
			if ev.Item.Type == "function_call" {
				idx := ensureResponsesToolCall(&toolCalls, toolIndexByOutput, toolIndexByItemID, ev.OutputIndex, ev.Item.ID)
				updateToolCallFromResponsesItem(&toolCalls[idx], ev.Item)
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
			toolCalls[idx].Function.Arguments = ev.Arguments
			toolEventGate.emit(modelStreamEvent{ToolCalls: toolCalls})
		case "response.output_item.done":
			ev := event.AsResponseOutputItemDone()
			if ev.Item.Type == "function_call" {
				idx := ensureResponsesToolCall(&toolCalls, toolIndexByOutput, toolIndexByItemID, ev.OutputIndex, ev.Item.ID)
				updateToolCallFromResponsesItem(&toolCalls[idx], ev.Item)
				toolEventGate.emit(modelStreamEvent{ToolCalls: toolCalls})
			} else if ev.Item.Type == "image_generation_call" {
				imageCall := ev.Item.AsImageGenerationCall()
				emitImage(imageCall.ID, imageCall.Result, false)
			}
		case "response.image_generation_call.partial_image":
			ev := event.AsResponseImageGenerationCallPartialImage()
			emitImage(ev.ItemID, ev.PartialImageB64, true)
		case "response.completed":
			gotTerminalEvent = true
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
		return nil, hasOutput(), err
	}
	if streamErr != nil {
		return nil, hasOutput(), streamErr
	}
	if !gotTerminalEvent {
		return nil, hasOutput(), errors.New("stream ended without terminal event")
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
	}, hasOutput(), nil
}

const openAIResponsesPromptCacheAnchorText = "<ally-prompt-cache-boundary/>"

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

func buildOpenAIResponsesRequest(cfg ConfigState, model string, messages []legacyopenai.ChatCompletionMessage, tools []legacyopenai.Tool) oaresp.ResponseNewParams {
	instructions, inputItems := buildOpenAIResponsesInput(messages)
	cacheKey := strings.TrimSpace(cfg.responsesPromptCacheKey)
	explicitPromptCache := supportsOpenAIResponsesGPT56PromptCaching(cfg, model)
	if explicitPromptCache {
		inputItems = appendOpenAIResponsesPromptCacheAnchor(inputItems)
	}
	body := oaresp.ResponseNewParams{
		Model:           oaresp.ResponsesModel(model),
		Input:           oaresp.ResponseNewParamsInputUnion{OfInputItemList: inputItems},
		MaxOutputTokens: oa.Int(int64(cfg.MaxTokens)),
	}
	if explicitPromptCache {
		body.PromptCacheOptions = oaresp.ResponseNewParamsPromptCacheOptions{
			Mode: "explicit",
		}
	}
	// Store and ParallelToolCalls are OpenAI-official fields that
	// compatible gateways may reject with 400 ("unsupported field").
	// Gate them behind the official-endpoint check so relays stay happy.
	if isOfficialOpenAIResponsesEndpoint(cfg) {
		body.ParallelToolCalls = oa.Bool(true)
		body.Store = oa.Bool(false)
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
	// Codex sends the stable session key on every Responses request, including
	// custom compatible endpoints. Only the explicit breakpoint/options below
	// are restricted to the official GPT-5.6 route because older gateways may
	// reject those newer fields.
	if cacheKey != "" {
		body.PromptCacheKey = oa.String(cacheKey)
	}
	return body
}

func supportsOpenAIResponsesGPT56PromptCaching(cfg ConfigState, model string) bool {
	if normalizeAPIFormat(cfg.APIFormat) != apiFormatOpenAIResponses || strings.TrimSpace(cfg.responsesPromptCacheKey) == "" || !isOfficialOpenAIResponsesEndpoint(cfg) {
		return false
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "gpt-5.6")
}

func appendOpenAIResponsesPromptCacheAnchor(input oaresp.ResponseInputParam) oaresp.ResponseInputParam {
	anchorContent := oaresp.ResponseInputContentParamOfInputText(openAIResponsesPromptCacheAnchorText)
	if anchorContent.OfInputText != nil {
		anchorContent.OfInputText.PromptCacheBreakpoint = oaresp.NewResponseInputTextPromptCacheBreakpointParam()
	}
	anchor := oaresp.ResponseInputItemParamOfMessage(
		oaresp.ResponseInputMessageContentListParam{anchorContent},
		oaresp.EasyInputMessageRoleDeveloper,
	)
	out := make(oaresp.ResponseInputParam, len(input)+1)
	out[0] = anchor
	copy(out[1:], input)
	return out
}

func supportsOpenAIResponsesImageGeneration(cfg ConfigState) bool {
	return isOfficialOpenAIResponsesEndpoint(cfg)
}

func isOfficialOpenAIResponsesEndpoint(cfg ConfigState) bool {
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
	client := anthropic.NewClient(
		anthropicoption.WithAPIKey(cfg.APIKey),
		anthropicoption.WithBaseURL(baseURL),
		anthropicoption.WithMaxRetries(0),
		anthropicoption.WithHTTPClient(httpClientWithUserAgent(cfg, true, 0)),
	)

	system, anthropicMessages := buildAnthropicMessages(messages)
	if len(anthropicMessages) == 0 || anthropicMessages[0].Role != anthropic.MessageParamRoleUser {
		anthropicMessages = append([]anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(""))}, anthropicMessages...)
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
		// Prompt-cache breakpoints for the official endpoint: one on the last
		// system block and one on the last content block of the last real
		// message. This replaces the previous top-level params.CacheControl,
		// whose breakpoint landed on the very last block — the per-step tail
		// injection itself — so every step's cache entry diverged from the
		// next request and incremental reuse never happened.
		markAnthropicPromptCacheBreakpoints(&params)
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
	toolEventGate := newModelToolCallEventGate(func(event modelStreamEvent) {
		emitModelStreamEvent(onEvent, event)
	})
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
		for stream.Next() {
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
						emitModelStreamEvent(onEvent, modelStreamEvent{ReasoningDelta: delta.Thinking})
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
	if result == nil {
		return nil
	}
	if normalizeAPIFormat(cfg.APIFormat) == apiFormatOpenAIChat {
		switch strings.TrimSpace(result.StopReason) {
		case "", "stop", "tool_calls", "function_call":
			return nil
		case "length":
			return errors.New("OpenAI-compatible response reached the Max Tokens limit; increase Max Tokens or shorten the conversation")
		case "content_filter":
			return errors.New("OpenAI-compatible response was stopped by the content filter")
		default:
			return fmt.Errorf("OpenAI-compatible response stopped with unsupported reason %q", result.StopReason)
		}
	}
	if normalizeAPIFormat(cfg.APIFormat) != apiFormatAnthropicMessages {
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
				input = append(input, responseInputItemParamOfFunctionCallOutput(m.ToolCallID, m.Content))
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

	for i := 0; i < len(messages); i++ {
		m := messages[i]
		switch m.Role {
		case legacyopenai.ChatMessageRoleSystem:
			if text := messageText(m); text != "" {
				systemParts = append(systemParts, text)
			}
		case legacyopenai.ChatMessageRoleUser:
			blocks := anthropicBlocksFromMessage(m)
			appendMessage(anthropic.MessageParamRoleUser, blocks)
		case legacyopenai.ChatMessageRoleAssistant:
			blocks := []anthropic.ContentBlockParamUnion{}
			if text := messageText(m); text != "" {
				blocks = append(blocks, anthropic.NewTextBlock(text))
			}
			for _, call := range m.ToolCalls {
				blocks = append(blocks, anthropic.NewToolUseBlock(effectiveToolCallID(call), decodeToolArguments(call.Function.Arguments), call.Function.Name))
			}
			appendMessage(anthropic.MessageParamRoleAssistant, blocks)
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
			appendMessage(anthropic.MessageParamRoleUser, blocks)
		}
	}
	return strings.Join(systemParts, "\n\n"), out
}

// markAnthropicPromptCacheBreakpoints places explicit prompt-cache
// breakpoints: one on the last system block (caches tools+system, reusable
// across runs while the header bytes stay stable) and one on the last content
// block of the last non-transient message (caches the stable request prefix
// so it grows incrementally across agent steps). Transient tail items such as
// <ally-context-budget> (currently disabled; see the commented call in
// runChat) are rebuilt every request and stay outside the cached prefix, so
// they can appear, change, or vanish without invalidating anything.
// Anthropic allows up to 4 breakpoints; 2 are used.
func markAnthropicPromptCacheBreakpoints(params *anthropic.MessageNewParams) {
	cc := anthropic.CacheControlEphemeralParam{TTL: anthropic.CacheControlEphemeralTTLTTL5m}
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

// anthropicMessageIsTransientInjection reports whether an outbound Anthropic
// message is a per-step tail injection (context budget) that is rebuilt fresh
// each request and never persisted into history. Such messages must remain
// outside the cached prefix.
func anthropicMessageIsTransientInjection(msg anthropic.MessageParam) bool {
	if msg.Role != "user" || len(msg.Content) == 0 {
		return false
	}
	for _, block := range msg.Content {
		if !anthropicBlockIsTransientInjection(block) {
			return false
		}
	}
	return true
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
	if args := strings.TrimSpace(item.Arguments.OfString); args != "" {
		call.Function.Arguments = args
	}
}

// truncatedToolCallArguments replaces streamed tool-call arguments that were
// cut off before the stream completed, leaving invalid JSON. Some providers
// parse tool_calls[].function.arguments server-side and reject the whole
// request with 400 when the string is malformed, so a truncated prefix must
// never be replayed to the provider or persisted into history. The failed
// tool result already explains the truncation to the model.
const truncatedToolCallArguments = `{"allyTruncatedArguments":true}`

// repairTruncatedToolCallArguments returns args unchanged when they are valid
// JSON and the truncation marker otherwise. It repairs persisted history at
// load time; live streamed arguments are rewritten the same way by
// prepareToolCallsForExecution before execution.
func repairTruncatedToolCallArguments(args string) string {
	if args == "" || json.Valid([]byte(args)) {
		return args
	}
	return truncatedToolCallArguments
}

// collapseRepeatedName folds a name that is an exact whole repetition of a
// shorter string (>= 2 folds of a >= 3-char unit) back to one fold — the
// artifact a relay that re-sends the function name in every streaming delta
// produced in histories written before mergeRepeatedStringDelta existed.
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

// isTruncatedArgsMarker 检测 args 是否是 normalizeToolCalls 替换出的截断标记
// {"allyTruncatedArguments":true}。这种 tool call 不应被执行，也不应保留
// 在历史里——它会毒化会话。
func isTruncatedArgsMarker(args []byte) bool {
	var v struct {
		AllyTruncatedArguments bool `json:"allyTruncatedArguments"`
	}
	return json.Unmarshal(args, &v) == nil && v.AllyTruncatedArguments
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
	// DeepSeek 顶层字段（sashabaranov 不解析，从原始 JSON 补取）。一次性
	// 解出超集：顶层字段存在时显式覆盖 nested 值（原实现靠 ">0 才覆盖"
	// 的隐式约定），两种命名并存时 DeepSeek 语义优先。
	var ds struct {
		Usage *struct {
			PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens"`
			PromptCacheMissTokens int `json:"prompt_cache_miss_tokens"`
		} `json:"usage"`
	}
	if len(raw) > 0 && json.Unmarshal(raw, &ds) == nil && ds.Usage != nil {
		hit = ds.Usage.PromptCacheHitTokens
		miss = ds.Usage.PromptCacheMissTokens
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
	return modelUsageFromResponseTokenCounts(
		usage.InputTokens,
		usage.OutputTokens,
		usage.InputTokensDetails.CachedTokens,
		usage.InputTokensDetails.CacheWriteTokens,
	)
}

func modelUsageFromResponseTokenCounts(input, output, hit, miss int64) *modelUsage {
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
	return modelUsageFromResponseTokenCounts(input, output, hit, miss)
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

// mergeRepeatedStringDelta merges one non-empty streamed id/name delta into
// its accumulated value. The OpenAI spec sends id and function name once in
// the first delta, but some relays re-send the full value in every delta;
// appending those verbatim produced names like "http_requesthttp_request..."
// that then failed dispatch with "unknown tool" and, once replayed, made
// providers that validate tool_calls reject every later request with 400.
// Exact re-sends are ignored, extended re-sends (delta starts with the
// accumulated value) replace it, and anything else is treated as a
// progressive chunk and appended.
func mergeRepeatedStringDelta(current, delta string) string {
	switch {
	case current == "":
		return delta
	case delta == current:
		return current
	case strings.HasPrefix(delta, current):
		return delta
	default:
		return current + delta
	}
}

// mergeToolCallDeltas merges streaming tool-call deltas into the accumulated
// tool call list, appending or extending by index. Shared by the OpenAI
// streaming adapters; the tests in app_test.go exercise the same package.
func mergeToolCallDeltas(toolCalls *[]legacyopenai.ToolCall, deltas []legacyopenai.ToolCall) {
	for _, delta := range deltas {
		idx := len(*toolCalls)
		if delta.Index != nil {
			idx = *delta.Index
		}
		for len(*toolCalls) <= idx {
			*toolCalls = append(*toolCalls, legacyopenai.ToolCall{Type: legacyopenai.ToolTypeFunction})
		}
		current := &(*toolCalls)[idx]
		// 不同工具名出现在同一个 Index:服务商可能对多个 tool_calls 使用了
		// 相同的 Index（或都不带 Index），导致两个不同调用被合并。标准 OpenAI
		// 规范里 name 只在首个 delta 出现一次，后续 delta name 为空。
		// 判断方法（按证据强度）：
		//  1. 两个非空且不同的 ID —— 无论工具名是否已知。两个 MCP 工具名拼接后
		//     仍带 mcp__ 前缀，会被前缀检查误判为“已知名”，只有 ID 能区分。
		//     标准流式不会在同一调用的后续 delta 里携带不同的 ID。
		//  2. 拼接后是已知工具名，说明是渐进式分片（如 "http_" + "request"），
		//     正常追加；如果拼接结果不是已知工具名但 delta 本身是已知工具名
		//     （如 delta="list_files" 而 current="read"），说明是不同工具调用
		//     被错误合并，追加新条目。
		if delta.Function.Name != "" && current.Function.Name != "" &&
			delta.Function.Name != current.Function.Name {
			distinctCall := false
			if delta.ID != "" && current.ID != "" && delta.ID != current.ID {
				distinctCall = true
			} else {
				combined := current.Function.Name + delta.Function.Name
				distinctCall = !isKnownToolName(combined) && isKnownToolName(delta.Function.Name)
			}
			if distinctCall {
				*toolCalls = append(*toolCalls, legacyopenai.ToolCall{
					Type: delta.Type,
					ID:   delta.ID,
					Function: legacyopenai.FunctionCall{
						Name:      delta.Function.Name,
						Arguments: delta.Function.Arguments,
					},
				})
				if (*toolCalls)[len(*toolCalls)-1].Type == "" {
					(*toolCalls)[len(*toolCalls)-1].Type = legacyopenai.ToolTypeFunction
				}
				continue
			}
		}
		if delta.ID != "" {
			current.ID = mergeRepeatedStringDelta(current.ID, delta.ID)
		}
		if delta.Type != "" {
			current.Type = delta.Type
		}
		if delta.Function.Name != "" {
			current.Function.Name = mergeRepeatedStringDelta(current.Function.Name, delta.Function.Name)
		}
		// Arguments are the one genuinely incremental field, but a relay that
		// duplicates the whole first delta would double the opening arguments
		// chunk too and corrupt the JSON; skip exact duplicates only — a
		// prefix-based replace is NOT safe here because a legitimate
		// continuation chunk can itself start with the accumulated prefix
		// (nested JSON objects).
		if delta.Function.Arguments != "" && delta.Function.Arguments != current.Function.Arguments {
			current.Function.Arguments += delta.Function.Arguments
		}
	}
}

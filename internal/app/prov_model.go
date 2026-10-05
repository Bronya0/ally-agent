// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

// 模型协议层：多 key 调度、流式归并、错误分类，加上协议常量表（API 格式、官方端点、
// token 参数名、思考档位拼写与端点默认值）——wire 拼写的唯一收口。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	oa "github.com/openai/openai-go/v3"
	legacyopenai "github.com/sashabaranov/go-openai"

	"ally-dev/internal/tools/toolcall"
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

// upstreamErrorSource 是"错误来自上游"这一事实在事件载荷里的取值，前端据此
// 在提示前加"上游模型服务返回错误"标识。取值只在这一处定义。
const upstreamErrorSource = "upstream"

// upstreamError 标记"错误来自上游模型服务"(HTTP 错误响应、流内错误事件、传输
// 失败)，而不是 Ally 自身的逻辑错误——界面据此把服务方的报错和本机的报错分开，
// 用户才知道该找谁。包装只加身份、不改文本:错误链(errors.Is/As)与基于文案的
// 关键词分类(重试/切换 key)都建立在原文之上，任何一处被改动都会静默改变行为。
type upstreamError struct{ inner error }

func (e *upstreamError) Error() string { return e.inner.Error() }
func (e *upstreamError) Unwrap() error { return e.inner }

// markUpstreamError 幂等地给发往上游的调用返回的错误打标记。nil、以及调用层
// 控制流的取消/超时(context.Canceled/DeadlineExceeded，用户主动停止不是上游
// 故障)一律原样返回。
func markUpstreamError(err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var marked *upstreamError
	if errors.As(err, &marked) {
		return err
	}
	return &upstreamError{inner: err}
}

// isUpstreamError 报告错误是否来自上游模型服务。
func isUpstreamError(err error) bool {
	var marked *upstreamError
	return errors.As(err, &marked)
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
	// 上下文超长先于状态码关键词：超长报错必带 token 数，文案里的数字极易与
	// 下面的状态码判据撞车（教训见 llmBillingStatusPattern），而超长正则足够
	// 特异，不会误吞普通限流/认证/计费文案。
	if llmContextTooLongPattern.MatchString(msg) {
		return llmErrorKindContextTooLong
	}
	if llmErrorTextMatchesAny(msg, llmBillingMarkers) || llmBillingStatusPattern.MatchString(msg) {
		return llmErrorKindBilling
	}
	if llmErrorTextMatchesAny(msg, llmRateLimitMarkers) || llmRateLimitStatusPattern.MatchString(msg) {
		return llmErrorKindRateLimited
	}
	if llmErrorTextMatchesAny(msg, llmAuthMarkers) || llmAuthStatusPattern.MatchString(msg) {
		return llmErrorKindAuth
	}
	if llmErrorTextMatchesAny(msg, llmModelNotFoundMarkers) {
		return llmErrorKindModelNotFound
	}
	if llmErrorTextMatchesAny(msg, llmHTTP400TextMarkers) {
		return llmErrorKindDeterministic400
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
		"insufficient_quota", "insufficient_balance", "payment required",
		"exceeded_current_quota_error", "check your account balance", "recharge your account",
		"please recharge", "account is in arrears", "account in arrears",
	}
	llmRateLimitMarkers = []string{
		"too many requests", "rate limit", "rate exceeded", "rate_limit",
	}
	llmAuthMarkers = []string{
		"invalid api key", "invalid_api_key", "invalid-api-key",
		"invalid key", "api key", "api_key", "unauthorized", "authentication failed",
		"not authorized", "permission denied", "permission", "forbidden",
		"access denied", "credential",
	}
	// 数字状态码不进子串清单、单独用词边界正则认：裸 "402" 会命中文案里任何
	// 含该片段的数字（真实案例："maximum context length is 40960 tokens …
	// you requested 40232 tokens" 的 "40232" 含 "402"，超长错误被误判成
	// Billing，溢出压缩恢复被跳过、还误切 key）。词边界只排除长数字里的片段，
	// 独立成词的状态码（"error code: 402"、"402 Payment Required"）照常命中。
	llmBillingStatusPattern   = regexp.MustCompile(`\b402\b`)
	llmRateLimitStatusPattern = regexp.MustCompile(`\b429\b`)
	llmAuthStatusPattern      = regexp.MustCompile(`\b40[13]\b`)
	llmModelNotFoundMarkers   = []string{
		"model not found", "no such model", "does not exist", "not_found",
		"status code: 404", "404 not found",
	}
	// 中继把状态码转述进文案、类型化错误丢失时的 400 判据。isProvider400Error 与
	// classifyLLMError 共用这一份，避免"认得出 400、却不认它是确定性失败"的分叉：
	// 只剩文案的 400 曾被归入 unknown 按瞬时错误盲重试 LLMRetries 次，每次都把
	// 整份上下文再发一遍，而同一个 400 不会因为重发而消失。
	llmHTTP400TextMarkers = []string{
		"status code: 400", "400 bad request",
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

// maxRetryAfterWait 是服务端 Retry-After 建议等待时间的上限兜底:个别网关会
// 返回几分钟甚至更大的值,无上限照单全收会把用户困在不可取消的长等待里。
// 超过上限时按上限等待后重试,由服务端再次裁决。
const maxRetryAfterWait = 60 * time.Second

// llmRetryDelayForError 在本地指数退避的基础上尊重服务端的 Retry-After
// (解析自错误响应头;Chat 兼容路径经 retryAfterCaptureTransport 捕获后由
// retryAfterError 携带)。取两者较大值:服务端说等多久就至少等多久;超过
// maxRetryAfterWait 的建议按上限兜底。解析失败或没有该头时退回纯指数退避。
func llmRetryDelayForError(attempt int, err error) time.Duration {
	d := llmRetryDelay(attempt)
	if ra := retryAfterFromError(err); ra > d {
		if ra > maxRetryAfterWait {
			ra = maxRetryAfterWait
		}
		d = ra
	}
	return d
}

// retryAfterError 给错误附加从传输层捕获的 Retry-After 建议等待时间。
// 与 upstreamError 同型:只加身份不改文案,错误链分类(classifyLLMError 等)
// 基于原文,不受包装影响。
type retryAfterError struct {
	inner error
	after time.Duration
}

func (e *retryAfterError) Error() string { return e.inner.Error() }
func (e *retryAfterError) Unwrap() error { return e.inner }

// retryAfterFromError 提取服务端建议的重试等待时间。Anthropic 与 OpenAI
// 官方 SDK 的错误类型携带原始 http.Response,直接读响应头;Chat 兼容路径的
// sashabaranov RequestError 不含响应头,由 retryAfterCaptureTransport 在传输
// 层捕获、经 retryAfterError 带到这里。没有则返回 0。
func retryAfterFromError(err error) time.Duration {
	if err == nil {
		return 0
	}
	var raErr *retryAfterError
	if errors.As(err, &raErr) && raErr.after > 0 {
		return raErr.after
	}
	var anthropicErr *anthropic.Error
	if errors.As(err, &anthropicErr) && anthropicErr.Response != nil {
		if d := parseRetryAfterHeader(anthropicErr.Response.Header); d > 0 {
			return d
		}
	}
	var oaErr *oa.Error
	if errors.As(err, &oaErr) && oaErr.Response != nil {
		if d := parseRetryAfterHeader(oaErr.Response.Header); d > 0 {
			return d
		}
	}
	return 0
}

// parseRetryAfterHeader 解析 Retry-After 响应头:delta-seconds 形式为主,
// HTTP-date 形式按“距现在的剩余时长”折算。取值不合法或已过期返回 0。
func parseRetryAfterHeader(h http.Header) time.Duration {
	if h == nil {
		return 0
	}
	return parseRetryAfterValue(h.Get("Retry-After"))
}

func parseRetryAfterValue(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
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
	return a.streamModelTextWithUsage(ctx, cfg, model, messages, maxTokens, nil)
}

// streamModelTextWithUsage is completeModelTextWithUsage with a live delta
// callback, so non-chat flows can render the response while it is produced
// (compaction shows the thinking phase and the summary body instead of a
// frozen timer). It owns the "what counts as the answer" fallbacks below, so a
// caller that streams the deltas and a caller that only reads the returned
// content always end up with the same text.
func (a *App) streamModelTextWithUsage(ctx context.Context, cfg ConfigState, model string, messages []legacyopenai.ChatCompletionMessage, maxTokens int, onDelta func(contentDelta, reasoningDelta string)) (string, *modelUsage, error) {
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
		if onDelta != nil && (event.ContentDelta != "" || event.ReasoningDelta != "") {
			onDelta(event.ContentDelta, event.ReasoningDelta)
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
	// 从这里往下才是真正发往上游的调用，返回的错误一律标记来源(见 upstreamError):
	// 配置类前置校验的报错(缺模型/缺 key)在更前面返回，不会被误标成上游问题。
	// 单 key 快速路径:完全保持原有的适配器内重试行为。
	if len(keys) == 1 {
		result, err := a.streamModelResponseWithKey(ctx, cfg, model, messages, tools, onEvent)
		if err != nil {
			err = markUpstreamError(wrapProviderRequestError(err))
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
		err = markUpstreamError(wrapProviderRequestError(err))
		lastErr = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// 记冷却与“能否中途换 key”是两件事：已发射流事件后不切换（防重复输出），
		// 但失败记录必须留下——否则这一轮与后续的 run 级重试都会从同一个坏 key
		// 开始，健康 key 一次都轮不到（表现为“配了多 key 却一直失败”）。
		// 瞬时针冷却 ≤10s，到期后该 key 自动回到候选；auth/配额才走 30min。
		failover := shouldFailoverKey(err)
		if failover {
			cooldown := keyTransientCooldownDuration
			if isAuthKeyError(err) {
				cooldown = keyAuthCooldownDuration
			}
			a.recordKeyFailure(cfg, key, cooldown)
		}
		if !(!emitted && failover) {
			return nil, err
		}
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
			wait = llmRetryDelayForError(retries, err)
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

// openAIReasoningFirstMajor is the first GPT generation that declares reasoning_effort
// and max_completion_tokens: gpt-5 onwards, while gpt-4o / gpt-4.1 reject both.
const openAIReasoningFirstMajor = 5

// isOpenAIReasoningModelName reports whether this id names an OpenAI reasoning family: the
// o-series (o1, o3, o4 — any later digit run too) or a GPT generation from
// openAIReasoningFirstMajor on. It is the OpenAI row of reasoningEffortFamilies as well as
// the token-param routing, so those two can never disagree about who the OpenAI reasoning
// family is.
//
// The rule reads the version NUMBER instead of pinning a generation (kimi-code pins
// /^gpt-5/, openai-legacy.ts:130-133): a pinned name answers only for the models that
// existed the day it was written, so the next one — the model catalog already lists
// gpt-6-astra — would silently lose both fields, leaving the provider's own default
// running while the UI kept showing the chosen level. What the pinned form does get right
// is the boundary, and that stays: a version ends at the id or continues after "-" / "."
// ("gpt-5", "gpt-5.1", "gpt-5-codex"), so a family prefix such as "gpt-5x" or
// "o1preview" matches nothing.
func isOpenAIReasoningModelName(model string) bool {
	m := baseModelName(model)
	if rest, ok := strings.CutPrefix(m, "gpt-"); ok {
		digits, tail := leadingDigits(rest)
		major, err := strconv.Atoi(digits)
		return err == nil && major >= openAIReasoningFirstMajor && modelIDBoundary(tail)
	}
	if rest, ok := strings.CutPrefix(m, "o"); ok {
		digits, tail := leadingDigits(rest)
		return digits != "" && modelIDBoundary(tail)
	}
	return false
}

// leadingDigits splits the leading decimal digits off a model id: "5.1" -> "5", ".1".
func leadingDigits(s string) (digits, rest string) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[:i], s[i:]
}

// modelIDBoundary reports whether a version ends here rather than continuing into a longer
// token: the end of the id, a "-" or a "." end one, the "x" of "gpt-5x" does not.
func modelIDBoundary(rest string) bool {
	return rest == "" || rest[0] == '-' || rest[0] == '.'
}

// baseModelName normalizes a model id for classification: lowercased, trimmed, with the
// routing metadata dropped — a provider prefix ("openai/o3-mini", "zhipu/glm-5.3") and a
// region tag ("gpt-5.1@eu", "claude-opus-4-6@eu" — the model catalog lists both forms) —
// so the id is judged by the model it actually names.
func baseModelName(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(m, "/"); i >= 0 && i+1 < len(m) {
		m = m[i+1:]
	}
	if i := strings.Index(m, "@"); i > 0 {
		m = m[:i]
	}
	return m
}

// reasoningEffortFamily declares one vendor's support for the reasoning_effort
// field: which levels it accepts, the translations its own docs state, and what
// (if anything) "off" means on that field.
type reasoningEffortFamily struct {
	// name is for readability and for naming the offending row in test output.
	name string
	// matches reports whether a normalized model name (baseModelName already
	// dropped any provider prefix) belongs to this vendor. It is keyed on the vendor
	// brand and never on a model version — see reasoningEffortFamilies.
	matches func(model string) bool
	// accepts lists the levels this vendor's endpoints accept, lowest first. Where
	// generations differ it holds the narrowest set the vendor's docs declare, so any
	// version that declares the field at all accepts what this row writes.
	accepts []string
	// alias holds the translations the vendor's docs state (our level -> its
	// level). A level inside the accepted range that is neither accepted nor
	// aliased is a gap in this row, which TestReasoningEffortFamiliesStayUsable
	// fails on instead of letting it silently drop the field at runtime.
	alias map[string]string
	// offValue is the value this vendor's docs name for turning thinking off on the
	// effort field ("none" for DeepSeek / 千问 / the OpenAI reasoning models). It stays
	// empty for a vendor whose models cannot stop thinking: the lowest accepted level
	// is then the only lever its docs offer (see offWireValue).
	offValue string
}

// reasoningEffortFamilies is the vendor table, ordered by match priority. A model that
// matches no row receives no effort field at all: a value the vendor never declared can
// only come back as a 400, while leaving the field out keeps the provider's own default.
//
// A row is keyed on the VENDOR, never on a model version. A version-keyed table (the
// shape this one started with: glm-5.3 / glm-5.2 / kimi-k3 / qwen3.8 / MiniMax-M3.1)
// answers only for the models that existed the day it was written: the vendor's next
// release matches no row, the level the user picked is silently dropped, and the provider
// runs its own default — the exact failure this table exists to prevent. What follows from
// keying on the vendor: a row states the vendor's CURRENT documented protocol, so a
// generation whose docs predate the field may receive a key it does not declare. That
// trade is spelled out per row.
//
// This table is the single source of truth for how a level reaches the wire. The previous
// implementation decided capability by model name shape ("looks like o*/gpt-5*"), so
// DeepSeek / 智谱 / Kimi silently received nothing: the user's choice of low was never in
// the request body and the provider ran its own default (DeepSeek high, GLM max, Kimi
// max) — the most expensive and slowest setting — while the UI kept showing the selected
// level.
//
// 千问's platform endpoint also serves 别家模型（doc 千问 §6 明列 GLM / Kimi / DeepSeek 系列由
// 千问直供），而那种组合下说得算的是端点自己的字段表 —— 千问给这些模型声明的是
// `enable_thinking`，不是 `thinking` / `reasoning_effort`。这张表看不到端点主机，所以
// 那种组合仍按模型名判厂商：按主机分派要把主机传进每一行，而平台只覆盖部分厂商
// （GLM 在千问那边走 thinking / thinking_budget），暂不做。
var reasoningEffortFamilies = []reasoningEffortFamily{
	{
		// DeepSeek: reasoning_effort takes none/low/high/max, and its docs state the
		// translations minimal→low, medium→high, xhigh→high; none is 关闭思考模式.
		name:    "deepseek",
		matches: vendorModelPrefix("deepseek"),
		accepts: []string{reasoningEffortLow, reasoningEffortHigh, reasoningEffortMax},
		alias: map[string]string{
			reasoningEffortMedium: reasoningEffortHigh,
			reasoningEffortXHigh:  reasoningEffortHigh,
		},
		offValue: reasoningEffortOffWireValue,
	},
	{
		// 智谱 GLM：档位收 low/high/max（其余输入报错），medium 映射 high、xhigh 映射 max ——
		// 取这组最窄的集合，任何一代只要声明了该字段就都接受（5.2 那代本身还收
		// medium/xhigh，收到映射值同样合法）。更早的一代（5.1 及以下、4.x）文档里没有这个
		// 字段，会收到一个它没声明的键 —— 这是按厂商当前一代取值的代价。
		//
		// 关不掉思考：两种写法都只写在上一代的表里 —— thinking.type=disabled（当前一代传了报错）
		// 与 reasoning_effort 的 none/minimal（= 放弃思考，当前一代只收 max/high/low）；两代都
		// 接受的只有档位本身，所以 offValue 留空 —— off 落到 low（上一代还会把它映射成 high，
		// 这正是按厂商取最窄集合要付的代价）。
		name:    "zhipu",
		matches: vendorModelPrefix("glm"),
		accepts: []string{reasoningEffortLow, reasoningEffortHigh, reasoningEffortMax},
		alias: map[string]string{
			reasoningEffortMedium: reasoningEffortHigh,
			reasoningEffortXHigh:  reasoningEffortMax,
		},
	},
	{
		// Kimi / Moonshot：Chat 顶层 reasoning_effort 收 low/high/max（默认 max），
		// medium 映射 high、xhigh 映射 max；K2.x 那代文档里没有这个字段。
		//
		// 关不掉思考：当前这代始终推理，文档给的办法就是把 reasoning_effort 设为 low
		// （“K3 的思考无法关闭”），所以 offValue 留空 —— off 落到 low。
		name:    "moonshot",
		matches: vendorModelPrefix("kimi", "moonshot"),
		accepts: []string{reasoningEffortLow, reasoningEffortHigh, reasoningEffortMax},
		alias: map[string]string{
			reasoningEffortMedium: reasoningEffortHigh,
			reasoningEffortXHigh:  reasoningEffortMax,
		},
	},
	{
		// 千问 Qwen：Chat 的 reasoning_effort 收 low/medium/xhigh（默认 xhigh）——没有 high、
		// 也没有 max，所以 high 按文档收敛到 xhigh，而 max 比该行的最高档还大，由越界收敛落到
		// 同一个值；更早的一代文档里只声明开关（enable_thinking），没有这个字段。与
		// thinking_budget 互斥；Ally 只发档位、不发预算。
		//
		// 这条线能真的关掉思考：Chat 走 enable_thinking:false（reasoningStopThinkingFamilies），
		// Responses 的 reasoning.effort 把 none 列进可取值，所以 offValue 是文档里的 none。
		name:    "qwen",
		matches: vendorModelPrefix("qwen"),
		accepts: []string{reasoningEffortLow, reasoningEffortMedium, reasoningEffortXHigh},
		alias: map[string]string{
			reasoningEffortHigh: reasoningEffortXHigh,
		},
		offValue: reasoningEffortOffWireValue,
	},
	{
		// MiniMax：reasoning_effort 收 low/medium/high/xhigh/max；更早的 M2.x 那代忽略该字段
		// （文档原文：非当前一代会被忽略，即不报错）。
		//
		// 关不掉思考：当前这代强制开启（传 disabled 或 none 报 400，requires adaptive
		// thinking），所以 offValue 留空 —— off 落到它档位字段的最低档 low（M3 那代也接受，
		// 只是不调深度）。
		name:    "minimax",
		matches: vendorModelPrefix("minimax"),
		accepts: []string{
			reasoningEffortLow, reasoningEffortMedium, reasoningEffortHigh,
			reasoningEffortXHigh, reasoningEffortMax,
		},
	},
	{
		// OpenAI reasoning models: the level goes out unchanged (the SDK enum
		// carries xhigh and max).
		name:    "openai-reasoning",
		matches: isOpenAIReasoningModelName,
		accepts: []string{
			reasoningEffortLow, reasoningEffortMedium, reasoningEffortHigh,
			reasoningEffortXHigh, reasoningEffortMax,
		},
		offValue: reasoningEffortOffWireValue,
	},
}

// vendorModelPrefix builds a match rule for one vendor's model ids: the brands the id may
// start with, on the normalized name — a vendor with two brand spellings lists both (Kimi's
// kimi / moonshot), which is why this takes a list rather than one brand. The version
// suffix is deliberately not part of the key: a rule tight enough to name this year's model
// is also tight enough to miss the next one.
func vendorModelPrefix(brands ...string) func(string) bool {
	return func(model string) bool {
		base := baseModelName(model)
		for _, brand := range brands {
			if strings.HasPrefix(base, brand) {
				return true
			}
		}
		return false
	}
}

// reasoningEffortFamilyFor returns the vendor row classifying this model, if any.
func reasoningEffortFamilyFor(model string) (reasoningEffortFamily, bool) {
	base := baseModelName(model)
	for _, family := range reasoningEffortFamilies {
		if family.matches != nil && family.matches(base) {
			return family, true
		}
	}
	return reasoningEffortFamily{}, false
}

// reasoningEffortForModel resolves the selected level onto the value this model's
// vendor accepts, or "" when the field must not be sent. "auto" stays silent (the
// provider decides) and a vendor that declares no effort field stays silent too,
// but a level the user picked is never dropped on a vendor that does declare the
// field: it goes out verbatim, through the vendor's documented translation, or
// clamped onto the vendor's range.
func reasoningEffortForModel(model, effort string) string {
	level := normalizeReasoningEffort(effort)
	if level == reasoningEffortAuto {
		return ""
	}
	family, ok := reasoningEffortFamilyFor(model)
	if !ok {
		return ""
	}
	if level == reasoningEffortOff {
		return family.offWireValue()
	}
	return family.wireValue(level)
}

// wireValue maps one canonical level onto this family's accepted values.
func (f reasoningEffortFamily) wireValue(level string) string {
	if len(f.accepts) == 0 {
		return ""
	}
	for _, accepted := range f.accepts {
		if accepted == level {
			return level
		}
	}
	if mapped := f.alias[level]; mapped != "" {
		return mapped
	}
	// Out of range (below the lowest or above the highest accepted level): clamp
	// onto the range so a selection can never be sent verbatim to a vendor that
	// does not know it.
	if lowest := f.accepts[0]; reasoningEffortRank(level) < reasoningEffortRank(lowest) {
		return lowest
	}
	if highest := f.accepts[len(f.accepts)-1]; reasoningEffortRank(level) > reasoningEffortRank(highest) {
		return highest
	}
	// Inside the declared range, not declared itself and without a documented
	// translation: this row is incomplete. Sending a value the vendor never
	// declared is worse than sending none — and the row fails
	// TestReasoningEffortFamiliesStayUsable instead of reaching a request.
	return ""
}

// offWireValue resolves the "关闭思考" selection on this family's effort field: the
// value its docs name for turning thinking off, or — for a model whose docs say it
// cannot stop thinking — the vendor's lowest accepted level. Sending nothing there
// would leave the provider's own default running (on those models the most expensive
// and slowest setting) while the UI keeps showing "关闭". Empty when the model matches
// no row at all, i.e. the vendor declares no effort field to write.
func (f reasoningEffortFamily) offWireValue() string {
	if f.offValue != "" {
		return f.offValue
	}
	if len(f.accepts) == 0 {
		return ""
	}
	return f.accepts[0]
}

// offIsSubstitute reports whether offWireValue above is a stand-in rather than a real
// off, which is the case exactly when the docs name no off value. A compatible Chat
// endpoint reads it to choose between the vendor's stop-thinking field and the lowest
// level (see reasoningWireForAdapter).
func (f reasoningEffortFamily) offIsSubstitute() bool { return f.offValue == "" }

// reasoningEffortRanks orders the canonical levels, which is what lets an
// out-of-range selection be clamped onto a vendor's range. Unknown levels rank 0.
var reasoningEffortRanks = map[string]int{
	reasoningEffortLow:    1,
	reasoningEffortMedium: 2,
	reasoningEffortHigh:   3,
	reasoningEffortXHigh:  4,
	reasoningEffortMax:    5,
}

func reasoningEffortRank(level string) int { return reasoningEffortRanks[level] }

// maxCompletionTokenVendors lists the vendors whose Chat endpoint documents
// max_completion_tokens alone, so "auto" must route the output cap there: MiMo's
// parameter table has max_completion_tokens (default 1024) and no max_tokens at all,
// so a request carrying the legacy field leaves the cap at the provider's own default
// and truncates the answer. Kimi / MiniMax / 千问 also call max_tokens deprecated, but
// they still document and honour it, so "auto" keeps sending it there — the per-model
// tokenParam switch is how a user asks for the newer field on those.
var maxCompletionTokenVendors = []struct {
	name    string
	matches func(model string) bool
}{
	{name: "mimo", matches: vendorModelPrefix("mimo")},
}

// vendorUsesMaxCompletionTokens reports whether this model's vendor declares the
// newer output-cap field (see maxCompletionTokenVendors).
func vendorUsesMaxCompletionTokens(model string) bool {
	for _, vendor := range maxCompletionTokenVendors {
		if vendor.matches != nil && vendor.matches(model) {
			return true
		}
	}
	return false
}

// shouldUseMaxCompletionTokens resolves which token-limit field the Chat request
// carries: an explicit per-model choice wins, otherwise OpenAI's reasoning series
// (which reject max_tokens) and the vendors that document only the newer field get
// max_completion_tokens, and everything else keeps max_tokens.
func shouldUseMaxCompletionTokens(param, model string) bool {
	switch normalizeTokenParam(param) {
	case tokenParamMaxCompletionTokens:
		return true
	case tokenParamMaxTokens:
		return false
	default:
		return isOpenAIReasoningModelName(model) || vendorUsesMaxCompletionTokens(model)
	}
}

func isIncompleteStreamJSON(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unexpected end of json input") || strings.Contains(msg, "unexpected eof")
}

// isOfficialOpenAIEndpoint reports whether the request targets the official
// OpenAI API. It is the single identity check for the request fields only OpenAI
// serves — prompt_cache_key / store / image_generation / explicit prompt-cache
// options — because compatible gateways reject fields they do not know.
func isOfficialOpenAIEndpoint(cfg ConfigState) bool {
	base := strings.ToLower(strings.TrimRight(baseURLForAPIFormat(cfg), "/"))
	return base == openAIOfficialAPIBaseURL || strings.HasPrefix(base, openAIOfficialAPIBaseURL+"/")
}

// isOfficialAnthropicEndpoint 报告请求是否指向 Anthropic 官方 API。与
// isOfficialOpenAIEndpoint 同型的单一身份判定:只有官方端点校验 thinking 块
// 签名的真实性,兼容网关大多只看字段存在性(baseURLForAPIFormat 已把空
// BaseURL 归一到官方默认地址,所以自定义过端点即为非官方)。
func isOfficialAnthropicEndpoint(cfg ConfigState) bool {
	base := strings.ToLower(strings.TrimRight(baseURLForAPIFormat(cfg), "/"))
	return base == defaultAnthropicMessagesURL || strings.HasPrefix(base, defaultAnthropicMessagesURL+"/")
}

// isDeepSeekAnthropicEndpoint reports whether this Messages request goes to DeepSeek's
// own Anthropic-compatible endpoint. DeepSeek is the one platform that spells the
// thinking switch on that wire as a top-level `reasoning` object instead of the
// thinking block (docs/provider-api-fields.md DeepSeek §4/§6：思考模式开关 =
// {"reasoning": {"effort": "none/low/high/max"}}（none 表示关闭思考模式），与 OpenAI
// 格式不同），而 SDK 没有这个字段 —— 那边要按请求补一个 JSON 键，并且不能再写它认识的
// thinking 块。
func isDeepSeekAnthropicEndpoint(cfg ConfigState) bool {
	return strings.Contains(strings.ToLower(baseURLForAPIFormat(cfg)), "api.deepseek.com")
}

func isOpenRouterEndpoint(cfg ConfigState) bool {
	return strings.Contains(strings.ToLower(baseURLForAPIFormat(cfg)), "openrouter")
}

// promptCacheKeyVendors lists the vendors whose own docs declare the
// session-sticky prompt_cache_key, and on which wire. The field belongs to the OpenAI
// protocol: the official API documents it, and three of the six platforms Ally
// targets document it too (docs/provider-api-fields.md) —
//   - Kimi: Chat §2（Coding Agent 通常是 session id / task id）与 Responses §3；
//   - 智谱 GLM: Responses §3（用于集群路由，以提高缓存命中率）；
//   - MiniMax: Responses §3（Prompt 缓存路由标识）。
//
// DeepSeek answers it with 不支持（静默忽略）and 千问 documents it nowhere, so those
// two keep receiving nothing. A relay built for another vendor matches no row on
// purpose: omitting the key costs only the sticky route, while a top-level key the
// endpoint does not know can come back as a 400 (pi attaches it only for
// api.openai.com, openai-completions.ts:810-814).
//
// A vendor that documents the field on one protocol and not the other carries exactly
// the protocol it documents: MiniMax's own note is 选填…（Chat 协议无此字段）, and
// 智谱 lists it for Responses alone.
var promptCacheKeyVendors = []struct {
	name      string
	matches   func(model string) bool
	responses bool
	chat      bool
}{
	{
		name:      "moonshot",
		matches:   vendorModelPrefix("kimi", "moonshot"),
		responses: true,
		chat:      true,
	},
	{
		name:      "zhipu",
		matches:   vendorModelPrefix("glm"),
		responses: true,
	},
	{
		name:      "minimax",
		matches:   vendorModelPrefix("minimax"),
		responses: true,
	},
}

// vendorDocumentsPromptCacheKey reports whether this vendor's docs declare
// prompt_cache_key on the given wire (see promptCacheKeyVendors).
func vendorDocumentsPromptCacheKey(model, apiFormat string) bool {
	format := normalizeAPIFormat(apiFormat)
	wantChat := format == apiFormatOpenAIChat
	wantResponses := format == apiFormatOpenAIResponses
	if !wantChat && !wantResponses {
		return false
	}
	for _, vendor := range promptCacheKeyVendors {
		if vendor.matches == nil || !vendor.matches(model) {
			continue
		}
		if (wantChat && vendor.chat) || (wantResponses && vendor.responses) {
			return true
		}
	}
	return false
}

// promptCacheKeyForRequest returns the session-sticky prompt_cache_key for a request
// on one of the OpenAI protocols, or "" when the field must not be sent. Both adapters
// ask this one predicate, because the rule belongs to the field and not to the adapter:
// the official API and the vendors in promptCacheKeyVendors receive the key, every
// other endpoint receives nothing. The value is the hashed session key, so no raw
// session id leaves the client. Sticky routing for a gateway that documents a header of
// its own travels in sessionAffinityHeaders instead.
func promptCacheKeyForRequest(cfg ConfigState, apiFormat, model string) string {
	key := strings.TrimSpace(cfg.responsesPromptCacheKey)
	if key == "" {
		return ""
	}
	if isOfficialOpenAIEndpoint(cfg) || vendorDocumentsPromptCacheKey(model, apiFormat) {
		return key
	}
	return ""
}

// promptCachePinStore reports whether this request may also pin `store: false`, the
// field that keeps a response from being retained server-side. It is an official OpenAI
// request field (pi pins it to false, openai-completions.ts:810-824); no platform in
// docs/provider-api-fields.md declares it on a Chat wire — DeepSeek reports it as always
// false in its responses and the others list it for Responses alone — so a compatible
// Chat endpoint receives the routing key without it.
func promptCachePinStore(cfg ConfigState) bool {
	return isOfficialOpenAIEndpoint(cfg)
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
		// name == unit repeated len(name)/period times holds exactly when every
		// byte repeats with this period; checking that in place avoids building
		// the repeated string for every candidate period.
		repeated := true
		for i := period; i < len(name); i++ {
			if name[i] != name[i%period] {
				repeated = false
				break
			}
		}
		if repeated && isKnownToolName(name[:period]) {
			return name[:period]
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
	return llmErrorTextMatchesAny(msg, llmHTTP400TextMarkers)
}

// isAnthropicSignatureRejectionError detects provider 400 errors specifically caused by
// invalid, mismatched, or corrupted thinking block signatures (e.g. Anthropic's
// "signature in thinking block ... cannot be modified" or "invalid signature").
func isAnthropicSignatureRejectionError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "signature in thinking block") {
		return true
	}
	hasThinking := strings.Contains(msg, "thinking block") || strings.Contains(msg, "`thinking`") || strings.Contains(msg, "redacted_thinking") || strings.Contains(msg, "thinking")
	hasSignatureFailure := strings.Contains(msg, "invalid signature") || strings.Contains(msg, "cannot be modified") || (strings.Contains(msg, "signature") && (strings.Contains(msg, "invalid") || strings.Contains(msg, "required") || strings.Contains(msg, "fail")))
	return hasThinking && hasSignatureFailure
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
	// send the id only after the call has already started. The name question of
	// rule 2 is asked here too: a relay that reuses one index for two calls names
	// the second one outright, and adopting its id onto the first call would splice
	// the two into one broken call (name and arguments together) instead of opening
	// the call the fragment actually declares.
	if id != "" && !hasID {
		if hasIndex && acc.synthetic[slotByIndex] && nameContinues(acc.calls[slotByIndex].Function.Name, name) {
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

const (
	apiFormatOpenAIChat        = "openai_chat"
	apiFormatOpenAIResponses   = "openai_responses"
	apiFormatAnthropicMessages = "anthropic_messages"
	// openAIOfficialAPIBaseURL is the single source of truth for the official
	// OpenAI endpoint: both adapters gate their OpenAI-only request fields on it
	// (see isOfficialOpenAIEndpoint).
	openAIOfficialAPIBaseURL      = "https://api.openai.com/v1"
	defaultOpenAIResponsesURL     = openAIOfficialAPIBaseURL
	defaultAnthropicMessagesURL   = "https://api.anthropic.com"
	tokenParamAuto                = "auto"
	tokenParamMaxTokens           = "max_tokens"
	tokenParamMaxCompletionTokens = "max_completion_tokens"
	reasoningEffortAuto           = "auto"
	// reasoningEffortOff is the explicit "stop thinking" level (the UI's 关闭思考),
	// as opposed to auto, which leaves the decision to the provider. How it reaches
	// the provider depends on the protocol: reasoningWireForAdapter owns the Chat
	// and Responses wires, configureAnthropicThinking the Anthropic one.
	reasoningEffortOff = "off"
	// reasoningEffortOffWireValue is the effort value that turns thinking off on an
	// OpenAI effort field. DeepSeek documents "none" as 关闭思考模式 for the
	// Responses API, and the official endpoint uses the same enum on its newest
	// reasoning models; a provider that accepts neither answers 400, which is the
	// same contract the other levels follow.
	reasoningEffortOffWireValue = "none"
	reasoningEffortLow          = "low"
	reasoningEffortMedium       = "medium"
	reasoningEffortHigh         = "high"
	reasoningEffortXHigh        = "xhigh"
	reasoningEffortMax          = "max"
)

// Provider-facing defaults: the endpoint a request falls back to when the model
// entry carries none, and the reasoning dialect tag the request rewrite falls
// back to (see chatReasoningBackfillKey). There is deliberately no default model
// here: the model always comes from models[] (expanded by identity), while the
// placeholder the UI shows when nothing is configured lives frontend-side
// (utils/config.mjs placeholderModel).
const (
	defaultBaseURL      = "https://api.deepseek.com"
	defaultReasoningTag = "reasoning_content"
)

// The normalizers below run on every request and every config read, so their
// Replacers are built once instead of per call.
var (
	separatorDropReplacer         = strings.NewReplacer("-", "", "_", "", " ", "")
	separatorToUnderscoreReplacer = strings.NewReplacer("-", "_", " ", "_")
)

// normalizeReasoningEffort accepts any supported spelling (with case, dash,
// space or underscore separators) and returns the canonical lowercase level.
// Unknown values fall back to "auto" so a stale or mistyped config never
// injects an unsupported parameter into a request.
func normalizeReasoningEffort(value string) string {
	v := strings.ToLower(strings.TrimSpace(value))
	v = separatorDropReplacer.Replace(v)
	switch v {
	case "auto", "default", "unset", "":
		return reasoningEffortAuto
	case "off", "none", "disabled", "nothinking", "nothink":
		return reasoningEffortOff
	case "low":
		return reasoningEffortLow
	case "medium", "med":
		return reasoningEffortMedium
	case "high":
		return reasoningEffortHigh
	case "xhigh", "extrahigh", "extremehigh":
		return reasoningEffortXHigh
	case "max", "maximum", "maximal":
		return reasoningEffortMax
	default:
		return reasoningEffortAuto
	}
}

// reasoningStopThinking names the shape of the stop-thinking request a Chat call
// carries for the "off" level. It is a named value rather than the JSON payload so the
// wire plan stays comparable (the tests compare plans wholesale); the payloads live in
// one table next to the request rewrite (reasoningStopThinkingFields).
type reasoningStopThinking string

const (
	// reasoningStopThinkingNone: this endpoint declares no stop-thinking field anywhere
	// (MiMo), so nothing is written instead of a key the endpoint does not even know
	// (see reasoningStopThinkingFamilies). A vendor whose field exists but does nothing
	// with it is deliberately not this case: it keeps its lowest effort level instead
	// (see offIsSubstitute).
	reasoningStopThinkingNone reasoningStopThinking = ""
	// reasoningStopThinkingThinkingDisabled: `thinking: {"type": "disabled"}` — the
	// spelling DeepSeek documents (its own sample passes it through extra_body, because
	// the OpenAI Chat schema has no such field) and that most compatible endpoints read.
	reasoningStopThinkingThinkingDisabled reasoningStopThinking = "thinking_disabled"
	// reasoningStopThinkingQwenEnableFalse: `enable_thinking: false` — 千问's spelling.
	// Its schema has no `thinking` parameter at all, so the generic field would be a
	// no-op there.
	reasoningStopThinkingQwenEnableFalse reasoningStopThinking = "enable_thinking_false"
)

// reasoningStopThinkingFamilies is the vendor table for the "off" level on a Chat
// wire: which field the request writes, or nothing at all where the endpoint declares
// no such field (MiMo). A model no row matches keeps the DeepSeek-style
// thinking:{"type":"disabled"}, which is what today's compatible endpoints read.
//
// Every row is keyed on the vendor, never on a model version (see
// reasoningEffortFamilies for why). A vendor whose models cannot stop thinking does not
// belong here at all: 智谱 / Kimi / MiniMax reject that field or never declared it, so
// "off" lands on their lowest effort level instead (see reasoningWireForAdapter). What
// stays is a vendor with exactly one switch on this wire (千问) or none anywhere (MiMo).
var reasoningStopThinkingFamilies = []struct {
	name     string
	matches  func(model string) bool
	spelling reasoningStopThinking
}{
	{
		// 千问 opens and closes thinking with enable_thinking (boolean); its Chat
		// schema lists no `thinking` parameter, so this is the switch on that wire.
		// 文档的适用范围清单逐个点名型号，没有列最新一代，但它那档写的是“混合模式，可按
		// 请求开关”，Chat 侧能表达开关的字段只有这一个，所以照发。
		name:     "qwen",
		matches:  vendorModelPrefix("qwen"),
		spelling: reasoningStopThinkingQwenEnableFalse,
	},
	{
		// MiMo documents no thinking switch on either wire: its Messages parameter table
		// lists model / messages / max_tokens / system / temperature / top_p / stream /
		// stop_sequences, its OpenAI-compatible page only the seven sampling parameters,
		// and its §6 states that no reasoning_effort / thinking / enable_thinking field
		// exists anywhere on the site (it does document reasoning_content on the response
		// side, which needs no request field). "Off" therefore cannot be expressed there,
		// and writing a field the endpoint never declared would only be ignored while the
		// model keeps reasoning, so nothing is sent.
		name:     "mimo",
		matches:  vendorModelPrefix("mimo"),
		spelling: reasoningStopThinkingNone,
	},
}

// reasoningStopThinkingFor returns the spelling the "off" level uses on this model's
// endpoint.
func reasoningStopThinkingFor(model string) reasoningStopThinking {
	for _, family := range reasoningStopThinkingFamilies {
		if family.matches != nil && family.matches(model) {
			return family.spelling
		}
	}
	return reasoningStopThinkingThinkingDisabled
}

// reasoningOffHasNoEffect reports whether this model's endpoint declares no
// stop-thinking switch at all (MiMo: its docs list no reasoning_effort / thinking /
// enable_thinking anywhere), in which case the off branch writes nothing — a key the
// endpoint never declared would only be ignored while the model kept reasoning.
//
// It is deliberately *not* the rule for the other two outcomes of "off":
//   - a vendor that rejects the off request outright (kimi-k2.7-code answers 400, so the
//     request says what the user asked and the user picks something else);
//   - a vendor that still has a level to fall back on (GLM-5.3, Kimi K3, MiniMax M3.1
//     land on their lowest; see offIsSubstitute). MiniMax M2.x is the price of a
//     vendor-keyed row: it ignores the level its M3.1 sibling honours, so "off" writes a
//     field that changes nothing there, and covering it would mean splitting the vendor
//     by generation — the mistake these tables exist to avoid.
//
// The fact belongs to the vendor rather than to one wire, so the Anthropic adapter
// reads the same predicate even though it spells the field differently.
func reasoningOffHasNoEffect(model string) bool {
	return reasoningStopThinkingFor(model) == reasoningStopThinkingNone
}

// reasoningWirePlan is what the selected thinking level puts on a non-Anthropic
// wire. It is the single mapping for every level, so an adapter never decides a
// spelling of its own.
type reasoningWirePlan struct {
	// Effort is the value for the protocol's effort field (Chat:
	// reasoning_effort, Responses: reasoning.effort); "" means the field must not
	// be sent at all.
	Effort string
	// StopThinking is the field the "off" level writes on a Chat Completions
	// request; reasoningStopThinkingNone means nothing is written.
	StopThinking reasoningStopThinking
}

// reasoningWireForAdapter maps the configured level to the fields this request
// carries. The level is resolved against the model's vendor first (see
// reasoningEffortFamilies), so a vendor that declares the field receives the level
// it can honour — verbatim, translated, or clamped — and a vendor that declares
// nothing receives nothing at all.
//
// "off" is the one level with no shared spelling, and the protocol decides its
// spelling:
//   - Responses, and the official OpenAI Chat endpoint: an effort value — the
//     vendor's own off value ("none" for DeepSeek and the OpenAI reasoning models),
//     or the vendor's lowest level where the docs say the model cannot stop thinking.
//   - Chat on a compatible endpoint: the vendor's own spelling of the stop-thinking
//     request (see reasoningStopThinkingFamilies) — `thinking: {"type": "disabled"}`,
//     the field DeepSeek documents and whose own sample passes through extra_body, for
//     most of them, `enable_thinking: false` for 千问, and nothing at all where the
//     endpoint documents the field as ineffective. The request rewrite adds it. A model
//     whose docs say it cannot stop thinking skips that field entirely and lands on its
//     lowest level instead (GLM-5.3 / Kimi K3 / MiniMax M3.1): the field is either one
//     the endpoint never declared or one it rejects, and the level is the lever its docs
//     do offer.
func reasoningWireForAdapter(cfg ConfigState, apiFormat, model, effort string) reasoningWirePlan {
	level := normalizeReasoningEffort(effort)
	if level == reasoningEffortOff && !reasoningOffTravelsAsEffort(cfg, apiFormat) {
		if family, ok := reasoningEffortFamilyFor(model); ok && family.offIsSubstitute() {
			return reasoningWirePlan{Effort: family.offWireValue()}
		}
		return reasoningWirePlan{StopThinking: reasoningStopThinkingFor(model)}
	}
	return reasoningWirePlan{Effort: reasoningEffortForModel(model, level)}
}

// reasoningOffTravelsAsEffort reports whether this request spells "off" as an
// effort value rather than as the stop-thinking field.
func reasoningOffTravelsAsEffort(cfg ConfigState, apiFormat string) bool {
	return normalizeAPIFormat(apiFormat) == apiFormatOpenAIResponses || isOfficialOpenAIEndpoint(cfg)
}

func normalizeTokenParam(value string) string {
	v := strings.ToLower(strings.TrimSpace(value))
	v = separatorToUnderscoreReplacer.Replace(v)
	switch v {
	case tokenParamMaxCompletionTokens, "max_completion_token", "completion_tokens", "completion":
		return tokenParamMaxCompletionTokens
	case tokenParamMaxTokens, "max_token", "tokens", "legacy":
		return tokenParamMaxTokens
	default:
		return tokenParamAuto
	}
}

func normalizeAPIFormat(value string) string {
	v := strings.ToLower(strings.TrimSpace(value))
	v = separatorToUnderscoreReplacer.Replace(v)
	switch v {
	case "", "openai", "openai_compatible", "openai_chat", "chat", "chat_completions", "chat_completion":
		return apiFormatOpenAIChat
	case "openai_responses", "responses", "response":
		return apiFormatOpenAIResponses
	case "anthropic", "anthropic_messages", "claude", "claude_messages", "messages":
		return apiFormatAnthropicMessages
	default:
		return apiFormatOpenAIChat
	}
}

func defaultBaseURLForAPIFormat(format string) string {
	switch normalizeAPIFormat(format) {
	case apiFormatOpenAIResponses:
		return defaultOpenAIResponsesURL
	case apiFormatAnthropicMessages:
		return defaultAnthropicMessagesURL
	default:
		return defaultBaseURL
	}
}

func baseURLForAPIFormat(cfg ConfigState) string {
	base := strings.TrimSpace(cfg.BaseURL)
	if base == "" {
		base = defaultBaseURLForAPIFormat(cfg.APIFormat)
	}
	base = strings.TrimRight(base, "/")
	if normalizeAPIFormat(cfg.APIFormat) == apiFormatAnthropicMessages && strings.HasSuffix(strings.ToLower(base), "/v1") {
		base = base[:len(base)-3]
	}
	return base
}

func defaultMaxTokensForAPIFormat(format string) int {
	// Unified fallback for every API format: one number, no per-format
	// branching. 131072 (128K) covers current catalog entries; relay
	// endpoints typically clamp server-side.
	return 131072
}

// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"hash"
	"strconv"
	"strings"
	"sync"

	openai "github.com/sashabaranov/go-openai"
)

// 请求前缀冻结核（request prefix freeze）。
//
// 命中供应商提示词缓存的前提是"这次发出去的请求，开头那一段与上一次逐字相同"。
// 系统提示词、工作区文件图、工具集已经按会话冻结（sessionSystemPrompts /
// sessionWorkspaceMaps / sessionToolsets），但"冻结"只说明没人主动重建它们，并
// 不构成任何证据：历史被改写、适配器按当前配置重写历史、图片按模型能力降级……
// 都会在发出去的那一刻改动前缀，而且不留痕迹——只能从缓存命中率莫名下降里事后猜。
//
// 这里做的是**测量**而不是**清单**：不维护"哪些代码会改前缀"（这种清单一定会
// 漏），而是拿上一轮真实发出去的样子当基准比对这一轮。任何来源的改动只要落到前
// 缀上就会被记下来，并指出是头部变了、还是第几条变了。
//
// 口径（刻意保持简单）：
//   - 头部算一个哈希：开头的连续 system 消息（系统提示词 + 工作区文件图）+ 工具
//     清单，只有上下文本身，不含任何请求参数（理由见 fingerprintRequest）；
//   - 之后每条消息一个哈希，顺序即线上顺序（整段合并成一个哈希就只能报"变了"，
//     报不出"第几条变"——而对话恰恰是最常变的地方）；
//   - 命名空间 = 模型 + 协议：换了就等于换缓存，直接重开基线（记一行日志），不做
//     比对——那时前缀本来就整体不同，比出来的差异没有意义；
//   - 历史被合法改写（压缩、删回合、从磁盘重载、落盘修复）时基线一并丢弃：复用
//     读缓存的失效钩子（invalidateSessionReadCache），两者同生同灭；
//   - 只读、只记：不改请求内容，也不拦请求。
//
// 性能：每轮一次 O(前缀字节) 的哈希（几十万字节 ≈ 零点几毫秒），相对一次网络请求
// 可以忽略；每个哈希只占 32 字节。要更省可以只算新增的那几条，现在不做——简单优先。

// contextLockLaneChat 是主对话的泳道名。子代理、压缩总结各自是一条独立泳道，前缀
// 本来就完全不同，混进一条基线只会天天假警（同 cache-route-vs-replay-scope 的教训）。
const contextLockLaneChat = "chat"

// contextFingerprint 是"上一轮发出去的样子"。
type contextFingerprint struct {
	head string   // 头部（system 段 + 工具）的哈希
	msgs []string // 每条消息一个哈希，顺序即线上顺序
}

// contextLockVerdict 是一次比对的结果；kind 为空表示没变化。index 是相对非头部
// 消息的下标（-1 表示与具体某条无关）。
type contextLockVerdict struct {
	kind  string // "head" | "message" | "removed"
	index int
}

// sessionContextLock 是一个会话的基线表：键是"泳道 + 命名空间"，所以同一会话里换过
// 模型会各留一条，互不干扰（旧的那条自然过期）。
type sessionContextLock struct {
	mu      sync.Mutex
	entries map[string]contextFingerprint
}

// contextLockNamespace 是"这份请求属于哪个缓存"：模型和协议一变，前缀本来就整体
// 不同，比对无意义，只能重新记基线。
func contextLockNamespace(cfg ConfigState) string {
	return cfg.Model + "\x00" + normalizeAPIFormat(cfg.APIFormat)
}

// 指纹里刻意**没有**"请求参数"这一项：缓存的判据是提示词的 token 前缀，而思考强度、
// max_tokens、token 参数、是否官方端点这些值各自只占请求体里的一个顶层字段，上下文一个
// 字节都不会动。把它们算进来只会得到假警——实测教训：思考强度 low→high 报过一次"缓存已
// 重建"，而那一次请求的提示词逐字没变。
//
// 两个容易被重新加回来的候选，各自的正确去处：
//   - 模型与协议：它们是命名空间（见 contextLockNamespace）。换了那份缓存本来就整体不同，
//     重开基线即可，不必进头部哈希；
//   - 视觉能力：它的差异落在**消息内容**上（图片换成占位文字），逐条消息的哈希本来就抓得到，
//     放进头部只会把"第几条变了"笼统地报成"头部变了"。
//
// 还有一种本层注定看不到的：由配置**派生**、真的会改写线上消息的规则（例如关思考时历史
// 助手消息里不再写空的思考字段）。指纹取的是改写**之前**的消息结构，所以这里量不到它——
// 要抓它只能把手指挪到改写之后去量，而不是拿配置值当代理：代理指标一定会收错项。

// fingerprintRequest 把一次请求切成"头部一个哈希 + 每条消息一个哈希"，并返回头部
// 由开头的几条 system 消息组成。这两部分合起来就是供应商要缓存的全部内容：系统段、工具
// 清单、按序排列的消息，别的一概不算。
func fingerprintRequest(messages []openai.ChatCompletionMessage, tools []openai.Tool) (int, contextFingerprint) {
	head := sha256.New()
	headCount := 0
	for headCount < len(messages) && messages[headCount].Role == openai.ChatMessageRoleSystem {
		writeFramedHash(head, []byte(messages[headCount].Role))
		writeFramedHash(head, []byte(messages[headCount].Content))
		headCount++
	}
	if raw, err := json.Marshal(tools); err == nil {
		writeFramedHash(head, raw)
	} else {
		writeFramedHash(head, []byte("tools-unhashable"))
	}

	fp := contextFingerprint{head: hex.EncodeToString(head.Sum(nil)), msgs: make([]string, 0, len(messages)-headCount)}
	for _, message := range messages[headCount:] {
		fp.msgs = append(fp.msgs, hashContextMessage(message))
	}
	return headCount, fp
}

// writeFramedHash 写入"8 字节长度 + 内容"：拼接必须有边界，否则 ["ab"]["c"] 与
// ["a"]["bc"] 会算出同一个哈希，两种不同的切分被当成同一份内容。
func writeFramedHash(h hash.Hash, data []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(data)))
	_, _ = h.Write(size[:])
	_, _ = h.Write(data)
}

// hashContextMessage 用消息的线上结构（JSON）算哈希：角色、正文、工具调用、思考
// 字段都在里面，任何一处变了都算变。
func hashContextMessage(message openai.ChatCompletionMessage) string {
	raw, err := json.Marshal(message)
	if err != nil {
		// 结构化字段里出现无法序列化的值时退化为 role + content：仍然确定，
		// 只是粒度粗一点。
		raw = []byte(message.Role + "\x00" + message.Content)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// compareContextFingerprint 比对基准与本次请求：头部不同 → "head"；某一条不同 →
// "message" 并给出下标；比基准短 → "removed"（历史被截断）。
func compareContextFingerprint(base, current contextFingerprint) contextLockVerdict {
	if base.head != current.head {
		return contextLockVerdict{kind: "head", index: -1}
	}
	for i := 0; i < len(base.msgs) && i < len(current.msgs); i++ {
		if base.msgs[i] != current.msgs[i] {
			return contextLockVerdict{kind: "message", index: i}
		}
	}
	if len(current.msgs) < len(base.msgs) {
		return contextLockVerdict{kind: "removed", index: len(current.msgs)}
	}
	return contextLockVerdict{index: -1}
}

// sessionContextLockFor 取（必要时建）一个会话的基线表，镜像 sessionReadCacheFor：
// a.mu 只保护这张总表本身，哈希计算一律在锁外。
func (a *App) sessionContextLockFor(sessionID string) *sessionContextLock {
	if strings.TrimSpace(sessionID) == "" {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.contextLocks == nil {
		a.contextLocks = map[string]*sessionContextLock{}
	}
	if lock, ok := a.contextLocks[sessionID]; ok && lock != nil {
		return lock
	}
	lock := &sessionContextLock{entries: map[string]contextFingerprint{}}
	a.contextLocks[sessionID] = lock
	return lock
}

// noteRequestPrefix 记录并比对一次即将发出的请求。发现前缀变化只记日志，不改请求、
// 不拦请求——它的价值是让"缓存莫名其妙不命中"变成一条能定位的记录（哪条泳道、是
// 头部还是第几条、那一条是什么角色）。
//
// 基线在比对的同时就被更新：一次漂移只报一次，失败重试不会反复报同一条。
func (a *App) noteRequestPrefix(sessionID, lane string, cfg ConfigState, messages []openai.ChatCompletionMessage, tools []openai.Tool) contextLockVerdict {
	headCount, fp := fingerprintRequest(messages, tools)
	lock := a.sessionContextLockFor(sessionID)
	if lock == nil {
		return contextLockVerdict{index: -1}
	}
	namespace := contextLockNamespace(cfg)
	entryKey := lane + "\x00" + namespace

	lock.mu.Lock()
	base, seen := lock.entries[entryKey]
	hadOthers := len(lock.entries) > 0
	lock.entries[entryKey] = fp
	lock.mu.Unlock()

	if !seen {
		if hadOthers {
			// 换模型/换协议：前缀整体不同，这是"重开基线"而不是漂移，记一行
			// 便于解释缓存为什么整段重建。
			a.logAppError("request prefix baseline reopened", "session", sessionID, "lane", lane, "namespace", strings.ReplaceAll(namespace, "\x00", "/"))
		}
		return contextLockVerdict{index: -1}
	}

	verdict := compareContextFingerprint(base, fp)
	if verdict.kind == "" {
		return verdict
	}
	at := ""
	if verdict.index >= 0 {
		at = contextMessageLabel(messages, headCount, verdict.index)
	}
	fields := []any{
		"session", sessionID,
		"lane", lane,
		"kind", verdict.kind,
		"headMessages", headCount,
		"messageCount", len(fp.msgs),
	}
	if verdict.index >= 0 {
		fields = append(fields, "index", verdict.index, "at", at)
	}
	a.logAppError("request prefix changed mid-session: the cached prefix is no longer reusable", fields...)
	// 同时告诉前端：只提示、不拦请求——这一轮照常发出去（App.vue 的
	// context:drift 处理）。换模型/协议那种"重开基线"不发这条，那是用户自己
	// 的动作，不是漂移。
	a.emit("context:drift", map[string]any{
		"sessionId":    sessionID,
		"lane":         lane,
		"kind":         verdict.kind,
		"index":        verdict.index,
		"at":           at,
		"headMessages": headCount,
		"messageCount": len(fp.msgs),
	})
	return verdict
}

// contextMessageLabel 给出一条消息的身份提示，让日志能直接对上"第几条是什么"。
func contextMessageLabel(messages []openai.ChatCompletionMessage, headCount, index int) string {
	pos := headCount + index
	if index < 0 || pos < 0 || pos >= len(messages) {
		return ""
	}
	message := messages[pos]
	label := string(message.Role)
	if len(message.ToolCalls) > 0 {
		label += "(tool_calls=" + strconv.Itoa(len(message.ToolCalls)) + ")"
	}
	if message.ToolCallID != "" {
		label += "(tool_call_id=" + message.ToolCallID + ")"
	}
	return label
}

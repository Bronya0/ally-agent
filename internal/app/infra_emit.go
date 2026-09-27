// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General Public
// License v3. See the LICENSE file for details.
package app

import "time"

// 事件节流收口（single source of truth）：所有高频事件（流式正文与思考、工具参数与
// 进度、命令输出、下载进度）的节流参数和节流原语都在这里，调用点只引用，不再各自
// 写 `time.Now().Sub(lastEmit) < X`。
//
// 三种语义必须分开实现，只共用参数与记账原语：
//   - 合并（coalescer）：窗口内攒住增量，到点发一条合并事件（textDeltaCoalescer，
//     见 infra_stream.go）。
//   - 采样（sampler）：定时去读"当前状态"（命令输出的尾巴），不是攒增量，所以仍是
//     ticker，只把参数放进本表。
//   - 收尾（force flush）：合并器与采样器都必须有人在结束时收尾，否则丢尾巴。
//
// 新增高频事件时在 eventCadenceTable 里显式选一档；没登记就是没有节流
// （cadenceForEvent 返回零值）——压缩流的思考增量曾经就是这样漏掉的。
// 流式文本事件有两个桶（正文 + 思考，窗口不同），登记处是 streamingTextEvents：
// 只在那里加事件名，两个桶的档位就一起生成。

// emitCadence 是一个事件的节流档位。
type emitCadence struct {
	// interval 是两次事件之间的最小间隔；0 表示不节流（每个增量都发）。
	interval time.Duration
	// byteThreshold 大于 0 时，载荷小于它的调用不受 interval 限制：小载荷本来就
	// 不贵，节流它们只会让界面显得卡顿。
	byteThreshold int
}

const (
	// runStreamInterval 是流式文本事件（正文 + 思考计数）的节流窗口。纯时间制：
	// 第一个字节立即 flush，之后按窗口合并。64ms ≈ 15 FPS，打字机效果足够顺，
	// 同时把 IPC 压到低位（Wails 事件是一次 JS 调用）。
	runStreamInterval = 64 * time.Millisecond
	// streamReasoningInterval 是思考增量自己的窗口，比正文宽：思考只驱动一个
	// "思考中"标签与一个估算 token 数，不需要正文那样的 15FPS，而长回合里它
	// 往往是事件量的大头（思考期 ≈5/s，而不是跟正文一起 15.6/s）。
	streamReasoningInterval = 200 * time.Millisecond
	// toolUpdateInterval 限制单个工具调用的参数/进度更新频率：窗口内、参数超过
	// toolUpdateThreshold 的更新被丢弃（最终状态由 forceEvents 兜底）。
	toolUpdateInterval = 200 * time.Millisecond
	// toolUpdateThreshold 是参数/输出多大才算"贵"的门槛（字节）。
	toolUpdateThreshold = 2048
	// updateProgressInterval 是自更新下载进度的事件间隔。
	updateProgressInterval = 500 * time.Millisecond
	// commandOutputSampleInterval 与 commandOutputStreamingTailBytes 是命令执行
	// 输出的采样参数：每 120ms 采一次，且只发尾部 16KB（完整输出在命令结束时随
	// 结果一次性返回）。
	commandOutputSampleInterval     = 120 * time.Millisecond
	commandOutputStreamingTailBytes = 16 * 1024
)

// 事件名与档位放在一起，避免"名字在一处、节流参数在另一处"。
const (
	// runStreamEvent 是主对话的合并流事件：正文与思考增量共用一次 IPC 发出，
	// 比拆成 run:reasoning + run:delta 少一半事件。
	runStreamEvent = "run:stream"
	// compactDeltaEvent 是压缩总结的流式增量事件。
	compactDeltaEvent = "compact:delta"
	// toolUpdateEvent 携带工具参数流或命令输出的进度。
	toolUpdateEvent = "tool:update"
	// updateProgressEvent 是应用自更新的进度事件。
	updateProgressEvent = "update:progress"
)

// 流式文本事件走一对桶：正文用事件名本身当桶名，思考桶名加后缀，两者窗口不同
// （正文 64ms / 思考 200ms），必须分开记账。
const reasoningBucketSuffix = "#reasoning"

// reasoningBucketFor 返回一个流式事件对应的思考桶名。
func reasoningBucketFor(event string) string {
	return event + reasoningBucketSuffix
}

// streamingTextEvents 是所有"正文 + 思考"的流式文本事件，也是这对档位的登记处：
// 新增流式事件时只需要加进这里，正文档与思考桶会一起生成——不会出现"登记了正文、
// 忘了思考桶"（那样思考增量会静默无节流，压缩流曾经就是这样漏的）。
var streamingTextEvents = []string{runStreamEvent, compactDeltaEvent}

// eventCadenceTable 是节流参数的唯一来源。
var eventCadenceTable = buildEventCadenceTable()

func buildEventCadenceTable() map[string]emitCadence {
	table := map[string]emitCadence{
		// 工具参数流与命令输出的进度：小载荷不节流（否则卡片半天不动），大载荷按
		// 窗口限制。
		toolUpdateEvent: {interval: toolUpdateInterval, byteThreshold: toolUpdateThreshold},
		// 下载进度：进度条不需要更密的更新。
		updateProgressEvent: {interval: updateProgressInterval},
	}
	// 流式文本：正文全量、思考只发字符数（见 textDeltaCoalescer）。
	for _, event := range streamingTextEvents {
		table[event] = emitCadence{interval: runStreamInterval}
		table[reasoningBucketFor(event)] = emitCadence{interval: streamReasoningInterval}
	}
	return table
}

// cadenceForEvent 取一个事件的档位；未登记的事件返回零值（= 不节流）。
func cadenceForEvent(name string) emitCadence {
	return eventCadenceTable[name]
}

// emitMap 把 a.emit 适配成合并器需要的 map 载荷回调（emit 的载荷类型是 any）。
func (a *App) emitMap() func(string, map[string]any) {
	return func(name string, payload map[string]any) { a.emit(name, payload) }
}

// emitThrottle 是所有合并/采样点共用的时间窗节流原语，取代散落各处的 lastEmit
// 字段与手写的时间比较。零值可用：首次调用总是放行。
type emitThrottle struct {
	last time.Time
}

// wouldAllow 只查询、不记账：调用点需要先做昂贵判断（构造流式参数的状态哈希）时
// 用它预筛，避免为注定被丢弃的事件白算一遍。
func (t *emitThrottle) wouldAllow(now time.Time, cadence emitCadence, payloadSize int) bool {
	if cadence.byteThreshold > 0 && payloadSize < cadence.byteThreshold {
		return true
	}
	return t.last.IsZero() || cadence.interval <= 0 || now.Sub(t.last) >= cadence.interval
}

// allow 判断并记账，返回这次事件是否放行。
func (t *emitThrottle) allow(now time.Time, cadence emitCadence, payloadSize int) bool {
	if !t.wouldAllow(now, cadence, payloadSize) {
		return false
	}
	t.last = now
	return true
}

// force 记账一次"无视节流的发送"，让随后的窗口从这一刻重新计时。
func (t *emitThrottle) force(now time.Time) {
	t.last = now
}

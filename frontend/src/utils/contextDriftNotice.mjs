// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

// 前缀漂移提示：对话流里的一条内联告警行（后端 biz_context_lock.go 的
// context:drift 事件）。
//
// 为什么不塞进 session.messages：那份数组会被存到磁盘，也会作为模型上下文发回供应
// 商——提示一旦进去，就自己改掉了它正在保护的那段前缀。所以提示只活在显示层，由
// App.vue 按会话单独存（像 archive 折叠行那样合并进显示列表），重启即丢。
//
// 文案由调用方传入：utils/*.mjs 不 import i18n.mjs（顶层会拉 naive-ui，node --test
// 起不来）。

// 判"同一个原因"的键：报出的是哪一类、第几条。历史段已合并成整段一个哈希，所以 tail
// 恒为 tail:-1（拿不到下标）；head 恒为 head。removed 仍带截断点，是唯一有真下标的。
export function contextDriftNoticeKey(data) {
  const kind = String(data?.kind || 'head');
  if (kind === 'head') return 'head';
  return `${kind}:${Number(data?.index ?? -1)}`;
}

// 同一条消息上反复重试（原因相同、之后没有新消息）只留一条，别把流刷满。
export function shouldRecordContextDriftNotice(notices, key, messageCount) {
  const list = Array.isArray(notices) ? notices : [];
  const last = list.length ? list[list.length - 1] : null;
  if (!last) return true;
  return !(last.key === key && last.messageCount === messageCount);
}

// 归档行的约定（role === 'archive'、count = 被裁掉的条数）由 toolPreview.displaySourceMessages
// 建立，这里只解读一次：可见来源段的起点、条数，以及"窗口下标 → 绝对下标"的换算。
function sourceWindowShape(sourceRows) {
  const rows = Array.isArray(sourceRows) ? sourceRows : [];
  const hasArchive = rows.length > 0 && rows[0] && rows[0].role === 'archive';
  const start = hasArchive ? Number(rows[0].count || 0) : 0;
  const offset = hasArchive ? 1 : 0;
  return { start, offset, count: start + rows.length - offset };
}

// 提示该落在哪一步之后：记录当时"可见来源消息"的条数。
//
// 为什么不存行号：显示列表是"末尾 N 行"的滑动窗口（displaySourceMessages），超过上限
// 后每加一条就从头部掉一条，行号整体前移。把行号存下来，窗口一滑它就不再指向同一批
// 内容——一旦对话超过上限，新提示算出的行号恒等于窗口末尾，于是永远贴在底部。来源
// 位置只在尾部增长，才是能跨窗口滑动保持不变的量。
export function contextDriftAnchor(sourceRows) {
  return sourceWindowShape(sourceRows).count;
}

// 可见来源窗口的起点，加上"窗口下标 → 绝对下标"的换算。调用方建显示行时用它给每一行
// 记下来源位置，作为 insertContextDriftNotices 的 rowSourceIndex（归档行落在起点之前
// 那一格，它代表的就是被裁掉的那一段）。
export function sourceWindowMapper(sourceRows) {
  const shape = sourceWindowShape(sourceRows);
  return {
    start: shape.start,
    indexOf: (windowIndex) => shape.start + windowIndex - shape.offset,
  };
}

// 把提示插回它发生的位置：紧挨着锚点所指的那一步之后。
//
// rows               显示行（不含提示）
// notices            提示，每条带 anchor（见 contextDriftAnchor）
// rowSourceIndex     与 rows 一一对应，第 k 项是产出该行的最后一个来源消息的绝对下标
// firstVisibleIndex  当前可见段的第一个来源消息下标（无归档行时为 0）
//
// 锚点已经滚出可见段（anchor < firstVisibleIndex）的提示直接丢掉：它要标注的那一步不在
// 渲染窗口里了，硬贴进来只会指向错误的位置。行按来源位置单调排布，于是一趟扫描就能把
// 每条提示放到它该在的那一行前面。
export function insertContextDriftNotices(rows, notices, rowSourceIndex, firstVisibleIndex) {
  const list = Array.isArray(rows) ? rows : [];
  const all = Array.isArray(notices) ? notices.filter(Boolean) : [];
  if (all.length === 0) return list;
  const indexes = Array.isArray(rowSourceIndex) ? rowSourceIndex : [];
  const visibleFrom = Number.isFinite(firstVisibleIndex) ? firstVisibleIndex : 0;
  const live = all.filter((notice) => {
    const anchor = Number(notice.anchor);
    return Number.isFinite(anchor) && anchor >= visibleFrom;
  });
  if (live.length === 0) return list;

  const out = [];
  let next = 0;
  for (let k = 0; k < list.length; k++) {
    const at = Number(indexes[k]);
    // 来源位置缺失时不做判定：提示宁可靠后，也不要乱插。
    if (Number.isFinite(at)) {
      while (next < live.length && Number(live[next].anchor) <= at) {
        out.push(live[next]);
        next++;
      }
    }
    out.push(list[k]);
  }
  while (next < live.length) {
    out.push(live[next]);
    next++;
  }
  return out;
}

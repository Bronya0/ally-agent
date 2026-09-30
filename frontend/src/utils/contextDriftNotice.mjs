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

// 判"同一个原因"的键：报出的是哪一类、第几条。
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

// insertAt 的计量基准 = 合并后列表里的"非提示行"条数。合并后的列表正好是
// 非提示行 + 提示行，所以减掉已有提示条数就是基准；这条算式收在这里，调用点不必
// 各自重推一遍——那个前提（提示确实活在列表里、且条数与 noticeCount 一致）也就从
// 注释里的约定变成了代码里的一次调用。
export function contextDriftInsertAt(mergedRows, noticeCount) {
  const total = Array.isArray(mergedRows) ? mergedRows.length : 0;
  const notices = Number(noticeCount);
  return Math.max(total - (Number.isFinite(notices) ? Math.max(0, notices) : 0), 0);
}

// 把提示插回它发生时所在的位置：insertAt 计的是当时的"非提示行"行数（即 rows 的
// 下标），而 rows 只会在尾部增长（历史被归档裁剪时从头部去掉），所以那个下标之后
// 仍落在同一批行上。越界时（例如列表刚被裁短）贴到末尾，不改行内容。
export function insertContextDriftNotices(rows, notices) {
  const list = Array.isArray(rows) ? rows : [];
  if (!Array.isArray(notices) || notices.length === 0) return list;
  const out = [...list];
  let inserted = 0;
  for (const notice of notices) {
    if (!notice) continue;
    const raw = Number(notice.insertAt);
    // 前面已插入的提示会把后面的位置顶下去一位（insertAt 递增，所以加 inserted 就
    // 是它现在的下标）。
    const at = Number.isFinite(raw) ? Math.min(Math.max(raw, 0) + inserted, out.length) : out.length;
    out.splice(at, 0, notice);
    inserted++;
  }
  return out;
}

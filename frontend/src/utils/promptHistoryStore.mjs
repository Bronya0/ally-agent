/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */

/**
 * 输入框上箭头召回历史（每个工作区一把），落 localStorage，重启后仍在。
 *
 * localStorage 配额与应用其他状态共享，所以这个 store 的上限是结构性的，不靠
 * 用户自觉：
 *   - 每个工作区最多 MAX_ENTRIES_PER_WORKSPACE 条，超出丢最旧的；
 *   - 单条超过 MAX_ENTRY_CHARS 直接不入库（靠方向键召回的不是大段粘贴，收下它
 *     一条就能把别的工作区整桶挤掉）；
 *   - 全库超过 MAX_TOTAL_CHARS 时，从最久未写入的工作区开始丢条目，桶空即移除；
 *   - 工作区从历史列表移除 / 临时目录销毁时由调用方显式 remove，桶不残留。
 *
 * 桶键是调用方归一化后的工作区路径（同一目录的不同斜杠/大小写共享一桶）；桶的
 * 排列顺序 = 最近写入顺序（最近写的排最后）。纯逻辑 + 注入 storage，便于单测。
 */

const STORAGE_VERSION = 2;

export const PROMPT_HISTORY_STORAGE_KEY = 'ally_prompt_history_store';
/** 每个工作区保留的条数上限。 */
export const PROMPT_HISTORY_MAX_ENTRIES = 20;
/** 单条长度上限（UTF-16 码元）：超过则不记，避免一条巨型粘贴挤掉其他工作区。 */
export const PROMPT_HISTORY_MAX_ENTRY_CHARS = 8000;
/** 全库字符预算，超限时按“最久未写入的工作区优先”淘汰。 */
export const PROMPT_HISTORY_MAX_TOTAL_CHARS = 200000;

/** 去重、丢弃非法/超长项、只保留最新 MAX_ENTRIES_PER_WORKSPACE 条（旧→新）。 */
function clampEntries(entries) {
  const kept = [];
  for (const raw of Array.isArray(entries) ? entries : []) {
    if (typeof raw !== 'string' || !raw || raw.length > PROMPT_HISTORY_MAX_ENTRY_CHARS) continue;
    const duplicate = kept.indexOf(raw);
    if (duplicate !== -1) kept.splice(duplicate, 1);
    kept.push(raw);
  }
  return kept.slice(-PROMPT_HISTORY_MAX_ENTRIES);
}

function countChars(buckets) {
  let total = 0;
  for (const [, entries] of buckets) {
    for (const entry of entries) total += entry.length;
  }
  return total;
}

/**
 * 把全库收进字符预算：从最久未写入的工作区开始，先丢该桶最旧的条目，桶空即整桶
 * 移除；极端情况（刚写入的那条自己就超预算）连它一起丢，保证结果始终有界。
 * 不修改入参。
 */
function fitBudget(buckets) {
  const fitted = buckets.map(([key, entries]) => [key, [...entries]]);
  let total = countChars(fitted);
  while (total > PROMPT_HISTORY_MAX_TOTAL_CHARS) {
    const oldest = fitted.find(([, entries]) => entries.length > 0);
    if (!oldest) break;
    total -= oldest[1].shift().length;
  }
  return fitted.filter(([, entries]) => entries.length > 0);
}

/**
 * 按工作区分桶的输入历史 store。storage 默认 localStorage，可注入假对象单测；
 * 读写异常（被禁用 / 超配额 / 内容损坏）一律降级处理，绝不抛给 UI。
 */
export function createPromptHistoryStore(storage = globalThis.localStorage) {
  let buckets = null; // [[bucketKey, entries]]，最久未写的在前

  function readStorage() {
    try {
      const raw = storage?.getItem(PROMPT_HISTORY_STORAGE_KEY);
      if (!raw) return [];
      const parsed = JSON.parse(raw);
      if (!parsed || parsed.version !== STORAGE_VERSION || !Array.isArray(parsed.buckets)) return [];
      const loaded = [];
      for (const item of parsed.buckets) {
        if (!Array.isArray(item) || typeof item[0] !== 'string') continue;
        const entries = clampEntries(item[1]);
        if (entries.length) loaded.push([item[0], entries]);
      }
      return fitBudget(loaded);
    } catch {
      return []; // 解析失败或 storage 不可用：当作空历史
    }
  }

  function all() {
    if (buckets === null) buckets = readStorage();
    return buckets;
  }

  function writeStorage() {
    try {
      storage?.setItem(PROMPT_HISTORY_STORAGE_KEY, JSON.stringify({ version: STORAGE_VERSION, buckets }));
    } catch {
      // 超配额/被禁用：本次会话仍以内存态为准（下次启动读不到），不阻塞输入。
      // 这里刻意不做“丢点东西再试”的投机恢复：全库本身已有上限，写失败基本
      // 意味着浏览器直接拒绝写入，删数据既救不回来，还会把刚记的内容删掉。
    }
  }

  function indexOf(key) {
    return all().findIndex(([bucketKey]) => bucketKey === key);
  }

  function list(key) {
    const at = indexOf(key);
    return at === -1 ? [] : [...all()[at][1]];
  }

  /** 追加一条并返回该工作区最终的条目列表（旧→新）。 */
  function record(key, text) {
    const value = typeof text === 'string' ? text : '';
    if (!value) return list(key);
    const store = all();
    const at = store.findIndex(([bucketKey]) => bucketKey === key);
    const previous = at === -1 ? [] : store[at][1];
    if (at !== -1) store.splice(at, 1);
    const clamped = clampEntries([...previous, value]);
    // 超长条目会被 clampEntries 丢掉：此时保持原桶不变，不能因为发了一条长消息
    // 就清空该工作区已有历史。
    const next = clamped.length ? clamped : previous;
    if (next.length) store.push([key, next]); // 重新入列 = 最近写入
    buckets = fitBudget(store);
    writeStorage();
    return list(key);
  }

  /** 弃置一个工作区的桶（工作区从历史移除 / 临时目录销毁）。 */
  function remove(key) {
    const at = indexOf(key);
    if (at === -1) return;
    all().splice(at, 1);
    writeStorage();
  }

  return { list, record, remove };
}

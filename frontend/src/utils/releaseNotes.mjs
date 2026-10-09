/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
// 新版本首次启动展示更新日志：「该不该弹」的判定与「看过没」的存储都收口在这里。
//
// 正文是 vite 构建时注入的常量（来自 release 事件，见 vite.config.js 的 define），
// 不进后端、不进配置；运行期不读盘、不联网。

function readInjected() {
  // typeof 守卫对未声明的标识符是安全的：node --test 下没有 vite 的 define，写成裸引用
  // 会 ReferenceError，整个模块在测试环境里直接崩。
  const injected = typeof __ALLY_RELEASE_NOTES__ === 'object' && __ALLY_RELEASE_NOTES__ !== null
    ? __ALLY_RELEASE_NOTES__
    : null;
  return {
    version: String(injected?.version || '').trim(),
    body: String(injected?.body || '').trim(),
  };
}

// 编译期注入、一个进程内不会变：是常量，不是状态。
export const releaseNotes = readInjected();

// 什么算"一版对外发布"的版本号。两处判据共用这一条，别各写一份正则：更新检查用它跳过
// dev 构建（dev 构建的版本号是时间戳），更新日志用它保证只在正式版本上弹一次。
const RELEASE_VERSION_RE = /^v?\d+\.\d+\.\d+$/;
export function isReleaseTag(version) {
  return RELEASE_VERSION_RE.test(String(version || '').trim());
}

export const RELEASE_NOTES_SEEN_KEY = 'ally_release_notes_seen_version';

// 该不该弹：正式版本号 + 有正文 + 这台机器还没看过这一版。
//
// 版本号那条同时挡住了 dev 构建（时间戳版本号，每次重新构建都会变，不挡就是天天弹）；
// 正文那条挡住了没带 release 正文的构建（手动触发、本地打包），此时没东西可展示。
export function shouldShowReleaseNotes({ notes, seenVersion }) {
  const version = String(notes?.version || '').trim();
  if (!isReleaseTag(version)) return false;
  if (!String(notes?.body || '').trim()) return false;
  return version !== String(seenVersion || '').trim();
}

export function readSeenVersion(storage = globalThis.localStorage) {
  try {
    return String(storage?.getItem(RELEASE_NOTES_SEEN_KEY) || '').trim();
  } catch {
    return '';
  }
}

// 写失败（配额满、隐私模式）不抛错：最坏结果是下次启动再弹一次，不该因此打断启动。
export function markReleaseNotesSeen(version, storage = globalThis.localStorage) {
  const value = String(version || '').trim();
  if (!value) return false;
  try {
    storage?.setItem(RELEASE_NOTES_SEEN_KEY, value);
    return true;
  } catch {
    return false;
  }
}

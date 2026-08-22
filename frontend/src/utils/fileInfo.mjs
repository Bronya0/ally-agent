/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */

// 文件信息分区构建（唯一来源）：把后端 GetWorkspaceFileInfo 的结果整理成
// 「分区 → 行」结构。属性弹框（FileInfoModal）与内容区信息面板
// （WorkspaceExplorer 的 infoMode）共用，保证两处展示的信息完全一致。

import { t } from '../i18n.mjs';

export function formatTimeValue(value) {
  if (!value) return '';
  const date = new Date(value);
  // 过滤零值时间（如 Linux 无 birthtime 时后端返回 "0001-01-01T00:00:00Z"），
  // 避免显示成「1/1/1」这类无意义日期
  if (Number.isNaN(date.getTime()) || date.getFullYear() <= 1) return '';
  return date.toLocaleString();
}

export function formatSizeValue(bytes) {
  if (bytes == null || bytes === '') return '';
  const n = Number(bytes);
  if (!Number.isFinite(n) || n < 0) return '';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  return `${v.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

export function formatSizeWithBytes(bytes) {
  const human = formatSizeValue(bytes);
  if (!human) return '';
  return `${human} (${bytes} B)`;
}

// data: 后端 WorkspaceFileInfo JSON；返回 [{ title, rows: [{ label, value, copyable? }] }]
export function buildFileInfoSections(data) {
  if (!data) return [];
  const result = [];
  result.push({
    title: t('app.fileInfo.sectionBasic'),
    rows: [
      { label: t('app.fileInfo.name'), value: data.name },
      { label: t('app.fileInfo.path'), value: data.path, copyable: true },
      { label: t('app.fileInfo.absolutePath'), value: data.absolute, copyable: true },
      { label: t('app.fileInfo.extension'), value: data.extension || '-' },
      { label: t('app.fileInfo.type'), value: data.isDir ? t('app.filePreview.folder') : t('app.filePreview.file') },
      data.isDir
        ? { label: t('app.fileInfo.dirCount'), value: String(data.dirCount) }
        : { label: t('app.fileInfo.size'), value: formatSizeWithBytes(data.size), copyable: String(data.size) },
    ].filter((row) => row.value !== '' && row.value != null),
  });
  const timeRows = [
    { label: t('app.fileInfo.modTime'), value: formatTimeValue(data.modTime) },
    { label: t('app.fileInfo.accessTime'), value: formatTimeValue(data.accessTime) },
    { label: t('app.fileInfo.changeTime'), value: formatTimeValue(data.changeTime) },
    { label: t('app.fileInfo.birthTime'), value: formatTimeValue(data.birthTime) },
  ].filter((row) => row.value);
  if (timeRows.length) result.push({ title: t('app.fileInfo.sectionTimes'), rows: timeRows });
  if (data.isDir) {
    result.push({
      title: t('app.fileInfo.sectionDirSummary'),
      rows: [
        { label: t('app.fileInfo.dirCount'), value: String(data.dirCount) },
        { label: t('app.fileInfo.fileCount'), value: String(data.fileCount) },
        { label: t('app.fileInfo.totalSize'), value: formatSizeWithBytes(data.totalSize) },
      ],
    });
  }
  if (!data.isDir) {
    const hashRows = [
      { label: 'MD5', value: data.md5, copyable: true },
      { label: 'SHA-1', value: data.sha1, copyable: true },
      { label: 'SHA-256', value: data.sha256, copyable: true },
      { label: 'SHA-512', value: data.sha512, copyable: true },
      { label: 'CRC32', value: data.crc32, copyable: true },
    ].filter((row) => row.value);
    if (hashRows.length) result.push({ title: t('app.fileInfo.sectionHashes'), rows: hashRows });
  }
  const statRows = [
    { label: t('app.fileInfo.mode'), value: data.mode },
    { label: t('app.fileInfo.allocSize'), value: data.allocSize ? formatSizeWithBytes(data.allocSize) : '' },
    { label: t('app.fileInfo.blocks'), value: data.blocks ? String(data.blocks) : '' },
    { label: t('app.fileInfo.blockSize'), value: data.blockSize ? String(data.blockSize) : '' },
    { label: t('app.fileInfo.linkTarget'), value: data.linkTarget },
  ].filter((row) => row.value);
  if (statRows.length) result.push({ title: t('app.fileInfo.sectionStat'), rows: statRows });
  return result;
}

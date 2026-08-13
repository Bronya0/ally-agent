// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.

// Exports go through a native save dialog (Wails ExportTextFile binding):
// WKWebView on macOS ignores the HTML5 <a download> attribute, so the classic
// blob-anchor download silently does nothing there. When the binding is
// unavailable (e.g. a pure-web development preview), fall back to the blob
// download, which still works on Windows WebView2.
import { ExportTextFile } from '../../bindings/ally-dev/internal/app/app';

export async function saveTextFile({ filename, content, filterName = '', filterPattern = '' }) {
  try {
    const path = await ExportTextFile(filename, content, filterName, filterPattern);
    if (path === '' || path == null) return { saved: false, cancelled: true };
    return { saved: true, path };
  } catch (err) {
    const blob = new Blob([content], { type: 'text/plain;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = filename;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    window.setTimeout(() => URL.revokeObjectURL(url), 1000);
    return { saved: true, path: '' };
  }
}

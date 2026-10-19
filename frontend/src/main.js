/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
import '@wailsio/runtime';
import { createApp } from 'vue';
import App from './App.vue';
import { locale, t } from './i18n.mjs';
import { initTheme } from './utils/theme.mjs';
import { isolateImeKeydown } from './utils/imeKeyboard.mjs';
import './style.css';

// 必须先于组件挂载，确保比各组件和 Naive UI 的键盘监听器先执行。
window.addEventListener('keydown', isolateImeKeydown, true);

document.documentElement.lang = locale;
initTheme();

const app = createApp(App);
app.config.globalProperties.$t = t;
app.mount('#app');

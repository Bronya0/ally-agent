/*
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Copyright (C) 2026 tangssst <tangssst@qq.com>
 * GitHub: https://github.com/Bronya0/ally-agent
 *
 * This file is part of ally-agent, licensed under the GNU General
 * Public License v3. See the LICENSE file for details.
 */
// 插件宿主层：插件页的加载、卸载，以及交给插件的 host 对象。
//
// 三条边界（改这里之前先读 docs/plugin-system.md 第 4、5 节）：
//   1. 插件**不可能** import 生成的 bindings（生产包里没有那个文件），所以后端能力
//      只能由这里按权限现构造后注入；
//   2. host 是「按插件权限现构造」的对象：未声明的能力直接不存在（拿不到引用），
//      而不是调用时才报错；
//   3. 插件资源与页面同源（/plugins/<id>/<entry>，见 orch_plugin_assets.go），所以
//      入口用原生动态 import 加载，多文件 bundle、相对导入、CSS 都能用。
//
// 注意：本文件在 utils/ 下，按项目约定**不许** import i18n.mjs（顶层会拉 naive-ui，
// node 测试直接崩）——面向用户的文案一律由调用方（组件）传进来。

import {
  DeletePlugin,
  ExportPlugin,
  GetWorkspaceFileInfoAt,
  GetWorkspaceMediaURL,
  ImportPlugin,
  ImportPluginFromDir,
  ListFiles,
  ListPlugins,
  LogFrontendError,
  OpenPathInFileManager,
  PluginHTTPRequest,
  PluginStoreGet,
  PluginStoreSet,
  ReadWorkspaceFileAt,
  SelectPluginPackage,
  SetPluginEnabled,
} from '../../bindings/ally-dev/internal/app/app';
import { themeSnapshot, onThemeChange } from './pluginTheme.mjs';
import { pluginWorkspacePath } from './pluginPath.mjs';

// PLUGIN_MODE_PREFIX 与后端 internal/tools/plugin 的 ModePrefix 是同一个契约：
// 侧栏一级菜单 key = `plugin:<id>`。后端把 mode 算好放在 PluginInfo.mode 里，这里
// 只用于反解（App.vue 判定"当前是不是插件页"）。
export const PLUGIN_MODE_PREFIX = 'plugin:';

export function pluginIdFromMode(mode) {
  const value = String(mode || '');
  if (!value.startsWith(PLUGIN_MODE_PREFIX)) return '';
  return value.slice(PLUGIN_MODE_PREFIX.length);
}

// ── 插件列表（管理页与侧栏菜单共用一份） ──

export async function fetchPlugins() {
  const list = await ListPlugins();
  return Array.isArray(list) ? list : [];
}

export function enabledPlugins(list) {
  return (Array.isArray(list) ? list : []).filter((item) => item && item.enabled && item.valid);
}

// ── 管理动作：组件侧拿错误信息直接展示，这里不吞异常 ──

export const pluginAdmin = {
  selectPackage: () => SelectPluginPackage(),
  import: (path) => ImportPlugin(path),
  importFromDir: (dir) => ImportPluginFromDir(dir),
  export: (id, includeData) => ExportPlugin(id, includeData),
  remove: (id, purgeData) => DeletePlugin(id, purgeData),
  setEnabled: (id, enabled) => SetPluginEnabled(id, enabled),
};

// ── 入口加载 ──

// pluginCacheStamp 把插件目录的更新时间压成 URL 安全的一段。形状（`@` + 字母数字与 -_）
// 与后端 plugin.StripCacheStamp 认得的那一种是同一个契约，改一边要改另一边。
function pluginCacheStamp(plugin) {
  const raw = String(plugin?.updatedAt || '').replace(/[^A-Za-z0-9_-]/g, '');
  return raw ? `@${raw}` : '';
}

// pluginAssetUrl 拼插件资源 URL。缓存戳取插件目录的更新时间，而且住在**目录**位置：
// `/plugins/<id>/@<戳>/<entry>`。
//
// 为什么不用查询串：模块表（module map）按 URL 常驻整个文档，重新导入只会让入口换一个
// URL，而插件内部 `import './util.js'` 解析出来的是不带戳的绝对 URL——子模块照样命中上次
// 那份实例，于是「改完重装还是旧行为」。戳在目录段时相对解析会继承它（`/@<戳>/util.js`），
// 整棵树才真的换新。后端中间件先按原样找文件、找不到才把首段当戳剥掉，所以插件里真叫
// `@foo/` 的目录不会被误剥。
export function pluginAssetUrl(plugin, relPath) {
  const id = encodeURIComponent(String(plugin?.id || ''));
  const segments = String(relPath || '')
    .split('/')
    .filter((part) => part && part !== '.')
    .map((part) => encodeURIComponent(part));
  const stamp = pluginCacheStamp(plugin);
  const prefix = stamp ? `${stamp}/` : '';
  return `/plugins/${id}/${prefix}${segments.join('/')}`;
}

async function loadPluginEntry(plugin) {
  const url = pluginAssetUrl(plugin, plugin.entry);
  let module = null;
  try {
    module = await import(/* @vite-ignore */ url);
  } catch (err) {
    throw new Error(`加载入口失败（${url}）：${err && err.message ? err.message : err}`);
  }
  if (!module || typeof module.mount !== 'function') {
    throw new Error('入口必须导出 mount(element, host) 函数');
  }
  return module;
}

// ── host 事件总线 ──

// 目前只发布宿主自己产生的三类事件（theme / shown / hidden）。后端事件流暂不开放：
// 那些载荷里带着会话与运行状态，属于用户主流程的数据。
function createEventBus() {
  const listeners = new Map();
  const notify = (name) => {
    const subs = listeners.get(name);
    if (!subs || !subs.size) return;
    const payload = name === 'theme' ? themeSnapshot() : undefined;
    for (const handler of [...subs]) {
      try {
        handler(payload);
      } catch (err) {
        console.error('[plugin] 事件处理出错', name, err);
      }
    }
  };
  return {
    on(name, handler) {
      const key = String(name || '');
      if (!key || typeof handler !== 'function') return () => {};
      if (!listeners.has(key)) listeners.set(key, new Set());
      listeners.get(key).add(handler);
      return () => {
        const subs = listeners.get(key);
        if (subs) subs.delete(handler);
      };
    },
    emit: notify,
    clear() {
      listeners.clear();
    },
  };
}

// ── host 构造 ──

// createPluginHost 按插件权限现构造 host。env 来自宿主页面（版本 / 语言 / 工作区），
// ui 由调用方注入（消息提示、被拒回调）。
//
// 「未声明的能力直接不存在」是这份实现的硬要求：读工作区要到清单声明的 read 级别
// 才挂上（见末尾的 canReadWorkspace），否则管理页那句权限摘要就是在说假话。
export function createPluginHost(plugin, env = {}, ui = {}) {
  const id = String(plugin?.id || '');
  const workspace = String(env.workspace || '');
  const permissions = plugin?.permissions || {};
  const canReadWorkspace = permissions.workspace === 'read' || permissions.workspace === 'write';
  const bus = createEventBus();
  const stopThemeWatch = onThemeChange(() => bus.emit('theme'));

  const store = {
    async get() {
      const raw = await PluginStoreGet(id);
      if (!raw) return null;
      try {
        return JSON.parse(raw);
      } catch {
        return null;
      }
    },
    async set(value) {
      await PluginStoreSet(id, JSON.stringify(value === undefined ? null : value));
    },
  };

  // 白名单拒绝要留痕：插件自己 catch 掉也照样记一笔，否则“权限摘要 + 被拒记录”
  // 这一半感知手段就悬空了（默认拒绝下，用户只能靠它知道插件试过什么）。
  const note = (message) => {
    if (typeof ui.onDenied === 'function') ui.onDenied({ id, message });
  };

  // 路径守门：host.files 的契约是「工作区只读」，所以绝对路径与上级引用在宿主层就拒掉
  // （后端那几个绑定对绝对路径是放行的——它们服务的是编辑器那套语义）。这不是安全边界
  // （插件能绕开 host 直接调绑定，见 docs/plugin-system.md 第 8 节），但 host 给出的能力
  // 必须与「工作区: 只读」这句摘要一致。
  const guardPath = (raw) => {
    try {
      return pluginWorkspacePath(raw);
    } catch (err) {
      note(String((err && err.message) || err));
      throw err;
    }
  };

  const files = {
    async read(path) {
      const result = await ReadWorkspaceFileAt({ workspace, path: guardPath(path) });
      return result && typeof result.content === 'string' ? result.content : '';
    },
    async list(path = '.', options = {}) {
      const result = await ListFiles({
        workspace,
        path: guardPath(path || '.'),
        maxDepth: Number(options.maxDepth) > 0 ? Math.floor(Number(options.maxDepth)) : 1,
        limit: Number(options.limit) > 0 ? Math.floor(Number(options.limit)) : 200,
        includeHidden: options.includeHidden === true,
        includeIgnored: options.includeIgnored === true,
      });
      return (result && result.entries) || [];
    },
    async stat(path) {
      return GetWorkspaceFileInfoAt({ workspace, path: guardPath(path) });
    },
    async mediaUrl(path) {
      return GetWorkspaceMediaURL({ workspace, path: guardPath(path) });
    },
  };

  const http = {
    async request(req) {
      const request = req && typeof req === 'object' ? req : {};
      if (!request.url) throw new Error('http.request 需要 url');
      // 白名单判定在后端：目标主机必须命中插件清单里的 permissions.http，
      // 未命中会以 [E_PLUGIN_HOST_DENIED] 拒绝（含重定向后的目标）。
      try {
        return await PluginHTTPRequest({
          pluginId: id,
          method: request.method || 'GET',
          url: String(request.url),
          headers: request.headers || {},
          query: request.query || {},
          body: request.body || '',
          json: request.json,
          timeout: Number(request.timeout) || 0,
          maxBytes: Number(request.maxBytes) || 0,
        });
      } catch (err) {
        const message = String((err && err.message) || err);
        if (message.includes('E_PLUGIN_HOST_DENIED')) note(message);
        throw err;
      }
    },
  };

  const host = {
    app: {
      version: String(env.version || ''),
      locale: String(env.locale || ''),
      workspace,
    },
    http,
    store,
    events: {
      on: (name, handler) => bus.on(name, handler),
    },
    log: {
      info: (message) => LogFrontendError(`[plugin:${id}] ${String(message || '')}`, ''),
      error: (message) => LogFrontendError(`[plugin:${id}] ${String(message || '')}`, ''),
    },
    ui: {
      notify: (message, type) => {
        if (typeof ui.notify === 'function') ui.notify(String(message || ''), String(type || 'info'));
      },
      theme: {
        snapshot: () => themeSnapshot(),
        on: (handler) =>
          bus.on('theme', (payload) => handler(payload || themeSnapshot())),
        tokens: () => Object.keys(themeSnapshot().tokens || {}),
      },
    },
    // 宿主内部用（插件也可以读，但别依赖）：页面显隐通知与清理。
    __host: {
      emit: bus.emit,
      dispose: () => {
        stopThemeWatch();
        bus.clear();
      },
    },
  };
  // 未声明的能力直接不存在：只有声明了 read（v1 只支持 read）才挂上工作区读写入口。
  // host.open 与 host.files 同一套守门：inFileManager 只接受工作区内的相对路径
  // （后端 OpenPathInFileManager 对绝对路径放行，服务的是编辑器语义——摘要那句
  // 「工作区: 只读」必须连这里一起成立，不能只管 files 不管 open）。
  if (canReadWorkspace) {
    host.files = files;
    host.open = {
      inFileManager: (path) => {
        const rel = guardPath(path);
        if (!workspace) {
          const err = new Error('host.open 需要工作区上下文');
          note(err.message);
          throw err;
        }
        return OpenPathInFileManager(`${workspace}/${rel}`);
      },
    };
  }
  return host;
}

// ── 挂载 / 卸载 ──

// mountPlugin 把插件挂到容器里：加载入口 → 构造 host → 调 mount。返回卸载句柄。
// 失败时把原始错误抛给调用方（组件负责显示，不能只躺在控制台）。句柄上的 setActive
// 用于把“页面当前是否可见”转成 shown/hidden 事件：插件页用 v-show 常驻，所以只靠
// 挂载/卸载是发不出这两个事件的。
export async function mountPlugin(plugin, element, env, ui) {
  if (!plugin || !element) throw new Error('插件或容器缺失');
  const module = await loadPluginEntry(plugin);
  const host = createPluginHost(plugin, env, ui);
  let unmount = null;
  try {
    unmount = await module.mount(element, host);
  } catch (err) {
    host.__host.dispose();
    throw err;
  }
  return {
    host,
    setActive(active) {
      host.__host.emit(active ? 'shown' : 'hidden');
    },
    dispose: () => {
      try {
        host.__host.emit('hidden');
      } catch {
        /* 卸载阶段的报错不该影响宿主 */
      }
      try {
        if (typeof unmount === 'function') unmount();
      } finally {
        host.__host.dispose();
        if (element) element.innerHTML = '';
      }
    },
  };
}

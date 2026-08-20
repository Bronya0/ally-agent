<!--
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Workspace file browser/editor. The component is intentionally mounted only
 * while visible so large trees, editor text and highlight caches are released
 * when the user closes the explorer.
 -->
<template>
  <section :class="['workspace-explorer', { 'has-file': showEditor, 'tree-only': !showEditor }]" :style="{ width: showEditor ? null : (treeWidth + 1) + 'px' }" aria-label="Workspace explorer">
    <main v-show="showEditor" class="workspace-explorer-editor">
      <header class="workspace-explorer-editor-header">
        <button type="button" class="workspace-explorer-close-content" :title="$t('common.close')" :aria-label="$t('common.close')" @click="closeContent"><CloseOutlined /></button>
        <div class="workspace-explorer-file-title" :title="activeFile?.path || ''">
          <span>{{ activeFile?.path || $t('app.filePreview.empty') }}</span>
          <span v-if="dirty" class="workspace-explorer-dirty" :title="$t('app.workspaceExplorer.unsaved')"></span>
        </div>
        <div class="workspace-explorer-actions">
          <n-button
            v-if="isMarkdown"
            size="tiny"
            :type="mdPreviewMode ? 'primary' : 'default'"
            ghost
            @click="toggleMdPreview"
          >{{ mdPreviewMode ? $t('app.workspaceExplorer.editSource') : $t('app.workspaceExplorer.preview') }}</n-button>
          <n-button
            v-if="activeFile && isEditable && !mdPreviewMode"
            size="tiny"
            type="primary"
            ghost
            :loading="saving"
            :disabled="!dirty || saving"
            @click="saveFile"
          >{{ $t('common.save') }}</n-button>
        </div>
      </header>

      <!-- Image preview -->
      <div v-show="isImageMode" class="workspace-explorer-image-body">
        <img :src="imageDataUrl" :alt="activeFile?.path" class="workspace-explorer-image" />
      </div>

      <!-- HTML preview (read-only, origin-isolated sandboxed iframe) -->
      <div v-show="isHtmlMode" class="workspace-explorer-html-body">
        <iframe
          class="workspace-explorer-html-frame"
          :srcdoc="htmlContent"
          sandbox="allow-scripts allow-forms allow-popups allow-modals"
          :title="activeFile?.path || 'HTML preview'"
        ></iframe>
      </div>

      <!-- Markdown rendered preview -->
      <div v-show="isMarkdown && mdPreviewMode" ref="mdPreviewRef" class="workspace-explorer-md-preview"></div>

      <!-- Ace editor (always in DOM, shown for text files when not in md preview) -->
      <div v-show="showEditor && isEditable && !mdPreviewMode" ref="aceContainerRef" class="workspace-explorer-ace"></div>

      <!-- 非预览型文件（如 exe 等二进制）：用树节点已有数据展示基本信息 -->
      <div v-if="infoMode" class="workspace-explorer-file-info">
        <div class="file-info-row"><span class="file-info-label">{{ $t('app.filePreview.name') }}</span><span class="file-info-value">{{ activeFile.info.label }}</span></div>
        <div class="file-info-row"><span class="file-info-label">{{ $t('app.filePreview.type') }}</span><span class="file-info-value">{{ fileTypeLabel }}</span></div>
        <div class="file-info-row" v-if="activeFile.info.size != null"><span class="file-info-label">{{ $t('app.filePreview.size') }}</span><span class="file-info-value">{{ formatSize(activeFile.info.size) }}</span></div>
        <div class="file-info-row" v-if="activeFile.info.modTime"><span class="file-info-label">{{ $t('app.filePreview.modified') }}</span><span class="file-info-value">{{ activeFile.info.modTime }}</span></div>
        <div class="file-info-row file-info-path"><span class="file-info-label">{{ $t('app.filePreview.path') }}</span><span class="file-info-value">{{ activeFile.info.path }}</span></div>
        <div class="file-info-hint">{{ $t('app.filePreview.binaryHint') }}</div>
      </div>

      <!-- Loading / error / empty states (only shown when no activeFile) -->
      <div v-show="loadingFile && !activeFile" class="workspace-explorer-editor-state"><n-spin size="small" /> {{ $t('app.filePreview.loading') }}</div>
      <div v-show="fileError && !activeFile" class="workspace-explorer-editor-state workspace-explorer-error">{{ fileError }}</div>
      <div v-show="!activeFile && !loadingFile && !fileError" class="workspace-explorer-editor-state">{{ $t('app.filePreview.empty') }}</div>
    </main>

    <div class="workspace-explorer-splitter" @mousedown.prevent="startDrag"></div>

    <aside class="workspace-explorer-tree" :style="{ flexBasis: treeWidth + 'px' }" @contextmenu.prevent="onTreeAreaContextmenu">
      <div class="workspace-explorer-tree-header">
        <span class="workspace-explorer-title">
          <span class="workspace-explorer-title-text" :title="workspace">{{ workspaceLabel }}</span>
        </span>
        <div class="workspace-explorer-header-actions">
          <button
            type="button"
            class="workspace-explorer-icon-btn"
            :title="$t('common.refresh')"
            :aria-label="$t('common.refresh')"
            :disabled="loadingTree || !workspace"
            @click="refreshTree"
          ><ReloadOutlined /></button>
          <button
            type="button"
            class="workspace-explorer-icon-btn"
            :title="$t('common.close')"
            :aria-label="$t('common.close')"
            @click="requestClose"
          ><CloseOutlined /></button>
        </div>
      </div>
      <n-spin ref="treeContainerRef" :show="loadingTree" size="small" class="workspace-explorer-spin">
        <n-tree
          v-if="workspace"
          class="workspace-explorer-tree-view"
          block-line
          selectable
          virtual-scroll
          :height="treeHeight"
          :indent="16"
          :theme-overrides="treeThemeOverrides"
          :data="treeData"
          :expanded-keys="expandedKeys"
          :selected-keys="selectedKeys"
          :node-props="nodeProps"
          expand-on-click
          :render-switcher-icon="renderSwitcherIcon"
          :render-label="renderLabel"
          @update:expanded-keys="onExpandedKeysChange"
          @update:selected-keys="onSelect"
        />
        <div v-else class="workspace-explorer-empty">{{ $t('app.workspace.none') }}</div>
      </n-spin>
    </aside>
  </section>

  <n-dropdown
    placement="bottom-start"
    trigger="manual"
    :show="contextMenuShow"
    :x="contextMenuX"
    :y="contextMenuY"
    :options="contextMenuOptions"
    @select="onContextMenuSelect"
    @clickoutside="contextMenuShow = false"
  />
</template>

<script setup>
import { computed, h, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { NIcon, NInput, useDialog, useMessage } from 'naive-ui';
import ace from 'ace-builds';
import 'ace-builds/src-noconflict/ext-language_tools';
import 'ace-builds/src-noconflict/ext-searchbox';
import 'ace-builds/src-noconflict/theme-tomorrow_night';
import 'ace-builds/src-noconflict/mode-text';
import 'ace-builds/src-noconflict/mode-javascript';
import 'ace-builds/src-noconflict/mode-typescript';
import 'ace-builds/src-noconflict/mode-json';
import 'ace-builds/src-noconflict/mode-python';
import 'ace-builds/src-noconflict/mode-golang';
import 'ace-builds/src-noconflict/mode-java';
import 'ace-builds/src-noconflict/mode-sh';
import 'ace-builds/src-noconflict/mode-yaml';
import 'ace-builds/src-noconflict/mode-css';
import 'ace-builds/src-noconflict/mode-html';
import 'ace-builds/src-noconflict/mode-xml';
import 'ace-builds/src-noconflict/mode-sql';
import 'ace-builds/src-noconflict/mode-c_cpp';
import 'ace-builds/src-noconflict/mode-markdown';
import 'ace-builds/src-noconflict/mode-rust';
import 'ace-builds/src-noconflict/mode-ruby';
import MarkdownIt from 'markdown-it';
import { ListFiles, ReadWorkspaceFile, ReadWorkspaceImage, SaveWorkspaceFile, DeletePath, OpenWorkspacePathInFileManager, CreateFile } from '../../bindings/ally-dev/internal/app/app';
import CloseOutlined from '@vicons/antd/CloseOutlined';
import ReloadOutlined from '@vicons/antd/ReloadOutlined';
import { RightOutlined } from '@vicons/antd';
import { t } from '../i18n.mjs';

const props = defineProps({
  workspace: { type: String, default: '' },
  active: { type: Boolean, default: false },
  initialWidth: { type: Number, default: 270 },
});
const emit = defineEmits(['close', 'treeWidthChange']);
const dialog = useDialog();
const message = useMessage();

const treeData = ref([]);
const expandedKeys = ref([]);
const selectedKeys = ref([]);
const loadingTree = ref(false);
const loadingFile = ref(false);
const saving = ref(false);
const fileError = ref('');
const activeFile = ref(null);
const draftContent = ref('');
const originalContent = ref('');
const originalHash = ref('');
const version = ref('');
const treeContainerRef = ref(null);
const treeHeight = ref(400);
const aceContainerRef = ref(null);
const mdPreviewRef = ref(null);
const treeWidth = ref(Math.max(150, Math.min(600, Number(props.initialWidth) || 270)));
const imageDataUrl = ref('');
const htmlContent = ref('');
const mdPreviewMode = ref(false);
const contextMenuShow = ref(false);
const contextMenuX = ref(0);
const contextMenuY = ref(0);
const contextMenuNode = ref(null);
let aceEditor = null;
let resizeObserver = null;
let aceResizeObserver = null;
// splitter 拖拽的 document 监听解绑函数；拖拽结束或组件卸载时执行
let dragTeardown = null;
let requestSequence = 0;
let disposed = false;
let navigationBusy = false;
let confirmPromise = null;
let mdRenderer = null;
// nodeWrapperPadding 默认 '3px 0'：每行 hover/聚焦高亮上下各缩进 3px，
// 相邻行之间出现 6px 视觉缝隙（表现为"聚焦区域之间存在间距"）。
// 这里归零，让 20px 行高的高亮区域完全相邻。
const treeThemeOverrides = {
  nodeHeight: '20px',
  fontSize: '12px',
  nodeWrapperPadding: '0',
  nodeColorHover: 'rgba(78, 161, 255, 0.08)',
  nodeColorPressed: 'rgba(78, 161, 255, 0.14)',
  nodeColorActive: 'rgba(78, 161, 255, 0.18)',
  nodeTextColor: '#cbd3df',
  nodeTextColorDisabled: '#697384',
};

const workspaceLabel = computed(() => {
  const value = String(props.workspace || '').replace(/[\\/]+$/, '');
  return value.split(/[\\/]/).filter(Boolean).pop() || value || t('app.workspace.none');
});
const dirty = computed(() => draftContent.value !== originalContent.value);
const showEditor = computed(() => Boolean(activeFile.value || loadingFile.value || fileError.value));
const isImageMode = computed(() => activeFile.value?.kind === 'image');
const isHtmlMode = computed(() => activeFile.value?.kind === 'html');
const isMarkdown = computed(() => activeFile.value?.kind === 'markdown');
// 可编辑（text / markdown）；image、html 为只读预览，info 为信息面板
const isEditable = computed(() => {
  const kind = activeFile.value?.kind;
  return kind === 'text' || kind === 'markdown';
});

const ACE_MODE_MAP = {
  ts: 'typescript', tsx: 'typescript', js: 'javascript', jsx: 'javascript',
  py: 'python', go: 'golang', java: 'java', sh: 'sh', bash: 'sh',
  json: 'json', yaml: 'yaml', yml: 'yaml', css: 'css', html: 'html',
  xml: 'xml', sql: 'sql', c: 'c_cpp', cpp: 'c_cpp', h: 'c_cpp',
  md: 'markdown', markdown: 'markdown', rs: 'rust', rb: 'ruby',
};
// 文件打开方式分类（唯一判断来源）。白名单只声明有专用渲染器的格式，
// 其余全部走默认 text 路径：能否编辑由后端按内容统一判定，不在前端枚举二进制格式。
//   image    → 只读图片预览
//   html     → 只读 HTML 预览（沙箱 iframe）
//   markdown → 文本编辑 + 渲染预览切换
//   text     → 默认文本编辑；不可编辑时由后端错误码触发回退基本信息面板
const IMAGE_EXTS = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'bmp', 'svg', 'ico']);
const HTML_EXTS = new Set(['html', 'htm']);
const MARKDOWN_EXTS = new Set(['md', 'markdown']);

function classifyFile(path) {
  const ext = extension(path);
  if (IMAGE_EXTS.has(ext)) return 'image';
  if (HTML_EXTS.has(ext)) return 'html';
  if (MARKDOWN_EXTS.has(ext)) return 'markdown';
  return 'text';
}

// 信息面板模式：当前打开的是不可编辑/预览的文件，用树节点已有数据展示基本信息
const infoMode = computed(() => Boolean(activeFile.value && activeFile.value.info));
const fileTypeLabel = computed(() => {
  const info = activeFile.value?.info;
  if (!info) return '';
  if (info.dir) return t('app.filePreview.folder');
  const ext = primaryExtension(info.path);
  return ext ? `${ext.toUpperCase()} ${t('app.filePreview.file')}` : t('app.filePreview.file');
});
function formatSize(bytes) {
  if (bytes == null) return '';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  let n = Number(bytes);
  if (!Number.isFinite(n) || n < 0) return '';
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  return `${n.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}
const extension = (path) => {
  const name = String(path || '').split(/[\\/]/).pop() || '';
  const dot = name.lastIndexOf('.');
  return dot > 0 ? name.slice(dot + 1).toLowerCase() : '';
};
// 主扩展名：文件名第一个点后的第一段（libcrypto.so.1.0.2 → so，notes.txt → txt），
// 供信息面板展示文件类型，天然适配版本化文件名
function primaryExtension(path) {
  const name = String(path || '').split(/[\\/]/).pop() || '';
  const dot = name.indexOf('.');
  if (dot <= 0) return '';
  return name.slice(dot + 1).split('.')[0].toLowerCase();
}

function aceModeForPath(path) {
  return ACE_MODE_MAP[extension(path)] || 'text';
}

function initAceEditor() {
  if (aceEditor || !aceContainerRef.value) return;
  aceEditor = ace.edit(aceContainerRef.value, {
    mode: 'ace/mode/text',
    theme: 'ace/theme/tomorrow_night',
    fontSize: '15px',
    fontFamily: "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New', monospace",
    showPrintMargin: false,
    tabSize: 2,
    useSoftTabs: true,
    wrap: false,
  });
  aceEditor.setOptions({
    enableBasicAutocompletion: true,
    enableLiveAutocompletion: false,
    showFoldWidgets: false,
    // 允许滚动到文件末尾之后再多滚半屏，光标可落在最后一行下方
    scrollPastEnd: 0.5,
    // 光标加粗，定位更显眼（之前你对光标定位很敏感）
    cursorStyle: 'wide',
  });
  aceEditor.on('input', onAceInput);
  aceEditor.container.addEventListener('keydown', onEditorKeydown);
  aceResizeObserver = new ResizeObserver(() => { aceEditor?.resize(); });
  aceResizeObserver.observe(aceContainerRef.value);
}

function destroyAceEditor() {
  if (aceResizeObserver) { aceResizeObserver.disconnect(); aceResizeObserver = null; }
  if (aceEditor) {
    aceEditor.off('input', onAceInput);
    aceEditor.container.removeEventListener('keydown', onEditorKeydown);
    aceEditor.destroy();
    aceEditor = null;
  }
}

function updateAceContent() {
  if (!aceEditor) return;
  const path = activeFile.value?.path || '';
  const mode = aceModeForPath(path);
  aceEditor.session.setMode(`ace/mode/${mode}`);
  aceEditor.setValue(draftContent.value || '', -1);
  // 重置撤销栈：setValue 不清空历史，否则 Ctrl+Z 可能撤回到上一个文件的内容
  aceEditor.session.getUndoManager().reset();
  aceEditor.clearSelection();
  aceEditor.session.setScrollTop(0);
  aceEditor.resize();
}

function renderMarkdownPreview() {
  if (!mdPreviewRef.value || !mdRenderer) return;
  const content = draftContent.value || '';
  mdPreviewRef.value.innerHTML = mdRenderer.render(content);
}

function toggleMdPreview() {
  mdPreviewMode.value = !mdPreviewMode.value;
  if (mdPreviewMode.value) {
    renderMarkdownPreview();
  } else {
    nextTick(() => { aceEditor?.resize(); });
  }
}

function makeNode(entry) {
  const dir = Boolean(entry.Dir ?? entry.dir);
  return {
    key: entry.Path || entry.path || entry.Name || entry.name,
    label: entry.Name || entry.name,
    path: entry.Path || entry.path,
    // 透传后端 FileEntry 的 size / modTime，供二进制文件的信息面板直接使用（无需二次请求）
    size: entry.Size ?? entry.size,
    modTime: entry.ModTime ?? entry.modTime,
    // Keep a stable, already-loaded children container. The actual entries
    // are filled after the tree has performed its normal expand rotation.
    children: dir ? [] : undefined,
    childrenLoaded: !dir,
    isLeaf: !dir,
    dir,
  };
}

async function listDirectory(path = '') {
  const entries = await ListFiles({ path, maxDepth: 1, limit: 1000, includeHidden: true, includeIgnored: false });
  return (Array.isArray(entries) ? entries : []).map(makeNode);
}

async function loadRoot() {
  const requestID = ++requestSequence;
  loadingTreeNodes.clear();
  selectedKeys.value = [];
  expandedKeys.value = [];
  activeFile.value = null;
  clearEditor();
  if (!props.workspace) return;
  loadingTree.value = true;
  try {
    const nextTree = await listDirectory('');
    if (disposed || requestID !== requestSequence) return;
    treeData.value = nextTree;
  } catch (err) {
    if (!disposed && requestID === requestSequence) message.error(t('app.workspaceExplorer.treeFailed', { error: errorText(err) }));
  } finally {
    if (!disposed && requestID === requestSequence) loadingTree.value = false;
  }
}

const loadingTreeNodes = new Set();
async function onExpandedKeysChange(keys, _options, meta) {
  const node = meta?.node;
  const shouldLoad = meta?.action === 'expand'
    && node?.dir
    && !node.childrenLoaded
    && !loadingTreeNodes.has(node.key);
  expandedKeys.value = Array.isArray(keys) ? keys : [];
  if (!shouldLoad) return;
  const requestID = requestSequence;
  loadingTreeNodes.add(node.key);
  try {
    const children = await listDirectory(node.path);
    if (disposed || requestID !== requestSequence) return;
    node.children = children;
    node.childrenLoaded = true;
  } catch (err) {
    if (disposed || requestID !== requestSequence) return;
    node.children = [];
    node.childrenLoaded = true;
    message.error(t('app.workspaceExplorer.treeFailed', { error: errorText(err) }));
  } finally {
    loadingTreeNodes.delete(node.key);
  }
}

async function refreshTree() {
  if (navigationBusy) return;
  navigationBusy = true;
  try {
    if (await confirmPendingChange()) await loadRoot();
  } finally {
    navigationBusy = false;
  }
}

// 目录（非叶子节点）的展开/折叠箭头：RightOutlined 来自已依赖的 @vicons/antd，
// Naive UI 会在展开时自动将其旋转 90°
function renderSwitcherIcon() {
  return h(NIcon, null, { default: () => h(RightOutlined) });
}

function renderLabel({ option }) {
  // 「.」开头的隐藏条目（.git、.idea 等）加 is-hidden 类，样式层统一压暗
  const name = String(option?.label || '');
  return h('span', {
    class: [
      'workspace-explorer-node-label',
      option?.dir ? 'is-directory' : 'is-file',
      name.startsWith('.') ? 'is-hidden' : '',
    ],
    title: option.path,
  }, option.label);
}

function nodeProps({ option }) {
  return {
    onContextmenu(e) {
      e.preventDefault();
      // 阻止冒泡到树容器的空白区域菜单
      e.stopPropagation();
      contextMenuNode.value = option;
      contextMenuX.value = e.clientX;
      contextMenuY.value = e.clientY;
      contextMenuShow.value = true;
    },
  };
}

// 树空白区域右键：节点级处理器已 stopPropagation，走到这里的必然是空白区域
function onTreeAreaContextmenu(e) {
  contextMenuNode.value = null;
  contextMenuX.value = e.clientX;
  contextMenuY.value = e.clientY;
  contextMenuShow.value = true;
}

const contextMenuOptions = computed(() => {
  if (!contextMenuNode.value) {
    return [
      { label: t('app.workspaceExplorer.newFile'), key: 'newFile' },
      { label: t('app.workspaceExplorer.openFolder'), key: 'openFolder' },
    ];
  }
  if (contextMenuNode.value.dir) {
    return [
      { label: t('app.workspaceExplorer.openFolder'), key: 'openFolder' },
      { label: t('app.workspaceExplorer.copyRelativePath'), key: 'copyRelativePath' },
      { label: t('app.workspaceExplorer.copyFullPath'), key: 'copyFullPath' },
      { label: t('common.delete'), key: 'delete' },
    ];
  }
  return [
    { label: t('app.workspaceExplorer.copyRelativePath'), key: 'copyRelativePath' },
    { label: t('app.workspaceExplorer.copyFullPath'), key: 'copyFullPath' },
    { label: t('common.delete'), key: 'delete' },
  ];
});

function onContextMenuSelect(key) {
  contextMenuShow.value = false;
  if (key === 'delete') void confirmDeleteNode(contextMenuNode.value);
  if (key === 'openFolder') void openWorkspaceFolder(contextMenuNode.value);
  if (key === 'newFile') void openNewFileDialog();
  if (key === 'copyRelativePath') copyNodePath(contextMenuNode.value, false);
  if (key === 'copyFullPath') copyNodePath(contextMenuNode.value, true);
}

// 复制节点路径：树节点 path 即工作区内相对路径（后端统一 / 分隔）。
// 完整路径按工作区根所属平台整体归一化分隔符：
// Windows 盘符/UNC 根 → 全部 \；POSIX 根（macOS/Linux）→ 全部 /，
// 避免「D:\...\wiki/文件.md」这类混合分隔符
function copyNodePath(node, full) {
  const relative = String(node?.path || '');
  if (!relative) return;
  const root = String(props.workspace || '').replace(/[\\/]+$/, '');
  const text = full && root ? joinFullPath(root, relative) : relative;
  copyTextToClipboard(text, () => message.success(t('app.copy.done')));
}

// 工作区根为 Windows 盘符（D:\ 或 D:/）或 UNC（\\server\share）时视为 Windows 风格
function isWindowsStyleRoot(root) {
  return /^[A-Za-z]:[\\/]/.test(root) || root.startsWith('\\\\');
}

function joinFullPath(root, relative) {
  if (isWindowsStyleRoot(root)) {
    // Windows：根与相对路径统一转为原生 \（后端相对路径固定 / 分隔，转换安全）
    return root.replace(/\//g, '\\') + '\\' + relative.replace(/\//g, '\\');
  }
  // macOS/Linux：仅用 / 拼接；根中的字面 \（Linux 合法文件名字符）不被动
  return root + '/' + relative;
}

// 优先 navigator.clipboard，失败时回退 execCommand（与 App.vue 的复制逻辑一致）
function copyTextToClipboard(text, onDone) {
  if (navigator.clipboard?.writeText) {
    navigator.clipboard.writeText(text).then(onDone).catch(() => {
      fallbackCopyText(text, onDone);
    });
  } else {
    fallbackCopyText(text, onDone);
  }
}

function fallbackCopyText(text, onDone) {
  try {
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.style.position = 'fixed';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.select();
    document.execCommand('copy');
    document.body.removeChild(ta);
    onDone();
  } catch {
    message.error(t('app.copy.failed'));
  }
}

// 在树空白区域右键「新建文本文件」：弹框输入带扩展名的完整文件名，
// 在工作区根目录创建空文件，并直接插入顶层树节点（不刷新、不丢失展开状态）。
function openNewFileDialog() {
  const name = ref('');
  dialog.create({
    title: t('app.workspaceExplorer.newFile'),
    content: () => h(NInput, {
      value: name.value,
      'onUpdate:value': (v) => { name.value = v; },
      placeholder: t('app.workspaceExplorer.newFileNamePlaceholder'),
      clearable: true,
      autofocus: true,
    }),
    positiveText: t('common.create'),
    negativeText: t('common.cancel'),
    onPositiveClick: async () => {
      const ok = await createFile(name.value);
      return ok; // 返回 false 保持弹窗打开，方便改名重试
    },
  });
}

async function createFile(rawName) {
  const name = String(rawName || '').trim();
  if (!name) {
    message.error(t('app.workspaceExplorer.newFileInvalid'));
    return false;
  }
  // 仅允许单层文件名（含扩展名）；剥离任何路径分隔与相对段，防止越界写入
  const base = name.split(/[\\/]/).pop();
  if (!base || base === '.' || base === '..' || base.includes('..')) {
    message.error(t('app.workspaceExplorer.newFileInvalid'));
    return false;
  }
  try {
    await CreateFile({ path: base, content: '', overwrite: false });
    if (!treeData.value.some((n) => n.label === base)) {
      treeData.value = [...treeData.value, makeNode({ Path: base, Name: base, Dir: false })];
    }
    message.success(t('app.workspaceExplorer.fileCreated', { name: base }));
    return true;
  } catch (err) {
    message.error(t('app.workspaceExplorer.createFailed', { error: errorText(err) }));
    return false;
  }
}

async function openWorkspaceFolder(node = null) {
  if (!props.workspace) return;
  try {
    await OpenWorkspacePathInFileManager(String(node?.path || ''));
  } catch (err) {
    message.error(t('app.workspaceExplorer.openFolderFailed', { error: errorText(err) }));
  }
}

async function confirmDeleteNode(node) {
  if (!node?.path) return;
  const filePath = String(node.path);
  // 树节点路径由后端 ListFiles 生成（工作区内相对路径）；
  // 前端只拦截明显的穿越/绝对路径异常，工作区边界由后端 DeletePath 严格校验
  const normalized = filePath.replace(/\\/g, '/');
  if (normalized === '..' || normalized.includes('../') || normalized.startsWith('/') || /^[a-zA-Z]:/.test(normalized)) {
    message.error(t('app.workspaceExplorer.deleteOutsideWorkspace'));
    return;
  }
  dialog.warning({
    title: t('app.workspaceExplorer.deleteConfirmTitle'),
    content: node.dir
      ? t('app.workspaceExplorer.deleteDirConfirm', { name: node.label })
      : t('app.workspaceExplorer.deleteFileConfirm', { name: node.label }),
    positiveText: t('common.delete'),
    negativeText: t('common.cancel'),
    onPositiveClick: async () => {
      try {
        await DeletePath({ path: filePath, recursive: node.dir });
        message.success(t('app.workspaceExplorer.deleted'));
        if (activeFile.value?.path === filePath) clearEditor();
        await refreshTree();
      } catch (err) {
        message.error(t('app.workspaceExplorer.deleteFailed', { error: errorText(err) }));
      }
    },
  });
}

async function onSelect(keys, options, meta) {
  if (navigationBusy) return;
  // n-tree 单选默认 cancelable：再点一次已选中文件会以 action='unselect' 发出
  // 空 keys/options，被点节点在 meta.node 上。若正是当前打开的文件 → 切换关闭编辑器
  // （复用 closeContent：脏内容仍走未保存确认，取消则保持打开）
  if (meta?.action === 'unselect') {
    const clicked = meta.node;
    if (clicked && !clicked.dir && clicked.path && clicked.path === activeFile.value?.path) {
      navigationBusy = true;
      try {
        await closeContent();
      } finally {
        navigationBusy = false;
      }
    }
    return;
  }
  const node = options?.[0];
  if (!node) return;
  if (node.dir) {
    selectedKeys.value = keys;
    return;
  }
  navigationBusy = true;
  try {
    const opened = await openFile(node);
    selectedKeys.value = opened ? keys : (activeFile.value?.path ? [activeFile.value.path] : []);
  } finally {
    navigationBusy = false;
  }
}

// 统一打开流程：classifyFile 决定打开方式，后端按内容判定可编辑性。
// 任何 kind 读取时命中不可编辑错误码（二进制/非 UTF-8/超限）都回退基本信息面板，
// 保证任何文件点击都有响应，前端无需感知具体文件格式
async function openFile(node) {
  if (!node?.path || node.path === activeFile.value?.path) return true;
  const proceed = await confirmPendingChange();
  if (!proceed) return false;
  const requestID = ++requestSequence;
  const kind = classifyFile(node.path);
  fileError.value = '';
  imageDataUrl.value = '';
  htmlContent.value = '';
  mdPreviewMode.value = false;
  // 清掉上一个文件的 Markdown 渲染残留，避免旧预览 DOM 驻留到下次预览
  if (mdPreviewRef.value) mdPreviewRef.value.innerHTML = '';
  loadingFile.value = true;
  try {
    if (kind === 'image') {
      const result = await ReadWorkspaceImage(node.path);
      if (disposed || requestID !== requestSequence) return false;
      imageDataUrl.value = String(result?.data || '');
      activeFile.value = { path: node.path, kind };
      draftContent.value = '';
      originalContent.value = '';
      loadingFile.value = false;
      return true;
    }
    // html / markdown / text 均以文本读取，仅展示方式不同
    const result = await ReadWorkspaceFile(node.path);
    if (disposed || requestID !== requestSequence) return false;
    if (kind === 'html') {
      // HTML：内容送入沙箱 iframe 只读渲染，不进编辑器
      htmlContent.value = String(result?.content || '');
      activeFile.value = { path: node.path, kind };
      draftContent.value = '';
      originalContent.value = '';
      loadingFile.value = false;
      return true;
    }
    const content = String(result?.content || '');
    activeFile.value = { path: node.path, kind };
    draftContent.value = content;
    originalContent.value = content;
    originalHash.value = result.sha256 || '';
    version.value = result.version || '';
    loadingFile.value = false;
    await nextTick();
    if (!aceEditor) initAceEditor();
    updateAceContent();
    return true;
  } catch (err) {
    if (!disposed && requestID === requestSequence) {
      if (isUnviewableFileError(err)) {
        activeFile.value = { path: node.path, info: node };
        draftContent.value = '';
        originalContent.value = '';
        loadingFile.value = false;
        return true;
      }
      fileError.value = t('app.filePreview.failed', { error: errorText(err) });
      loadingFile.value = false;
    }
    return false;
  }
}

// 后端判定无法作为文本编辑/预览的错误码（message 形如「[E_BINARY_FILE] ...」）
const UNVIEWABLE_FILE_CODES = ['E_BINARY_FILE', 'E_NOT_UTF8', 'E_FILE_TOO_LARGE'];
function isUnviewableFileError(err) {
  const text = String(err?.message || err || '');
  return UNVIEWABLE_FILE_CODES.some((code) => text.includes(code));
}

function onAceInput() {
  if (!aceEditor) return;
  draftContent.value = aceEditor.getValue();
  fileError.value = '';
  if (mdPreviewMode.value) renderMarkdownPreview();
}

function onEditorKeydown(event) {
  const key = String(event.key || '').toLowerCase();
  if ((event.ctrlKey || event.metaKey) && key === 's') {
    event.preventDefault();
    void saveFile();
  }
}

function startDrag(e) {
  const startX = e.clientX;
  const startW = treeWidth.value;
  // rAF 合并：高轮询率鼠标每秒可触发数百次 mousemove，
  // 响应式写入 + 父级样式更新按帧合并，避免中间帧抖动
  let raf = 0;
  let pendingWidth = startW;
  const applyWidth = () => {
    raf = 0;
    treeWidth.value = pendingWidth;
    emit('treeWidthChange', pendingWidth);
  };
  const onMove = (ev) => {
    // 树停靠右侧：向左拖（delta 为负）增加宽度，故与左锚布局相反取 startX - x
    const delta = startX - ev.clientX;
    pendingWidth = Math.max(150, Math.min(600, startW + delta));
    if (!raf) raf = requestAnimationFrame(applyWidth);
  };
  const onUp = () => {
    document.removeEventListener('mousemove', onMove);
    document.removeEventListener('mouseup', onUp);
    if (raf) cancelAnimationFrame(raf);
    applyWidth();
    dragTeardown = null;
  };
  document.addEventListener('mousemove', onMove);
  document.addEventListener('mouseup', onUp);
  // 拖拽期间组件被卸载时，mouseup 不再触发；记录解绑函数供 onBeforeUnmount 兜底
  dragTeardown = () => {
    document.removeEventListener('mousemove', onMove);
    document.removeEventListener('mouseup', onUp);
    if (raf) cancelAnimationFrame(raf);
  };
}

async function saveFile() {
  if (!activeFile.value || !dirty.value || saving.value) return true;
  saving.value = true;
  try {
    const result = await SaveWorkspaceFile({ path: activeFile.value.path, version: version.value, content: draftContent.value });
    originalContent.value = draftContent.value;
    originalHash.value = result?.sha256 || originalHash.value;
    version.value = result?.version || version.value;
    message.success(t('app.workspaceExplorer.saved'));
    return true;
  } catch (err) {
    message.error(t('app.workspaceExplorer.saveFailed', { error: errorText(err) }));
    return false;
  } finally {
    saving.value = false;
  }
}

function confirmPendingChange() {
  if (!dirty.value) return Promise.resolve(true);
  if (confirmPromise) return confirmPromise;
  confirmPromise = new Promise((resolve) => {
    let settled = false;
    const finish = (value) => {
      if (!settled) {
        settled = true;
        confirmPromise = null;
        resolve(value);
      }
    };
    dialog.warning({
      title: t('app.workspaceExplorer.unsavedTitle'),
      content: t('app.workspaceExplorer.unsavedContent'),
      positiveText: t('common.save'),
      negativeText: t('app.workspaceExplorer.discard'),
      onPositiveClick: async () => finish(await saveFile()),
      onNegativeClick: () => finish(true),
      onClose: () => finish(false),
    });
  });
  return confirmPromise;
}

async function requestClose() {
  if (!(await confirmPendingChange())) return false;
  requestSequence += 1;
  clearEditor();
  treeData.value = [];
  selectedKeys.value = [];
  emit('close');
  return true;
}

async function closeContent() {
  if (!(await confirmPendingChange())) return;
  requestSequence += 1;
  clearEditor();
  selectedKeys.value = [];
}

function clearEditor() {
  activeFile.value = null;
  draftContent.value = '';
  originalContent.value = '';
  originalHash.value = '';
  version.value = '';
  fileError.value = '';
  imageDataUrl.value = '';
  htmlContent.value = '';
  mdPreviewMode.value = false;
  if (aceEditor) aceEditor.setValue('', -1);
  if (mdPreviewRef.value) mdPreviewRef.value.innerHTML = '';
}

function errorText(err) {
  return String(err?.message || err || t('common.failed'));
}

watch(() => props.workspace, () => { void loadRoot(); }, { immediate: true });

onMounted(() => {
  const element = treeContainerRef.value?.$el || treeContainerRef.value;
  if (element && typeof ResizeObserver !== 'undefined') {
    resizeObserver = new ResizeObserver((entries) => {
      const height = Math.floor(entries[0]?.contentRect?.height || 0);
      if (height > 0) treeHeight.value = height;
    });
    resizeObserver.observe(element);
  }
  window.addEventListener('keydown', onGlobalKeydown, true);
  mdRenderer = new MarkdownIt({ html: false, linkify: true, typographer: true });
});

function onGlobalKeydown(event) {
  // 多个 Tab 的 explorer 常驻挂载，仅当前激活 Tab 的实例响应 ESC
  if (!props.active) return;
  if (event.key === 'Escape') {
    event.stopImmediatePropagation();
    if (activeFile.value) { void closeContent(); return; }
    void requestClose();
  }
}

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onGlobalKeydown, true);
  dragTeardown?.();
  dragTeardown = null;
  disposed = true;
  requestSequence += 1;
  resizeObserver?.disconnect();
  resizeObserver = null;
  destroyAceEditor();
  mdRenderer = null;
  clearEditor();
  treeData.value = [];
});

defineExpose({ requestClose, loadRoot });
</script>

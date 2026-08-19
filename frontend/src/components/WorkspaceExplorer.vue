<!--
 * SPDX-License-Identifier: GPL-3.0-only
 *
 * Workspace file browser/editor. The component is intentionally mounted only
 * while visible so large trees, editor text and highlight caches are released
 * when the user closes the explorer.
 -->
<template>
  <section :class="['workspace-explorer', { 'has-file': showEditor, 'tree-only': !showEditor }]" :style="{ width: showEditor ? null : (treeWidth + 1) + 'px' }" aria-label="Workspace explorer">
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
          :selected-keys="selectedKeys"
          :node-props="nodeProps"
          expand-on-click
          :render-prefix="renderPrefix"
          :render-label="renderLabel"
          :on-load="loadTreeNode"
          @update:selected-keys="onSelect"
        />
        <div v-else class="workspace-explorer-empty">{{ $t('app.workspace.none') }}</div>
      </n-spin>
    </aside>

    <div class="workspace-explorer-splitter" @mousedown.prevent="startDrag"></div>

    <main v-show="showEditor" class="workspace-explorer-editor">
      <header class="workspace-explorer-editor-header">
        <button type="button" class="workspace-explorer-close-content" :title="$t('common.close')" :aria-label="$t('common.close')" @click="closeContent"><CloseOutlined /></button>
        <div class="workspace-explorer-file-title" :title="activeFile?.path || ''">
          <span>{{ activeFile?.path || $t('app.filePreview.empty') }}</span>
          <span v-if="dirty" class="workspace-explorer-dirty" :title="$t('app.workspaceExplorer.unsaved')">●</span>
        </div>
        <div class="workspace-explorer-actions">
          <n-button
            v-if="isMarkdown && activeFile && !isImageMode"
            size="tiny"
            :type="mdPreviewMode ? 'primary' : 'default'"
            ghost
            @click="toggleMdPreview"
          >{{ mdPreviewMode ? $t('app.workspaceExplorer.editSource') : $t('app.workspaceExplorer.preview') }}</n-button>
          <n-button
            v-if="activeFile && !isImageMode && !mdPreviewMode"
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

      <!-- Markdown rendered preview -->
      <div v-show="isMarkdown && mdPreviewMode && !isImageMode" ref="mdPreviewRef" class="workspace-explorer-md-preview"></div>

      <!-- Ace editor (always in DOM, shown for text files when not in md preview) -->
      <div v-show="showEditor && !isImageMode && !mdPreviewMode" ref="aceContainerRef" class="workspace-explorer-ace"></div>

      <!-- Loading / error / empty states (only shown when no activeFile) -->
      <div v-show="loadingFile && !activeFile" class="workspace-explorer-editor-state"><n-spin size="small" /> {{ $t('app.filePreview.loading') }}</div>
      <div v-show="fileError && !activeFile" class="workspace-explorer-editor-state workspace-explorer-error">{{ fileError }}</div>
      <div v-show="!activeFile && !loadingFile && !fileError" class="workspace-explorer-editor-state">{{ $t('app.filePreview.empty') }}</div>
    </main>
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
import { useDialog, useMessage } from 'naive-ui';
import ace from 'ace-builds';
import 'ace-builds/src-noconflict/ext-language_tools';
import 'ace-builds/src-noconflict/ext-searchbox';
import 'ace-builds/src-noconflict/theme-monokai';
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
import { ListFiles, ReadWorkspaceFile, ReadWorkspaceImage, SaveWorkspaceFile, DeletePath, OpenPathInFileManager } from '../../bindings/ally-dev/internal/app/app';
import CloseOutlined from '@vicons/antd/CloseOutlined';
import FileOutlined from '@vicons/antd/FileOutlined';
import ReloadOutlined from '@vicons/antd/ReloadOutlined';
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
const mdPreviewMode = ref(false);
const contextMenuShow = ref(false);
const contextMenuX = ref(0);
const contextMenuY = ref(0);
const contextMenuNode = ref(null);
let aceEditor = null;
let resizeObserver = null;
let aceResizeObserver = null;
let requestSequence = 0;
let disposed = false;
let navigationBusy = false;
let confirmPromise = null;
let mdRenderer = null;
// nodeWrapperPadding 默认 '3px 0'：每行 hover/聚焦高亮上下各缩进 3px，
// 相邻行之间出现 6px 视觉缝隙（表现为"聚焦区域之间存在间距"）。
// 这里归零，让 20px 行高的高亮区域完全相邻。
const treeThemeOverrides = { nodeHeight: '20px', fontSize: '12px', nodeWrapperPadding: '0' };

const workspaceLabel = computed(() => {
  const value = String(props.workspace || '').replace(/[\\/]+$/, '');
  return value.split(/[\\/]/).filter(Boolean).pop() || value || t('app.workspace.none');
});
const dirty = computed(() => draftContent.value !== originalContent.value);
const showEditor = computed(() => Boolean(activeFile.value || loadingFile.value || fileError.value));
const isImageMode = computed(() => Boolean(activeFile.value && imageDataUrl.value));
const isMarkdown = computed(() => {
  if (!activeFile.value) return false;
  const ext = extension(activeFile.value.path);
  return ext === 'md' || ext === 'markdown';
});

const ACE_MODE_MAP = {
  ts: 'typescript', tsx: 'typescript', js: 'javascript', jsx: 'javascript',
  py: 'python', go: 'golang', java: 'java', sh: 'sh', bash: 'sh',
  json: 'json', yaml: 'yaml', yml: 'yaml', css: 'css', html: 'html',
  xml: 'xml', sql: 'sql', c: 'c_cpp', cpp: 'c_cpp', h: 'c_cpp',
  md: 'markdown', markdown: 'markdown', rs: 'rust', rb: 'ruby',
};
const IMAGE_EXTS = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'bmp', 'svg', 'ico']);
const isImageFile = (path) => IMAGE_EXTS.has(extension(path));
const extension = (path) => {
  const name = String(path || '').split(/[\\/]/).pop() || '';
  const dot = name.lastIndexOf('.');
  return dot > 0 ? name.slice(dot + 1).toLowerCase() : '';
};

function aceModeForPath(path) {
  return ACE_MODE_MAP[extension(path)] || 'text';
}

function initAceEditor() {
  if (aceEditor || !aceContainerRef.value) return;
  aceEditor = ace.edit(aceContainerRef.value, {
    mode: 'ace/mode/text',
    theme: 'ace/theme/monokai',
    fontSize: '13px',
    fontFamily: "Inter, 'Segoe UI', 'Microsoft YaHei', 'PingFang SC', sans-serif",
    showPrintMargin: false,
    tabSize: 2,
    useSoftTabs: true,
    wrap: false,
  });
  aceEditor.setOptions({
    enableBasicAutocompletion: true,
    enableLiveAutocompletion: false,
    showFoldWidgets: false,
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
  return {
    key: entry.Path || entry.path || entry.Name || entry.name,
    label: entry.Name || entry.name,
    path: entry.Path || entry.path,
    isLeaf: !(entry.Dir ?? entry.dir),
    dir: Boolean(entry.Dir ?? entry.dir),
  };
}

async function listDirectory(path = '') {
  const entries = await ListFiles({ path, maxDepth: 1, limit: 1000, includeHidden: true, includeIgnored: false });
  return (Array.isArray(entries) ? entries : []).map(makeNode);
}

async function loadRoot() {
  const requestID = ++requestSequence;
  selectedKeys.value = [];
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

async function loadTreeNode(node) {
  if (!node?.dir || Array.isArray(node.children)) return;
  try {
    node.children = await listDirectory(node.path);
  } catch (err) {
    node.children = [];
    message.error(t('app.workspaceExplorer.treeFailed', { error: errorText(err) }));
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

function renderPrefix({ option }) {
  if (option?.dir) return null;
  return h(FileOutlined);
}

function renderLabel({ option }) {
  return h('span', { class: 'workspace-explorer-node-label', title: option.path }, option.label);
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
    return [{ label: t('app.workspaceExplorer.openFolder'), key: 'openFolder' }];
  }
  return [
    {
      label: t('common.delete'),
      key: 'delete',
    },
  ];
});

function onContextMenuSelect(key) {
  contextMenuShow.value = false;
  if (key === 'delete') void confirmDeleteNode(contextMenuNode.value);
  if (key === 'openFolder') void openWorkspaceFolder();
}

async function openWorkspaceFolder() {
  if (!props.workspace) return;
  try {
    await OpenPathInFileManager(props.workspace);
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
  if (normalized.includes('../') || normalized.startsWith('/') || /^[a-zA-Z]:/.test(normalized)) {
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

async function onSelect(keys, options) {
  if (navigationBusy) return;
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

async function openFile(node) {
  if (!node?.path || node.path === activeFile.value?.path) return true;
  const proceed = await confirmPendingChange();
  if (!proceed) return false;
  const requestID = ++requestSequence;
  fileError.value = '';
  imageDataUrl.value = '';
  mdPreviewMode.value = false;
  loadingFile.value = true;
  try {
    if (isImageFile(node.path)) {
      const result = await ReadWorkspaceImage(node.path);
      if (disposed || requestID !== requestSequence) return false;
      imageDataUrl.value = String(result?.data || '');
      activeFile.value = { path: node.path, image: true };
      draftContent.value = '';
      originalContent.value = '';
      loadingFile.value = false;
      return true;
    }
    const result = await ReadWorkspaceFile(node.path);
    if (disposed || requestID !== requestSequence) return false;
    const content = String(result?.content || '');
    activeFile.value = { path: node.path };
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
      fileError.value = t('app.filePreview.failed', { error: errorText(err) });
      loadingFile.value = false;
    }
    return false;
  }
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
    const delta = ev.clientX - startX;
    pendingWidth = Math.max(150, Math.min(600, startW + delta));
    if (!raf) raf = requestAnimationFrame(applyWidth);
  };
  const onUp = () => {
    document.removeEventListener('mousemove', onMove);
    document.removeEventListener('mouseup', onUp);
    if (raf) cancelAnimationFrame(raf);
    applyWidth();
  };
  document.addEventListener('mousemove', onMove);
  document.addEventListener('mouseup', onUp);
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

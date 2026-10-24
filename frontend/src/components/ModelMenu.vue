<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU
Public License v3. See the LICENSE file for details.
-->
<template>
  <!-- Shared model selector dropdown: provider-grouped, usage-sorted, with a
       live search box and an optional "manage models" row. Extracted from
       ComposerInfoBar so the games panel gets the exact same menu; styling is
       the global .model-menu block in style.css. The trigger element is the
       caller's slot, so each host keeps its own trigger look. -->
  <n-dropdown
    trigger="click"
    :placement="placement"
    scrollable
    :disabled="disabled"
    :options="modelMenuOptions"
    :render-label="renderModelMenuLabel"
    :node-props="modelMenuNodeProps"
    :menu-props="modelMenuProps"
    @select="onSelect"
    @update:show="onShow"
  ><slot /></n-dropdown>
</template>

<script setup>
import { computed, h, ref } from 'vue';
import { NDropdown, NIcon, NInput } from 'naive-ui';
import CheckOutlined from '@vicons/antd/CheckOutlined';
import SearchOutlined from '@vicons/antd/SearchOutlined';
import { t } from '../i18n.mjs';
import { modelConfigIdentity } from '../utils/modelConfigIO.mjs';
import { getModelUsage, getRecentModelUsage, recordModelUsage, recordRecentModelUsage } from '../utils/modelUsage.mjs';

const props = defineProps({
  // Array of model presets (config.models shape).
  models: { type: Array, default: () => [] },
  // modelConfigIdentity() of the currently active model; '' shows no checkmark.
  activeIdentity: { type: String, default: '' },
  lastUsedIdentity: { type: String, default: '' },
  disabled: { type: Boolean, default: false },
  placement: { type: String, default: 'top-start' },
  showManage: { type: Boolean, default: true },
  // Persist the selection into the shared per-provider usage map (localStorage)
  // that re-orders this menu's groups. The chat composer records; isolated
  // hosts like the games panel pass false so their picks never leak into the
  // chat dropdown's ordering.
  recordUsage: { type: Boolean, default: true },
});
const emit = defineEmits(['select', 'manage']);

// Live filter query; reset on the next open to preserve filtering during leave.
const modelSearch = ref('');
const modelUsage = ref(getModelUsage());
const recentModelUsage = ref(getRecentModelUsage());

function providerLabel(model) {
  return (model?.providerName || '').trim() || 'OpenAI Compatible';
}

function modelProviderKey(model) {
  return providerLabel(model).toLocaleLowerCase();
}

function isActiveModel(model) {
  return modelConfigIdentity(model) === props.activeIdentity;
}

function compareModelLabels(left, right) {
  return String(left || '').localeCompare(String(right || ''), undefined, {
    sensitivity: 'base',
    numeric: true,
  });
}

const modelGroups = computed(() => {
  const usage = modelUsage.value;
  const groups = new Map();
  (props.models || []).forEach((model, index) => {
    const label = providerLabel(model);
    const key = modelProviderKey(model);
    if (!groups.has(key)) groups.set(key, { key, label, models: [] });
    groups.get(key).models.push({ model, index });
  });

  return [...groups.values()]
    .map((group) => ({
      ...group,
      useCount: Number(usage[group.key]) || 0,
      models: group.models.sort((left, right) => compareModelLabels(left.model?.model, right.model?.model)),
    }))
    .sort((left, right) => {
      // Most-used group first; ties (including all-zero for fresh users) keep
      // the previous stable alphabetical order.
      if (left.useCount !== right.useCount) return right.useCount - left.useCount;
      return compareModelLabels(left.label, right.label);
    });
});

const modelMenuOptions = computed(() => {
  const options = [];
  // 搜索行是滚动内容的第一行，靠 style.css 里的 sticky 钉在弹出层顶部，
  // 不随下面的分组列表滚走。naive 把 type: 'render' 的 props 贴在它外面
  // 那层 div 上，所以类名必须走 props：sticky 元素自己不能被一个只有自身
  // 高度的小盒子包着（sticky 无法超出父盒）。
  options.push({
    key: 'search',
    type: 'render',
    props: {
      class: 'model-menu-search',
      // While the dropdown is open naive's NDropdown keeps a document-level
      // keydown handler (vooks useKeyboard) that `preventDefault`s the arrow
      // keys (no caret movement in a text field) and treats Enter as "select
      // the pending option" (a stray Enter could switch models and close the
      // menu). Keep those keys inside this component; Escape is let through
      // so it still closes the dropdown.
      onKeydown: (event) => { if (event.key !== 'Escape') event.stopPropagation(); },
    },
    render: renderModelMenuSearch,
  });
  const query = modelSearch.value.trim().toLocaleLowerCase();
  const matches = (item) => {
    if (!query) return true;
    const name = [item.model?.displayName, item.model?.model].filter(Boolean).join(' ').toLocaleLowerCase();
    const provider = providerLabel(item.model).toLocaleLowerCase();
    return name.includes(query) || provider.includes(query);
  };
  // 旧版本没有最近列表，先用已保存的最近模型（或当前模型）衔接。
  // 累计频次无法还原使用顺序，因此不拿频次伪造其余记录。
  const recent = recentModelUsage.value
    .map((identity) => {
      const index = props.models.findIndex((model) => modelConfigIdentity(model) === identity);
      return index < 0 ? null : { model: props.models[index], index };
    })
    .filter(Boolean);
  if (!recent.length) {
    for (const identity of [props.lastUsedIdentity, props.activeIdentity]) {
      const index = props.models.findIndex((model) => modelConfigIdentity(model) === identity);
      if (index < 0) continue;
      recent.push({ model: props.models[index], index });
      break;
    }
  }
  const visibleRecent = recent.filter(matches);
  if (visibleRecent.length) {
    options.push({
      key: 'recent-title', type: 'render',
      props: { class: 'model-menu-section-title' },
      render: () => t('composer.models.recent'),
    });
  }
  for (const item of visibleRecent) {
    options.push({
      key: `recent:${item.index}`,
      isRecent: true,
      label: item.model.displayName || item.model.model || '-',
      provider: providerLabel(item.model),
      active: isActiveModel(item.model),
    });
  }
  if (visibleRecent.length) options.push({ type: 'divider', key: 'recent-divider' });
  const groups = modelGroups.value
    .map((group) => ({ ...group, models: group.models.filter(matches) }))
    .filter((group) => group.models.length > 0);
  if (groups.length === 0) {
    options.push({ key: 'empty', label: query ? t('composer.models.noMatch') : t('composer.models.empty'), disabled: true, isEmpty: true });
  } else {
    for (const group of groups) {
      options.push({
        key: `group:${group.key}`,
        label: group.label,
        count: group.models.length,
        hasActiveModel: group.models.some((item) => isActiveModel(item.model)),
        children: group.models.map((item) => ({
          key: `model:${item.index}`,
          label: item.model.displayName || item.model.model || '-',
          active: isActiveModel(item.model),
        })),
      });
    }
  }
  if (props.showManage) {
    // 管理入口固定在底部。
    // 类名走 nodeProps（贴在 option 本体上）：rawNode.props 会被 naive 挂到
    // 内层 option-body，而那层只有一行高，sticky 出不去。
    options.push({ key: 'manage', label: t('composer.models.manage'), isManage: true });
  }
  return options;
});

function renderModelMenuSearch() {
  return h(NInput, {
    size: 'small',
    value: modelSearch.value,
    placeholder: t('composer.models.search'),
    // 边框交给外层容器（圆角浅底），输入框本身只留文字和放大镜。
    bordered: false,
    clearable: true,
    'onUpdate:value': (value) => { modelSearch.value = String(value || ''); },
    onClick: (event) => event.stopPropagation(),
  }, {
    prefix: () => h(NIcon, { size: 14 }, () => h(SearchOutlined)),
  });
}

function modelMenuProps() {
  return {
    class: 'model-menu',
    style: { minWidth: '260px', maxHeight: 'min(440px, calc(100vh - 140px))' },
  };
}

// 当前生效的模型行加一个稳定的类名，样式据此铺底（naive 的 pending 只覆盖悬停态）；
// 管理行同理，靠这个类把它钉在列表底部。
function modelMenuNodeProps(option) {
  if (option?.isManage) return { class: 'model-menu-manage-row' };
  return option?.active ? { class: 'model-menu-row-active' } : undefined;
}

function onSelect(key) {
  if (key === 'manage') {
    emit('manage');
    return;
  }
  if (typeof key === 'string' && /^(model|recent):/.test(key)) {
    const index = Number(key.split(':')[1]);
    if (!Number.isNaN(index)) {
      if (props.recordUsage) recordModelSwitch(index);
      emit('select', index);
    }
  }
}

// recordModelSwitch bumps the usage count for the selected model's provider
// group, pruning keys for providers no longer present in the config, then
// refreshes the reactive snapshot so modelGroups re-sorts.
function recordModelSwitch(index) {
  const model = (props.models || [])[index];
  if (!model) return;
  const validKeys = (props.models || []).map((m) => modelProviderKey(m));
  const validIdentities = (props.models || []).map((m) => modelConfigIdentity(m));
  recordModelUsage(modelProviderKey(model), validKeys, modelConfigIdentity(model), validIdentities);
  modelUsage.value = getModelUsage();
  recentModelUsage.value = getRecentModelUsage();
}

function renderModelMenuLabel(option) {
  if (option.isManage) {
    return h('span', { class: 'model-menu-manage' }, option.label);
  }
  if (option.isEmpty) {
    return h('span', { class: 'model-menu-empty' }, option.label);
  }
  if (option.children?.length) {
    return h('span', { class: ['model-menu-group', { active: option.hasActiveModel }] }, [
      h('span', { class: 'model-menu-group-name' }, option.label),
      h('span', { class: 'model-menu-group-count' }, String(option.count)),
    ]);
  }
  return h('span', { class: ['model-menu-item', { active: option.active, 'model-menu-item-recent': option.isRecent }] }, [
    h('span', { class: 'model-menu-item-name' }, option.label),
    option.provider ? h('span', { class: 'model-menu-item-provider' }, option.provider) : null,
    option.active ? h(NIcon, { size: 14, class: 'model-menu-item-mark' }, () => h(CheckOutlined)) : null,
  ]);
}

function onShow(show) {
  // Keep the filtered list intact while the dropdown's leave transition runs.
  // Reset on open so each visit still starts with the full list.
  if (show) {
    modelSearch.value = '';
    modelUsage.value = getModelUsage();
    recentModelUsage.value = getRecentModelUsage();
    const valid = props.models.map(modelConfigIdentity);
    if (props.recordUsage && !recentModelUsage.value.some((id) => valid.includes(id))) {
      const seed = [props.lastUsedIdentity, props.activeIdentity].find((id) => valid.includes(id));
      if (seed) {
        recordRecentModelUsage(seed, valid);
        recentModelUsage.value = getRecentModelUsage();
      }
    }
  }
}
</script>

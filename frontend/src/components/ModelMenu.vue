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
    :menu-props="modelMenuProps"
    @select="onSelect"
    @update:show="onShow"
  ><slot /></n-dropdown>
</template>

<script setup>
import { computed, h, ref } from 'vue';
import { NDropdown, NInput } from 'naive-ui';
import { t } from '../i18n.mjs';
import { modelConfigIdentity } from '../utils/modelConfigIO.mjs';
import { getModelUsage, recordModelUsage } from '../utils/modelUsage.mjs';

const props = defineProps({
  // Array of model presets (config.models shape).
  models: { type: Array, default: () => [] },
  // modelConfigIdentity() of the currently active model; '' shows no checkmark.
  activeIdentity: { type: String, default: '' },
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

// Live filter query for the model dropdown's search box; cleared on close.
const modelSearch = ref('');
const modelUsage = ref(getModelUsage());

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
    if (!groups.has(key)) groups.set(key, { key, label, models: [], hasActiveModel: false });
    const group = groups.get(key);
    group.models.push({ model, index });
    if (isActiveModel(model)) group.hasActiveModel = true;
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
  // Live search box, rendered as the first (non-selectable) row of the menu;
  // typing filters the model groups below by model name or provider label
  // (case-insensitive substring). The menu is `scrollable`, so this row lives
  // inside the scroll container and scrolls away with the list rather than
  // staying pinned at the top.
  options.push({
    key: 'search',
    type: 'render',
    render: () => h(
      'div',
      {
        class: 'model-menu-search',
        // While the dropdown is open naive's NDropdown keeps a document-level
        // keydown handler (vooks useKeyboard) that `preventDefault`s the arrow
        // keys (no caret movement in a text field) and treats Enter as "select
        // the pending option" (a stray Enter could switch models and close the
        // menu). Keep those keys inside this component; Escape is let through
        // so it still closes the dropdown.
        onKeydown: (event) => { if (event.key !== 'Escape') event.stopPropagation(); },
      },
      [
        h(NInput, {
          size: 'tiny',
          value: modelSearch.value,
          placeholder: t('composer.models.search'),
          'onUpdate:value': (value) => { modelSearch.value = String(value || ''); },
          onClick: (event) => event.stopPropagation(),
        }),
      ],
    ),
  });
  const query = modelSearch.value.trim().toLocaleLowerCase();
  const matches = (item) => {
    if (!query) return true;
    const name = String(item.model?.model || '').toLocaleLowerCase();
    const provider = providerLabel(item.model).toLocaleLowerCase();
    return name.includes(query) || provider.includes(query);
  };
  const groups = modelGroups.value
    .map((group) => {
      // Recompute the active flag from the *filtered* models so a group is not
      // highlighted while the active model is hidden by the query.
      const models = group.models.filter(matches);
      return { ...group, models, hasActiveModel: models.some((item) => isActiveModel(item.model)) };
    })
    .filter((group) => group.models.length > 0);
  if (groups.length === 0) {
    options.push({ key: 'empty', label: query ? t('composer.models.noMatch') : t('composer.models.empty'), disabled: true, isEmpty: true });
  } else {
    for (const group of groups) {
      options.push({
        key: `group:${group.key}`,
        label: group.label,
        count: group.models.length,
        hasActiveModel: group.hasActiveModel,
        children: group.models.map((item) => ({
          key: `model:${item.index}`,
          label: item.model.model || '-',
          active: isActiveModel(item.model),
        })),
      });
    }
  }
  if (props.showManage) {
    options.push({ type: 'divider', key: 'divider' });
    options.push({ key: 'manage', label: t('composer.models.manage'), isManage: true });
  }
  return options;
});

function modelMenuProps() {
  return {
    class: 'model-menu',
    style: { minWidth: '240px', maxHeight: 'min(420px, calc(100vh - 160px))' },
  };
}

function onSelect(key) {
  if (key === 'manage') {
    emit('manage');
    return;
  }
  if (typeof key === 'string' && key.startsWith('model:')) {
    const index = parseInt(key.slice(6), 10);
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
}

function renderModelMenuLabel(option) {
  if (option.isManage) {
    return h('span', { class: 'model-menu-manage' }, option.label);
  }
  if (option.isEmpty) {
    return h('span', { class: 'model-menu-empty' }, option.label);
  }
  if (option.children && option.children.length) {
    return h('span', { class: ['model-menu-group', { active: option.hasActiveModel }] }, [
      h('span', { class: 'model-menu-group-name' }, option.label),
      h('span', { class: 'model-menu-group-count' }, String(option.count)),
    ]);
  }
  return h('span', { class: ['model-menu-item', { active: option.active }] }, [
    h('span', { class: 'model-menu-item-name' }, option.label),
    option.active ? h('span', { class: 'model-menu-item-mark' }, '✓') : null,
  ]);
}

function onShow(show) {
  // Reset the filter when the dropdown closes so the next open starts fresh.
  if (!show) modelSearch.value = '';
}
</script>

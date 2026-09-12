<!--
SPDX-License-Identifier: GPL-3.0-only

Copyright (C) 2026 tangssst <tangssst@qq.com>
GitHub: https://github.com/Bronya0/ally-agent

This file is part of ally-agent, licensed under the GNU General
Public License v3. See the LICENSE file for details.
-->

<template>
  <!-- Inline skills page: rendered inside the main-area container (App.vue
       mode === 'skills' via v-show). Always mounted so the list survives mode
       switches; every entry refreshes in place. Extracted from the old
       Settings → Skills page so skills live on the mode rail below the
       knowledge base, fully decoupled from the Settings panel. -->
  <div class="config-inline-panel">
    <header class="config-inline-header">
      <div class="panel-header-copy">
        <span class="config-inline-title">{{ t('app.mode.skills') }}</span>
        <span class="config-inline-subtitle">{{ t('app.skills.subtitle', { enabled: activeSkillNames.length, available: availableSkills.length }) }}</span>
      </div>
      <div class="panel-header-actions">
        <n-input
          v-model:value="skillSearch"
          class="panel-search-input"
          size="small"
          clearable
          :placeholder="t('common.searchPlaceholder')"
        >
          <template #prefix><SearchOutlined class="panel-search-icon" /></template>
        </n-input>
        <n-button size="small" secondary :loading="skillsLoading" @click="refreshSkillState">{{ t('common.refresh') }}</n-button>
      </div>
    </header>

    <div class="panel-scroll-body">
      <div v-if="sourceTabs.length" class="skill-source-tabs-row">
        <!-- Source tabs double as the list filter. :key rebuilds the tabs when
             a refresh adds/removes a source so the active-line bar recalculates
             (n-tabs caches its pixel offset otherwise). -->
        <n-tabs
          :key="sourceTabSetKey"
          :value="activeSourceTab"
          type="line"
          size="small"
          class="skill-source-tabs"
          @update:value="(value) => (activeSourceTab = value)"
        >
          <n-tab v-for="tab in sourceTabs" :key="tab.source" :name="tab.source">
            {{ skillSourceLabel(tab.source) }} {{ tab.count }}
          </n-tab>
        </n-tabs>
      </div>

      <div class="skill-settings-list">
        <div v-if="skillsLoading && !availableSkills.length" class="saved-model-empty">{{ t('settings.skillsLoading') }}</div>
        <div v-else-if="!availableSkills.length" class="saved-model-empty">{{ t('settings.skillsEmpty') }}</div>
        <div v-else-if="!filteredSkills.length" class="saved-model-empty">{{ skillSearch.trim() ? t('common.searchEmpty') : t('app.skills.filterEmpty') }}</div>
        <div v-for="sk in filteredSkills" :key="`${sk.source || 'skill'}:${sk.name}`" :class="['skill-settings-item', { active: isSkillActive(sk.name, activeSkillNames), builtin: sk.source === 'builtin' }]">
          <div class="skill-settings-main">
            <div class="skill-title-row">
              <span class="skill-name">{{ sk.name }}</span>
            </div>
            <div class="skill-description">{{ sk.description || sk.whenToUse || $t('common.noDescription') }}</div>
            <div
              class="skill-meta clickable"
              :title="t('settings.openSkillPath', { path: sk.path || sk.dir })"
              @click="handleOpenSkillPath(sk)"
            >{{ sk.path || sk.dir || '-' }}</div>
          </div>
          <n-switch
            :value="isSkillActive(sk.name, activeSkillNames)"
            :disabled="skillsLoading || skillToggleInFlight === sk.name || sk.source === 'builtin'"
            @update:value="(value) => toggleSkill(sk, value)"
          />
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed, ref, watch } from 'vue';
import { useMessage } from 'naive-ui';
import { SearchOutlined } from '@vicons/antd';
import { t } from '../i18n.mjs';
import { isSkillActive } from '../utils/skills.mjs';
import {
  ListSkills, ActivateSkill, DeactivateSkill, GetActiveSkills, OpenPathInFileManager,
} from '../../bindings/ally-dev/internal/app/app';

// Inline skills page (App.vue mode === 'skills'): owns its whole state — the
// discovered skill list, the active set, and per-skill toggle busy flags —
// and reports every change back to App.vue through `skills-changed` so the
// welcome table / slash-command metadata / config.disabledSkills stay fresh.
const props = defineProps({ show: { type: Boolean, default: false } });
const emit = defineEmits(['skills-changed']);
const message = useMessage();

// Skills state
const availableSkills = ref([]);
const activeSkillNames = ref([]);
const skillsLoading = ref(false);
const skillToggleInFlight = ref('');

// 头部搜索框：按名称/描述/whenToUse/路径实时过滤。
const skillSearch = ref('');

function skillMatchesSearch(sk, needle) {
  if (!needle) return true;
  return [sk.name, sk.description, sk.whenToUse, sk.path, sk.dir]
    .some((field) => String(field || '').toLowerCase().includes(needle));
}

// 来源 tab：每个来源一个 tab，兼作列表过滤。固定 builtin → project → user
// 顺序，其余来源按出现顺序排在最后。搜索时只保留有命中的来源（计数显示
// 命中数），避免激活 tab 停在无命中来源上让用户误以为没有数据；清空搜索
// 恢复全部来源与总数。
const activeSourceTab = ref('');

const sourceTabs = computed(() => {
  const needle = skillSearch.value.trim().toLowerCase();
  const order = ['builtin', 'project', 'user'];
  const bySource = new Map();
  for (const sk of availableSkills.value) {
    if (!skillMatchesSearch(sk, needle)) continue;
    const source = sk.source || 'unknown';
    bySource.set(source, (bySource.get(source) || 0) + 1);
  }
  return [...bySource.entries()]
    .sort((a, b) => {
      const ia = order.indexOf(a[0]);
      const ib = order.indexOf(b[0]);
      if (ia !== -1 && ib !== -1) return ia - ib;
      if (ia !== -1) return -1;
      if (ib !== -1) return 1;
      return 0;
    })
    .map(([source, count]) => ({ source, count }));
});

// n-tabs 会缓存激活下划线的像素偏移：tab 集合变化（搜索过滤来源、刷新后
// 来源增删）时用 key 强制重建让 bar 重算。
const sourceTabSetKey = computed(() => sourceTabs.value.map((tab) => tab.source).join('\u0000'));

// 激活 tab 不在可见集合（来源被刷新移除、或搜索后本来源无命中）时，
// 回退第一个可见 tab。
watch(sourceTabSetKey, () => {
  if (!sourceTabs.value.some((tab) => tab.source === activeSourceTab.value)) {
    activeSourceTab.value = sourceTabs.value[0]?.source || '';
  }
});

// 当前来源 tab + 搜索词双重过滤；来源 tab 内 builtin 置顶无意义，按名称排序。
const filteredSkills = computed(() => {
  const needle = skillSearch.value.trim().toLowerCase();
  return availableSkills.value
    .filter((sk) => (sk.source || 'unknown') === activeSourceTab.value && skillMatchesSearch(sk, needle))
    .sort((a, b) => String(a.name).localeCompare(b.name));
});

// 来源展示名国际化：徽标 CSS class 与过滤键仍用后端原始 source 值，
// 仅展示层经 t() 映射，未知来源原样回退。
function skillSourceLabel(source) {
  const key = String(source || '').trim();
  const labelKeys = {
    project: 'app.skills.source.project',
    user: 'app.skills.source.user',
    builtin: 'app.skills.source.builtin',
  };
  if (labelKeys[key]) return t(labelKeys[key]);
  return key || t('app.skills.source.unknown');
}

async function handleOpenSkillPath(sk) {
  const path = sk?.path || sk?.dir;
  if (!path) return;
  try {
    await OpenPathInFileManager(path);
  } catch (err) {
    message.warning(t('settings.openSkillPathFailed', { error: err }));
  }
}

async function refreshSkillState() {
  skillsLoading.value = true;
  try {
    const [skills, active] = await Promise.all([ListSkills(), GetActiveSkills()]);
    availableSkills.value = skills || [];
    activeSkillNames.value = active || [];
  } catch (err) {
    message.error(t('app.skills.stateFailed', { error: err }));
  } finally {
    skillsLoading.value = false;
  }
}

async function toggleSkill(skill, active) {
  const skillName = skill?.name || '';
  if (!skillName) return;
  skillToggleInFlight.value = skillName;
  try {
    if (active) {
      await ActivateSkill(skillName);
    } else {
      await DeactivateSkill(skillName);
    }
    await refreshSkillState();
    emit('skills-changed');
  } catch (err) {
    message.error(t('settings.skillToggleFailed', {
      action: active ? t('settings.skillEnable') : t('settings.skillDisable'),
      error: err,
    }));
  } finally {
    skillToggleInFlight.value = '';
  }
}

// The page stays mounted (parent v-show): the list persists across mode
// switches and every entry triggers an in-place refresh.
watch(
  () => props.show,
  (visible) => {
    if (visible) refreshSkillState();
  },
);
</script>

<style scoped>
/* Inline skills page: fills the main-area container like the settings and
   token-stats pages. */
.config-inline-panel {
  display: flex;
  flex-direction: column;
  width: 100%;
  height: 100%;
  min-width: 0;
  min-height: 0;
  background: var(--ally-surface-content);
  overflow: hidden;
}

.config-inline-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 14px 22px 12px;
  border-bottom: 1px solid var(--ally-border);
  flex-shrink: 0;
}

.config-inline-title {
  font-size: 18px;
  font-weight: 700;
  letter-spacing: 0.5px;
  color: var(--ally-text-primary);
}

.panel-scroll-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 16px 24px 28px;
}

.saved-model-empty {
  color: var(--ally-text-faint);
  font-size: var(--ally-sub-font-size);
  padding: 28px 0;
  text-align: center;
}

.skill-settings-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.skill-settings-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 10px;
  border-radius: 6px;
  transition: background 0.12s;
  gap: 10px;
}

.skill-settings-item:hover {
  background: var(--ally-hover-faint);
}

.skill-settings-item.active {
  background: var(--ally-state-hover);
}

.skill-settings-main {
  flex: 1;
  min-width: 0;
}

.skill-title-row {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

.skill-name {
  font-size: var(--ally-sub-font-size);
  font-weight: 500;
  color: var(--ally-text-primary);
}

.panel-header-copy {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.config-inline-subtitle {
  font-size: 12px;
  color: var(--ally-text-muted);
}

/* 头部右侧动作区与搜索框：与 MCP/Models 面板完全同构。 */
.panel-header-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: none;
  flex-wrap: wrap;
  justify-content: flex-end;
}

.panel-search-input {
  width: 200px;
}

.panel-search-icon {
  font-size: 13px;
  color: var(--ally-text-muted);
}

/* 来源 tabs：兼作列表过滤，行内嵌在滚动区顶部。 */
.skill-source-tabs-row {
  margin-bottom: 6px;
}

.skill-source-tabs :deep(.n-tabs-tab) {
  padding: 6px 10px;
  font-size: var(--ally-sub-font-size);
}

.skill-settings-item.builtin {
  opacity: 0.85;
}

.skill-settings-item.builtin .skill-name {
  color: var(--ally-text-body);
}

.skill-settings-item.builtin .skill-description {
  color: var(--ally-text-muted);
}

.skill-description {
  font-size: 12px;
  color: var(--ally-text-muted);
  margin-top: 2px;
}

.skill-meta {
  font-size: 11px;
  color: var(--ally-text-ghost);
  margin-top: 1px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 320px;
}

.skill-meta.clickable {
  cursor: pointer;
  color: var(--ally-text-muted);
}

.skill-meta.clickable:hover {
  color: var(--ally-success-deep);
  text-decoration: underline;
}
</style>

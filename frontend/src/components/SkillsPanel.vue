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
      <span class="config-inline-title">{{ t('app.mode.skills') }}</span>
      <n-button size="small" quaternary :loading="skillsLoading" @click="refreshSkillState">{{ t('common.refresh') }}</n-button>
    </header>

    <div class="panel-scroll-body">
      <div class="config-section-subtitle skill-summary-row">
        {{ t('settings.skillsSummary', { enabled: activeSkillNames.length, available: availableSkills.length }) }}
        <span v-for="s in skillSourceCounts" :key="s.source" :class="['skill-badge', s.source]">{{ s.source }} {{ s.count }}</span>
      </div>

      <div class="skill-settings-list">
        <div v-if="skillsLoading && !availableSkills.length" class="saved-model-empty">{{ t('settings.skillsLoading') }}</div>
        <div v-else-if="!availableSkills.length" class="saved-model-empty">{{ t('settings.skillsEmpty') }}</div>
        <div v-for="sk in sortedSkills" :key="`${sk.source || 'skill'}:${sk.name}`" :class="['skill-settings-item', { active: isSkillActive(sk.name, activeSkillNames), builtin: sk.source === 'builtin' }]">
          <div class="skill-settings-main">
            <div class="skill-title-row">
              <span class="skill-name">{{ sk.name }}</span>
              <span :class="['skill-badge', sk.source || 'unknown']">{{ sk.source || 'unknown' }}</span>
              <span v-if="isSkillActive(sk.name, activeSkillNames)" class="skill-badge loaded">{{ t('common.enabled') }}</span>
              <span v-if="sk.source === 'builtin'" class="skill-badge builtin-locked">{{ t('settings.builtinAlwaysOn') }}</span>
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

// 来源统计标签：按 source 分组计数（project / user / builtin），
// 仅显示当前扫描范围内的来源（计数为 0 的来源不出现）。
const skillSourceCounts = computed(() => {
  const counts = {};
  for (const sk of availableSkills.value) {
    const source = sk.source || 'unknown';
    counts[source] = (counts[source] || 0) + 1;
  }
  return Object.entries(counts).map(([source, count]) => ({ source, count }));
});

// Sort skills: built-in skills first (always enabled), then others alphabetically
const sortedSkills = computed(() => {
  return [...availableSkills.value].sort((a, b) => {
    const aBuiltin = a.source === 'builtin';
    const bBuiltin = b.source === 'builtin';
    if (aBuiltin && !bBuiltin) return -1;
    if (!aBuiltin && bBuiltin) return 1;
    return String(a.name).localeCompare(b.name);
  });
});

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

.config-section-subtitle {
  font-size: 12px;
  color: var(--ally-text-muted);
  margin-top: 2px;
  margin-bottom: 14px;
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

.skill-badge {
  display: inline-block;
  font-size: 10px;
  padding: 1px 5px;
  border-radius: 4px;
  font-weight: 500;
  text-transform: uppercase;
  letter-spacing: 0.3px;
  background: var(--ally-hover-strong);
  color: var(--ally-text-muted);
}

.skill-summary-row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px;
}

.skill-summary-row .skill-badge {
  text-transform: none;
  letter-spacing: 0;
  font-size: 10px;
}

.skill-badge.user {
  background: #2a3a5c;
  color: #8ab4ff;
}

.skill-badge.project {
  background: #2a4a3a;
  color: var(--ally-success-pale);
}

.skill-badge.loaded {
  background: #3a4a2a;
  color: #b8d4a0;
}

.skill-badge.builtin-locked {
  background: var(--ally-scrollbar);
  color: var(--ally-text-soft);
  border: 1px solid var(--ally-border-strong);
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

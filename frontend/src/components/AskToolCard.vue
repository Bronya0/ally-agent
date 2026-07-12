<template>
  <div :class="['rich-tool-card', 'ask', msg.status]">
    <div class="tool-line ask-tool-line">
      <span :class="['tool-status-icon', msg.status]">{{ statusIcon }}</span>
      <span class="tool-verb">{{ statusVerb }}</span>
      <span class="tool-name">Ask</span>
      <span class="tool-chip">· {{ questions.length }} 个问题</span>
      <span v-if="msg.durationText" class="tool-duration">{{ msg.durationText }}</span>
    </div>

    <div v-if="msg.status === 'error'" class="ask-closed-state">{{ msg.body || '提问已取消' }}</div>

    <div v-else-if="questions.length" class="ask-content">
      <div class="ask-tabs-row">
        <button class="ask-nav-btn" type="button" title="上一个问题" :disabled="activeIndex === 0" @click="activeIndex--">‹</button>
        <div class="ask-tabs" role="tablist">
          <button
            v-for="(question, index) in questions"
            :key="question.id"
            type="button"
            :class="['ask-tab', { active: activeIndex === index, answered: isAnswered(question) }]"
            role="tab"
            :aria-selected="activeIndex === index"
            @click="activeIndex = index"
          >
            {{ index + 1 }}
          </button>
        </div>
        <button class="ask-nav-btn" type="button" title="下一个问题" :disabled="activeIndex >= questions.length - 1" @click="activeIndex++">›</button>
      </div>

      <template v-if="activeQuestion">
        <div class="ask-question-head">
          <div class="ask-question-text">{{ activeQuestion.question }}</div>
        </div>

        <div v-if="msg.askSubmitted || msg.status === 'success'" class="ask-answer-summary">
          <div v-for="selection in submittedSelections(activeQuestion)" :key="selectionKey(selection)" class="ask-answer-line">
            <span class="ask-answer-check">✓</span>
            <span>{{ selection.label }}</span>
            <span v-if="selection.recommended" class="ask-recommended">推荐</span>
          </div>
        </div>

        <div v-else class="ask-options">
          <label v-for="option in activeQuestion.options" :key="option.id" :class="['ask-option', { selected: isOptionSelected(activeQuestion.id, option.id), recommended: option.recommended }]">
            <input
              type="checkbox"
              :checked="isOptionSelected(activeQuestion.id, option.id)"
              :disabled="msg.askSubmitting"
              @change="toggleOption(activeQuestion.id, option.id)"
            />
            <span class="ask-option-copy">
              <span class="ask-option-title-row">
                <span class="ask-option-label">{{ option.label }}</span>
                <span v-if="option.recommended" class="ask-recommended">推荐</span>
              </span>
              <span class="ask-option-description">{{ option.description }}</span>
            </span>
          </label>

          <label :class="['ask-option', 'custom', { selected: answerState(activeQuestion.id).customSelected }]">
            <input
              type="checkbox"
              :checked="answerState(activeQuestion.id).customSelected"
              :disabled="msg.askSubmitting"
              @change="toggleCustom(activeQuestion.id)"
            />
            <span class="ask-option-copy">
              <span class="ask-option-label">自定义回答</span>
              <textarea
                v-if="answerState(activeQuestion.id).customSelected"
                v-model="answerState(activeQuestion.id).customText"
                class="ask-custom-input"
                rows="3"
                placeholder="输入你的回答"
                :disabled="msg.askSubmitting"
                @click.stop
              ></textarea>
            </span>
          </label>
        </div>
      </template>

      <div v-if="!msg.askSubmitted && msg.status !== 'success'" class="ask-actions">
        <span class="ask-progress">{{ answeredCount }}/{{ questions.length }} 已回答</span>
        <button class="ask-submit-btn" type="button" :disabled="!canSubmit || msg.askSubmitting || !msg.askReady" @click="submitAnswers">
          {{ msg.askSubmitting ? '提交中' : '提交回答' }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed, reactive, ref, watch } from 'vue';

const props = defineProps({
  msg: { type: Object, required: true },
});

const emit = defineEmits(['submit']);
const activeIndex = ref(0);
const answers = reactive({});
const questions = computed(() => Array.isArray(props.msg.askQuestions) ? props.msg.askQuestions : []);
const activeQuestion = computed(() => questions.value[activeIndex.value] || null);

watch(questions, (next) => {
  for (const question of next) answerState(question.id);
  if (activeIndex.value >= next.length) activeIndex.value = Math.max(0, next.length - 1);
}, { immediate: true, deep: true });

function answerState(questionId) {
  if (!answers[questionId]) {
    answers[questionId] = { selectedOptionIds: [], customSelected: false, customText: '' };
  }
  return answers[questionId];
}

function isOptionSelected(questionId, optionId) {
  return answerState(questionId).selectedOptionIds.includes(optionId);
}

function toggleOption(questionId, optionId) {
  const state = answerState(questionId);
  const index = state.selectedOptionIds.indexOf(optionId);
  if (index >= 0) state.selectedOptionIds.splice(index, 1);
  else state.selectedOptionIds.push(optionId);
}

function toggleCustom(questionId) {
  const state = answerState(questionId);
  state.customSelected = !state.customSelected;
  if (!state.customSelected) state.customText = '';
}

function isAnswered(question) {
  const state = answerState(question.id);
  return state.selectedOptionIds.length > 0 || (state.customSelected && state.customText.trim() !== '');
}

const answeredCount = computed(() => questions.value.filter(isAnswered).length);
const canSubmit = computed(() => questions.value.length > 0 && questions.value.every((question) => {
  const state = answerState(question.id);
  if (state.customSelected && !state.customText.trim()) return false;
  return state.selectedOptionIds.length > 0 || state.customSelected;
}));

const statusIcon = computed(() => {
  if (props.msg.status === 'success') return '✓';
  if (props.msg.status === 'error') return '✗';
  return '';
});

const statusVerb = computed(() => {
  if (props.msg.status === 'success') return 'Answered';
  if (props.msg.status === 'error') return props.msg.errorCode === 'E_ASK_CANCELLED' || props.msg.body === '提问已取消' ? 'Cancelled' : 'Failed';
  return 'Asking';
});

function submitAnswers() {
  if (!canSubmit.value) return;
  emit('submit', questions.value.map((question) => {
    const state = answerState(question.id);
    return {
      questionId: question.id,
      selectedOptionIds: [...state.selectedOptionIds],
      customText: state.customSelected ? state.customText.trim() : '',
    };
  }));
}

function submittedSelections(question) {
  const answer = (props.msg.askAnswers || []).find((item) => item.questionId === question.id);
  if (Array.isArray(answer?.selections)) return answer.selections;
  const state = answerState(question.id);
  const selections = question.options
    .filter((option) => state.selectedOptionIds.includes(option.id))
    .map((option) => ({ label: option.label, recommended: option.recommended, optionId: option.id }));
  if (state.customSelected && state.customText.trim()) selections.push({ label: state.customText.trim(), custom: true });
  return selections;
}

function selectionKey(selection) {
  return selection.optionId || `custom:${selection.label}`;
}
</script>

<style scoped>
.ask-content {
  margin: 6px 0 12px 20px;
  max-width: 720px;
}

.ask-tabs-row {
  display: grid;
  grid-template-columns: 30px minmax(0, 1fr) 30px;
  align-items: center;
  gap: 6px;
  min-height: 38px;
  padding: 2px 0 5px;
}

.ask-tabs {
  display: flex;
  justify-content: center;
  gap: 5px;
  min-width: 0;
}

.ask-tab,
.ask-nav-btn {
  display: inline-grid;
  place-items: center;
  width: 28px;
  height: 28px;
  padding: 0;
  border: 1px solid rgba(255, 255, 255, 0.09);
  border-radius: 4px;
  color: #858585;
  background: transparent;
  cursor: pointer;
}

.ask-tab.active {
  color: #e5e5e5;
  border-color: rgba(96, 165, 250, 0.48);
  background: rgba(96, 165, 250, 0.1);
}

.ask-tab.answered::after {
  content: '';
  position: absolute;
  width: 4px;
  height: 4px;
  margin-top: 19px;
  border-radius: 50%;
  background: #6fa47d;
}

.ask-tab {
  position: relative;
}

.ask-nav-btn:disabled {
  opacity: 0.28;
  cursor: default;
}

.ask-question-head {
  padding: 10px 0 8px;
}

.ask-question-text {
  color: #e5e5e5;
  font-size: 14px;
  line-height: 1.5;
  white-space: normal;
  overflow-wrap: anywhere;
}

.ask-options,
.ask-answer-summary {
  padding: 0 0 8px;
}

.ask-option {
  display: grid;
  grid-template-columns: 18px minmax(0, 1fr);
  gap: 9px;
  align-items: start;
  padding: 9px 4px;
  border-top: 1px solid rgba(255, 255, 255, 0.055);
  cursor: pointer;
}

.ask-option input {
  width: 15px;
  height: 15px;
  margin: 2px 0 0;
  accent-color: #5f8fc5;
}

.ask-option-copy {
  display: flex;
  flex-direction: column;
  gap: 3px;
  min-width: 0;
}

.ask-option-title-row {
  display: flex;
  align-items: center;
  gap: 7px;
  min-width: 0;
}

.ask-option-label {
  color: #d4d4d4;
  font-size: 13px;
  line-height: 1.4;
  overflow-wrap: anywhere;
}

.ask-option-description {
  color: #7f7f7f;
  font-size: 12px;
  line-height: 1.45;
  overflow-wrap: anywhere;
}

.ask-option.selected .ask-option-label {
  color: #f0f0f0;
}

.ask-recommended {
  flex-shrink: 0;
  padding: 1px 5px;
  border: 1px solid rgba(111, 164, 125, 0.35);
  border-radius: 3px;
  color: #88b794;
  font-size: 10px;
  line-height: 15px;
}

.ask-custom-input {
  box-sizing: border-box;
  width: 100%;
  min-height: 68px;
  margin-top: 5px;
  padding: 7px 8px;
  resize: vertical;
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: 4px;
  outline: none;
  color: #e5e5e5;
  background: rgba(0, 0, 0, 0.2);
  font: inherit;
  line-height: 1.45;
}

.ask-custom-input:focus {
  border-color: rgba(96, 165, 250, 0.45);
}

.ask-actions {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  min-height: 42px;
  padding: 7px 0 0;
  border-top: 1px solid rgba(255, 255, 255, 0.07);
}

.ask-progress {
  color: #737373;
  font-size: 12px;
}

.ask-submit-btn {
  min-height: 28px;
  padding: 4px 12px;
  border: 1px solid rgba(96, 165, 250, 0.38);
  border-radius: 4px;
  color: #b9d8fb;
  background: rgba(96, 165, 250, 0.09);
  cursor: pointer;
}

.ask-submit-btn:disabled {
  opacity: 0.35;
  cursor: default;
}

.ask-answer-line {
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 7px 0;
  border-top: 1px solid rgba(255, 255, 255, 0.055);
  color: #d4d4d4;
  font-size: 13px;
}

.ask-answer-check {
  color: #6fa47d;
}

.ask-closed-state {
  margin: 3px 0 8px 20px;
  color: #8a8a8a;
  font-size: 12px;
}

@media (max-width: 640px) {
  .ask-content,
  .ask-closed-state {
    margin-left: 0;
  }
}
</style>

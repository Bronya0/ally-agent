<template>
  <!-- Inline games page: rendered inside the main-area container
       (App.vue mode === 'games') instead of a modal card. Local
       play-against-model only. -->
  <div v-if="show" class="games-inline-panel">
    <header class="games-inline-header">
      <div class="games-inline-heading">
        <span class="games-inline-title">人机对战</span>
        <span class="games-inline-subtitle">{{ headerSubtitle }}</span>
      </div>
    </header>
    <div class="games-inline-body">
    <div class="game-layout">
      <aside class="game-sidebar">
        <section class="game-card">
          <div class="game-card-title">{{ aiVsAi ? '双模型对弈' : '执子模型' }}</div>
          <div class="game-model-row composer-info">
            <ModelMenu
              :models="props.models"
              :active-identity="modelAIdentity"
              :disabled="aiThinking"
              placement="right-start"
              :show-manage="false"
              :record-usage="false"
              @select="selectModelA"
            >
              <span class="info-model" style="cursor:pointer">{{ modelALabel }}</span>
            </ModelMenu>
            <n-dropdown trigger="click" placement="right-start" :options="reasoningEffortOptions" @select="(level) => onEffortSelect('a', level)">
              <span class="info-effort" :title="t('composer.effort.title')">
                <span class="info-effort-label">{{ effortLabel('a') }}</span>
                <span class="info-effort-caret">▾</span>
              </span>
            </n-dropdown>
          </div>
          <template v-if="aiVsAi">
            <div class="game-model-row composer-info">
              <ModelMenu
                :models="props.models"
                :active-identity="modelBIdentity"
                :disabled="aiThinking"
                placement="right-start"
                :show-manage="false"
                :record-usage="false"
                @select="selectModelB"
              >
                <span class="info-model" style="cursor:pointer">{{ modelBLabel }}</span>
              </ModelMenu>
              <n-dropdown trigger="click" placement="right-start" :options="reasoningEffortOptions" @select="(level) => onEffortSelect('b', level)">
                <span class="info-effort" :title="t('composer.effort.title')">
                  <span class="info-effort-label">{{ effortLabel('b') }}</span>
                  <span class="info-effort-caret">▾</span>
                </span>
              </n-dropdown>
            </div>
            <button class="game-pick-item" :disabled="aiThinking" @click="aiVsAi = false">改回与模型对弈</button>
          </template>
          <button v-else class="game-pick-item" :disabled="aiThinking" @click="aiVsAi = true">切换双模型对弈（AI vs AI）</button>
          <div v-if="aiUsage.calls" class="game-usage">
            <span>上轮 输入 {{ fmtTokens(aiUsage.lastPrompt) }} · 输出 {{ fmtTokens(aiUsage.lastCompletion) }}</span>
            <span>本局 {{ aiUsage.calls }} 次调用 · 累计 {{ fmtTokens(aiUsage.totalPrompt + aiUsage.totalCompletion) }} tokens</span>
          </div>
          <div class="game-btn-row">
            <n-button size="small" type="primary" class="game-main-btn" :loading="aiThinking" :disabled="!canStart" @click="startAIGame">{{ state ? '重新开始' : '开始对局' }}</n-button>
            <n-button v-if="aiThinking" size="small" secondary type="error" @click="cancelAICall">停止</n-button>
          </div>
        </section>
        <section class="game-card">
          <div class="game-card-title">棋牌</div>
          <div class="game-pick">
            <button
              v-for="option in gameOptions"
              :key="option.value"
              :class="['game-pick-item', { active: selectedGame === option.value }]"
              :disabled="aiThinking"
              @click="pickGame(option.value)"
            >{{ option.label }}</button>
          </div>
          <div class="game-hint">{{ modeHint }}</div>
          <n-button v-if="state && state.game === 'doudizhu' && state.phase === 'deal' && !aiVsAi && canDeal" size="small" block @click="act({ type: 'start' })">发牌</n-button>
          <n-button v-if="state && state.game === 'go' && !aiVsAi" size="small" block :disabled="!canActNow" @click="act({ type: 'pass' })">停一手</n-button>
        </section>
      </aside>
      <main class="game-board-wrap">
        <div v-if="!state" class="game-empty">
          <div class="game-empty-icon">♟</div>
          <div>选择模型后点击“开始对局”，与模型下一盘</div>
        </div>
        <template v-else-if="state.game === 'gomoku' || state.game === 'go'">
          <div class="board-frame">
            <div class="board-grid" :class="{ 'is-locked': boardLocked }" :style="gridStyle">
              <button v-for="(cell, index) in state.board" :key="index" :class="['board-cell', { occupied: !!cell }]" @click="place(index)">
                <span v-if="cell" :class="['stone', cell === 1 ? 'black' : 'white', { 'is-last': state.lastIndex === index }]"></span>
              </button>
            </div>
          </div>
          <div class="game-status" :class="{ mine: isMyTurn }">{{ turnText }}</div>
        </template>
        <template v-else-if="state.game === 'xiangqi'">
          <div class="board-frame tall">
            <div class="xiangqi-board" :class="{ 'is-locked': boardLocked }">
              <svg class="board-lines" viewBox="0 0 8 9" preserveAspectRatio="none" aria-hidden="true">
                <g stroke="currentColor" stroke-width="1" vector-effect="non-scaling-stroke" stroke-linecap="square">
                  <line v-for="n in 7" :key="`vt-${n}`" :x1="n" y1="0" :x2="n" y2="4" />
                  <line v-for="n in 7" :key="`vb-${n}`" :x1="n" y1="5" :x2="n" y2="9" />
                  <line x1="0" y1="0" x2="0" y2="9" />
                  <line x1="8" y1="0" x2="8" y2="9" />
                  <line v-for="n in 10" :key="`h-${n}`" x1="0" :y1="n - 1" x2="8" :y2="n - 1" />
                  <line x1="3" y1="0" x2="5" y2="2" />
                  <line x1="5" y1="0" x2="3" y2="2" />
                  <line x1="3" y1="7" x2="5" y2="9" />
                  <line x1="5" y1="7" x2="3" y2="9" />
                </g>
              </svg>
              <div class="river"><span>楚 河</span><span>汉 界</span></div>
              <button
                v-for="(piece, index) in flatXiangqi"
                :key="index"
                :class="['xiangqi-cell', { 'is-red': piece?.[0] === 'r', 'is-selected': !!selectedPiece && selectedPiece.x === index % 9 && selectedPiece.y === Math.floor(index / 9), 'is-hint': xiangqiHints.has(`${index % 9},${Math.floor(index / 9)}`) }]"
                @click="moveXiangqi(index)"
              >
                <span v-if="piece" class="xiangqi-piece">{{ xiangqiPieceLabel(piece) }}</span>
              </button>
            </div>
          </div>
          <div class="game-status" :class="{ mine: isMyTurn }">{{ turnText }}</div>
        </template>
        <template v-else>
          <div class="poker-table">
            <div class="game-status" :class="{ mine: isMyTurn }">{{ doudizhuStatus }}</div>
            <div class="cards-row">
              <button
                v-for="entry in myHand"
                :key="entry.index"
                :class="['playing-card', { selected: selectedCards.includes(entry.index), joker: entry.card >= 16, 'joker-big': entry.card === 17 }]"
                :data-label="cardLabel(entry.card)"
                @click="toggleCard(entry.index)"
              >{{ cardLabel(entry.card) }}</button>
            </div>
            <div v-if="state.phase === 'bid'" class="game-actions">
              <n-button size="small" type="primary" :disabled="!canActNow" @click="bid(1)">叫地主</n-button>
              <n-button size="small" :disabled="!canActNow" @click="bid(0)">不叫</n-button>
            </div>
            <div v-else-if="state.phase === 'play'" class="game-actions">
              <n-button size="small" type="primary" :disabled="!canActNow" @click="playCards">出牌</n-button>
              <n-button size="small" :disabled="!canActNow" @click="act({ type: 'pass' })">不要</n-button>
            </div>
          </div>
        </template>
      </main>
    </div>
    </div>
  </div>
</template>

<script setup>
import { computed, onUnmounted, ref } from 'vue';
import { NButton, NDropdown, useMessage } from 'naive-ui';
import ModelMenu from '../components/ModelMenu.vue';
import { GameAIAction, GameAIActionCancel } from '../../bindings/ally-dev/internal/app/app';
import { applyAction, createState, GAME_META, xiangqiPieceLabel } from './rules.mjs';
import { AI_ALT_ID, AI_PLAYER_ID, buildGameMessages, describeAction, legalXiangqiMoves, parseAIAction } from './ai.mjs';
import { modelConfigIdentity, modelSnapshotFrom, reasoningEffortLevels } from '../utils/modelConfigIO.mjs';
import { formatModelLabel } from '../utils/modelLabel.mjs';
import { reasoningEffortLabel, t } from '../i18n.mjs';

const props = defineProps({
  show: { type: Boolean, default: false },
  // Raw config.models presets; the shared ModelMenu consumes them directly so
  // the games selector is the exact same menu as the composer's.
  models: { type: Array, default: () => [] },
  defaultModel: { type: Object, default: null },
});
defineEmits(['close']);
const message = useMessage();
const selectedGame = ref('gomoku');
const state = ref(null);
const selectedCards = ref([]);
const selectedPiece = ref(null);
// AI vs AI: seats alternate between model A (even seats) and model B (odd
// seats); otherwise seat 0 is the human and every other seat is model A.
const aiVsAi = ref(false);
const modelAIdentity = ref('');
const modelBIdentity = ref('');
// Game-level effort overrides ('' follows the selected preset's value) so a
// quick “让它多想想” never mutates the saved config.
const effortA = ref('');
const effortB = ref('');
const aiThinking = ref(false);
const aiUsage = ref({ lastPrompt: 0, lastCompletion: 0, totalPrompt: 0, totalCompletion: 0, calls: 0 });
// Bounded move-notation memory (last 12 actions, human + AI alike) sent with
// every prompt so the model keeps continuity without re-sending old boards.
const aiHistory = ref([]);
let aiEpoch = 0;

const gameOptions = Object.entries(GAME_META).map(([value, item]) => ({ value, label: item.label }));

function snapshotFor(identity, effort, fallback) {
  const preset = (props.models || []).find((item) => modelConfigIdentity(item) === identity);
  const base = preset ? modelSnapshotFrom(preset) : fallback;
  if (!base) return null;
  return effort ? { ...base, reasoningEffort: effort } : base;
}
const modelASnapshot = computed(() => snapshotFor(modelAIdentity.value, effortA.value, props.defaultModel));
const modelBSnapshot = computed(() => snapshotFor(modelBIdentity.value, effortB.value, null));
function modelForSeat(seat) {
  if (!aiVsAi.value) return modelASnapshot.value; // human holds seat 0
  return seat % 2 === 0 ? modelASnapshot.value : modelBSnapshot.value;
}
const canStart = computed(() => !!modelForSeat(0)?.model && (!aiVsAi.value || !!modelBSnapshot.value?.model));
const modelALabel = computed(() => { const p = (props.models || []).find((m) => modelConfigIdentity(m) === modelAIdentity.value); return (p || props.defaultModel) ? formatModelLabel(p || props.defaultModel) : t('composer.models.empty'); });
const modelBLabel = computed(() => { const p = (props.models || []).find((m) => modelConfigIdentity(m) === modelBIdentity.value); return p ? formatModelLabel(p) : t('composer.models.empty'); });
function effortLabel(which) {
  const identity = which === 'a' ? modelAIdentity.value : modelBIdentity.value;
  const preset = (props.models || []).find((m) => modelConfigIdentity(m) === identity);
  const effort = (which === 'a' ? effortA.value : effortB.value) || preset?.reasoningEffort || 'auto';
  return reasoningEffortLabel(effort);
}
const reasoningEffortOptions = reasoningEffortLevels.map((level) => ({ label: reasoningEffortLabel(level), key: level }));
const playerIndex = computed(() => state.value?.players?.[0] === 'human' ? 0 : -1);
const canDeal = computed(() => !!state.value && state.value.phase === 'deal' && !aiThinking.value);
const canActNow = computed(() => !!state.value && state.value.winner == null && state.value.turn === playerIndex.value && !aiThinking.value && playerIndex.value >= 0);
const gridStyle = computed(() => ({ '--board-size': state.value?.size || 15 }));
const flatXiangqi = computed(() => state.value?.board?.flat() || []);
// Hand shown sorted by value (indices ride along so selection keeps mapping to
// the real hand array regardless of the deal order).
const myHand = computed(() => (state.value?.hands?.[playerIndex.value] || [])
  .map((card, index) => ({ card, index }))
  .sort((a, b) => a.card - b.card || a.index - b.index));
// Legal-destination dots for the selected xiangqi piece (engine-enumerated).
const xiangqiHints = computed(() => {
  if (!selectedPiece.value || state.value?.game !== 'xiangqi' || playerIndex.value < 0) return new Set();
  return new Set(legalXiangqiMoves(state.value, playerIndex.value)
    .filter((m) => m.fromX === selectedPiece.value.x && m.fromY === selectedPiece.value.y)
    .map((m) => `${m.toX},${m.toY}`));
});
function seatLabel(seat) { return aiVsAi.value ? (seat % 2 === 0 ? '模型A' : '模型B') : '模型'; }
const turnText = computed(() => {
  if (!state.value) return '';
  if (state.value.winner === -1) return '和棋（困毙）';
  if (state.value.winner != null) return aiVsAi.value ? `座位${state.value.winner + 1}（${seatLabel(state.value.winner)}）获胜` : state.value.winner === 0 ? '你赢了 🎉' : '模型赢了';
  if (aiVsAi.value) return `座位${state.value.turn + 1}（${seatLabel(state.value.turn)}）思考中…`;
  return state.value.turn === playerIndex.value ? '轮到你' : '模型思考中…';
});
const doudizhuStatus = computed(() => state.value?.phase === 'deal' ? '点击“发牌”开始' : state.value?.phase === 'bid' && !aiVsAi.value ? '叫地主阶段' : turnText.value);
const headerSubtitle = computed(() => `${GAME_META[selectedGame.value].label} · ${aiVsAi.value ? '双模型对弈' : '人机对战'}`);
const boardLocked = computed(() => !state.value || state.value.winner != null || state.value.turn !== playerIndex.value || aiThinking.value);
const isMyTurn = computed(() => !!state.value && state.value.winner == null && state.value.turn === playerIndex.value);
const modeHint = computed(() => aiVsAi.value ? '两个模型轮流执子对弈，你观战' : `${GAME_META[selectedGame.value].min} 人局 · 你执先手，其余座位由模型接管`);

onUnmounted(() => { aiEpoch++; });

function aiSeatCount(game) { return (GAME_META[game]?.min || 2) - 1; }
function isAISeat(id) { return id === AI_PLAYER_ID || id === AI_ALT_ID; }

function selectModelA(index) {
  const preset = (props.models || [])[index];
  if (preset) { modelAIdentity.value = modelConfigIdentity(preset); effortA.value = ''; }
}
function selectModelB(index) {
  const preset = (props.models || [])[index];
  if (preset) { modelBIdentity.value = modelConfigIdentity(preset); effortB.value = ''; }
}

function onEffortSelect(which, level) { if (which === 'a') effortA.value = level; else effortB.value = level; }

// Switching game (or re-clicking the active one) just starts fresh — the old
// board is abandoned, never a dead end of disabled buttons.
function pickGame(game) {
  if (aiThinking.value) return;
  aiEpoch++;
  selectedGame.value = game;
  state.value = null;
  selectedCards.value = [];
  selectedPiece.value = null;
  aiHistory.value = [];
  aiUsage.value = { lastPrompt: 0, lastCompletion: 0, totalPrompt: 0, totalCompletion: 0, calls: 0 };
}

function cancelAICall() {
  aiEpoch++;
  aiThinking.value = false;
  GameAIActionCancel().catch(() => {});
}

function fmtTokens(value) {
  if (!Number.isFinite(value) || value <= 0) return '0';
  if (value < 1000) return String(value);
  if (value < 10000) return `${(value / 1000).toFixed(2)}k`;
  if (value < 1000000) return `${(value / 1000).toFixed(1)}k`;
  return `${(value / 1000000).toFixed(2)}M`;
}

function startAIGame() {
  if (!canStart.value) { message.error('请先选择模型（可在设置中配置）'); return; }
  aiEpoch++;
  const seats = aiVsAi.value
    ? Array.from({ length: GAME_META[selectedGame.value].min }, (_, i) => (i % 2 === 0 ? AI_PLAYER_ID : AI_ALT_ID))
    : ['human', ...Array.from({ length: aiSeatCount(selectedGame.value) }, () => AI_PLAYER_ID)];
  state.value = createState(selectedGame.value, seats);
  selectedCards.value = [];
  selectedPiece.value = null;
  aiUsage.value = { lastPrompt: 0, lastCompletion: 0, totalPrompt: 0, totalCompletion: 0, calls: 0 };
  aiHistory.value = [];
  // AI-vs-AI has no human to press the deal button; deal automatically.
  if (aiVsAi.value && selectedGame.value === 'doudizhu') applyAndLog(0, { type: 'start' });
  maybeAIMove();
}

async function maybeAIMove() {
  if (!state.value || state.value.winner != null || aiThinking.value) return;
  if (!isAISeat(state.value.players[state.value.turn])) return;
  const epoch = ++aiEpoch;
  aiThinking.value = true;
  try {
    while (state.value && state.value.winner == null && isAISeat(state.value.players[state.value.turn]) && epoch === aiEpoch) {
      const ok = await aiTakeTurn(state.value.turn);
      if (!ok || epoch !== aiEpoch) break;
    }
  } finally {
    if (epoch === aiEpoch) aiThinking.value = false;
  }
}

function applyAndLog(seat, action) {
  const note = describeAction(state.value.game, state.value, seat, action);
  state.value = applyAction(state.value, seat, action);
  if (note) {
    aiHistory.value.push(note);
    if (aiHistory.value.length > 12) aiHistory.value.shift();
  }
}

async function aiTakeTurn(seat) {
  const epoch = aiEpoch;
  const failures = [];
  for (let attempt = 0; attempt < 3; attempt++) {
    if (!state.value || state.value.winner != null || epoch !== aiEpoch) return false;
    let result = null;
    try {
      const { system, user } = buildGameMessages(state.value, seat, failures, aiHistory.value);
      result = await GameAIAction(modelForSeat(seat), system, user);
    } catch (err) {
      // Cancelled (epoch bumped by cancelAICall/startAIGame/switch): the loop
      // is already stopped; don't surface the abort as an error toast.
      if (epoch !== aiEpoch) return false;
      message.error(`模型调用失败：${err?.message || err}`);
      return false;
    }
    if (result.promptTokens > 0 || result.completionTokens > 0) {
      aiUsage.value = {
        lastPrompt: result.promptTokens,
        lastCompletion: result.completionTokens,
        totalPrompt: aiUsage.value.totalPrompt + result.promptTokens,
        totalCompletion: aiUsage.value.totalCompletion + result.completionTokens,
        calls: aiUsage.value.calls + 1,
      };
    }
    const action = parseAIAction(result.reply, state.value.game);
    if (!action) { failures.push(`回复无法解析为 JSON：${String(result.reply).slice(0, 120)}`); continue; }
    try {
      applyAndLog(seat, action);
      return true;
    } catch (err) {
      failures.push(`${JSON.stringify(action)} → 规则引擎拒绝：${err?.message || '非法'}`);
    }
  }
  message.error('模型连续 3 次未给出合法走法，对局已暂停，可点击“重新开局”重试');
  return false;
}

function act(action) {
  if (!canActNow.value) { message.info(aiThinking.value ? '模型思考中，请稍候' : '当前无你的可操作回合'); return; }
  try { applyAndLog(playerIndex.value, action); } catch (err) { message.error(err?.message || '操作不合法'); return; }
  maybeAIMove();
}
function place(index) { const size = state.value.size; act({ type: 'place', x: index % size, y: Math.floor(index / size) }); }
function moveXiangqi(index) {
  if (!state.value || state.value.winner != null) return;
  const x = index % 9, y = Math.floor(index / 9), piece = state.value.board[y][x];
  const mine = playerIndex.value === 0 ? 'r' : 'b';
  if (!selectedPiece.value) {
    if (state.value.turn === playerIndex.value && piece && piece[0] === mine) selectedPiece.value = { x, y };
    return;
  }
  const from = selectedPiece.value;
  if (from.x === x && from.y === y) { selectedPiece.value = null; return; }
  if (piece && piece[0] === mine) { selectedPiece.value = { x, y }; return; }
  selectedPiece.value = null;
  act({ type: 'move', fromX: from.x, fromY: from.y, toX: x, toY: y });
}
function toggleCard(index) { const at = selectedCards.value.indexOf(index); if (at >= 0) selectedCards.value.splice(at, 1); else selectedCards.value.push(index); }
function playCards() { act({ type: 'play', cards: selectedCards.value.map((i) => state.value.hands[playerIndex.value][i]) }); selectedCards.value = []; }
function bid(value) { act({ type: 'bid', value }); }
function cardLabel(card) { return card === 16 ? '小王' : card === 17 ? '大王' : ({ 11: 'J', 12: 'Q', 13: 'K', 14: 'A', 15: '2' }[card] || String(card)); }
</script>

<style scoped>
/* Inline games page: fills the main-area container instead of a modal card. */
.games-inline-panel {
  display: flex;
  flex-direction: column;
  width: 100%;
  height: 100%;
  min-width: 0;
  min-height: 0;
  background: var(--game-panel-bg);
  overflow: hidden;
}

.games-inline-header {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 14px 22px 12px;
  border-bottom: 1px solid var(--game-header-border);
  flex-shrink: 0;
}
.games-inline-heading { display: flex; align-items: baseline; gap: 10px; min-width: 0; }
.games-inline-title { font-size: 18px; font-weight: 700; letter-spacing: 0.5px; color: var(--game-title-color); }
.games-inline-subtitle { color: var(--game-hint-color); font-size: 12px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.games-inline-badges { margin-left: auto; display: flex; gap: 8px; }
.game-btn-row { display: flex; gap: 8px; }
.game-main-btn { flex: 1; }
.xiangqi-cell.is-hint::after { content: ''; position: absolute; z-index: 1; inset: 38%; border-radius: 50%; background: color-mix(in srgb, #18a058 75%, transparent); box-shadow: 0 0 6px rgba(24, 160, 88, 0.6); pointer-events: none; }
.game-badge { padding: 3px 10px; border-radius: 999px; font-size: 11px; color: var(--game-hint-color); background: var(--game-panel-card-bg); border: 1px solid var(--game-panel-card-border); }
.game-badge.online { color: #3fbf7f; border-color: color-mix(in srgb, #3fbf7f 45%, transparent); background: color-mix(in srgb, #3fbf7f 12%, transparent); }

.games-inline-body { flex: 1; min-height: 0; display: flex; padding: 16px 22px 20px; overflow: auto; }
.game-layout { display: grid; grid-template-columns: 250px minmax(0, 1fr); gap: 18px; flex: 1; min-height: 0; }
.game-sidebar { display: flex; flex-direction: column; gap: 12px; border-right: 1px solid var(--game-sidebar-border); padding-right: 16px; overflow: auto; }

.game-card { display: flex; flex-direction: column; gap: 8px; padding: 12px; border-radius: 12px; background: var(--game-panel-card-bg); border: 1px solid var(--game-panel-card-border); }
.game-card-title { color: var(--game-section-title-color); font-size: 12px; font-weight: 600; letter-spacing: 0.4px; }
.game-hint, .game-connected { color: var(--game-hint-color); font-size: 12px; line-height: 1.5; }

.game-model-row { margin-top: 0; padding-top: 0; flex-wrap: wrap; white-space: normal; min-width: 0; }

.game-thinking-row { display: flex; align-items: center; gap: 8px; }

.game-usage { display: flex; flex-direction: column; gap: 2px; padding: 6px 8px; border-radius: 8px; background: color-mix(in srgb, var(--game-section-title-color) 7%, transparent); color: var(--game-hint-color); font-size: 11.5px; line-height: 1.4; font-variant-numeric: tabular-nums; }

.game-pick { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 6px; }
.game-pick-item { padding: 8px 4px; border-radius: 9px; border: 1px solid var(--game-panel-card-border); background: transparent; color: var(--game-section-title-color); font-family: inherit; font-size: 13px; cursor: pointer; transition: background 0.15s ease, border-color 0.15s ease, color 0.15s ease; }
.game-pick-item:hover:not(:disabled) { background: color-mix(in srgb, var(--game-section-title-color) 12%, transparent); }
.game-pick-item.active { color: var(--game-title-color); font-weight: 600; border-color: color-mix(in srgb, #18a058 60%, transparent); background: color-mix(in srgb, #18a058 14%, transparent); }
.game-pick-item:disabled { opacity: 0.55; cursor: not-allowed; }

.game-actions { display: flex; gap: 8px; flex-wrap: wrap; }

.game-board-wrap { display: flex; align-items: center; justify-content: center; flex-direction: column; gap: 14px; min-width: 0; }
.game-empty { display: flex; flex-direction: column; align-items: center; gap: 10px; color: var(--game-empty-color); font-size: 13px; text-align: center; }
.game-empty-icon { font-size: 42px; line-height: 1; opacity: 0.3; }

.game-status { padding: 5px 16px; border-radius: 999px; font-size: 12px; color: var(--game-hint-color); background: var(--game-status-bg); border: 1px solid var(--game-panel-card-border); }
.game-status.mine { color: #3fbf7f; border-color: color-mix(in srgb, #3fbf7f 40%, transparent); background: color-mix(in srgb, #3fbf7f 12%, transparent); }

/* ── Wooden board shell shared by grid games and xiangqi ── */
.board-frame { box-sizing: border-box; width: min(560px, 100%); padding: 10px; border-radius: 14px; background: var(--game-board-grid-border); box-shadow: 0 12px 30px rgba(0, 0, 0, 0.3); }
.board-frame.tall { width: min(480px, 100%); }

.board-grid {
  position: relative;
  display: grid;
  box-sizing: border-box;
  grid-template-columns: repeat(var(--board-size), 1fr);
  grid-template-rows: repeat(var(--board-size), 1fr);
  width: 100%;
  aspect-ratio: 1;
  /* Half-cell inset so every stone sits on a line intersection. */
  padding: calc(100% / (var(--board-size) + 2));
  border-radius: 6px;
  background: repeating-linear-gradient(96deg, rgba(60, 35, 10, 0.05) 0 2px, transparent 2px 7px), radial-gradient(120% 100% at 18% 0%, rgba(255, 255, 255, 0.16), transparent 62%), var(--game-board-grid-bg);
  box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--game-board-line) 60%, transparent), inset 0 0 26px rgba(60, 35, 10, 0.22);
}

.board-cell { position: relative; display: grid; place-items: center; border: 0; padding: 0; background: transparent; cursor: pointer; }
.board-cell::before { content: ''; position: absolute; left: 0; right: 0; top: 50%; height: 1px; transform: translateY(-50%); background: var(--game-board-line); }
.board-cell::after { content: ''; position: absolute; top: 0; bottom: 0; left: 50%; width: 1px; transform: translateX(-50%); background: var(--game-board-line); }
.board-grid:not(.is-locked) .board-cell:not(.occupied):hover { background: radial-gradient(circle at center, color-mix(in srgb, var(--game-board-line) 70%, transparent) 0 38%, transparent 40%); }
.board-grid.is-locked .board-cell { cursor: default; }

.stone { position: relative; z-index: 1; width: 84%; aspect-ratio: 1; border-radius: 50%; }
.stone.black { background: radial-gradient(circle at 33% 28%, #6d7684 0%, #2c313a 44%, #0b0d11 100%); box-shadow: 0 3px 6px rgba(0, 0, 0, 0.45), inset 0 -2px 4px rgba(0, 0, 0, 0.5), inset 0 2px 3px rgba(255, 255, 255, 0.16); }
.stone.white { background: radial-gradient(circle at 33% 28%, #ffffff 0%, #f2f3f6 46%, #c7ccd6 100%); box-shadow: 0 3px 6px rgba(0, 0, 0, 0.32), inset 0 -2px 4px rgba(90, 100, 120, 0.28), inset 0 2px 3px rgba(255, 255, 255, 0.95); }
.stone.is-last::after { content: ''; position: absolute; inset: 36%; border-radius: 50%; background: #ff7a45; box-shadow: 0 0 6px rgba(255, 122, 69, 0.85); }
.stone.white.is-last::after { background: #e5484d; box-shadow: 0 0 6px rgba(229, 72, 77, 0.8); }

/* ── Xiangqi ── */
.xiangqi-board {
  position: relative;
  display: grid;
  box-sizing: border-box;
  grid-template-columns: repeat(9, 1fr);
  grid-template-rows: repeat(10, 1fr);
  width: 100%;
  aspect-ratio: 9 / 10;
  /* Half-cell insets: horizontal = W/20, vertical = (10/9)W/22. */
  padding: 5.0505% 5%;
  border-radius: 6px;
  background: repeating-linear-gradient(96deg, rgba(60, 35, 10, 0.05) 0 2px, transparent 2px 7px), radial-gradient(120% 100% at 18% 0%, rgba(255, 255, 255, 0.16), transparent 62%), var(--game-xiangqi-bg);
  box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--game-board-line) 60%, transparent), inset 0 0 26px rgba(60, 35, 10, 0.22);
}
.board-lines { position: absolute; left: 5%; top: 4.5455%; width: 90%; height: 90.909%; color: var(--game-board-line); pointer-events: none; }
.river { position: absolute; left: 5%; right: 5%; top: 40.909%; height: 9.091%; display: flex; align-items: center; justify-content: space-between; padding: 0 12%; color: var(--game-board-line); font-size: clamp(10px, 1.5vw, 14px); letter-spacing: 0.5em; pointer-events: none; }

.xiangqi-cell { position: relative; display: grid; place-items: center; border: 0; padding: 0; background: transparent; cursor: pointer; }
.xiangqi-piece {
  display: grid;
  place-items: center;
  width: 88%;
  aspect-ratio: 1;
  border-radius: 50%;
  border: 1px solid rgba(120, 92, 55, 0.55);
  background: radial-gradient(circle at 34% 28%, #fffdf6 0%, #f1e2c4 58%, #d9c49e 100%);
  color: #1d2634;
  font-weight: 700;
  font-size: clamp(11px, 1.6vw, 20px);
  box-shadow: 0 2px 4px rgba(0, 0, 0, 0.34), inset 0 -2px 3px rgba(120, 90, 50, 0.32), inset 0 2px 3px rgba(255, 255, 255, 0.9);
  transition: box-shadow 0.15s ease, transform 0.15s ease;
}
.xiangqi-cell.is-red .xiangqi-piece { color: #b23528; }
.xiangqi-board:not(.is-locked) .xiangqi-cell:hover .xiangqi-piece { transform: translateY(-2px); }
.xiangqi-board.is-locked .xiangqi-cell { cursor: default; }
.xiangqi-cell.is-selected .xiangqi-piece { box-shadow: 0 0 0 2px #18a058, 0 6px 12px rgba(24, 160, 88, 0.4); }

/* ── Doudizhu ── */
.poker-table { display: grid; gap: 20px; width: 100%; max-width: 640px; padding: 20px 18px; border-radius: 14px; background: var(--game-panel-card-bg); border: 1px solid var(--game-panel-card-border); }
.cards-row { display: flex; flex-wrap: wrap; align-items: flex-end; justify-content: center; gap: 6px; min-height: 78px; }
.playing-card {
  position: relative;
  display: grid;
  place-items: center;
  width: 46px;
  height: 66px;
  padding: 0;
  border-radius: 7px;
  border: 1px solid rgba(20, 24, 32, 0.16);
  background: linear-gradient(165deg, #ffffff 0%, #eceef3 100%);
  color: #1d2330;
  font-family: inherit;
  font-size: 19px;
  font-weight: 700;
  cursor: pointer;
  user-select: none;
  box-shadow: 0 2px 6px rgba(0, 0, 0, 0.3);
  transition: transform 0.12s ease, box-shadow 0.12s ease, border-color 0.12s ease;
}
.playing-card::before { content: attr(data-label); position: absolute; top: 4px; left: 5px; font-size: 10px; font-weight: 700; opacity: 0.55; }
.playing-card:hover { transform: translateY(-7px); }
.playing-card.selected { transform: translateY(-14px); border-color: #18a058; box-shadow: 0 0 0 2px color-mix(in srgb, #18a058 55%, transparent), 0 10px 18px rgba(0, 0, 0, 0.35); }
.playing-card.joker { font-size: 12px; letter-spacing: 0.5px; }
.playing-card.joker::before { content: none; }
.playing-card.joker-big { color: #c0392b; }

@media (max-width: 680px) {
  .game-layout { grid-template-columns: 1fr; }
  .game-sidebar { border-right: 0; border-bottom: 1px solid var(--game-sidebar-border); padding: 0 0 14px; }
}
</style>

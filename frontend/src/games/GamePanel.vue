<template>
  <!-- Inline games page: rendered inside the main-area container
       (App.vue mode === 'games') instead of a modal card. -->
  <div v-if="show" class="games-inline-panel">
    <header class="games-inline-header">
      <div class="games-inline-heading">
        <span class="games-inline-title">协作休息区</span>
        <span class="games-inline-subtitle">{{ headerSubtitle }}</span>
      </div>
      <div class="games-inline-badges">
        <span class="game-badge" :class="{ online: connected }">{{ connected ? `已连接 ${peers.length} 人` : '未联机' }}</span>
      </div>
    </header>
    <div class="games-inline-body">
    <div class="game-layout">
      <aside class="game-sidebar">
        <section class="game-card">
          <div class="game-card-title">联机房间</div>
          <div class="game-hint">本机 IP：{{ localIPs.join('、') || '未发现内网 IPv4' }}</div>
          <n-select
            v-model:value="selectedHostIP"
            size="small"
            :options="hostIPOptions"
            :disabled="!localIPs.length"
            placeholder="选择给队友连接的本机 IP"
          />
          <div v-if="localIPs.length > 1" class="game-hint">请选择与队友处于同一内网的地址，避免使用 VPN 或虚拟网卡地址。</div>
          <div class="game-hint">房主点击“启动房间”后，把生成的邀请信息整行发给队友；队友完整粘贴后点击“加入房间”。</div>
          <div class="game-hint">服务监听 TCP 端口 {{ GAME_SERVER_PORT }}；若队友始终连不上，请在系统防火墙中放行该端口。</div>
          <n-input v-model:value="invite" size="small" placeholder="例如：ALLY-GAME-1|192.168.1.8|39877|..." :disabled="connected" />
          <div class="game-actions">
            <n-button size="small" type="primary" :loading="working" :disabled="connected" @click="host">启动房间</n-button>
            <n-button size="small" :loading="working" :disabled="connected || !invite" @click="join">加入房间</n-button>
          </div>
          <div v-if="serverInfo.running" class="game-invite">
            <div>服务端口：{{ serverInfo.port }}</div>
            <div>房间邀请信息（整行复制给队友）</div>
            <code>{{ inviteText }}</code>
            <n-button size="tiny" secondary @click="copyInvite">复制邀请信息</n-button>
          </div>
          <div v-if="connected" class="game-connected">已连接，玩家 {{ peers.length }}/4</div>
          <n-button v-if="connected || serverInfo.running" size="small" block type="error" secondary :loading="working" @click="isHost || serverInfo.running ? closeRoom() : leaveRoom()">
            {{ isHost || serverInfo.running ? '关闭房间' : '离开房间' }}
          </n-button>
          <div v-if="errorText" class="game-error">{{ errorText }}</div>
        </section>
        <section class="game-card">
          <div class="game-card-title">对局</div>
          <div class="game-pick">
            <button
              v-for="option in gameOptions"
              :key="option.value"
              :class="['game-pick-item', { active: selectedGame === option.value }]"
              :disabled="!!state"
              @click="selectedGame = option.value"
            >{{ option.label }}</button>
          </div>
          <div class="game-hint">{{ GAME_META[selectedGame].min }} 人开局 · 房主负责开局与发牌</div>
          <n-button v-if="state && state.game === 'doudizhu' && state.phase === 'deal' && isHost" size="small" block @click="act({ type: 'start' })">发牌</n-button>
          <n-button v-if="state && state.game === 'go'" size="small" block :disabled="playerIndex !== state.turn" @click="act({ type: 'pass' })">停一手</n-button>
          <n-button v-if="state && isHost" size="small" block secondary @click="resetGame">重新开始</n-button>
        </section>
      </aside>
      <main class="game-board-wrap">
        <div v-if="!state" class="game-empty">
          <div class="game-empty-icon">♟</div>
          <div>{{ connected ? '等待玩家加入（需要 ' + GAME_META[selectedGame].min + ' 人开局）…' : '启动或加入房间后即可开局' }}</div>
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
                :class="['xiangqi-cell', { 'is-red': piece?.[0] === 'r', 'is-selected': !!selectedPiece && selectedPiece.x === index % 9 && selectedPiece.y === Math.floor(index / 9) }]"
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
                v-for="(card, index) in myHand"
                :key="`${card}-${index}`"
                :class="['playing-card', { selected: selectedCards.includes(index), joker: card >= 16, 'joker-big': card === 17 }]"
                :data-label="cardLabel(card)"
                @click="toggleCard(index)"
              >{{ cardLabel(card) }}</button>
            </div>
            <div v-if="state.phase === 'bid'" class="game-actions">
              <n-button size="small" type="primary" :disabled="playerIndex !== state.turn" @click="bid(1)">叫地主</n-button>
              <n-button size="small" :disabled="playerIndex !== state.turn" @click="bid(0)">不叫</n-button>
            </div>
            <div v-else-if="state.phase === 'play'" class="game-actions">
              <n-button size="small" type="primary" :disabled="playerIndex !== state.turn" @click="playCards">出牌</n-button>
              <n-button size="small" :disabled="playerIndex !== state.turn" @click="act({ type: 'pass' })">不要</n-button>
            </div>
          </div>
        </template>
      </main>
    </div>
    </div>
  </div>
</template>

<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { useMessage } from 'naive-ui';
import { GetNetworkInfo, StartServer, StopServer } from '../../bindings/ally-dev/internal/game/service';
import { applyAction, createState, GAME_META, xiangqiPieceLabel } from './rules.mjs';
import { buildInvite, GameConnection, parseInvite } from './connection.mjs';

const props = defineProps({ show: { type: Boolean, default: false } });
defineEmits(['close']);
const message = useMessage();
const GAME_SERVER_PORT = 51873;
const invite = ref('');
const localIPs = ref([]);
const working = ref(false);
const errorText = ref('');
const selectedHostIP = ref('');
const selectedGame = ref('gomoku');
const connection = ref(null);
const peerId = ref('');
const hostId = ref('');
const peers = ref([]);
const state = ref(null);
const selectedCards = ref([]);
const selectedPiece = ref(null);
const serverInfo = ref({ running: false, addresses: [] });

const gameOptions = Object.entries(GAME_META).map(([value, item]) => ({ value, label: item.label }));
const connected = computed(() => !!connection.value && !!peerId.value);
const isHost = computed(() => peerId.value && peerId.value === hostId.value);
const hostIPOptions = computed(() => localIPs.value.map((ip) => ({ label: ip, value: ip })));
const inviteText = computed(() => serverInfo.value.running && selectedHostIP.value ? buildInvite({ host: selectedHostIP.value, port: serverInfo.value.port, roomId: serverInfo.value.roomId, secret: serverInfo.value.secret }) : '');
const playerIndex = computed(() => state.value?.players?.indexOf(peerId.value) ?? -1);
const gridStyle = computed(() => ({ '--board-size': state.value?.size || 15 }));
const flatXiangqi = computed(() => state.value?.board?.flat() || []);
const myHand = computed(() => state.value?.hands?.[playerIndex.value] || []);
const turnText = computed(() => state.value?.winner === -1 ? '和棋（困毙）' : state.value?.winner != null ? `玩家 ${state.value.winner + 1} 获胜` : state.value?.turn === playerIndex.value ? '轮到你' : '等待对手');
const doudizhuStatus = computed(() => state.value?.phase === 'deal' ? (isHost.value ? '房主可以发牌' : '等待房主发牌') : state.value?.phase === 'bid' ? '叫地主阶段' : turnText.value);
const headerSubtitle = computed(() => connected.value ? `${GAME_META[selectedGame.value].label} · ${peers.value.length} 人在房间` : '本地棋牌 · 内网联机对战');
const boardLocked = computed(() => !state.value || state.value.winner != null || state.value.turn !== playerIndex.value);
const isMyTurn = computed(() => !!state.value && state.value.winner == null && state.value.turn === playerIndex.value);

onMounted(async () => {
  try {
    const info = await GetNetworkInfo();
    localIPs.value = info.addresses || [];
    if (localIPs.value.length === 1) selectedHostIP.value = localIPs.value[0];
  } catch {}
});
onUnmounted(() => { connection.value?.close(); if (serverInfo.value.running) StopServer().catch(() => {}); });

function resetRoomState() {
  connection.value = null;
  peerId.value = '';
  hostId.value = '';
  peers.value = [];
  state.value = null;
  selectedCards.value = [];
  selectedPiece.value = null;
  serverInfo.value = { running: false, addresses: localIPs.value };
}
async function closeRoom() {
  if (!isHost.value && !serverInfo.value.running) return;
  working.value = true;
  try {
    connection.value?.close();
    await StopServer();
    resetRoomState();
  } catch (err) {
    errorText.value = err?.message || '关闭房间失败';
  } finally {
    working.value = false;
  }
}
function leaveRoom() {
  if (isHost.value) return;
  connection.value?.close();
  resetRoomState();
}
async function host() {
  working.value = true; errorText.value = '';
  let started = false;
  try {
    if (!selectedHostIP.value) throw new Error('请先选择给队友连接的本机 IP');
    serverInfo.value = await StartServer({ port: GAME_SERVER_PORT, address: selectedHostIP.value });
    started = true;
    if (!serverInfo.value.addresses?.includes(selectedHostIP.value)) throw new Error('所选本机 IP 已不可用，请重新选择');
    await connect({ host: selectedHostIP.value, port: serverInfo.value.port, roomId: serverInfo.value.roomId, secret: serverInfo.value.secret });
  }
  catch (err) {
    if (started) {
      await StopServer().catch(() => {});
      resetRoomState();
    }
    errorText.value = err?.message || '启动失败';
  }
  finally { working.value = false; }
}
async function join() { working.value = true; errorText.value = ''; try { await connect(parseInvite(invite.value)); } catch (err) { errorText.value = err?.message || '加入失败'; } finally { working.value = false; } }
async function connect(info) {
  const c = new GameConnection({ name: `玩家-${Math.random().toString(36).slice(2, 5)}`, onReady: (v) => { peerId.value = v.peerId; hostId.value = v.hostId; ensureState(); }, onPeers: (list) => { peers.value = list; ensureState(); }, onClose: () => { errorText.value = '连接已断开'; }, onError: (text) => { errorText.value = text; }, onMessage: handleMessage });
  await c.connect(info); connection.value = c;
}
function orderedPlayerIDs() { return [hostId.value, ...peers.value.map((p) => p.id).filter((id) => id !== hostId.value)].slice(0, GAME_META[selectedGame.value].max); }
function ensureState() { if (isHost.value && state.value) { syncState(); return; } if (isHost.value && !state.value && peers.value.length >= GAME_META[selectedGame.value].min) { state.value = createState(selectedGame.value, orderedPlayerIDs()); syncState(); } }
function resetGame() { if (!isHost.value) return; state.value = createState(selectedGame.value, orderedPlayerIDs()); syncState(); }
async function act(action) { if (playerIndex.value < 0) { message.info('当前为观战状态'); return; } try { if (isHost.value) { state.value = applyAction(state.value, playerIndex.value, action); await syncState(); } else { await connection.value.send('action', action, hostId.value); } } catch (err) { message.error(err?.message || '操作不合法'); } }
function stateFor(viewerID) { const copy = JSON.parse(JSON.stringify(state.value)); if (copy.game === 'doudizhu' && Array.isArray(copy.hands)) { const viewer = copy.players.indexOf(viewerID); copy.hands = copy.hands.map((hand, index) => index === viewer ? hand : Array(hand.length).fill(null)); } return copy; }
async function syncState(to = '') { if (!isHost.value || !connection.value) return; const targets = to ? peers.value.filter((p) => p.id === to) : peers.value; await Promise.all(targets.filter((p) => p.id !== peerId.value).map((p) => connection.value.send('sync', stateFor(p.id), p.id))); }
async function handleMessage(msg) { if (msg.type === 'sync') { if (msg.from !== hostId.value || isHost.value || !msg.data?.game) return; state.value = msg.data; selectedCards.value = []; return; } if (msg.type === 'action' && isHost.value) { try { const index = state.value?.players?.indexOf(msg.from) ?? -1; if (index < 0) return; state.value = applyAction(state.value, index, msg.data); await syncState(); } catch {} } }
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
function playCards() { act({ type: 'play', cards: selectedCards.value.map((i) => myHand.value[i]) }); selectedCards.value = []; }
function bid(value) { act({ type: 'bid', value }); }
function cardLabel(card) { return card === 16 ? '小王' : card === 17 ? '大王' : ({ 11: 'J', 12: 'Q', 13: 'K', 14: 'A', 15: '2' }[card] || String(card)); }
async function copyInvite() { try { await navigator.clipboard.writeText(inviteText.value); message.success('已复制'); } catch { message.error('复制失败'); } }
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
.game-badge { padding: 3px 10px; border-radius: 999px; font-size: 11px; color: var(--game-hint-color); background: var(--game-panel-card-bg); border: 1px solid var(--game-panel-card-border); }
.game-badge.online { color: #3fbf7f; border-color: color-mix(in srgb, #3fbf7f 45%, transparent); background: color-mix(in srgb, #3fbf7f 12%, transparent); }

.games-inline-body { flex: 1; min-height: 0; display: flex; padding: 16px 22px 20px; overflow: auto; }
.game-layout { display: grid; grid-template-columns: 250px minmax(0, 1fr); gap: 18px; flex: 1; min-height: 0; }
.game-sidebar { display: flex; flex-direction: column; gap: 12px; border-right: 1px solid var(--game-sidebar-border); padding-right: 16px; overflow: auto; }

.game-card { display: flex; flex-direction: column; gap: 8px; padding: 12px; border-radius: 12px; background: var(--game-panel-card-bg); border: 1px solid var(--game-panel-card-border); }
.game-card-title { color: var(--game-section-title-color); font-size: 12px; font-weight: 600; letter-spacing: 0.4px; }
.game-hint, .game-connected { color: var(--game-hint-color); font-size: 12px; line-height: 1.5; }

.game-pick { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 6px; }
.game-pick-item { padding: 8px 4px; border-radius: 9px; border: 1px solid var(--game-panel-card-border); background: transparent; color: var(--game-section-title-color); font-family: inherit; font-size: 13px; cursor: pointer; transition: background 0.15s ease, border-color 0.15s ease, color 0.15s ease; }
.game-pick-item:hover:not(:disabled) { background: color-mix(in srgb, var(--game-section-title-color) 12%, transparent); }
.game-pick-item.active { color: var(--game-title-color); font-weight: 600; border-color: color-mix(in srgb, #18a058 60%, transparent); background: color-mix(in srgb, #18a058 14%, transparent); }
.game-pick-item:disabled { opacity: 0.55; cursor: not-allowed; }

.game-actions { display: flex; gap: 8px; flex-wrap: wrap; }
.game-invite { display: grid; gap: 6px; color: var(--game-hint-color); font-size: 11px; }
.game-invite code { word-break: break-all; color: var(--game-invite-code-color); background: var(--game-invite-code-bg); padding: 6px 8px; border-radius: 6px; }
.game-error { color: #e88989; font-size: 12px; }

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

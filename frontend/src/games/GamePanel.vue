<template>
  <n-modal :show="show" preset="card" title="协作休息区" class="game-modal" :mask-closable="false" @close="hide">
    <div class="game-layout">
      <aside class="game-sidebar">
        <div class="game-section-title">联机</div>
        <div class="game-hint">本机 IP：{{ localIPs.join('、') || '未发现内网 IPv4' }}</div>
        <div class="game-hint">房主点击“启动房间”后，把生成的整行邀请信息发给队友；队友完整粘贴到这里再点击“加入房间”。</div>
        <n-input v-model:value="invite" size="small" placeholder="例如：ALLY-GAME-1|192.168.1.8|39877|..." :disabled="connected" />
        <div class="game-actions">
          <n-button size="small" type="primary" :loading="working" :disabled="connected" @click="host">启动房间</n-button>
          <n-button size="small" :loading="working" :disabled="connected || !invite" @click="join">加入房间</n-button>
        </div>
        <div v-if="serverInfo.running" class="game-invite">
          <div>房间邀请信息（整行复制给队友）</div>
          <code>{{ inviteText }}</code>
          <n-button size="tiny" secondary @click="copyInvite">复制邀请信息</n-button>
        </div>
        <div v-if="connected" class="game-connected">已连接，玩家 {{ peers.length }}/4</div>
        <n-button v-if="connected || serverInfo.running" size="small" block type="error" secondary :loading="working" @click="isHost || serverInfo.running ? closeRoom() : leaveRoom()">
          {{ isHost || serverInfo.running ? '关闭房间' : '离开房间' }}
        </n-button>
        <div v-if="errorText" class="game-error">{{ errorText }}</div>
        <div class="game-section-title">游戏</div>
        <n-select v-model:value="selectedGame" :options="gameOptions" size="small" :disabled="!!state" />
        <n-button v-if="state && selectedGame === 'doudizhu' && state.phase === 'deal' && isHost" size="small" block @click="act({ type: 'start' })">发牌</n-button>
        <n-button v-if="state && selectedGame === 'go'" size="small" block @click="act({ type: 'pass' })">停一手</n-button>
        <n-button v-if="state && isHost" size="small" block secondary @click="resetGame">重新开始</n-button>
      </aside>
      <main class="game-board-wrap">
        <div v-if="!state" class="game-empty">启动或加入房间后选择一个游戏</div>
        <template v-else-if="selectedGame === 'gomoku' || selectedGame === 'go'">
          <div class="board-grid" :style="gridStyle">
            <button v-for="(cell, index) in state.board" :key="index" class="board-cell" @click="place(index)">
              <span v-if="cell" :class="['stone', cell === 1 ? 'black' : 'white']"></span>
            </button>
          </div>
          <div class="game-status">{{ turnText }}</div>
        </template>
        <template v-else-if="selectedGame === 'xiangqi'">
          <div class="xiangqi-board">
            <button v-for="(piece, index) in flatXiangqi" :key="index" class="xiangqi-cell" @click="moveXiangqi(index)">{{ xiangqiPieceLabel(piece) }}</button>
          </div>
          <div class="game-status">{{ turnText }}</div>
        </template>
        <template v-else>
          <div class="poker-table">
            <div class="game-status">{{ doudizhuStatus }}</div>
            <div class="cards-row"><button v-for="(card, index) in myHand" :key="`${card}-${index}`" :class="['playing-card', { selected: selectedCards.includes(index) }]" @click="toggleCard(index)">{{ cardLabel(card) }}</button></div>
            <div v-if="state.phase === 'bid'" class="game-actions"><n-button size="small" type="primary" @click="bid(1)">叫地主</n-button><n-button size="small" @click="bid(0)">不叫</n-button></div>
            <div v-else-if="state.phase === 'play'" class="game-actions"><n-button size="small" type="primary" @click="playCards">出牌</n-button><n-button size="small" @click="act({ type: 'pass' })">不要</n-button></div>
          </div>
        </template>
      </main>
    </div>
  </n-modal>
</template>

<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { useMessage } from 'naive-ui';
import { GetNetworkInfo, StartServer, StopServer } from '../../bindings/ally-dev/internal/game/service';
import { applyAction, createState, GAME_META, xiangqiPieceLabel } from './rules.mjs';
import { buildInvite, GameConnection, parseInvite } from './connection.mjs';

const props = defineProps({ show: { type: Boolean, default: false } });
const emit = defineEmits(['close']);
const message = useMessage();
const invite = ref('');
const localIPs = ref([]);
const working = ref(false);
const errorText = ref('');
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
const inviteText = computed(() => serverInfo.value.running && serverInfo.value.addresses?.[0] ? buildInvite({ host: serverInfo.value.addresses[0], port: serverInfo.value.port, roomId: serverInfo.value.roomId, secret: serverInfo.value.secret }) : '');
const playerIndex = computed(() => state.value?.players?.indexOf(peerId.value) ?? -1);
const gridStyle = computed(() => ({ '--board-size': state.value?.size || 15 }));
const flatXiangqi = computed(() => state.value?.board?.flat() || []);
const myHand = computed(() => state.value?.hands?.[playerIndex.value] || []);
const turnText = computed(() => state.value?.winner != null ? `玩家 ${state.value.winner + 1} 获胜` : state.value?.turn === playerIndex.value ? '轮到你' : '等待对手');
const doudizhuStatus = computed(() => state.value?.phase === 'deal' ? (isHost.value ? '房主可以发牌' : '等待房主发牌') : state.value?.phase === 'bid' ? '叫地主阶段' : turnText.value);

onMounted(async () => { try { const info = await GetNetworkInfo(); localIPs.value = info.addresses || []; } catch {} });
onUnmounted(() => { connection.value?.close(); if (serverInfo.value.running) StopServer().catch(() => {}); });

function hide() { emit('close'); }
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
  try { serverInfo.value = await StartServer({ port: 0 }); if (!serverInfo.value.addresses?.length) throw new Error('未找到可用的内网 IPv4'); await connect({ host: serverInfo.value.addresses[0], port: serverInfo.value.port, roomId: serverInfo.value.roomId, secret: serverInfo.value.secret }); }
  catch (err) { errorText.value = err?.message || '启动失败'; }
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
async function act(action) { try { if (playerIndex.value < 0) throw new Error('当前为观战状态'); if (isHost.value) { state.value = applyAction(state.value, playerIndex.value, action); await syncState(); } else { await connection.value.send('action', action, hostId.value); } } catch (err) { message.error(err?.message || '操作不合法'); } }
function stateFor(viewerID) { const copy = JSON.parse(JSON.stringify(state.value)); if (copy.game === 'doudizhu' && Array.isArray(copy.hands)) { const viewer = copy.players.indexOf(viewerID); copy.hands = copy.hands.map((hand, index) => index === viewer ? hand : Array(hand.length).fill(null)); } return copy; }
async function syncState(to = '') { if (!isHost.value || !connection.value) return; const targets = to ? peers.value.filter((p) => p.id === to) : peers.value; await Promise.all(targets.filter((p) => p.id !== peerId.value).map((p) => connection.value.send('sync', stateFor(p.id), p.id))); }
async function handleMessage(msg) { if (msg.type === 'sync') { if (msg.from !== hostId.value || isHost.value || !msg.data?.game) return; state.value = msg.data; selectedGame.value = msg.data.game; selectedCards.value = []; return; } if (msg.type === 'action' && isHost.value) { try { const index = state.value?.players?.indexOf(msg.from) ?? -1; if (index < 0) return; state.value = applyAction(state.value, index, msg.data); await syncState(); } catch {} } }
function place(index) { const size = state.value.size; act({ type: 'place', x: index % size, y: Math.floor(index / size) }); }
function moveXiangqi(index) { const x = index % 9, y = Math.floor(index / 9); if (!selectedPiece.value) { if (state.value.board[y][x]) selectedPiece.value = { x, y }; return; } const from = selectedPiece.value; selectedPiece.value = null; act({ type: 'move', fromX: from.x, fromY: from.y, toX: x, toY: y }); }
function toggleCard(index) { const at = selectedCards.value.indexOf(index); if (at >= 0) selectedCards.value.splice(at, 1); else selectedCards.value.push(index); }
function playCards() { act({ type: 'play', cards: selectedCards.value.map((i) => myHand.value[i]) }); selectedCards.value = []; }
function bid(value) { act({ type: 'bid', value }); }
function cardLabel(card) { return card === 16 ? '小王' : card === 17 ? '大王' : ({ 11: 'J', 12: 'Q', 13: 'K', 14: 'A', 15: '2' }[card] || String(card)); }
async function copyInvite() { try { await navigator.clipboard.writeText(inviteText.value); message.success('已复制'); } catch { message.error('复制失败'); } }
</script>

<style scoped>
.game-modal { width: min(900px, 94vw); }
.game-layout { display: grid; grid-template-columns: 220px minmax(0, 1fr); gap: 18px; min-height: 500px; }
.game-sidebar { display: flex; flex-direction: column; gap: 9px; border-right: 1px solid #2b2b2b; padding-right: 14px; }
.game-section-title { color: #ddd; font-size: 12px; font-weight: 600; margin-top: 4px; }
.game-hint, .game-connected, .game-status { color: #999; font-size: 12px; line-height: 1.5; }
.game-hint + .game-hint { margin-top: -4px; }
.game-actions { display: flex; gap: 8px; flex-wrap: wrap; }
.game-invite { display: grid; gap: 6px; color: #aaa; font-size: 11px; }
.game-invite code { word-break: break-all; color: #ddd; background: #181818; padding: 6px; }
.game-error { color: #e88989; font-size: 12px; }
.game-board-wrap { display: flex; align-items: center; justify-content: center; flex-direction: column; gap: 16px; min-width: 0; }
.game-empty { color: #777; font-size: 13px; }
.board-grid { display: grid; grid-template-columns: repeat(var(--board-size), minmax(16px, 1fr)); width: min(520px, 70vw); aspect-ratio: 1; background: #bca16a; padding: 8px; gap: 1px; }
.board-cell { border: 0; background: rgba(255,255,255,.08); padding: 0; display: grid; place-items: center; cursor: pointer; }
.stone { width: 78%; aspect-ratio: 1; border-radius: 50%; box-shadow: 0 1px 3px #0008; }.stone.black { background: #1c1c1c; }.stone.white { background: #eee; }
.xiangqi-board { display: grid; grid-template-columns: repeat(9, minmax(26px, 1fr)); width: min(520px, 75vw); aspect-ratio: 9 / 10; background: #c29a62; padding: 6px; gap: 1px; }
.xiangqi-cell { border: 1px solid #805c35; background: transparent; color: #351f10; font-size: clamp(16px, 3vw, 28px); cursor: pointer; }
.poker-table { width: 100%; display: grid; gap: 20px; }.cards-row { display: flex; flex-wrap: wrap; justify-content: center; gap: 5px; }.playing-card { min-width: 34px; height: 52px; background: #f5f5f5; color: #222; border: 1px solid #aaa; border-radius: 3px; cursor: pointer; }.playing-card.selected { transform: translateY(-8px); border-color: #18a058; }
@media (max-width: 680px) { .game-layout { grid-template-columns: 1fr; }.game-sidebar { border-right: 0; border-bottom: 1px solid #2b2b2b; padding: 0 0 12px; } }
</style>

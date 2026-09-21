// Text protocol between the games panel and the model. The model never sees
// images: every game state is serialized into a coordinate-annotated text
// board (or a hand description for doudizhu), and the model must answer with
// a single JSON action using the exact same schema rules.mjs's applyAction
// accepts. Illegal replies are fed back for a bounded number of retries —
// rules.mjs stays the single arbiter, so the model can never make an illegal
// move. All coordinates in prompts are 1-based; this module converts them to
// the 0-based indices applyAction expects.

export const AI_PLAYER_ID = 'ai';
// Second AI identity for AI-vs-AI games (odd seats). buildGameMessages only
// cares about the seat number, so both ids serialize identically.
export const AI_ALT_ID = 'ai2';

import { applyAction, xiangqiPieceLabel } from './rules.mjs';

const CARD_NAMES = { 11: 'J', 12: 'Q', 13: 'K', 14: 'A', 15: '2', 16: '小王', 17: '大王' };
function cardName(value) { return CARD_NAMES[value] || String(value); }
function cardList(cards) { return [...cards].sort((a, b) => a - b).map(cardName).join(' '); }
function cardCodes(cards) { return [...cards].sort((a, b) => a - b).join(' '); }

function renderGrid(board, size, symbols) {
  const width = String(size).length;
  const pad = (n) => String(n).padStart(width, ' ');
  const lines = ['     ' + Array.from({ length: size }, (_, i) => pad(i + 1)).join(' ')];
  for (let y = 0; y < size; y++) {
    lines.push(`行${pad(y + 1)} ` + Array.from({ length: size }, (_, x) => symbols[board[y * size + x]] || '?').join(' '));
  }
  return lines.join('\n');
}

const XQ_RED = { R: '车', N: '马', B: '相', A: '仕', K: '帅', C: '炮', P: '兵' };
const XQ_BLACK = { R: '车', N: '马', B: '象', A: '士', K: '将', C: '炮', P: '卒' };
function renderXiangqi(board) {
  const lines = ['      1 2 3 4 5 6 7 8 9  ←列'];
  for (let y = 0; y < 10; y++) {
    const cells = board[y].map((p) => {
      if (!p) return '·';
      return p[0] === 'r' ? XQ_RED[p[1]] : `(${XQ_BLACK[p[1]]})`;
    });
    lines.push(`行${String(y + 1).padStart(2)} ` + cells.join(' '));
  }
  return lines.join('\n');
}

// Enumerate every legal xiangqi move for `seat` by brute-forcing applyAction
// (the rules engine stays the single arbiter — no move legality is duplicated
// here). ~1400 probes per turn is cheap, and handing the model the explicit
// list is what keeps it from hallucinating illegal moves or burning its
// reasoning on re-deriving which moves exist.
export function legalXiangqiMoves(state, seat) {
  const moves = [];
  const red = seat === 0;
  for (let fy = 0; fy < 10; fy++) for (let fx = 0; fx < 9; fx++) {
    const piece = state.board[fy][fx];
    if (!piece || (piece[0] === 'r') !== red) continue;
    for (let ty = 0; ty < 10; ty++) for (let tx = 0; tx < 9; tx++) {
      const target = state.board[ty][tx];
      if (target && (target[0] === 'r') === red) continue;
      try {
        applyAction(state, seat, { type: 'move', fromX: fx, fromY: fy, toX: tx, toY: ty });
        moves.push({ fromX: fx, fromY: fy, toX: tx, toY: ty, piece, capture: target || null });
      } catch { /* illegal, skip */ }
    }
  }
  return moves;
}

function moveLine(move, index) {
  // Short JSON form ("f":[col,row],"t":[col,row], 1-based): the verbose
  // fromX/fromY keys repeated ~40× dominated the prompt (~1.2k tokens).
  const desc = move.capture ? ` 吃${xiangqiPieceLabel(move.capture)}` : '';
  return `${index + 1}. ${xiangqiPieceLabel(move.piece)} {"f":[${move.fromX + 1},${move.fromY + 1}],"t":[${move.toX + 1},${move.toY + 1}]}${desc}`;
}

// Probe every empty point with applyAction and collect the ones the engine
// rejects (suicide + ko). 81 probes; the engine stays the arbiter.
function goIllegalPoints(state, seat) {
  const bad = [];
  for (let i = 0; i < state.board.length; i++) {
    if (state.board[i]) continue;
    try { applyAction(state, seat, { type: 'place', x: i % 9, y: Math.floor(i / 9) }); }
    catch { bad.push(i); }
  }
  return bad;
}

// Enumerate every playable card subset of the seat's hand by walking the
// multiset (counts per value) and probing applyAction — the engine validates
// combo shape AND beats-last, so no combo logic is duplicated here. Worst-case
// subset count for a 20-card hand is ~13k probes, well inside the budget.
const DDZ_PROBE_BUDGET = 20000;
const DDZ_LIST_CAP = 100;
function doudizhuLegalPlays(state, seat) {
  const hand = [...(state.hands?.[seat] || [])].sort((a, b) => a - b);
  // applyAction enforces `player === state.turn`; the seat we are enumerating
  // for is by definition the one about to move, so probe against a turn-
  // normalized shallow copy (hands/board stay shared — applyAction clones).
  const probeState = { ...state, turn: seat };
  const counts = [];
  for (const c of hand) {
    const last = counts[counts.length - 1];
    if (last && last.v === c) last.n++;
    else counts.push({ v: c, n: 1 });
  }
  const results = [];
  let probes = 0;
  const rec = (i, chosen) => {
    if (probes > DDZ_PROBE_BUDGET) return;
    if (i === counts.length) {
      if (!chosen.length) return;
      probes++;
      try { applyAction(probeState, seat, { type: 'play', cards: chosen.slice() }); results.push(chosen.slice()); } catch { /* illegal */ }
      return;
    }
    const { v, n } = counts[i];
    for (let k = 0; k <= n; k++) {
      for (let t = 0; t < k; t++) chosen.push(v);
      rec(i + 1, chosen);
      for (let t = 0; t < k; t++) chosen.pop();
    }
  };
  rec(0, []);
  // Longer plays first (bombs/planes/straights before single cards) so the
  // cap below keeps the interesting options when the list overflows.
  results.sort((a, b) => b.length - a.length || a[0] - b[0]);
  return results.slice(0, DDZ_LIST_CAP);
}

const SYSTEM_TEXT = {
  gomoku: `你在和一个人类下五子棋（15×15 棋盘）。双方轮流在空交叉点落子，先在横、竖或斜任一方向连成五子（含以上）者获胜。
棋盘用文本表示：'.' 是空点，'X' 是黑子，'O' 是白子。列号 1-15 从左到右，行号 1-15 从上到下。黑先行。
轮到你时只输出一个 JSON 对象：{"type":"place","x":<列号1-15>,"y":<行号1-15>}，除此之外不要输出任何文字。`,
  go: `你在和一个人类下围棋（9×9 小棋盘）。黑先行。'.' 是空点，'X' 是黑子，'O' 是白子；列号 1-9 从左到右，行号 1-9 从上到下。
规则：落子后无气的对方棋群会被提掉；不能自杀（落子后自己这块棋无气且提不掉任何对方子则非法）；打劫时不得立即提回。
提示会标注当前全部禁着点（自杀点与打劫点），除禁着点外的所有空点均可落子。
双方连续各停一手则终局，按数子法（子数 + 围住的空点，只算单色边界）判定胜负。
轮到你时只输出一个 JSON 对象：落子 {"type":"place","x":<1-9>,"y":<1-9>}，或停一手 {"type":"pass"}。除此之外不要输出任何文字。`,
  xiangqi: `你和人类下中国象棋。棋盘 9 列 × 10 行：列 1-9 从左到右，行 1-10 从上到下；红在下方先行。棋盘文本中红子用汉字：车 马 相 仕 帅 炮 兵；黑子加括号：(车)(马)(象)(士)(将)(炮)(卒)；'·' 是空位。
子力价值：车>炮>马>兵>相/仕；吃掉对方帅/将即胜，将军与将死是主要战术目标。
【重要】每次提示都会给出你的全部合法着法清单（f=起点，t=终点，[列,行]，1 基坐标），你只能从中选一个并原样输出其 JSON，不要自己发明。选着法时优先：吃对方大子、将军、保护被攻击的子、控制要道。
轮到你时只输出一个 JSON 对象：{"type":"move","f":[<列>,<行>],"t":[<列>,<行>]}。除此之外不要输出任何文字。`,
  doudizhu: `你在玩三人斗地主（1 地主 vs 2 农民，先出完手牌的一方获胜）。
牌用点数编码：3-10 原数字，11=J、12=Q、13=K、14=A、15=2、16=小王、17=大王，牌力递增，2 和双王不入顺子。
牌型：单张/对子/三张/三带一/三带二；顺子（≥5 连单）；连对（≥3 连对）；四带二单/二对；飞机（≥2 组连三张）带单/带对；炸弹（四张同点，压一切非炸弹）；王炸（双王，最大）。同型同长比主牌点数。
上家有牌时你必须压过（更大同型/炸弹/王炸）或 pass；新轮首手可出任意牌型。
提示会给出你的全部合法出法清单，你只能从中选一个并原样输出其 JSON；清单为空则只能 pass。
发牌后先叫地主：{"type":"bid","value":1} 叫地主（收 3 张底牌并先出牌），{"type":"bid","value":0} 不叫。
轮到你时只输出一个 JSON 对象：{"type":"play","c":[点数,...]}、{"type":"pass"} 或 bid。除此之外不要输出任何文字。`,
};

export function buildGameMessages(state, seat, failures = [], history = []) {
  const game = state.game;
  const system = `你是一个棋类/牌类对弈引擎。${SYSTEM_TEXT[game]}`;
  const seatNo = seat + 1;
  const parts = [];
  if (Array.isArray(history) && history.length) {
    parts.push(`最近的着法记录（旧→新，共 ${history.length} 条）：\n${history.map((item, index) => `${index + 1}. ${item}`).join('\n')}`);
  }
  if (game === 'gomoku') {
    parts.push(`你执 ${seat === 0 ? 'X（黑，先手）' : 'O（白，后手）'}，座位 ${seatNo}；对手是人类。`);
    parts.push(`当前棋盘（已下 ${state.seq} 手）：\n${renderGrid(state.board, 15, { 0: '.', 1: 'X', 2: 'O' })}`);
    if (state.lastIndex >= 0) parts.push(`对手最后一手：列 ${state.lastIndex % 15 + 1}、行 ${Math.floor(state.lastIndex / 15) + 1}。`);
  } else if (game === 'go') {
    parts.push(`你执 ${seat === 0 ? 'X（黑，先手）' : 'O（白，后手）'}，座位 ${seatNo}；对手是人类。`);
    parts.push(`当前棋盘（已下 ${state.seq} 手）：\n${renderGrid(state.board, 9, { 0: '.', 1: 'X', 2: 'O' })}`);
    const illegal = goIllegalPoints(state, seat);
    if (illegal.length) parts.push(`禁着点（不能落子）：${illegal.map((i) => `列${i % 9 + 1}行${Math.floor(i / 9) + 1}`).join('、')}；其余空点均可落子。`);
    else parts.push('当前无禁着点，所有空点均可落子。');
    if (state.passes > 0) parts.push(`对手刚刚停了一手（连续停手 ${state.passes} 次；再连续停一手即终局）。`);
  } else if (game === 'xiangqi') {
    parts.push(`你是${seat === 0 ? '红方（帅，在下方，行8-10）' : '黑方（(将)，在上方，行1-4）'}，座位 ${seatNo}；对手是人类。`);
    parts.push(`当前棋盘（第 ${state.seq} 手后，轮到你走第 ${state.seq + 1} 手）：\n${renderXiangqi(state.board)}`);
    if (state.lastIndex >= 0) {
      const lx = state.lastIndex % 9, ly = Math.floor(state.lastIndex / 9);
      const p = state.board[ly][lx];
      parts.push(`对手最后一手落在：列 ${lx + 1}、行 ${ly + 1}${p ? `（现在是${p[0] === 'r' ? '红' : '黑'}${xiangqiPieceLabel(p)}）` : ''}。`);
    }
    parts.push(`你的全部合法着法清单（f=起点 t=终点，[列,行]，必须从中选一个）：\n${legalXiangqiMoves(state, seat).map(moveLine).join('\n')}`);
  } else if (game === 'doudizhu') {
    const hand = state.hands?.[seat] || [];
    parts.push(`你控制座位 ${seatNo}。你的手牌（${hand.length} 张）：${cardList(hand)}`);
    state.players.forEach((_, index) => {
      if (index !== seat) parts.push(`座位 ${index + 1} 剩余 ${state.hands?.[index]?.length ?? 0} 张。`);
    });
    if (state.phase === 'bid') {
      parts.push(`阶段：叫地主（你是${state.turn === seat ? '当前' : '等待中的'}叫牌方）。`);
    } else if (state.phase === 'play') {
      parts.push(`地主：座位 ${(state.landlord ?? 0) + 1}；底牌（已公开）：${cardList(state.bottom || [])}。`);
      if (state.last) parts.push(`上一手：座位 ${state.last.player + 1} 出了 ${cardList(state.last.cards)}，其后已有 ${state.passes} 家不要。`);
      else parts.push('新的一轮，你是首手，可以出任意合法牌型。');
      const plays = doudizhuLegalPlays(state, seat);
      if (plays.length) {
        parts.push(`你的合法出法清单（c=要点数的牌，同一张牌出现几次就写几次；从中选一个原样输出，或 pass）：\n${plays.map((cards, index) => `${index + 1}. {"c":[${cards.join(',')}]}`).join('\n')}`);
      } else if (state.last) {
        parts.push('你没有任何能压过上一手的牌，只能输出 {"type":"pass"}。');
      }
    }
  }
  if (Array.isArray(failures) && failures.length) {
    parts.push('你之前的回复未通过对弈规则校验，全部被拒绝：\n' + failures.map((item, index) => `${index + 1}. ${item}`).join('\n') + '\n请重新思考局面，给出一个合法的走法。');
  }
  parts.push('现在轮到你行动，只输出一个 JSON 对象。');
  return { system, user: parts.join('\n\n') };
}

// Compact move notation for the bounded history window. Full board + legal
// lists are sent only for the current round; past rounds ride along as these
// ~25-token lines, so 12 rounds of memory cost ~300 tokens instead of
// re-sending 12 full prompts (~20k+).
export function describeAction(game, state, seat, action) {
  const seq = (state?.seq ?? 0) + 1;
  if (game === 'gomoku') return `第${seq}手 ${seat === 0 ? '黑' : '白'} 落子 列${(action.x ?? 0) + 1}行${(action.y ?? 0) + 1}`;
  if (game === 'go') {
    if (action.type === 'pass') return `第${seq}手 ${seat === 0 ? '黑' : '白'} 停一手`;
    return `第${seq}手 ${seat === 0 ? '黑' : '白'} 落子 列${(action.x ?? 0) + 1}行${(action.y ?? 0) + 1}`;
  }
  if (game === 'xiangqi') {
    const piece = state.board[action.fromY]?.[action.fromX];
    const capture = state.board[action.toY]?.[action.toX];
    const side = seat === 0 ? '红' : '黑';
    return `第${seq}手 ${side} ${xiangqiPieceLabel(piece)} (${(action.fromX ?? 0) + 1},${(action.fromY ?? 0) + 1})→(${(action.toX ?? 0) + 1},${(action.toY ?? 0) + 1})${capture ? ` 吃${xiangqiPieceLabel(capture)}` : ''}`;
  }
  if (game === 'doudizhu') {
    if (action.type === 'start') return '发牌';
    if (action.type === 'bid') return `座位${seat + 1} ${action.value ? '叫地主' : '不叫'}`;
    if (action.type === 'pass') return `座位${seat + 1} 不要`;
    return `座位${seat + 1} 出 ${cardList(action.cards)}`;
  }
  return '';
}

function coerceInt(value) {
  const n = Number(value);
  return Number.isInteger(n) ? n : null;
}

// Extract the first balanced JSON object from a model reply and coerce it into
// an applyAction-shaped action. Returns null when the reply is unusable.
export function parseAIAction(text, game) {
  const raw = String(text || '').replace(/```(?:json)?/gi, '');
  const start = raw.indexOf('{');
  if (start < 0) return null;
  let depth = 0;
  let end = -1;
  for (let i = start; i < raw.length; i++) {
    if (raw[i] === '{') depth++;
    else if (raw[i] === '}') { depth--; if (!depth) { end = i; break; } }
  }
  if (end < 0) return null;
  let action;
  try { action = JSON.parse(raw.slice(start, end + 1)); } catch { return null; }
  if (!action || typeof action !== 'object') return null;
  if (typeof action.type !== 'string') {
    // Compact list lines carry no "type" (space saver): default per shape.
    if (game === 'xiangqi' || (Array.isArray(action.f) && Array.isArray(action.t))) action.type = 'move';
    else if (game === 'doudizhu' && Array.isArray(action.c)) action.type = 'play';
  }
  if (typeof action.type !== 'string') return null;
  const xy = (a, xKey, yKey, maxX, maxY) => {
    const x = coerceInt(a[xKey]), y = coerceInt(a[yKey]);
    if (x == null || y == null || x < 1 || y < 1 || x > maxX || y > maxY) return null;
    return { x: x - 1, y: y - 1 };
  };
  if (game === 'gomoku' || game === 'go') {
    if (action.type === 'pass' && game === 'go') return { type: 'pass' };
    if (action.type !== 'place') return null;
    const spot = xy(action, 'x', 'y', game === 'gomoku' ? 15 : 9, game === 'gomoku' ? 15 : 9);
    return spot ? { type: 'place', x: spot.x, y: spot.y } : null;
  }
  if (game === 'xiangqi') {
    if (action.type !== 'move') return null;
    // Short form: {"f":[x,y],"t":[x,y]} (1-based) — matches the compact list
    // lines; fromX/fromY long form is still accepted for compatibility.
    if (Array.isArray(action.f) && Array.isArray(action.t)) {
      const fx = coerceInt(action.f[0]), fy = coerceInt(action.f[1]);
      const tx = coerceInt(action.t[0]), ty = coerceInt(action.t[1]);
      if (fx == null || fy == null || tx == null || ty == null) return null;
      if (fx < 1 || fy < 1 || tx < 1 || ty < 1 || fx > 9 || tx > 9 || fy > 10 || ty > 10) return null;
      return { type: 'move', fromX: fx - 1, fromY: fy - 1, toX: tx - 1, toY: ty - 1 };
    }
    const from = xy(action, 'fromX', 'fromY', 9, 10);
    const to = xy(action, 'toX', 'toY', 9, 10);
    if (!from || !to) return null;
    return { type: 'move', fromX: from.x, fromY: from.y, toX: to.x, toY: to.y };
  }
  if (game === 'doudizhu') {
    if (action.type === 'bid') return coerceInt(action.value) === 1 || coerceInt(action.value) === 0 ? { type: 'bid', value: coerceInt(action.value) } : null;
    if (action.type === 'pass') return { type: 'pass' };
    // Compact list lines use {"c":[...]}; cards/play both accepted.
    const raw = Array.isArray(action.c) ? action.c : (action.type === 'play' ? action.cards : null);
    if (!Array.isArray(raw) || !raw.length) return null;
    const cards = raw.map(coerceInt);
    if (cards.some((c) => c == null || c < 3 || c > 17)) return null;
    return { type: 'play', cards };
  }
  return null;
}

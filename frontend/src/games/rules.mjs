const GOMOKU_SIZE = 15;

export const GAME_META = {
  gomoku: { label: '五子棋', min: 2, max: 2 },
  go: { label: '围棋', min: 2, max: 2 },
  xiangqi: { label: '象棋', min: 2, max: 2 },
  doudizhu: { label: '斗地主', min: 3, max: 3 },
};

export function createState(game, players = []) {
  if (game === 'gomoku') return { game, size: GOMOKU_SIZE, board: Array(GOMOKU_SIZE * GOMOKU_SIZE).fill(0), turn: 0, players, winner: null, seq: 0 };
  if (game === 'go') return { game, size: 9, board: Array(81).fill(0), turn: 0, players, passes: 0, ko: -1, winner: null, seq: 0 };
  if (game === 'xiangqi') return { game, board: xiangqiStart(), turn: 0, players, winner: null, seq: 0 };
  return { game, phase: 'deal', players, hands: [], bottom: [], landlord: -1, turn: 0, last: null, passes: 0, winner: null, seq: 0 };
}

export function applyAction(state, playerIndex, action) {
  if (!state || state.winner != null) throw new Error('对局已经结束');
  if (state.game === 'gomoku') return applyGomoku(state, playerIndex, action);
  if (state.game === 'go') return applyGo(state, playerIndex, action);
  if (state.game === 'xiangqi') return applyXiangqi(state, playerIndex, action);
  return applyDoudizhu(state, playerIndex, action);
}

function clone(state) { return JSON.parse(JSON.stringify(state)); }

function applyGomoku(state, player, action) {
  if (player !== state.turn || action.type !== 'place') throw new Error('还没轮到你');
  const x = Number(action.x), y = Number(action.y);
  if (!Number.isInteger(x) || !Number.isInteger(y) || x < 0 || y < 0 || x >= GOMOKU_SIZE || y >= GOMOKU_SIZE) throw new Error('落子位置无效');
  const next = clone(state), index = y * GOMOKU_SIZE + x;
  if (next.board[index]) throw new Error('该位置已有棋子');
  next.board[index] = player + 1;
  next.turn = 1 - player;
  next.seq++;
  if (hasFive(next.board, x, y, player + 1)) next.winner = player;
  return next;
}

function hasFive(board, x, y, value) {
  for (const [dx, dy] of [[1, 0], [0, 1], [1, 1], [1, -1]]) {
    let count = 1;
    for (const sign of [-1, 1]) for (let n = 1; n < 15; n++) {
      const xx = x + dx * n * sign, yy = y + dy * n * sign;
      if (xx < 0 || yy < 0 || xx >= 15 || yy >= 15 || board[yy * 15 + xx] !== value) break;
      count++;
    }
    if (count >= 5) return true;
  }
  return false;
}

function applyGo(state, player, action) {
  if (player !== state.turn) throw new Error('还没轮到你');
  if (action.type === 'pass') {
    const next = clone(state); next.turn = 1 - player; next.passes++;
    next.seq++; if (next.passes >= 2) next.winner = scoreGo(next.board);
    return next;
  }
  if (action.type !== 'place') throw new Error('操作无效');
  const x = Number(action.x), y = Number(action.y);
  if (!Number.isInteger(x) || !Number.isInteger(y) || x < 0 || y < 0 || x >= 9 || y >= 9) throw new Error('落子位置无效');
  const index = y * 9 + x;
  if (state.board[index] || state.ko === index) throw new Error('该位置不能落子');
  const next = clone(state); next.board[index] = player + 1;
  const captured = new Set();
  for (const n of neighbors(index)) if (next.board[n] === 2 - player) {
    const group = collectGroup(next.board, n);
    if (group.liberties.length === 0) group.stones.forEach((stone) => captured.add(stone));
  }
  captured.forEach((n) => { next.board[n] = 0; });
  const own = collectGroup(next.board, index);
  if (!own.liberties.length) throw new Error('不能自杀');
  next.ko = captured.size === 1 && own.stones.length === 1 ? [...captured][0] : -1;
  next.passes = 0; next.turn = 1 - player; next.seq++;
  return next;
}

function neighbors(index) { const x = index % 9, y = Math.floor(index / 9), out = []; if (x) out.push(index - 1); if (x < 8) out.push(index + 1); if (y) out.push(index - 9); if (y < 8) out.push(index + 9); return out; }
function collectGroup(board, start) {
  const color = board[start], seen = new Set([start]), stones = [], liberties = new Set(), queue = [start];
  while (queue.length) { const n = queue.pop(); stones.push(n); for (const x of neighbors(n)) { if (!board[x]) liberties.add(x); else if (board[x] === color && !seen.has(x)) { seen.add(x); queue.push(x); } } }
  return { stones, liberties: [...liberties] };
}
function scoreGo(board) { let score = board.filter((x) => x === 1).length - board.filter((x) => x === 2).length; const seen = new Set(); for (let i = 0; i < 81; i++) if (!board[i] && !seen.has(i)) { const area = collectEmpty(board, i, seen); const borders = new Set(area.borders); if (borders.size === 1) score += borders.has(1) ? area.stones.length : -area.stones.length; } return score >= 0 ? 0 : 1; }
function collectEmpty(board, start, seen) { const stones = [], borders = new Set(), q = [start]; seen.add(start); while (q.length) { const n = q.pop(); stones.push(n); for (const x of neighbors(n)) { if (!board[x] && !seen.has(x)) { seen.add(x); q.push(x); } else if (board[x]) borders.add(board[x]); } } return { stones, borders }; }

const XIANGQI = [
  ['bR','bN','bB','bA','bK','bA','bB','bN','bR'], ['', '', '', '', '', '', '', '', ''], ['', 'bC', '', '', '', '', '', 'bC', ''],
  ['bP', '', 'bP', '', 'bP', '', 'bP', '', 'bP'], ['', '', '', '', '', '', '', '', ''], ['', '', '', '', '', '', '', '', ''],
  ['rP', '', 'rP', '', 'rP', '', 'rP', '', 'rP'], ['', 'rC', '', '', '', '', '', 'rC', ''], ['', '', '', '', '', '', '', '', ''],
  ['rR','rN','rB','rA','rK','rA','rB','rN','rR'],
];
const PIECE_LABEL = { R: '车', N: '马', B: '象', A: '士', K: '将', C: '炮', P: '兵' };
function xiangqiStart() { return XIANGQI.map((row) => row.slice()); }
function isRed(piece) { return piece?.[0] === 'r'; }
export function xiangqiPieceLabel(piece) { if (!piece) return ''; if (piece === 'rK') return '帅'; if (piece === 'rB') return '相'; if (piece === 'rA') return '仕'; if (piece === 'bP') return '卒'; return PIECE_LABEL[piece[1]] || ''; }
function applyXiangqi(state, player, action) {
  if (player !== state.turn || action.type !== 'move') throw new Error('还没轮到你');
  const fx = Number(action.fromX), fy = Number(action.fromY), tx = Number(action.toX), ty = Number(action.toY), b = state.board;
  if (![fx, fy, tx, ty].every(Number.isInteger) || fx < 0 || fx > 8 || tx < 0 || tx > 8 || fy < 0 || fy > 9 || ty < 0 || ty > 9) throw new Error('走法无效');
  const p = b[fy][fx], target = b[ty][tx]; if (!p || (player === 0) !== isRed(p) || (target && isRed(target) === isRed(p))) throw new Error('不能走这枚棋');
  if (!xiangqiMoveOK(b, fx, fy, tx, ty, p)) throw new Error('不符合棋子走法');
  const next = clone(state); next.board[ty][tx] = p; next.board[fy][fx] = ''; next.turn = 1 - player; next.seq++;
  if (isInCheck(next.board, player === 0)) throw new Error('不能送将或让将帅照面');
  if (!next.board.flat().includes(player === 0 ? 'bK' : 'rK') || !hasLegalXiangqiMove(next.board, player !== 0)) next.winner = player;
  return next;
}
function xiangqiMoveOK(b, fx, fy, tx, ty, p) {
  const dx = tx - fx, dy = ty - fy, ax = Math.abs(dx), ay = Math.abs(dy), red = isRed(p);
  const count = () => { let n = 0, x = fx + Math.sign(dx), y = fy + Math.sign(dy); while (x !== tx || y !== ty) { if (b[y][x]) n++; x += Math.sign(dx); y += Math.sign(dy); } return n; };
  const type = p[1];
  if (type === 'R') return (dx === 0 || dy === 0) && count() === 0;
  if (type === 'C') return (dx === 0 || dy === 0) && count() === (b[ty][tx] ? 1 : 0);
  if (type === 'N') return ax * ay === 2 && !b[fy + (ay === 2 ? Math.sign(dy) : 0)][fx + (ax === 2 ? Math.sign(dx) : 0)];
  if (type === 'B') return ax === 2 && ay === 2 && !b[fy + dy / 2][fx + dx / 2] && (red ? ty >= 5 : ty <= 4);
  if (type === 'A') return ax === 1 && ay === 1 && tx >= 3 && tx <= 5 && (red ? ty >= 7 : ty <= 2);
  if (type === 'K') {
    if (fx === tx && b[ty][tx]?.[1] === 'K' && count() === 0) return true;
    return ax + ay === 1 && tx >= 3 && tx <= 5 && (red ? ty >= 7 : ty <= 2);
  }
  if (type === 'P') { const forward = red ? -1 : 1, crossed = red ? fy <= 4 : fy >= 5; return (dx === 0 && dy === forward) || (crossed && ax === 1 && dy === 0); }
  return false;
}
function isInCheck(board, red) { let gx = -1, gy = -1; for (let y = 0; y < 10; y++) for (let x = 0; x < 9; x++) if (board[y][x] === (red ? 'rK' : 'bK')) { gx = x; gy = y; } if (gx < 0) return true; for (let y = 0; y < 10; y++) for (let x = 0; x < 9; x++) { const p = board[y][x]; if (p && isRed(p) !== red && xiangqiMoveOK(board, x, y, gx, gy, p)) return true; } const otherY = red ? 0 : 9; if (board[otherY][gx]?.[1] === 'K') { let clear = true; for (let y = Math.min(gy, otherY) + 1; y < Math.max(gy, otherY); y++) if (board[y][gx]) clear = false; if (clear) return true; } return false; }
function hasLegalXiangqiMove(board, red) { for (let fy = 0; fy < 10; fy++) for (let fx = 0; fx < 9; fx++) { const p = board[fy][fx]; if (!p || isRed(p) !== red) continue; for (let ty = 0; ty < 10; ty++) for (let tx = 0; tx < 9; tx++) { if (board[ty][tx] && isRed(board[ty][tx]) === red) continue; if (!xiangqiMoveOK(board, fx, fy, tx, ty, p)) continue; const next = board.map((r) => r.slice()); next[ty][tx] = p; next[fy][fx] = ''; if (!isInCheck(next, red)) return true; } } return false; }

function makeDeck() { const out = []; for (let v = 3; v <= 15; v++) for (let n = 0; n < 4; n++) out.push(v); out.push(16, 17); for (let i = out.length - 1; i > 0; i--) { const random = crypto.getRandomValues(new Uint32Array(1))[0]; const j = random % (i + 1); [out[i], out[j]] = [out[j], out[i]]; } return out; }
function applyDoudizhu(state, player, action) {
  const next = clone(state);
  if (state.phase === 'deal') { if (player !== 0 || action.type !== 'start') throw new Error('等待房主发牌'); const deck = makeDeck(); next.hands = [deck.slice(0, 17), deck.slice(17, 34), deck.slice(34, 51)]; next.bottom = deck.slice(51); next.landlord = -1; next.last = null; next.passes = 0; next.phase = 'bid'; next.turn = 0; next.seq++; return next; }
  if (state.phase === 'bid') { if (player !== state.turn || action.type !== 'bid' || ![0, 1].includes(action.value)) throw new Error('叫地主操作无效'); next.turn = (player + 1) % 3; if (action.value) { next.landlord = player; next.hands[player].push(...next.bottom); next.phase = 'play'; next.turn = player; } else if (next.turn === 0) { next.landlord = 0; next.hands[0].push(...next.bottom); next.phase = 'play'; next.turn = 0; } next.seq++; return next; }
  if (state.phase !== 'play' || player !== state.turn) throw new Error('还没轮到你');
  if (action.type === 'pass') { if (!state.last) throw new Error('第一手不能过'); next.passes++; next.turn = (player + 1) % 3; if (next.passes >= 2) { next.last = null; next.passes = 0; } next.seq++; return next; }
  if (action.type !== 'play' || !Array.isArray(action.cards) || !validCards(action.cards, state.hands[player]) || !beats(action.cards, state.last?.cards)) throw new Error('出牌不合法');
  next.hands[player] = removeCards(state.hands[player], action.cards); next.last = { player, cards: action.cards.slice() }; next.passes = 0; next.turn = (player + 1) % 3; next.seq++; if (!next.hands[player].length) next.winner = player === state.landlord ? player : 1; return next;
}
function validCards(cards, hand) { const copy = hand.slice(); for (const c of cards) { const i = copy.indexOf(c); if (i < 0) return false; copy.splice(i, 1); } return cards.length > 0 && isCombo(cards); }
function removeCards(hand, cards) { const out = hand.slice(); for (const c of cards) out.splice(out.indexOf(c), 1); return out; }
function rank(cards) {
  const sorted = [...cards].sort((a, b) => a - b), map = new Map();
  for (const value of sorted) map.set(value, (map.get(value) || 0) + 1);
  const entries = [...map.entries()].sort((a, b) => a[0] - b[0]);
  const valuesWith = (count) => entries.filter(([, n]) => n === count).map(([v]) => v);
  const consecutive = (values) => values.length > 0 && values.at(-1) < 15 && values.every((v, i) => i === 0 || v === values[i - 1] + 1);
  const result = (type, main, length = sorted.length) => ({ type, main, length });
  if (sorted.length === 2 && map.has(16) && map.has(17)) return result('rocket', 17);
  if (sorted.length === 4 && valuesWith(4).length === 1) return result('bomb', valuesWith(4)[0]);
  if (sorted.length === 1) return result('single', sorted[0]);
  if (sorted.length === 2 && valuesWith(2).length === 1) return result('pair', sorted[0]);
  if (sorted.length === 3 && valuesWith(3).length === 1) return result('triple', sorted[0]);
  if (sorted.length === 4 && valuesWith(3).length === 1) return result('triple_single', valuesWith(3)[0]);
  if (sorted.length === 5 && valuesWith(3).length === 1 && valuesWith(2).length === 1) return result('triple_pair', valuesWith(3)[0]);
  if (sorted.length >= 5 && entries.every(([, n]) => n === 1) && consecutive(entries.map(([v]) => v))) return result('straight', sorted.at(-1));
  if (sorted.length >= 6 && sorted.length % 2 === 0 && entries.every(([, n]) => n === 2) && consecutive(entries.map(([v]) => v))) return result('double_straight', entries.at(-1)[0]);
  for (const wing of ['none', 'single', 'pair']) {
    const unit = wing === 'none' ? 3 : wing === 'single' ? 4 : 5;
    if (sorted.length % unit !== 0) continue;
    const triples = sorted.length / unit;
    if (triples < 2) continue;
    const candidates = entries.filter(([v, n]) => v < 15 && n >= 3).map(([v]) => v);
    for (let start = 0; start + triples <= candidates.length; start++) {
      const sequence = candidates.slice(start, start + triples);
      if (!consecutive(sequence)) continue;
      const remain = new Map(map); sequence.forEach((v) => remain.set(v, remain.get(v) - 3));
      const leftovers = [...remain.values()].filter(Boolean);
      if (wing === 'single' && leftovers.length === triples && leftovers.every((n) => n === 1)) return result('plane_single', sequence.at(-1));
      if (wing === 'none' && leftovers.length === 0) return result('plane', sequence.at(-1));
      if (wing === 'pair' && leftovers.length === triples && leftovers.every((n) => n === 2)) return result('plane_pair', sequence.at(-1));
    }
  }
  if (sorted.length === 6 && valuesWith(4).length === 1) return result('four_two_single', valuesWith(4)[0]);
  if (sorted.length === 8 && valuesWith(4).length === 1 && valuesWith(2).length === 2) return result('four_two_pair', valuesWith(4)[0]);
  return result('', 0);
}
function isCombo(cards) { return rank(cards).type !== ''; }
function beats(cards, last) { if (!last) return true; const a = rank(cards), b = rank(last); if (!a.type) return false; if (a.type === 'rocket') return true; if (b.type === 'rocket') return false; if (a.type === 'bomb' && b.type !== 'bomb') return true; return a.type === b.type && a.length === b.length && a.main > b.main; }

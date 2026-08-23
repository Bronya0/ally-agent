import { accessToken, decryptJSON, deriveKey, encryptJSON } from './crypto.mjs';

const INVITE_PREFIX = 'ALLY-GAME-1';

export function buildInvite({ host, port, roomId, secret }) {
  return [INVITE_PREFIX, host, port, roomId, secret].join('|');
}

export function parseInvite(raw) {
  const parts = String(raw || '').trim().split('|');
  if (parts.length !== 5 || parts[0] !== INVITE_PREFIX) throw new Error('联机信息格式不正确');
  const port = Number(parts[2]);
  if (!/^\d{1,3}(\.\d{1,3}){3}$/.test(parts[1]) || !Number.isInteger(port) || port < 1024 || port > 65535 || parts[3].length < 8 || parts[4].length < 32) throw new Error('联机信息无效');
  return { host: parts[1], port, roomId: parts[3], secret: parts[4] };
}

export class GameConnection {
  constructor(options) {
    this.options = options;
    this.socket = null;
    this.peerId = '';
    this.hostId = '';
    this.roomKey = null;
    this.seq = 0;
    this.lastSeq = new Map();
    this.peers = new Map();
    this.closed = false;
  }

  async connect(invite) {
    const info = typeof invite === 'string' ? parseInvite(invite) : invite;
    this.roomKey = await deriveKey(info.secret);
    const token = await accessToken(info.secret);
    const url = `ws://${info.host}:${info.port}/game/ws?room=${encodeURIComponent(info.roomId)}&token=${encodeURIComponent(token)}`;
    const socket = new WebSocket(url);
    this.socket = socket;
    socket.addEventListener('message', (event) => this.onRawMessage(event.data));
    socket.addEventListener('close', () => { if (!this.closed) this.options.onClose?.(); });
    socket.addEventListener('error', () => this.options.onError?.('WebSocket 连接失败'));
    await new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error('连接超时')), 7000);
      socket.addEventListener('open', () => { clearTimeout(timer); resolve(); }, { once: true });
      socket.addEventListener('error', () => { clearTimeout(timer); reject(new Error('无法连接房主')); }, { once: true });
    });
  }

  sendHello() {
    this.sendOuter({ kind: 'hello', payload: { v: 1, name: String(this.options.name || '玩家').slice(0, 20) } });
  }

  async send(type, data, to = '') {
    if (!this.peerId || !this.roomKey) throw new Error('尚未连接');
    const body = { seq: ++this.seq, type, data };
    const payload = await encryptJSON(this.roomKey, body, `${this.peerId}:${to || '*'}`);
    this.sendOuter({ kind: 'data', to, payload });
  }

  sendOuter(value) {
    if (!this.socket || this.socket.readyState !== WebSocket.OPEN) throw new Error('连接已断开');
    const encoded = JSON.stringify(value);
    if (encoded.length > 60 * 1024) throw new Error('消息过大');
    this.socket.send(encoded);
  }

  async onRawMessage(raw) {
    if (typeof raw !== 'string' || raw.length > 70 * 1024) return;
    let msg;
    try { msg = JSON.parse(raw); } catch { return; }
    if (msg.kind === 'welcome') {
      this.peerId = msg.from;
      this.hostId = msg.host;
      this.peers.set(this.peerId, { id: this.peerId, name: this.options.name });
      for (const peer of msg.peers || []) if (peer.payload) this.acceptHello(peer.id, peer.payload);
      this.sendHello();
      this.options.onReady?.({ peerId: this.peerId, hostId: this.hostId });
      this.emitPeers();
      return;
    }
    if (msg.kind === 'hello' && msg.from && msg.payload) { this.acceptHello(msg.from, msg.payload); this.emitPeers(); return; }
    if (msg.kind === 'left') { this.peers.delete(msg.from); this.emitPeers(); return; }
    if (msg.kind !== 'data' || !msg.from || msg.from === this.peerId) return;
    try {
      const body = await decryptJSON(this.roomKey, msg.payload, `${msg.from}:${msg.to || '*'}`);
      const last = this.lastSeq.get(msg.from) || 0;
      if (!Number.isSafeInteger(body.seq) || body.seq <= last || typeof body.type !== 'string') return;
      this.lastSeq.set(msg.from, body.seq);
      this.options.onMessage?.({ from: msg.from, type: body.type, data: body.data });
    } catch {
      // Authentication failures are intentionally silent: malformed or
      // tampered ciphertext must not affect the game state or flood the UI.
    }
  }

  acceptHello(id, payload) {
    if (!id || !payload || payload.v !== 1 || typeof payload.name !== 'string') return;
    this.peers.set(id, { id, name: payload.name.slice(0, 20) || '玩家' });
  }

  emitPeers() { this.options.onPeers?.([...this.peers.values()]); }

  close() {
    this.closed = true;
    if (this.socket) this.socket.close(1000, 'closed');
    this.socket = null;
    this.peers.clear();
    this.lastSeq.clear();
  }
}

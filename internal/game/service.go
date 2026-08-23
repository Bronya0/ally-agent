// SPDX-License-Identifier: GPL-3.0-only
package game

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/wailsapp/wails/v3/pkg/application"
)

const (
	maxPeers          = 4
	maxFrameBytes     = 64 * 1024
	writeTimeout      = 5 * time.Second
	idleTimeout       = 75 * time.Second
	maxMessagesWindow = 40
)

// Service is deliberately independent from internal/app. It owns only the
// temporary LAN game relay and exposes a very small Wails binding surface.
type Service struct {
	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
	server *http.Server
	ln     net.Listener
	hub    *hub
	info   ServerInfo
}

type StartRequest struct {
	Port int `json:"port"`
}

type ServerInfo struct {
	Running   bool     `json:"running"`
	Port      int      `json:"port"`
	RoomID    string   `json:"roomId"`
	Secret    string   `json:"secret"`
	Addresses []string `json:"addresses"`
}

type NetworkInfo struct {
	Addresses []string `json:"addresses"`
}

func NewService() *Service { return &Service{} }

func (s *Service) ServiceName() string { return "GameService" }

func (s *Service) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()
	return nil
}

func (s *Service) ServiceShutdown() error {
	s.StopServer()
	return nil
}

func (s *Service) GetNetworkInfo() NetworkInfo {
	return NetworkInfo{Addresses: localIPv4Addresses()}
}

func (s *Service) GetServerInfo() ServerInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneServerInfo(s.info)
}

func (s *Service) StartServer(req StartRequest) (ServerInfo, error) {
	port := req.Port
	if port < 0 || port > 65535 {
		return ServerInfo{}, errors.New("port must be 0 or between 1024 and 65535")
	}
	if port != 0 && port < 1024 {
		return ServerInfo{}, errors.New("port must be 0 or between 1024 and 65535")
	}

	secret, err := randomToken(32)
	if err != nil {
		return ServerInfo{}, fmt.Errorf("generate room secret: %w", err)
	}
	roomID, err := randomToken(9)
	if err != nil {
		return ServerInfo{}, fmt.Errorf("generate room id: %w", err)
	}
	accessToken := deriveAccessToken(secret)

	s.mu.Lock()
	if s.server != nil {
		s.mu.Unlock()
		return ServerInfo{}, errors.New("game server is already running")
	}
	parent := s.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	h := newHub(roomID, accessToken)
	mux := http.NewServeMux()
	mux.HandleFunc("/game/ws", h.handleWebSocket)
	mux.HandleFunc("/game/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok"))
	})
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    8 * 1024,
	}
	ln, err := net.Listen("tcp4", net.JoinHostPort("0.0.0.0", strconv.Itoa(port)))
	if err != nil {
		cancel()
		s.mu.Unlock()
		return ServerInfo{}, fmt.Errorf("listen on port %d: %w", port, err)
	}
	h.onHostLeft = func() { s.stopServer(server) }
	actualPort := ln.Addr().(*net.TCPAddr).Port
	info := ServerInfo{
		Running:   true,
		Port:      actualPort,
		RoomID:    roomID,
		Secret:    secret,
		Addresses: localIPv4Addresses(),
	}
	s.ctx = parent
	s.cancel = cancel
	s.server = server
	s.ln = ln
	s.hub = h
	s.info = info
	s.mu.Unlock()

	go func() {
		<-ctx.Done()
		s.stopServer(server)
	}()
	go func() {
		if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.stopServer(server)
		}
	}()
	return cloneServerInfo(info), nil
}

func (s *Service) StopServer() {
	s.mu.Lock()
	cancel := s.cancel
	server := s.server
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.stopServer(server)
}

func (s *Service) stopServer(target *http.Server) {
	if target == nil {
		return
	}
	s.mu.Lock()
	if s.server != target {
		s.mu.Unlock()
		return
	}
	h := s.hub
	s.server = nil
	s.ln = nil
	s.hub = nil
	s.cancel = nil
	s.info = ServerInfo{}
	s.mu.Unlock()
	if h != nil {
		h.close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = target.Shutdown(ctx)
}

func cloneServerInfo(info ServerInfo) ServerInfo {
	info.Addresses = append([]string(nil), info.Addresses...)
	return info
}

func randomToken(size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func deriveAccessToken(secret string) string {
	sum := sha256.Sum256([]byte("ally-game-access-v1:" + secret))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func localIPv4Addresses() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, 4)
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			v4 := ip.To4()
			if v4 == nil || !isPrivateIPv4(v4) {
				continue
			}
			value := v4.String()
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			out = append(out, value)
		}
	}
	return out
}

func isPrivateIPv4(ip net.IP) bool {
	return ip[0] == 10 || (ip[0] == 172 && ip[1] >= 16 && ip[1] <= 31) || (ip[0] == 192 && ip[1] == 168)
}

type clientEnvelope struct {
	Kind    string          `json:"kind"`
	To      string          `json:"to,omitempty"`
	Payload json.RawMessage `json:"payload"`
}

type serverEnvelope struct {
	Kind    string          `json:"kind"`
	From    string          `json:"from,omitempty"`
	To      string          `json:"to,omitempty"`
	Host    string          `json:"host,omitempty"`
	Peers   []peerHello     `json:"peers,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type peerHello struct {
	ID      string          `json:"id"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type peer struct {
	id       string
	conn     *websocket.Conn
	send     chan []byte
	hello    json.RawMessage
	rateMu   sync.Mutex
	windowAt time.Time
	count    int
}

func (p *peer) allowMessage(now time.Time) bool {
	p.rateMu.Lock()
	defer p.rateMu.Unlock()
	if now.Sub(p.windowAt) >= time.Second {
		p.windowAt = now
		p.count = 0
	}
	p.count++
	return p.count <= maxMessagesWindow
}

type hub struct {
	roomID      string
	accessToken string
	mu          sync.RWMutex
	peers       map[string]*peer
	hostID      string
	closed      bool
	onHostLeft  func()
}

func newHub(roomID, accessToken string) *hub {
	return &hub{roomID: roomID, accessToken: accessToken, peers: make(map[string]*peer)}
}

func (h *hub) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.Query().Get("room") != h.roomID || !constantEqual(r.URL.Query().Get("token"), h.accessToken) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !allowedOrigin(r.Header.Get("Origin")) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	h.mu.Lock()
	if h.closed || len(h.peers) >= maxPeers {
		h.mu.Unlock()
		http.Error(w, "room is full", http.StatusServiceUnavailable)
		return
	}
	id, err := randomToken(9)
	if err != nil {
		h.mu.Unlock()
		http.Error(w, "cannot allocate peer", http.StatusInternalServerError)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		h.mu.Unlock()
		return
	}
	conn.SetReadLimit(maxFrameBytes)
	p := &peer{id: id, conn: conn, send: make(chan []byte, 32), windowAt: time.Now()}
	h.peers[id] = p
	if h.hostID == "" {
		h.hostID = id
	}
	hostID := h.hostID
	roster := h.rosterLocked()
	h.mu.Unlock()

	h.queue(p, mustJSON(serverEnvelope{Kind: "welcome", From: id, Host: hostID, Peers: roster}))
	h.broadcast(mustJSON(serverEnvelope{Kind: "joined", From: id, Host: hostID}), "")
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go h.writeLoop(ctx, p)
	h.readLoop(ctx, p)
	h.removePeer(p)
}

func (h *hub) readLoop(ctx context.Context, p *peer) {
	for {
		kind, data, err := p.conn.Read(ctx)
		if err != nil {
			return
		}
		if kind != websocket.MessageText || len(data) == 0 || len(data) > maxFrameBytes || !p.allowMessage(time.Now()) {
			continue
		}
		var msg clientEnvelope
		if json.Unmarshal(data, &msg) != nil || len(msg.Payload) == 0 {
			continue
		}
		switch msg.Kind {
		case "hello":
			if len(msg.Payload) > 2048 || !json.Valid(msg.Payload) {
				continue
			}
			h.mu.Lock()
			if current := h.peers[p.id]; current == p {
				p.hello = append(p.hello[:0], msg.Payload...)
			}
			h.mu.Unlock()
			h.broadcast(mustJSON(serverEnvelope{Kind: "hello", From: p.id, Payload: msg.Payload}), "")
		case "data":
			if len(msg.Payload) > maxFrameBytes-512 || !json.Valid(msg.Payload) {
				continue
			}
			packet := mustJSON(serverEnvelope{Kind: "data", From: p.id, To: msg.To, Payload: msg.Payload})
			if msg.To == "" {
				h.broadcast(packet, "")
			} else if msg.To != p.id {
				h.mu.RLock()
				target := h.peers[msg.To]
				h.mu.RUnlock()
				if target != nil {
					h.queue(target, packet)
				}
			}
		}
	}
}

func (h *hub) writeLoop(ctx context.Context, p *peer) {
	for {
		select {
		case <-ctx.Done():
			return
		case data, ok := <-p.send:
			if !ok {
				return
			}
			writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
			err := p.conn.Write(writeCtx, websocket.MessageText, data)
			cancel()
			if err != nil {
				_ = p.conn.CloseNow()
				return
			}
		}
	}
}

func (h *hub) queue(p *peer, data []byte) {
	select {
	case p.send <- data:
	default:
		_ = p.conn.Close(websocket.StatusPolicyViolation, "slow client")
	}
}

func (h *hub) broadcast(data []byte, except string) {
	h.mu.RLock()
	peers := make([]*peer, 0, len(h.peers))
	for id, p := range h.peers {
		if id != except {
			peers = append(peers, p)
		}
	}
	h.mu.RUnlock()
	for _, p := range peers {
		h.queue(p, data)
	}
}

func (h *hub) rosterLocked() []peerHello {
	out := make([]peerHello, 0, len(h.peers))
	for id, p := range h.peers {
		out = append(out, peerHello{ID: id, Payload: append(json.RawMessage(nil), p.hello...)})
	}
	return out
}

func (h *hub) removePeer(p *peer) {
	h.mu.Lock()
	if h.peers[p.id] != p {
		h.mu.Unlock()
		return
	}
	delete(h.peers, p.id)
	wasHost := p.id == h.hostID
	h.mu.Unlock()
	_ = p.conn.CloseNow()
	h.broadcast(mustJSON(serverEnvelope{Kind: "left", From: p.id}), "")
	if wasHost {
		h.close()
	}
}

func (h *hub) close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	peers := make([]*peer, 0, len(h.peers))
	for _, p := range h.peers {
		peers = append(peers, p)
	}
	h.peers = make(map[string]*peer)
	onHostLeft := h.onHostLeft
	h.mu.Unlock()
	for _, p := range peers {
		_ = p.conn.Close(websocket.StatusGoingAway, "server stopped")
	}
	if onHostLeft != nil {
		onHostLeft()
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func constantEqual(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func allowedOrigin(raw string) bool {
	if raw == "" {
		return true
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return u.Scheme == "wails" || host == "localhost" || host == "127.0.0.1" || host == "::1" || strings.HasSuffix(host, ".localhost")
}

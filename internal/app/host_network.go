package app

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// networkEventSink 把 Agent 事件通过 HTTP 暴露给外部进程，供 WebSocket / SSE /
// HTTP 轮询客户端消费。它实现 eventSink 接口，由 ServiceStartup 注入到
// fanoutEventSink 中，核心代码零感知。
//
// 当前端点：
//   - GET /events?since=<seq>   SSE 流（event: 行放事件名，data: 行放 JSON）
//   - GET /poll?since=<seq>     一次性轮询，返回 {"events":[...],"next":seq}
//   - GET /healthz              存活检查
//
// WebSocket 预留：eventSubscriber 抽象与传输无关（chan networkEvent），未来
// 增加 WS handler 时复用同一订阅者即可；标准库不含 WS 实现，暂不引入依赖。
//
// 启用方式（默认关闭，不影响桌面端任何行为）：
//   - ALLY_NETWORK_EVENTS=1      开启
//   - ALLY_NETWORK_ADDR=127.0.0.1:39876   监听地址（默认仅回环）
//   - ALLY_NETWORK_TOKEN=xxx     可选 Bearer token 鉴权
//
// 安全：监听非回环地址时未设置 token 会拒绝启动（fail closed）。
type networkEventSink struct {
	addr  string
	token string

	mu        sync.Mutex
	subs      map[*eventSubscriber]struct{}
	ring      *eventRing
	server    *http.Server
	closeOnce sync.Once
	sses int // 当前活跃 SSE 连接数
}

const (
	defaultNetworkAddr       = "127.0.0.1:39876"
	networkHistoryDefault    = 200
	subscriberQueueSize      = 64
	SSEKeepaliveInterval     = 15 * time.Second
	// maxNetworkPayloadBytes 限制单条事件入环/入订阅的 payload 大小。
	// tool:result 等事件可携带数百 KB 的完整工具输出（read 结果刻意不压缩），
	// 若不截断，200 条历史会让本机任意进程拉走大量文件内容并占用内存。
	maxNetworkPayloadBytes = 64 * 1024
	// maxSSEConnections 限制并发 SSE 连接数，防止慢客户端耗尽 goroutine。
	maxSSEConnections = 32
)

// networkEvent 是网络端的事件信封：名字 + JSON payload + 单调序号 + 时间。
type networkEvent struct {
	Name    string          `json:"name"`
	Payload json.RawMessage `json:"payload"`
	Seq     uint64          `json:"seq"`
	TS      time.Time       `json:"ts"`
}

// eventRing 是最近事件环形缓冲：新连接先补发历史，轮询客户端按 seq 增量拉取。
// 序号全局单调递增；缓冲被覆盖的旧事件在 since 查询时跳过（不阻塞、不重放脏数据）。
type eventRing struct {
	mu    sync.Mutex
	buf   []networkEvent
	base  uint64 // 缓冲中最早事件的全局序号
	count int
	next  uint64 // 下一个要分配的序号
	max   int
}

func newEventRing(max int) *eventRing {
	if max <= 0 {
		max = networkHistoryDefault
	}
	return &eventRing{buf: make([]networkEvent, max), max: max}
}

func (r *eventRing) add(e networkEvent) networkEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	e.Seq = r.next
	if r.count == r.max {
		r.buf[r.base%uint64(r.max)] = e
		r.base++
	} else {
		r.buf[r.next%uint64(r.max)] = e
		r.count++
	}
	r.next++
	return e
}

// since 返回全局序号大于等于 seq 的全部可用事件（被覆盖的跳过），以及最新序号。
// 客户端首次用 since=0 拉全量，随后用返回的 next 作为下一次 since 增量拉取。
func (r *eventRing) since(seq uint64) ([]networkEvent, uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if seq >= r.next {
		return nil, r.next
	}
	from := seq
	if from < r.base {
		from = r.base
	}
	out := make([]networkEvent, 0, r.next-from)
	for i := from; i < r.next; i++ {
		out = append(out, r.buf[i%uint64(r.max)])
	}
	return out, r.next
}

// eventSubscriber 是传输无关的订阅者：SSE/未来 WS handler 各自持有一个，
// 通过 channel 接收实时事件；channel 满时新事件被丢弃（不阻塞核心 Emit）。
type eventSubscriber struct {
	ch chan networkEvent
}

func (s *networkEventSink) subscribe() *eventSubscriber {
	sub := &eventSubscriber{ch: make(chan networkEvent, subscriberQueueSize)}
	s.mu.Lock()
	s.subs[sub] = struct{}{}
	s.mu.Unlock()
	return sub
}

func (s *networkEventSink) unsubscribe(sub *eventSubscriber) {
	s.mu.Lock()
	delete(s.subs, sub)
	s.mu.Unlock()
}

// Emit 实现 eventSink：在调用方 goroutine 上同步 marshal（无法避免，ring 需要
// 历史数据），但入环与扇出非阻塞；序列化失败只影响网络端。
// ring.add 与订阅扇出必须在同一临界区内完成，保证两个并发 Emit 的
// 全局序号顺序与订阅者收到的顺序一致（否则 B 后入环却先扇出会乱序）。
func (s *networkEventSink) Emit(name string, payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		log.Printf("network event sink: marshal payload for %q: %v", name, err)
		return
	}
	if len(raw) > maxNetworkPayloadBytes {
		// 按字节硬切会切断 UTF-8 序列或 JSON token，产生非法 JSON，
		// 毒事件入环后会让 SSE 回放/轮询全部解析失败。截断后必须校验，
		// 非法则替换为合法哨兵，保证下游永远拿到可解析的 JSON。
		// 同时必须拷贝出截断后的切片：直接 raw[:N] 只改 slice 头，底层数组
		// 仍持有完整 marshal 结果（tool:result 可达数百 KB、run:image 可达 8MB），
		// 被 ring 和订阅队列共享引用会让"64KB 截断"的内存边界失效。
		full := len(raw)
		raw = raw[:maxNetworkPayloadBytes]
		if !json.Valid(raw) {
			raw = []byte(fmt.Sprintf(`{"truncated":true,"size":%d}`, full))
		} else {
			raw = append([]byte(nil), raw...)
		}
	}
	e := networkEvent{Name: name, Payload: raw, TS: time.Now()}
	s.mu.Lock()
	e = s.ring.add(e) // add 返回带全局 Seq 的事件，扇出的副本必须与 ring 一致
	for sub := range s.subs {
		select {
		case sub.ch <- e:
		default:
			// 订阅者消费不过来就丢弃该事件（fire-and-forget 语义）。
		}
	}
	s.mu.Unlock()
}

// newNetworkEventSink 构造 sink；监听非回环地址时必须提供 token，否则拒绝。
// 注意：host 为空的地址（如 ":39876"）会被 net.Listen 绑定到所有网卡，
// 因此空 host 必须按非回环处理（fail-closed），不能放行。
func newNetworkEventSink(addr, token string, history int) (*networkEventSink, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid network addr %q: %w", addr, err)
	}
	loopback := host == "127.0.0.1" || host == "::1" || host == "localhost"
	if !loopback && token == "" {
		return nil, errors.New("ALLY_NETWORK_TOKEN is required when listening on a non-loopback address")
	}
	return &networkEventSink{
		addr:  addr,
		token: token,
		subs:  map[*eventSubscriber]struct{}{},
		ring:  newEventRing(history),
	}, nil
}

// start 监听 addr 并注册 HTTP handler；ctx 取消时关闭服务器。
func (s *networkEventSink) start(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.addr = ln.Addr().String() // 实际绑定地址（传 :0 时是随机端口）
	mux := http.NewServeMux()
	mux.HandleFunc("/events", s.handleSSE)
	mux.HandleFunc("/poll", s.handlePoll)
	mux.HandleFunc("/healthz", s.handleHealthz)
	s.server = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		s.close()
	}()
	go func() {
		if err := s.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("network event sink serve error on %s: %v", s.addr, err)
		}
	}()
	return nil
}

// close 关闭服务器；活跃 SSE 连接由 http.Server.Close 断开，handler 的
// r.Context() 随之取消并自行 unsubscribe，无需显式关闭订阅 channel。
// closeOnce 保证幂等，避免 start 与 ctx goroutine 双路径竞争。
func (s *networkEventSink) close() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.server != nil {
			_ = s.server.Close()
		}
	})
}

// newNetworkEventSinkFromEnv 按环境变量创建并启动 sink；未启用时返回 nil。
// 启动失败（如端口占用）只记录日志并跳过，不破坏桌面端功能。
func newNetworkEventSinkFromEnv(ctx context.Context) *networkEventSink {
	if os.Getenv("ALLY_NETWORK_EVENTS") != "1" {
		return nil
	}
	addr := os.Getenv("ALLY_NETWORK_ADDR")
	if addr == "" {
		addr = defaultNetworkAddr
	}
	s, err := newNetworkEventSink(addr, os.Getenv("ALLY_NETWORK_TOKEN"), networkHistoryDefault)
	if err != nil {
		log.Printf("network event sink disabled: %v", err)
		return nil
	}
	if err := s.start(ctx); err != nil {
		log.Printf("network event sink failed to start on %s: %v", addr, err)
		return nil
	}
	// 打实际绑定地址：ALLY_NETWORK_ADDR=127.0.0.1:0 时用户需要真实端口。
	log.Printf("network event sink listening on %s (SSE /events, poll /poll)", s.addr)
	return s
}

func (s *networkEventSink) authOK(r *http.Request) bool {
	if s.token == "" {
		return true
	}
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) == 1
}

func (s *networkEventSink) handleSSE(w http.ResponseWriter, r *http.Request) {
	if !s.authOK(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	// 并发连接上限：慢客户端不读会让 writeSSE 阻塞，长期占用 goroutine。
	s.mu.Lock()
	if s.sses >= maxSSEConnections {
		s.mu.Unlock()
		http.Error(w, "too many connections", http.StatusServiceUnavailable)
		return
	}
	s.sses++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.sses--
		s.mu.Unlock()
	}()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// 先订阅再补发历史：补发与订阅之间发出的事件会进入 channel，
	// 由 seq 去重（< 回放下限的跳过），避免回放快照与订阅之间的丢事件窗口。
	// 回放下限用 since 返回的 next（而非 0）：空 ring 时 next=0，首个
	// 实时事件 seq=0 不会被误判为重复而丢弃。
	sub := s.subscribe()
	defer s.unsubscribe(sub)
	skipBefore := uint64(0)
	if events, next := s.ring.since(parseSeq(r.URL.Query().Get("since"))); len(events) > 0 {
		skipBefore = next
		for _, e := range events {
			if writeSSE(w, e) != nil {
				return
			}
		}
		flusher.Flush()
	}
	ticker := time.NewTicker(SSEKeepaliveInterval)
	defer ticker.Stop()
	for {
		select {
		case e := <-sub.ch:
			if e.Seq < skipBefore {
				continue // 回放与订阅重叠区间的事件已补发过，跳过。
			}
			if writeSSE(w, e) != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			// SSE 注释行心跳，防止代理/客户端超时断开。
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *networkEventSink) handlePoll(w http.ResponseWriter, r *http.Request) {
	if !s.authOK(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	since := parseSeq(r.URL.Query().Get("since"))
	events, next := s.ring.since(since)
	writeJSON(w, map[string]any{"events": events, "next": next})
}

func (s *networkEventSink) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok")
}

// writeSSE 写一条 SSE 消息：event: 行放事件名，data: 行放 JSON 信封。
// json.Marshal 会把字符串内的换行转义为 \n，data 行本身不会含裸换行，
// 因此 data 无需额外转义；event 名必须清洗换行（SSE 字段行不允许）。
func writeSSE(w io.Writer, e networkEvent) error {
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	name := strings.NewReplacer("\r", "", "\n", "").Replace(e.Name)
	if name == "" {
		name = "message"
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data)
	return err
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	// Encode 失败（如超大/非法值）仅影响本次响应，返回 200 空 body；
	// 调用方已通过 json.Marshal 校验过事件，此路径极少触发。
	_ = json.NewEncoder(w).Encode(v)
}

func parseSeq(v string) uint64 {
	// 非法/空输入静默降级为 0（全量拉取），不报错不 panic。
	n, _ := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
	return n
}

// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- eventRing ----

func TestEventRingAddAndSince(t *testing.T) {
	r := newEventRing(3)
	for i := 0; i < 5; i++ {
		r.add(networkEvent{Name: "e", Payload: json.RawMessage(`{}`), TS: time.Now()})
	}
	// 超过容量：旧事件被覆盖，序号仍单调。
	ev, next := r.since(0)
	if len(ev) != 3 {
		t.Fatalf("since(0) = %d events, want 3", len(ev))
	}
	if ev[0].Seq != 2 || ev[2].Seq != 4 {
		t.Fatalf("seq order wrong: %d..%d", ev[0].Seq, ev[2].Seq)
	}
	if next != 5 {
		t.Fatalf("next = %d, want 5", next)
	}
	// since(3) 返回 seq 3、4（>= 3）。
	ev, _ = r.since(3)
	if len(ev) != 2 || ev[0].Seq != 3 || ev[1].Seq != 4 {
		t.Fatalf("since(3) = %+v, want [seq3 seq4]", ev)
	}
	// 空 / 最新 seq。
	ev, next = r.since(5)
	if len(ev) != 0 || next != 5 {
		t.Fatalf("since(5) = %v/%d, want empty/5", ev, next)
	}
}

func TestEventRingEmpty(t *testing.T) {
	r := newEventRing(3)
	ev, next := r.since(0)
	if len(ev) != 0 || next != 0 {
		t.Fatalf("empty ring: %v/%d", ev, next)
	}
}

// ---- fanoutEventSink ----

type recordingSink struct {
	mu   sync.Mutex
	got  []string
	done chan struct{}
}

func (r *recordingSink) Emit(name string, _ any) {
	r.mu.Lock()
	r.got = append(r.got, name)
	r.mu.Unlock()
	if r.done != nil {
		select {
		case r.done <- struct{}{}:
		default:
		}
	}
}

func (r *recordingSink) names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.got...)
}

// 一个下游 panic 不应影响其他下游（Emit 串行调用，panic 会冒泡，这里验证正常路径）。
func TestFanoutEventSinkBroadcasts(t *testing.T) {
	f := newFanoutEventSink()
	a := &recordingSink{done: make(chan struct{}, 8)}
	b := &recordingSink{done: make(chan struct{}, 8)}
	f.Add(a)
	f.Add(b)
	f.Add(nil) // nil 应被忽略
	f.Emit("run:stream", map[string]any{"x": 1})
	select {
	case <-a.done:
	case <-time.After(time.Second):
		t.Fatal("sink a did not receive event")
	}
	if got := a.names(); len(got) != 1 || got[0] != "run:stream" {
		t.Fatalf("sink a got %v", got)
	}
	if got := b.names(); len(got) != 1 || got[0] != "run:stream" {
		t.Fatalf("sink b got %v", got)
	}
}

func TestFanoutEventSinkSnapshotOnEmit(t *testing.T) {
	f := newFanoutEventSink()
	a := &recordingSink{done: make(chan struct{}, 8)}
	f.Add(a)
	// 用一次同步 emit 触发，然后立刻 add，再 emit。
	f.Emit("first", nil)
	late := &recordingSink{done: make(chan struct{}, 8)}
	f.Add(late)
	f.Emit("second", nil)
	if len(late.names()) != 1 || late.names()[0] != "second" {
		t.Fatalf("late sink got %v, want only second", late.names())
	}
}

// 一个下游 panic 不应影响其他下游，也不应打穿调用方 goroutine。
type panickingSink struct{}

func (panickingSink) Emit(_ string, _ any) { panic("boom") }

func TestFanoutEventSinkIsolatesPanic(t *testing.T) {
	f := newFanoutEventSink()
	f.Add(panickingSink{})
	a := &recordingSink{done: make(chan struct{}, 8)}
	f.Add(a)
	f.Emit("run:start", nil) // 不应 panic，后续 sink 应收到
	select {
	case <-a.done:
	default:
		t.Fatal("sink after panicking sink did not receive event")
	}
}

// ---- networkEventSink 集成（真实 HTTP）----

func startTestSink(t *testing.T, addr, token string) *networkEventSink {
	t.Helper()
	s, err := newNetworkEventSink(addr, token, 10)
	if err != nil {
		t.Fatalf("newNetworkEventSink: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := s.start(ctx); err != nil {
		cancel()
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		s.close()
		cancel()
	})
	return s
}

func TestNewNetworkEventSinkRejectsNonLoopbackWithoutToken(t *testing.T) {
	// 非回环地址必须带 token（fail closed）。
	if _, err := newNetworkEventSink("0.0.0.0:39876", "", 10); err == nil {
		t.Fatal("expected error for non-loopback without token")
	}
	if _, err := newNetworkEventSink("127.0.0.1:0", "", 10); err != nil {
		t.Fatalf("loopback without token should be allowed: %v", err)
	}
	// 空 host 会绑定所有网卡（net.Listen 通配），必须按非回环处理（fail-closed）。
	if _, err := newNetworkEventSink(":39876", "", 10); err == nil {
		t.Fatal("expected error for empty-host address without token")
	}
	if _, err := newNetworkEventSink("0.0.0.0:39876", "secret", 10); err != nil {
		t.Fatalf("non-loopback with token should be allowed: %v", err)
	}
}

func TestNetworkEventSinkPollAndAuth(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // 让 sink 重新监听同一地址

	s := startTestSink(t, addr, "tok123")
	s.Emit("run:start", map[string]any{"a": 1})
	s.Emit("tool:result", map[string]any{"b": 2})

	// 无 token -> 401
	resp, err := http.Get("http://" + addr + "/poll?since=0")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: status %d, want 401", resp.StatusCode)
	}

	// 带 token -> 200，返回全部事件
	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/poll?since=0", nil)
	req.Header.Set("Authorization", "Bearer tok123")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("with token: status %d", resp.StatusCode)
	}
	var body struct {
		Events []networkEvent `json:"events"`
		Next   uint64         `json:"next"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Events) != 2 || body.Next != 2 {
		t.Fatalf("poll = %d events, next %d; want 2/2", len(body.Events), body.Next)
	}
	if body.Events[0].Name != "run:start" {
		t.Fatalf("first event name = %q", body.Events[0].Name)
	}
}

func TestNetworkEventSinkSSEStreamsAndHistory(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	s := startTestSink(t, addr, "")
	// 先产生一条历史，连接时 since=0 应补发。
	s.Emit("run:start", map[string]any{"a": 1})

	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/events?since=0", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type = %q", ct)
	}

	// 异步发一条实时事件。
	go func() {
		time.Sleep(100 * time.Millisecond)
		s.Emit("run:stream", map[string]any{"b": 2})
	}()

	reader := bufio.NewReader(resp.Body)
	deadline := time.Now().Add(3 * time.Second)
	var sawStart, sawStream bool
	for time.Now().Before(deadline) {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("read: %v", err)
		}
		if strings.HasPrefix(line, "event: ") {
			if strings.TrimSpace(strings.TrimPrefix(line, "event: ")) == "run:start" {
				sawStart = true
			}
			if strings.TrimSpace(strings.TrimPrefix(line, "event: ")) == "run:stream" {
				sawStream = true
			}
		}
		if sawStart && sawStream {
			break
		}
	}
	if !sawStart {
		t.Fatal("SSE did not replay history run:start")
	}
	if !sawStream {
		t.Fatal("SSE did not deliver live run:stream")
	}
}

func TestNetworkEventSinkHealthz(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	_ = startTestSink(t, addr, "")
	resp, err := http.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz status = %d", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if string(b) != "ok" {
		t.Fatalf("healthz body = %q", string(b))
	}
}

// 大 payload 截断后必须是合法 JSON（不能切断 UTF-8/token 毒化 ring）。
func TestNetworkEventSinkTruncatesLargePayloadToValidJSON(t *testing.T) {
	s, err := newNetworkEventSink("127.0.0.1:0", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("a", maxNetworkPayloadBytes*2)
	s.Emit("tool:result", map[string]any{"output": big})
	ev, _ := s.ring.since(0)
	if len(ev) != 1 {
		t.Fatalf("ring has %d events, want 1", len(ev))
	}
	if len(ev[0].Payload) > maxNetworkPayloadBytes {
		t.Fatalf("payload not truncated: %d bytes", len(ev[0].Payload))
	}
	if !json.Valid(ev[0].Payload) {
		t.Fatalf("payload is invalid JSON after truncation: %s", string(ev[0].Payload[:min(120, len(ev[0].Payload))]))
	}
	// 截断哨兵：超大 payload 被替换为合法标记，客户端可感知。
	var probe map[string]any
	if err := json.Unmarshal(ev[0].Payload, &probe); err != nil {
		t.Fatalf("truncated payload unmarshal: %v", err)
	}
	if probe["truncated"] != true {
		t.Fatalf("expected truncated marker, got %v", probe)
	}
}

// 空 ring 时连接，首个实时事件（seq 0）不能被去重逻辑误删。
func TestNetworkEventSinkSSEFirstLiveEventNotDropped(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	s := startTestSink(t, addr, "")
	// 连接时 ring 为空（未发任何事件）。
	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/events?since=0", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// 连接建立后发第一条事件（seq 0）。
	go func() {
		time.Sleep(100 * time.Millisecond)
		s.Emit("run:start", map[string]any{"a": 1})
	}()

	reader := bufio.NewReader(resp.Body)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("read: %v", err)
		}
		if strings.HasPrefix(line, "event: run:start") {
			return // 首事件送达
		}
	}
	t.Fatal("first live event (seq 0) was dropped")
}

// SSE 并发连接数达到上限后，新连接应被拒绝（503）。
func TestNetworkEventSinkSSEConnectionLimit(t *testing.T) {
	s, err := newNetworkEventSink("127.0.0.1:0", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := s.start(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	defer func() { s.close(); cancel() }()

	// 占满连接上限。
	s.mu.Lock()
	s.sses = maxSSEConnections
	s.mu.Unlock()

	req, _ := http.NewRequest(http.MethodGet, "http://"+s.addr+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

// newNetworkEventSinkFromEnv：未启用返回 nil；启用后正常监听。
func TestNetworkEventSinkFromEnv(t *testing.T) {
	t.Setenv("ALLY_NETWORK_EVENTS", "")
	t.Setenv("ALLY_NETWORK_ADDR", "")
	t.Setenv("ALLY_NETWORK_TOKEN", "")
	if s := newNetworkEventSinkFromEnv(context.Background()); s != nil {
		t.Fatal("disabled env should return nil sink")
	}

	t.Setenv("ALLY_NETWORK_EVENTS", "1")
	t.Setenv("ALLY_NETWORK_ADDR", "127.0.0.1:0")
	ctx, cancel := context.WithCancel(context.Background())
	s := newNetworkEventSinkFromEnv(ctx)
	if s == nil {
		cancel()
		t.Fatal("enabled env should start sink")
	}
	defer func() { s.close(); cancel() }()
	if s.addr == "" {
		t.Fatal("sink did not bind address")
	}
}

// 订阅者 channel 满时 Emit 必须丢弃新事件而非阻塞（fire-and-forget 核心保证）。
func TestNetworkEventSinkEmitDoesNotBlockWhenSubscriberFull(t *testing.T) {
	s, err := newNetworkEventSink("127.0.0.1:0", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	sub := s.subscribe()
	for i := 0; i < subscriberQueueSize; i++ {
		sub.ch <- networkEvent{Name: "x"}
	}
	// 填满后再 Emit：必须在有限时间内返回（不阻塞）。
	done := make(chan struct{})
	go func() {
		s.Emit("run:start", map[string]any{"a": 1})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Emit blocked on full subscriber channel")
	}
}

// writeSSE 必须清洗事件名里的 CR/LF，防止破坏 SSE 协议字段行；
// data 行是完整信封 JSON（含 name/payload/seq/ts）。
func TestWriteSSESanitizesEventName(t *testing.T) {
	var buf strings.Builder
	e := networkEvent{Name: "run:\nstream\r", Payload: json.RawMessage(`{"a":1}`), Seq: 1}
	if err := writeSSE(&buf, e); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "\nstream") || strings.Contains(out, "\r") {
		t.Fatalf("event name not sanitized: %q", out)
	}
	if !strings.Contains(out, "event: run:stream") {
		t.Fatalf("expected sanitized event name, got %q", out)
	}
	if !strings.Contains(out, `"payload":{"a":1}`) {
		t.Fatalf("data line missing payload: %q", out)
	}
}

// 端到端链路：App.emit → fanoutEventSink → networkEventSink → HTTP /poll。
// 验证核心事件发射边界与网络出口的真实贯穿（ServiceStartup 之外的最小装配）。
func TestNetworkEventSinkEndToEndViaAppEmit(t *testing.T) {
	// 启动真实网络 sink（:0 随机端口）。
	s, err := newNetworkEventSink("127.0.0.1:0", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := s.start(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	defer func() { s.close(); cancel() }()

	// 最小装配：App.events = fanout(网络 sink)，等价于 ServiceStartup 的注入。
	fan := newFanoutEventSink()
	fan.Add(s)
	app := &App{events: fan}

	// 经核心事件边界发射。
	app.emit("run:start", map[string]any{"session": "s1"})

	// 经 /poll 断言收到。
	resp, err := http.Get("http://" + s.addr + "/poll?since=0")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("poll status = %d", resp.StatusCode)
	}
	var body struct {
		Events []networkEvent `json:"events"`
		Next   uint64         `json:"next"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Events) != 1 || body.Events[0].Name != "run:start" {
		t.Fatalf("events = %+v, want one run:start", body.Events)
	}
	var payload map[string]any
	if err := json.Unmarshal(body.Events[0].Payload, &payload); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if payload["session"] != "s1" {
		t.Fatalf("payload = %v, want session=s1", payload)
	}
}

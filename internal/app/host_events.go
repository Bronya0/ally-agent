package app

import (
	"log"
	"sync"
)

// eventSink is the narrow boundary from Agent/runtime code to a host UI.
// The core only publishes an event name and payload; Wails is one adapter.
type eventSink interface {
	Emit(name string, payload any)
}

// appEventSink lets long-lived runtime modules publish through App's host
// adapter without depending on Wails runtime APIs themselves.
type appEventSink struct {
	app *App
}

func (s appEventSink) Emit(name string, payload any) {
	if s.app != nil {
		s.app.emit(name, payload)
	}
}

// emit is the sole event publication boundary used by Agent/runtime modules.
// A host adapter may be absent in tests or future headless embeddings.
func (a *App) emit(name string, payload any) {
	if a.events == nil {
		return
	}
	a.events.Emit(name, payload)
}

// fanoutEventSink 把同一事件串行投递给多个下游（桌面 Wails + 可选网络端）。
// 下游 panic 被隔离（不打断 chat loop），慢消费由各下游自行保证不阻塞。
// 用法：保留原 a.events（Wails），另挂网络 sink。
type fanoutEventSink struct {
	mu   sync.Mutex
	subs []eventSink
}

func newFanoutEventSink() *fanoutEventSink {
	return &fanoutEventSink{}
}

func (f *fanoutEventSink) Add(s eventSink) {
	if s == nil {
		return
	}
	f.mu.Lock()
	f.subs = append(f.subs, s)
	f.mu.Unlock()
}

func (f *fanoutEventSink) Emit(name string, payload any) {
	f.mu.Lock()
	subs := make([]eventSink, len(f.subs))
	copy(subs, f.subs)
	f.mu.Unlock()
	for _, s := range subs {
		// 下游 panic（如第三方 adapter 的 bug）必须隔离，不能打穿
		// chat loop 等核心 goroutine；但 panic 不能静默吞掉，要记日志。
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("fanout event sink: downstream %T panicked on %q: %v", s, name, r)
				}
			}()
			s.Emit(name, payload)
		}()
	}
}

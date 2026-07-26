package main

import (
	"context"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// eventSink is the narrow boundary from Agent/runtime code to a host UI.
// The core only publishes an event name and payload; Wails is one adapter.
type eventSink interface {
	Emit(name string, payload any)
}

// wailsEventSink is the desktop adapter used by the Wails-bound App.
type wailsEventSink struct {
	ctx context.Context
}

func (s wailsEventSink) Emit(name string, payload any) {
	if s.ctx == nil || s.ctx.Err() != nil {
		return
	}
	wruntime.EventsEmit(s.ctx, name, payload)
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

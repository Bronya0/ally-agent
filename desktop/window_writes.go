package desktop

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

const windowWriteTimeout = 15 * time.Second

// Workers report errors before closing their ordered queue tail. A successful
// retry replaces the failure for that resource; consumed errors cannot poison
// every later attempt to quit/update.
type windowWriteFailures struct {
	mu    sync.Mutex
	items map[string]error
}

func (w *windowWriteFailures) record(resource string, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err == nil {
		delete(w.items, resource)
		return
	}
	if w.items == nil {
		w.items = map[string]error{}
	}
	w.items[resource] = fmt.Errorf("%s: %w", resource, err)
}

func (w *windowWriteFailures) take() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	keys := make([]string, 0, len(w.items))
	for key := range w.items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var errs []error
	for _, key := range keys {
		errs = append(errs, w.items[key])
	}
	w.items = nil
	return errors.Join(errs...)
}

func waitWindowWrites(ctx context.Context, tails []<-chan struct{}, failures *windowWriteFailures) error {
	for _, tail := range tails {
		if tail == nil {
			continue
		}
		select {
		case <-tail:
		case <-ctx.Done():
			return fmt.Errorf("等待保存超时，尚未退出：%w", ctx.Err())
		}
	}
	return failures.take()
}

// Capture snapshots/tails on the UI thread, wait only in a worker, and publish
// the outcome back to the UI. Both quit and update use this barrier.
func (a *desktop) flushWindowWrites(done func(error)) {
	if a.page == "settings" {
		a.saveSettings()
	}
	a.savePrefs()
	seen := map[string]bool{}
	store := func(t *conversation) {
		if t == nil || seen[t.ID] {
			return
		}
		seen[t.ID] = true
		if len(t.Messages) > 0 || t.Draft != "" || t.SnapshotQueued {
			a.persist(t)
		}
	}
	for _, t := range a.tabs {
		store(t)
	}
	for _, t := range a.openSessions {
		store(t)
	}
	tails := []<-chan struct{}{a.configSaveTail, a.persistTail, a.prefsSaveTail, a.sshAuthTail}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), windowWriteTimeout)
		defer cancel()
		err := waitWindowWrites(ctx, tails, &a.writeFailures)
		a.win.Update(func() { done(err) })
	}()
}

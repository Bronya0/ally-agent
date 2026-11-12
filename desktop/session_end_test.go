package desktop

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

// The session-end listener may not hand the writes to a worker: the OS ends the
// process as soon as it returns, so the barrier has to wait on this thread and
// still honour its own deadline.
func TestSessionEndFlushWaitsOnTheCallingThread(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a := &desktop{}
		a.sshAuthTail = make(chan struct{})
		start := time.Now()
		err := a.flushWindowWritesNow(sessionEndWriteTimeout)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("flush = %v, want the deadline", err)
		}
		if waited := time.Since(start); waited < sessionEndWriteTimeout {
			t.Fatalf("returned after %v without waiting for the queued write", waited)
		}
	})
}

// Everything already written means nothing to wait for: logout must not stall
// for the whole budget when the queues are empty.
func TestSessionEndFlushReturnsAtOnceWhenNothingIsQueued(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a := &desktop{}
		done := make(chan struct{})
		a.prefsSaveTail = done
		close(done)
		start := time.Now()
		if err := a.flushWindowWritesNow(sessionEndWriteTimeout); err != nil {
			t.Fatalf("flush = %v", err)
		}
		if waited := time.Since(start); waited != 0 {
			t.Fatalf("waited %v with no pending writes", waited)
		}
	})
}

// SPDX-License-Identifier: GPL-3.0-only
//go:build windows

package screenshot

import (
	"runtime"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestMonitorEnumerationDoesNotExhaustCallbackSlots(t *testing.T) {
	// This used to terminate the entire test process with the Go runtime's
	// "too many callback functions" fatal error. No screen pixels are captured.
	for i := 0; i < 2050; i++ {
		enumDisplayRects()
	}
}

func TestMonitorEnumerationHasIndependentConcurrentState(t *testing.T) {
	want := len(enumDisplayRects())
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 16; j++ {
				if got := len(enumDisplayRects()); got != want {
					t.Errorf("enumerations shared their collectors: got %d monitors, want %d", got, want)
				}
			}
		}()
	}
	workers.Wait()
}

func TestDPIAwarenessStaysOnThreadAndRestoresAfterPanic(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	getContext := user32.NewProc("GetThreadDpiAwarenessContext")
	if err := getContext.Find(); err != nil {
		t.Skip(err)
	}
	before, _, _ := getContext.Call()
	func() {
		defer func() {
			if recover() != "dpi test" {
				t.Error("unexpected panic")
			}
		}()
		setThreadDpiAware(func() {
			thread := windows.GetCurrentThreadId()
			for i := 0; i < 20; i++ {
				time.Sleep(time.Millisecond)
				runtime.Gosched()
				if windows.GetCurrentThreadId() != thread {
					t.Error("DPI work migrated to a different OS thread")
				}
			}
			panic("dpi test")
		})
	}()
	after, _, _ := getContext.Call()
	if after != before {
		t.Fatalf("thread DPI state leaked after panic: before=%v after=%v", before, after)
	}
}

func TestDPIAwarenessPinsUnboundGoroutine(t *testing.T) {
	setThreadDpiAware(func() {
		thread := windows.GetCurrentThreadId()
		for i := 0; i < 30; i++ {
			time.Sleep(time.Millisecond)
			runtime.Gosched()
			if windows.GetCurrentThreadId() != thread {
				t.Fatal("DPI work migrated to a different OS thread")
			}
		}
	})
}

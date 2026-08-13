// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"context"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestInjectRunMessageRequiresText(t *testing.T) {
	app := NewApp()
	if err := app.InjectRunMessage("run-1", "   "); err == nil {
		t.Fatal("InjectRunMessage(blank) error = nil, want error")
	}
}

func TestInjectRunMessageUnknownRun(t *testing.T) {
	app := NewApp()
	if err := app.InjectRunMessage("run-missing", "hello"); err == nil {
		t.Fatal("InjectRunMessage(unknown run) error = nil, want error")
	}
}

func TestInjectRunMessageQueueFull(t *testing.T) {
	app := NewApp()
	runID := "run-full"
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	app.mu.Lock()
	app.runs[runID] = cancel
	app.runInputs[runID] = make(chan string, 2)
	app.mu.Unlock()

	// Fill the queue directly, then a normal call must fail fast instead of
	// blocking the frontend.
	app.mu.Lock()
	app.runInputs[runID] <- "a"
	app.runInputs[runID] <- "b"
	app.mu.Unlock()

	if err := app.InjectRunMessage(runID, "c"); err == nil {
		t.Fatal("InjectRunMessage(queue full) error = nil, want error")
	}
}

func TestInjectRunMessageAfterFinishRun(t *testing.T) {
	app := NewApp()
	runID := "run-finished"
	app.mu.Lock()
	app.runInputs[runID] = make(chan string, 2)
	app.mu.Unlock()

	app.finishRun(runID)
	if err := app.InjectRunMessage(runID, "x"); err == nil {
		t.Fatal("InjectRunMessage(after finishRun) error = nil, want error")
	}
}

func TestAppendPendingRunInputsEmpty(t *testing.T) {
	app := NewApp()
	runID := "run-empty"
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	app.mu.Lock()
	app.runs[runID] = cancel
	app.runInputs[runID] = make(chan string, runInputBufferSize)
	app.mu.Unlock()

	messages := []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleAssistant, Content: "ok"}}
	out, injected := app.appendPendingRunInputs(runID, messages)
	if injected {
		t.Fatal("appendPendingRunInputs(empty queue) injected = true, want false")
	}
	if len(out) != len(messages) {
		t.Fatalf("appendPendingRunInputs(empty queue) = %d messages, want %d", len(out), len(messages))
	}
}

func TestAppendPendingRunInputsOrder(t *testing.T) {
	app := NewApp()
	runID := "run-order"
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	app.mu.Lock()
	app.runs[runID] = cancel
	app.runSessions[runID] = "session-order"
	app.runInputs[runID] = make(chan string, runInputBufferSize)
	app.mu.Unlock()

	if err := app.InjectRunMessage(runID, "first"); err != nil {
		t.Fatalf("InjectRunMessage() error = %v", err)
	}
	if err := app.InjectRunMessage(runID, "second"); err != nil {
		t.Fatalf("InjectRunMessage() error = %v", err)
	}

	base := []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleAssistant, Content: "prior"}}
	out, injected := app.appendPendingRunInputs(runID, base)
	if !injected {
		t.Fatal("appendPendingRunInputs(queued) injected = false, want true")
	}
	if len(out) != 3 {
		t.Fatalf("appendPendingRunInputs() = %d messages, want 3", len(out))
	}
	for i, want := range []string{"first", "second"} {
		msg := out[len(base)+i]
		if msg.Role != openai.ChatMessageRoleUser || msg.Content != want {
			t.Fatalf("injected message %d = %+v, want user %q", i, msg, want)
		}
	}
	// The queue is drained: a second call injects nothing.
	if _, injected := app.appendPendingRunInputs(runID, out); injected {
		t.Fatal("appendPendingRunInputs(after drain) injected = true, want false")
	}
}

func TestAppendPendingRunInputsAfterFinishRun(t *testing.T) {
	app := NewApp()
	runID := "run-gone"
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	app.mu.Lock()
	app.runs[runID] = cancel
	app.runInputs[runID] = make(chan string, runInputBufferSize)
	app.mu.Unlock()
	app.finishRun(runID)

	base := []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "keep"}}
	out, injected := app.appendPendingRunInputs(runID, base)
	if injected || len(out) != 1 || out[0].Content != "keep" {
		t.Fatalf("appendPendingRunInputs(after finish) = injected %v, %d messages; want false, 1", injected, len(out))
	}
}

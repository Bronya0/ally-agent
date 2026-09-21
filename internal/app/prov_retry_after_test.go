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
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestParseRetryAfterValue(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"  ", 0},
		{"0", 0},
		{"-5", 0},
		{"30", 30 * time.Second},
		{" 120 ", 120 * time.Second},
		{"garbage", 0},
		{"Mon, 02 Jan 2006 15:04:05 GMT", 0}, // past date
	}
	for _, tc := range cases {
		if got := parseRetryAfterValue(tc.in); got != tc.want {
			t.Errorf("parseRetryAfterValue(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
	future := time.Now().Add(90 * time.Second).UTC().Format(http.TimeFormat)
	got := parseRetryAfterValue(future)
	if got <= 0 || got > 90*time.Second {
		t.Errorf("future HTTP-date must fold to a positive remaining duration ≤90s, got %v", got)
	}
}

func TestLLMRetryDelayForError(t *testing.T) {
	if got := llmRetryDelayForError(1, nil); got != 500*time.Millisecond {
		t.Fatalf("no error must keep the base backoff, got %v", got)
	}
	if got := llmRetryDelayForError(1, errors.New("boom")); got != 500*time.Millisecond {
		t.Fatalf("error without Retry-After must keep the base backoff, got %v", got)
	}
	// Server advice shorter than the local curve: keep the local backoff.
	short := &retryAfterError{inner: errors.New("rate limited"), after: 200 * time.Millisecond}
	if got := llmRetryDelayForError(1, short); got != 500*time.Millisecond {
		t.Fatalf("short Retry-After must not shorten the base backoff, got %v", got)
	}
	// Server advice longer: honor it…
	long := &retryAfterError{inner: errors.New("rate limited"), after: 30 * time.Second}
	if got := llmRetryDelayForError(1, long); got != 30*time.Second {
		t.Fatalf("long Retry-After must be honored, got %v", got)
	}
	// …but never above the ceiling.
	huge := &retryAfterError{inner: errors.New("rate limited"), after: 10 * time.Minute}
	if got := llmRetryDelayForError(1, huge); got != maxRetryAfterWait {
		t.Fatalf("Retry-After above the ceiling must be capped at %v, got %v", maxRetryAfterWait, got)
	}
	// Extraction must survive outer wrappers that only add identity.
	wrapped := markUpstreamError(fmt.Errorf("wrapped: %w", long))
	if got := llmRetryDelayForError(2, wrapped); got != 30*time.Second {
		t.Fatalf("Retry-After must survive identity-only wrappers, got %v", got)
	}
	// The wrapper must not change the error text the keyword classifiers read.
	if long.Error() != "rate limited" {
		t.Fatalf("retryAfterError must preserve the inner message, got %q", long.Error())
	}
}

// roundTripFunc adapts a closure into an http.RoundTripper for transport tests.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestRetryAfterCaptureTransport(t *testing.T) {
	cap := &retryAfterCapture{}
	ctx := context.WithValue(context.Background(), retryAfterCaptureKey{}, cap)
	tr := &retryAfterCaptureTransport{base: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if strings.HasSuffix(req.URL.Path, "/limited") {
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Header:     http.Header{"Retry-After": []string{"25"}},
				Body:       http.NoBody,
			}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: http.NoBody}, nil
	})}

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://relay.example/ok", nil)
	if _, err := tr.RoundTrip(req); err != nil {
		t.Fatalf("roundtrip: %v", err)
	}
	if got := cap.get(); got != 0 {
		t.Fatalf("2xx without Retry-After must not be captured, got %v", got)
	}

	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, "https://relay.example/limited", nil)
	if _, err := tr.RoundTrip(req); err != nil {
		t.Fatalf("roundtrip: %v", err)
	}
	if got := cap.get(); got != 25*time.Second {
		t.Fatalf("429 Retry-After must be captured, got %v", got)
	}

	// Requests without a capture slot in the ctx pass through untouched.
	plain, _ := http.NewRequest(http.MethodGet, "https://relay.example/limited", nil)
	if _, err := tr.RoundTrip(plain); err != nil {
		t.Fatalf("roundtrip without capture slot: %v", err)
	}
}

func TestIsOfficialAnthropicEndpoint(t *testing.T) {
	cases := []struct {
		baseURL string
		want    bool
	}{
		{"", true}, // empty BaseURL resolves to the official default
		{"https://api.anthropic.com", true},
		{"https://api.anthropic.com/", true},
		{"https://api.anthropic.com/v1", true}, // trailing /v1 is normalized away for the Anthropic format
		{"https://relay.example.com", false},
		{"https://bigmodel.example.com/anthropic", false},
	}
	for _, tc := range cases {
		cfg := ConfigState{APIFormat: apiFormatAnthropicMessages, BaseURL: tc.baseURL}
		if got := isOfficialAnthropicEndpoint(cfg); got != tc.want {
			t.Errorf("isOfficialAnthropicEndpoint(BaseURL=%q) = %v, want %v", tc.baseURL, got, tc.want)
		}
	}
}

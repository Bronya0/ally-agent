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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsDeepSeekOfficialEndpointMatchesOnlyTheOfficialHost(t *testing.T) {
	cases := map[string]bool{
		"https://api.deepseek.com":          true,
		"https://api.deepseek.com/v1":       true,
		"https://api.deepseek.com/v1/":      true,
		"http://API.DeepSeek.com/beta":      true,
		"https://api.deepseek.com.cn/v1":    false,
		"https://api.deepseek.com.evil.dev": false,
		"https://my-relay.example.com/v1":   false,
		"https://deepseek.com/v1":           false,
		"http://localhost:11434/v1":         false,
		"":                                  false,
		"not a url":                         false,
	}
	for base, want := range cases {
		if got := IsDeepSeekOfficialEndpoint(base); got != want {
			t.Errorf("IsDeepSeekOfficialEndpoint(%q) = %v, want %v", base, got, want)
		}
	}
}

func TestDeepSeekBalanceEndpointDropsBasePaths(t *testing.T) {
	cases := map[string]string{
		"https://api.deepseek.com":      "https://api.deepseek.com/user/balance",
		"https://api.deepseek.com/":     "https://api.deepseek.com/user/balance",
		"https://api.deepseek.com/v1":   "https://api.deepseek.com/user/balance",
		"https://api.deepseek.com/v1/":  "https://api.deepseek.com/user/balance",
		"https://api.deepseek.com/beta": "https://api.deepseek.com/user/balance",
	}
	for base, want := range cases {
		got, err := deepSeekBalanceEndpoint(base)
		if err != nil {
			t.Fatalf("deepSeekBalanceEndpoint(%q) returned %v", base, err)
		}
		if got != want {
			t.Errorf("deepSeekBalanceEndpoint(%q) = %q, want %q", base, got, want)
		}
	}
	if _, err := deepSeekBalanceEndpoint("https://relay.example.com/v1"); err == nil {
		t.Fatal("a relay endpoint must not resolve to a balance URL")
	}
}

func TestParseDeepSeekBalanceReadsCurrencyLines(t *testing.T) {
	body := []byte(`{"is_available":true,"balance_infos":[
		{"currency":"CNY","total_balance":"110.00","granted_balance":"10.00","topped_up_balance":"100.00"},
		{"currency":"USD","total_balance":"5.50","granted_balance":"0.00","topped_up_balance":"5.50"}
	]}`)
	got, err := parseDeepSeekBalance(body)
	if err != nil {
		t.Fatalf("parseDeepSeekBalance returned %v", err)
	}
	want := []ModelBalance{
		{Currency: "CNY", Total: "110.00"},
		{Currency: "USD", Total: "5.50"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d currency lines, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("balance line %d = %+v, want %+v", i, got[i], want[i])
		}
	}

	if _, err := parseDeepSeekBalance([]byte(`{"is_available":false,"balance_infos":[]}`)); err == nil {
		t.Error("an empty balance list must be reported, not shown as zero")
	}
	if _, err := parseDeepSeekBalance([]byte(`<html>bad gateway</html>`)); err == nil {
		t.Error("a non-JSON body must be reported")
	}
}

// The query must never reach a non-official host: the button is hidden for it,
// and a direct call has to refuse before any request is made.
func TestQueryModelBalanceRefusesOtherEndpointsAndMissingKeys(t *testing.T) {
	a := &App{}
	if _, err := a.QueryModelBalance(ModelConfig{BaseURL: "https://relay.example.com/v1", Model: "deepseek-chat", APIKey: "sk-test"}); err == nil || !strings.Contains(err.Error(), "api.deepseek.com") {
		t.Fatalf("relay endpoint error = %v, want a refusal naming the official host", err)
	}
	if _, err := a.QueryModelBalance(ModelConfig{BaseURL: "https://api.deepseek.com/v1", Model: "deepseek-chat"}); err == nil || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("keyless query error = %v, want an API key requirement", err)
	}
}

// The request itself: bearer key, JSON accept, and the documented response ->
// the currency lines the card shows.
func TestFetchDeepSeekBalanceSendsBearerKeyAndParsesTheAnswer(t *testing.T) {
	var gotPath, gotAuth, gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth, gotAccept = r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"110.00","granted_balance":"10.00","topped_up_balance":"100.00"}]}`))
	}))
	defer srv.Close()

	got, err := fetchDeepSeekBalance(context.Background(), srv.Client(), ConfigState{}, srv.URL+"/user/balance", "sk-test")
	if err != nil {
		t.Fatalf("fetchDeepSeekBalance returned %v", err)
	}
	if gotPath != "/user/balance" {
		t.Errorf("requested path = %q, want /user/balance", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("Authorization = %q, want the configured key as a bearer token", gotAuth)
	}
	if gotAccept != "application/json" {
		t.Errorf("Accept = %q, want application/json", gotAccept)
	}
	if len(got) != 1 || got[0].Total != "110.00" || got[0].Currency != "CNY" {
		t.Fatalf("balances = %+v, want one CNY line with total 110.00", got)
	}
}

func TestFetchDeepSeekBalanceReportsHTTPFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"invalid api key"}}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := fetchDeepSeekBalance(context.Background(), srv.Client(), ConfigState{}, srv.URL+"/user/balance", "sk-bad")
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("error = %v, want an HTTP 401 report", err)
	}
}

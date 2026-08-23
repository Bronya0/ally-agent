// SPDX-License-Identifier: GPL-3.0-only
package game

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestDeriveAccessTokenStableAndSecretSpecific(t *testing.T) {
	a := deriveAccessToken("a")
	if a == "" || a != deriveAccessToken("a") || a == deriveAccessToken("b") {
		t.Fatalf("unexpected token derivation: %q", a)
	}
}

func TestAllowedOrigin(t *testing.T) {
	for _, origin := range []string{"", "wails://wails", "http://localhost:34115", "http://wails.localhost"} {
		if !allowedOrigin(origin) {
			t.Fatalf("expected origin allowed: %s", origin)
		}
	}
	if allowedOrigin("https://evil.example") {
		t.Fatal("public web origin must be rejected")
	}
}

func TestHubRejectsBadTokenAndRelaysWithServerIdentity(t *testing.T) {
	h := newHub("room", "token")
	mux := http.NewServeMux()
	mux.HandleFunc("/game/ws", h.handleWebSocket)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	badURL := "ws" + srv.URL[4:] + "/game/ws?room=room&token=bad"
	if _, _, err := websocket.Dial(context.Background(), badURL, nil); err == nil {
		t.Fatal("bad token unexpectedly connected")
	}

	goodURL := "ws" + srv.URL[4:] + "/game/ws?room=room&token=token"
	c1, _, err := websocket.Dial(context.Background(), goodURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c1.CloseNow()
	c2, _, err := websocket.Dial(context.Background(), goodURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.CloseNow()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, first, err := c1.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var welcome serverEnvelope
	if json.Unmarshal(first, &welcome) != nil || welcome.Kind != "welcome" || welcome.From == "" {
		t.Fatalf("unexpected welcome: %s", first)
	}
	// Drain the joined event, then send a payload. The relay supplies From;
	// clients cannot forge another peer's identity in the outer envelope.
	_, _, _ = c1.Read(ctx)
	msg := clientEnvelope{Kind: "data", Payload: json.RawMessage(`{"iv":"a","data":"b"}`)}
	if err := c2.Write(ctx, websocket.MessageText, mustJSON(msg)); err != nil {
		t.Fatal(err)
	}
	for {
		_, raw, err := c1.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var got serverEnvelope
		if json.Unmarshal(raw, &got) == nil && got.Kind == "data" {
			if got.From == "" || got.From == welcome.From {
				t.Fatalf("unexpected sender identity: %+v", got)
			}
			break
		}
	}
}

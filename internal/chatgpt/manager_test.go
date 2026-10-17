// SPDX-License-Identifier: GPL-3.0-only
package chatgpt

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

type testProtector struct{ cipher.AEAD }

func (p testProtector) Seal(b []byte) ([]byte, error) {
	n := make([]byte, p.NonceSize())
	_, _ = rand.Read(n)
	return p.AEAD.Seal(n, n, b, nil), nil
}
func (p testProtector) Open(b []byte) ([]byte, error) {
	if len(b) < p.NonceSize() {
		return nil, errors.New("bad ciphertext")
	}
	return p.AEAD.Open(nil, b[:p.NonceSize()], b[p.NonceSize():], nil)
}
func newTestProtector() Protector {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	block, _ := aes.NewCipher(key)
	aead, _ := cipher.NewGCM(block)
	return testProtector{aead}
}

type authFixture struct {
	t                                                             *testing.T
	m                                                             *Manager
	s                                                             *httptest.Server
	key                                                           *rsa.PrivateKey
	nonce, challenge, redirect, subject, clientID, scope, invalid string
	refreshError, lastRefresh                                     string
	refreshCount                                                  atomic.Int32
	modelStatus, revokeStatus                                     int
	seenHost                                                      string
}

func fixture(t *testing.T) *authFixture {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &authFixture{t: t, key: key, subject: "subject-one", clientID: "oaiapp_one", scope: requestedScopes, modelStatus: 200, revokeStatus: 200}
	f.s = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.s.Close)
	f.m = New(filepath.Join(dir, "oauth"), newTestProtector())
	f.m.issuer = f.s.URL
	f.m.resource = f.s.URL + "/v1"
	f.m.loginTimeout = 2 * time.Second
	t.Cleanup(f.m.Close)
	return f
}
func (f *authFixture) jwt() string {
	claims := map[string]any{"iss": f.s.URL, "aud": f.clientID, "sub": f.subject, "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "nonce": f.nonce, "email": "same@example.test"}
	switch f.invalid {
	case "issuer":
		claims["iss"] = "https://wrong.test"
	case "audience":
		claims["aud"] = "wrong"
	case "expired":
		claims["exp"] = time.Now().Add(-time.Hour).Unix()
	case "nonce":
		claims["nonce"] = "wrong"
	}
	signer, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: f.key}, (&jose.SignerOptions{}).WithType("JWT"))
	data, _ := json.Marshal(claims)
	signed, _ := signer.Sign(data)
	raw, _ := signed.CompactSerialize()
	if f.invalid == "signature" {
		raw = raw[:strings.LastIndex(raw, ".")+1] + base64.RawURLEncoding.EncodeToString(make([]byte, 256))
	}
	return raw
}
func (f *authFixture) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": f.s.URL, "authorization_endpoint": f.s.URL + "/api/accounts/authorize", "token_endpoint": f.s.URL + "/api/accounts/oauth/token", "jwks_uri": f.s.URL + "/jwks", "revocation_endpoint": f.s.URL + "/revoke", "id_token_signing_alg_values_supported": []string{"RS256"}})
	case "/jwks":
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &f.key.PublicKey, Use: "sig", Algorithm: "RS256"}}})
	case "/api/accounts/oauth/token":
		_ = r.ParseForm()
		if r.Form.Get("client_id") != f.clientID || r.Form.Get("resource") != f.m.resource {
			f.t.Error("wrong exchange client/resource")
			w.WriteHeader(400)
			return
		}
		if r.Form.Get("grant_type") == "refresh_token" {
			f.refreshCount.Add(1)
			f.lastRefresh = r.Form.Get("refresh_token")
			if r.Form.Get("scope") != "" {
				f.t.Error("refresh must retain scopes")
			}
			if f.refreshError != "" {
				w.WriteHeader(400)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": f.refreshError})
				return
			}
			_ = json.NewEncoder(w).Encode(tokenResponse{Access: "access-refreshed", Refresh: "refresh-rotated", Type: "Bearer", Expires: 3600})
			return
		}
		digest := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if base64.RawURLEncoding.EncodeToString(digest[:]) != f.challenge || r.Form.Get("redirect_uri") != f.redirect {
			f.t.Error("PKCE or redirect mismatch")
		}
		_ = json.NewEncoder(w).Encode(tokenResponse{Access: "access-secret", Refresh: "refresh-secret", IDToken: f.jwt(), Type: "Bearer", Expires: 3600, Scope: f.scope})
	case "/v1/models":
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer access-") {
			f.t.Error("missing bearer credential")
		}
		w.WriteHeader(f.modelStatus)
		_, _ = io.WriteString(w, `{"models":[{"slug":"model-second","display_name":"Second","visibility":"list"},{"slug":"hidden","visibility":"hide"},{"slug":"model-first","display_name":"First","visibility":"list"}]}`)
	case "/revoke":
		_ = r.ParseForm()
		if r.Form.Get("token") == "" || r.Form.Get("token_type_hint") != "refresh_token" || r.Form.Get("client_id") != f.clientID {
			f.t.Error("invalid revocation")
		}
		w.WriteHeader(f.revokeStatus)
	case "/v1/responses":
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	default:
		http.NotFound(w, r)
	}
}
func (f *authFixture) browser(edit func(url.Values)) func(string) error {
	return func(raw string) error {
		u, _ := url.Parse(raw)
		q := u.Query()
		f.nonce = q.Get("nonce")
		f.challenge = q.Get("code_challenge")
		f.redirect = q.Get("redirect_uri")
		if q.Get("code_challenge_method") != "S256" || q.Get("state") == "" || f.nonce == "" {
			f.t.Error("missing OAuth random bindings")
		}
		host := q.Get("ext_agent_host_id")
		if !strings.HasPrefix(host, "urn:uuid:") {
			f.t.Error("invalid host")
		}
		if f.seenHost != "" && host != f.seenHost {
			f.t.Error("host ID changed")
		}
		f.seenHost = host
		if q.Get("client_id") == dynamicClient {
			if q.Get("agent_name_hint") != "Ally" {
				f.t.Error("agent hint missing")
			}
		} else {
			if q.Has("agent_name_hint") {
				f.t.Error("reauth sent registration hint")
			}
		}
		callback := url.Values{"code": {"test-code"}, "client_id": {f.clientID}, "state": {q.Get("state")}}
		if edit != nil {
			edit(callback)
		}
		resp, err := http.Get(f.redirect + "?" + callback.Encode())
		if err != nil {
			return err
		}
		resp.Body.Close()
		return nil
	}
}
func (f *authFixture) login(id string) Account {
	f.t.Helper()
	a, err := f.m.Login(context.Background(), f.s.Client(), id, false, f.browser(nil))
	if err != nil {
		f.t.Fatal(err)
	}
	return a
}
func (f *authFixture) expire() {
	f.t.Helper()
	if err := f.m.withVault(context.Background(), func(v *vault) (bool, error) { v.Accounts[0].ExpiresAt = time.Now().Add(-time.Hour); return true, nil }); err != nil {
		f.t.Fatal(err)
	}
}

func TestRegistrationRestoreSwitchAndReauthorization(t *testing.T) {
	f := fixture(t)
	one := f.login("")
	if !one.PlanEnabled || !one.SignedIn {
		t.Fatal(one)
	}
	f.clientID = "oaiapp_two"
	f.subject = "subject-two"
	two := f.login("")
	if one.ID == two.ID {
		t.Fatal("registrations merged")
	}
	f.clientID = one.ID
	f.subject = one.Subject
	f.login(one.ID)
	if _, err := f.m.Login(context.Background(), f.s.Client(), one.ID, false, f.browser(func(q url.Values) { q.Del("client_id") })); err != nil {
		t.Fatal("returning callback may omit client ID:", err)
	}
	if _, err := f.m.Login(context.Background(), f.s.Client(), one.ID, false, f.browser(func(q url.Values) { q.Set("client_id", "wrong-client") })); err == nil {
		t.Fatal("returning callback changed client ID")
	}
	restored := New(f.m.dir, f.m.protector)
	restored.issuer = f.s.URL
	restored.resource = f.m.resource
	accounts, err := restored.Accounts(context.Background())
	if err != nil || len(accounts) != 2 {
		t.Fatalf("restore: %v %v", accounts, err)
	}
	models, err := restored.Models(context.Background(), f.s.Client(), two.ID)
	if err != nil || len(models) != 2 || models[0].Slug != "model-second" || models[0].DisplayName != "Second" {
		t.Fatalf("models %v %v", models, err)
	}
	data, err := os.ReadFile(filepath.Join(f.m.dir, "credentials.enc"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "access-secret") || strings.Contains(string(data), "refresh-secret") {
		t.Fatal("plaintext storage")
	}
	public, _ := json.Marshal(accounts)
	if strings.Contains(string(public), "secret") || strings.Contains(string(public), "id_token") {
		t.Fatal("public credentials leaked")
	}
}

func TestIDTokenValidationPreservesExistingAccount(t *testing.T) {
	for _, mode := range []string{"signature", "issuer", "audience", "expired", "nonce"} {
		t.Run(mode, func(t *testing.T) {
			f := fixture(t)
			account := f.login("")
			f.invalid = mode
			if _, err := f.m.Login(context.Background(), f.s.Client(), account.ID, false, f.browser(nil)); err == nil {
				t.Fatal("invalid JWT accepted")
			}
			accounts, _ := f.m.Accounts(context.Background())
			if len(accounts) != 1 || !accounts[0].SignedIn {
				t.Fatal("failed login destroyed account")
			}
		})
	}
	f := fixture(t)
	account := f.login("")
	f.subject = "other-user"
	if _, err := f.m.Login(context.Background(), f.s.Client(), account.ID, false, f.browser(nil)); err == nil {
		t.Fatal("wrong identity accepted")
	}
}

func TestCallbackAndCancellation(t *testing.T) {
	for _, mode := range []string{"denied", "state", "missing_client", "dynamic_client", "cancel", "timeout", "browser"} {
		t.Run(mode, func(t *testing.T) {
			f := fixture(t)
			f.m.loginTimeout = 100 * time.Millisecond
			open := f.browser(func(q url.Values) {
				switch mode {
				case "denied":
					q.Set("error", "access_denied")
				case "state":
					q.Set("state", "wrong")
				case "missing_client":
					q.Del("client_id")
				case "dynamic_client":
					q.Set("client_id", dynamicClient)
				}
			})
			if mode == "cancel" {
				open = func(string) error { f.m.CancelLogin(); return nil }
			}
			if mode == "timeout" {
				open = func(string) error { return nil }
			}
			if mode == "browser" {
				open = func(string) error { return errors.New("browser error") }
			}
			if _, err := f.m.Login(context.Background(), f.s.Client(), "", false, open); err == nil {
				t.Fatal("invalid callback accepted")
			}
			accounts, _ := f.m.Accounts(context.Background())
			if len(accounts) != 0 {
				t.Fatal("failed login saved credentials")
			}
		})
	}
}

func TestGrantedScopeAndModelFailures(t *testing.T) {
	f := fixture(t)
	f.scope = "openid profile email offline_access resource.invoke"
	a := f.login("")
	if a.PlanEnabled || !a.SignedIn {
		t.Fatal(a)
	}
	if _, err := f.m.Models(context.Background(), f.s.Client(), a.ID); err == nil {
		t.Fatal("missing direct scope allowed inference")
	}
	f.scope = requestedScopes
	f.login(a.ID)
	for _, status := range []int{401, 403, 429, 503} {
		f.modelStatus = status
		if _, err := f.m.Models(context.Background(), f.s.Client(), a.ID); err == nil || !strings.Contains(err.Error(), fmtSprint(status)) {
			t.Fatalf("status %d: %v", status, err)
		}
	}
}
func fmtSprint(v int) string { b, _ := json.Marshal(v); return string(b) }

func TestRefreshSerializedAcrossManagersAndRotation(t *testing.T) {
	f := fixture(t)
	a := f.login("")
	f.expire()
	other := New(f.m.dir, f.m.protector)
	other.issuer = f.m.issuer
	other.resource = f.m.resource
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m := f.m
			if i%2 == 0 {
				m = other
			}
			if _, err := m.Models(context.Background(), f.s.Client(), a.ID); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if f.refreshCount.Load() != 1 {
		t.Fatalf("refreshes: %d", f.refreshCount.Load())
	}
	if f.lastRefresh != "refresh-secret" {
		t.Fatal("wrong refresh session")
	}
	_ = f.m.withVault(context.Background(), func(v *vault) (bool, error) {
		if v.Accounts[0].Refresh != "refresh-rotated" || v.Accounts[0].Access != "access-refreshed" {
			t.Fatal("rotation not persisted")
		}
		return false, nil
	})
	f.expire()
	if _, err := f.m.Models(context.Background(), f.s.Client(), a.ID); err != nil {
		t.Fatal(err)
	}
	if f.lastRefresh != "refresh-rotated" {
		t.Fatal("rotated refresh token was not used")
	}
}

func TestTokenEndpointRefusesCredentialRedirect(t *testing.T) {
	var leaked atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Store(true) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	m := New(t.TempDir(), newTestProtector())
	m.issuer = source.URL
	_, err := m.exchange(context.Background(), source.Client(), url.Values{"refresh_token": {"synthetic-secret"}})
	if err == nil || leaked.Load() {
		t.Fatal("token endpoint redirect followed")
	}
}

func TestRefreshFailureRecovery(t *testing.T) {
	for _, code := range []string{"invalid_grant", "refresh_token_reused", "invalid_client", "temporary"} {
		t.Run(code, func(t *testing.T) {
			f := fixture(t)
			a := f.login("")
			f.expire()
			f.refreshError = code
			if _, err := f.m.Models(context.Background(), f.s.Client(), a.ID); err == nil {
				t.Fatal("refresh failure ignored")
			}
			accounts, _ := f.m.Accounts(context.Background())
			wantSignedIn := code == "invalid_client" || code == "temporary"
			if accounts[0].SignedIn != wantSignedIn {
				t.Fatalf("credentials cleared incorrectly for %s", code)
			}
		})
	}
}

func TestLogoutClearsTokensRetainsRegistrationAndStopsStream(t *testing.T) {
	for _, status := range []int{200, 503} {
		t.Run(fmtSprint(status), func(t *testing.T) {
			f := fixture(t)
			a := f.login("")
			f.revokeStatus = status
			req, _ := http.NewRequest("POST", f.m.resource+"/responses", nil)
			resp, err := f.m.Do(f.s.Client(), a.ID, req)
			if err != nil {
				t.Fatal(err)
			}
			result, err := f.m.Logout(context.Background(), f.s.Client(), a.ID)
			if err != nil || !result.LocalCleared || result.RemoteRevoked != (status == 200) {
				t.Fatalf("logout %v %v", result, err)
			}
			if _, err = resp.Body.Read(make([]byte, 1)); err == nil {
				t.Fatal("stream not interrupted")
			}
			resp.Body.Close()
			accounts, _ := f.m.Accounts(context.Background())
			if len(accounts) != 1 || accounts[0].SignedIn {
				t.Fatal(accounts)
			}
			_ = f.m.withVault(context.Background(), func(v *vault) (bool, error) {
				c := v.Accounts[0]
				if c.Access != "" || c.Refresh != "" || c.IDToken != "" || v.HostID != f.seenHost {
					t.Fatal("incomplete local clear")
				}
				return false, nil
			})
			f.login(a.ID)
		})
	}
}

func TestEndpointGuardAndNetworkError(t *testing.T) {
	f := fixture(t)
	a := f.login("")
	req, _ := http.NewRequest("GET", f.s.URL+"/not-allowed", nil)
	if _, err := f.m.Do(f.s.Client(), a.ID, req); err == nil {
		t.Fatal("token sent to arbitrary endpoint")
	}
	f.s.Close()
	if _, err := f.m.Models(context.Background(), f.s.Client(), a.ID); err == nil || errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "network") {
		t.Fatalf("network error lost or misclassified: %v", err)
	}
	accounts, _ := f.m.Accounts(context.Background())
	if !accounts[0].SignedIn {
		t.Fatal("network error erased account")
	}
}

type failedSaveProtector struct{ Protector }

func (p failedSaveProtector) Seal([]byte) ([]byte, error) {
	return nil, errors.New("secure store locked")
}

func TestLogoutStorageFailureStillStopsLocalUse(t *testing.T) {
	f := fixture(t)
	a := f.login("")
	f.m.protector = failedSaveProtector{f.m.protector}
	result, err := f.m.Logout(context.Background(), f.s.Client(), a.ID)
	if err == nil || result.LocalCleared {
		t.Fatal("failed credential clear reported success")
	}
	if _, err := f.m.Models(context.Background(), f.s.Client(), a.ID); err == nil || !IsPermanent(err) {
		t.Fatal("sign-out storage failure allowed local credential use")
	}
}

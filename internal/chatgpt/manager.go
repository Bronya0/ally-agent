// SPDX-License-Identifier: GPL-3.0-only
// Package chatgpt implements the official public-client SIWC flow. It has no
// dependency on Ally runtime state or desktop APIs.
package chatgpt

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
)

const Issuer = "https://auth.openai.com"
const Resource = "https://api.openai.com/v1"
const dynamicClient = "dynamic_agent_client"
const requestedScopes = "openid profile email offline_access resource.invoke chatgpt.tokens.use.direct"

var errSession = errors.New("ChatGPT sign-in required")
var errPermission = errors.New("ChatGPT plan permission required")

// IsPermanent prevents runtime retries from looping on a signed-out account,
// missing consent or a terminal refresh error. Network/5xx failures retain their
// bounded retry behavior and never discard credentials.
func IsPermanent(err error) bool {
	var oauth *oauthError
	return errors.Is(err, errSession) || errors.Is(err, errPermission) ||
		(errors.As(err, &oauth) && oauth.status >= 400 && oauth.status < 500 && oauth.status != 429)
}

// Account is the complete public projection. Tokens never cross the bridge.
type Account struct {
	ID          string    `json:"id"`
	Email       string    `json:"email"`
	Subject     string    `json:"subject"`
	SignedIn    bool      `json:"signedIn"`
	PlanEnabled bool      `json:"planEnabled"`
	ExpiresAt   time.Time `json:"expiresAt"`
}
type credential struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Subject   string    `json:"subject"`
	Issuer    string    `json:"issuer"`
	Access    string    `json:"access_token"`
	Refresh   string    `json:"refresh_token"`
	IDToken   string    `json:"id_token"`
	Scopes    []string  `json:"scopes"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (c credential) public() Account {
	return Account{ID: c.ID, Email: c.Email, Subject: c.Subject, SignedIn: c.Access != "", PlanEnabled: c.Access != "" && slices.Contains(c.Scopes, "chatgpt.tokens.use.direct") && slices.Contains(c.Scopes, "resource.invoke"), ExpiresAt: c.ExpiresAt}
}
func (c *credential) clear() {
	c.Access = ""
	c.Refresh = ""
	c.IDToken = ""
	c.Scopes = nil
	c.ExpiresAt = time.Time{}
}

type Manager struct {
	dir          string
	protector    Protector
	issuer       string
	resource     string
	loginTimeout time.Duration
	mu           sync.Mutex
	pending      context.CancelFunc
	closing      bool
	requests     map[string]map[*http.Request]context.CancelFunc
	paused       map[string]error
}

func New(dir string, protector Protector) *Manager {
	return &Manager{dir: dir, protector: protector, issuer: Issuer, resource: Resource, loginTimeout: 5 * time.Minute, requests: make(map[string]map[*http.Request]context.CancelFunc), paused: make(map[string]error)}
}
func (m *Manager) Accounts(ctx context.Context) ([]Account, error) {
	out := []Account{}
	err := m.withVault(ctx, func(v *vault) (bool, error) {
		for _, c := range v.Accounts {
			out = append(out, c.public())
		}
		return false, nil
	})
	return out, err
}
func (m *Manager) CancelLogin() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pending != nil {
		m.pending()
	}
}
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closing = true
	if m.pending != nil {
		m.pending()
	}
	for _, requests := range m.requests {
		for _, cancel := range requests {
			cancel()
		}
	}
}
func randomValue() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func find(v *vault, id string) *credential {
	for i := range v.Accounts {
		if v.Accounts[i].ID == id {
			return &v.Accounts[i]
		}
	}
	return nil
}

// Login opens the system browser only through a caller-supplied host function.
// No authorization URL (which can contain an id_token_hint) is returned to UI.
func (m *Manager) Login(ctx context.Context, client *http.Client, id string, consent bool, open func(string) error) (Account, error) {
	ctx, cancel := context.WithTimeout(ctx, m.loginTimeout)
	m.mu.Lock()
	if m.pending != nil || m.closing {
		m.mu.Unlock()
		cancel()
		return Account{}, errors.New("ChatGPT sign-in is already running or Ally is closing")
	}
	m.pending = cancel
	m.mu.Unlock()
	defer func() { cancel(); m.mu.Lock(); m.pending = nil; m.mu.Unlock() }()
	var original credential
	var hostID string
	err := m.withVault(ctx, func(v *vault) (bool, error) {
		if id != "" {
			c := find(v, id)
			if c == nil {
				return false, errors.New("ChatGPT account registration not found")
			}
			original = *c
		}
		created := v.HostID == ""
		if created {
			v.HostID = "urn:uuid:" + uuid.NewString()
		}
		hostID = v.HostID
		return created, nil
	})
	if err != nil {
		return Account{}, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return Account{}, errors.New("ChatGPT loopback callback could not start")
	}
	redirect := "http://" + listener.Addr().String() + "/auth/callback"
	state, nonce, verifier := randomValue(), randomValue(), randomValue()
	challenge := sha256.Sum256([]byte(verifier))
	type callback struct {
		code, id string
		err      error
	}
	ch := make(chan callback, 1)
	var once sync.Once
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if r.Method != http.MethodGet || r.Host != listener.Addr().String() {
			http.Error(w, "Invalid callback", 400)
			return
		}
		q := r.URL.Query()
		if len(q["state"]) != 1 || subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(state)) != 1 {
			http.Error(w, "Invalid authorization state", 400)
			return
		}
		result := callback{code: q.Get("code"), id: q.Get("client_id")}
		if q.Get("error") != "" {
			result.err = errors.New("ChatGPT authorization was declined or cancelled")
		} else if result.code == "" || len(q["code"]) != 1 {
			result.err = errors.New("ChatGPT authorization code missing")
		} else if id != "" {
			if result.id != "" && result.id != id {
				result.err = errors.New("ChatGPT client registration mismatch")
			}
			result.id = id
		} else if result.id == "" || result.id == dynamicClient || len(q["client_id"]) != 1 {
			result.err = errors.New("ChatGPT issued client ID missing")
		}
		once.Do(func() { ch <- result })
		_, _ = io.WriteString(w, "Authorization received. You can return to Ally.")
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, MaxHeaderBytes: 16 << 10}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()
	q := url.Values{"client_id": {dynamicClient}, "agent_name_hint": {"Ally"}, "ext_agent_host_id": {hostID}, "response_type": {"code"}, "scope": {requestedScopes}, "resource": {m.resource}, "redirect_uri": {redirect}, "state": {state}, "nonce": {nonce}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}}
	if id != "" {
		q.Set("client_id", id)
		q.Del("agent_name_hint")
		if original.IDToken != "" {
			q.Set("id_token_hint", original.IDToken)
		}
		if original.Email != "" {
			q.Set("login_hint", original.Email)
		}
	}
	if consent {
		q.Set("prompt", "consent")
	}
	if err = open(m.issuer + "/api/accounts/authorize?" + q.Encode()); err != nil {
		return Account{}, errors.New("System browser could not open ChatGPT sign-in")
	}
	var result callback
	select {
	case result = <-ch:
	case <-ctx.Done():
		return Account{}, errors.New("ChatGPT sign-in cancelled or timed out")
	}
	_ = server.Close()
	if result.err != nil {
		return Account{}, result.err
	}
	tokens, err := m.exchange(ctx, client, url.Values{"grant_type": {"authorization_code"}, "client_id": {result.id}, "code": {result.code}, "code_verifier": {verifier}, "redirect_uri": {redirect}, "resource": {m.resource}})
	if err != nil {
		return Account{}, err
	}
	identity, err := m.verifyID(ctx, client, tokens.IDToken, result.id, nonce)
	if err != nil {
		return Account{}, err
	}
	if id != "" && (identity.Subject != original.Subject || original.Issuer != m.issuer) {
		return Account{}, errors.New("ChatGPT account identity mismatch; previous account was preserved")
	}
	c := credential{ID: result.id, Email: identity.Email, Subject: identity.Subject, Issuer: m.issuer}
	if err = applyTokens(&c, tokens, false); err != nil {
		return Account{}, err
	}
	err = m.withVault(ctx, func(v *vault) (bool, error) {
		old := find(v, c.ID)
		if old != nil && old.Subject != c.Subject {
			return false, errors.New("ChatGPT registration belongs to another identity")
		}
		// A sign-out during an existing login must not be undone by its callback.
		if id != "" && (old == nil || old.Refresh != original.Refresh || old.IDToken != original.IDToken) {
			return false, errors.New("ChatGPT session changed during sign-in; please retry")
		}
		if old != nil {
			*old = c
		} else {
			v.Accounts = append(v.Accounts, c)
		}
		return true, nil
	})
	if err == nil {
		m.mu.Lock()
		delete(m.paused, c.ID)
		m.mu.Unlock()
	}
	return c.public(), err
}

type tokenResponse struct {
	Access  string `json:"access_token"`
	Refresh string `json:"refresh_token"`
	IDToken string `json:"id_token"`
	Type    string `json:"token_type"`
	Scope   string `json:"scope"`
	Expires int64  `json:"expires_in"`
}
type oauthError struct {
	code   string
	status int
}

func (e *oauthError) Error() string {
	return fmt.Sprintf("ChatGPT OAuth HTTP %d (%s); sign in again for an expired or revoked session", e.status, e.code)
}

func withoutRedirects(client *http.Client) *http.Client {
	copy := *client
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &copy
}
func terminalRefresh(err error) bool {
	var e *oauthError
	return errors.As(err, &e) && slices.Contains([]string{"invalid_grant", "invalid_refresh_token", "token_expired", "refresh_token_expired", "refresh_token_invalidated", "refresh_token_reused"}, e.code)
}
func (m *Manager) exchange(ctx context.Context, client *http.Client, form url.Values) (tokenResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var t tokenResponse
	req, _ := http.NewRequestWithContext(ctx, "POST", m.issuer+"/api/accounts/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := withoutRedirects(client).Do(req)
	if err != nil {
		return t, errors.New("ChatGPT token request failed; check network and proxy settings")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return t, errors.New("ChatGPT token response interrupted")
	}
	if resp.StatusCode != 200 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		// Only known machine codes are safe to forward, never arbitrary token bodies.
		if !slices.Contains([]string{"invalid_grant", "invalid_refresh_token", "token_expired", "refresh_token_expired", "refresh_token_invalidated", "refresh_token_reused", "invalid_client", "access_denied"}, e.Error) {
			e.Error = "token_request_failed"
		}
		return t, &oauthError{e.Error, resp.StatusCode}
	}
	if json.Unmarshal(data, &t) != nil {
		return t, errors.New("ChatGPT token response invalid")
	}
	return t, nil
}
func applyTokens(c *credential, t tokenResponse, refresh bool) error {
	if t.Access == "" || !strings.EqualFold(t.Type, "Bearer") || t.Expires <= 0 || t.Expires > 365*24*3600 {
		return errors.New("ChatGPT token response missing valid bearer token or expiry")
	}
	if !refresh && (t.IDToken == "" || t.Scope == "") {
		return errors.New("ChatGPT identity token or granted scopes missing")
	}
	c.Access = t.Access
	c.ExpiresAt = time.Now().Add(time.Duration(t.Expires) * time.Second)
	if t.Refresh != "" {
		c.Refresh = t.Refresh
	}
	if t.IDToken != "" {
		c.IDToken = t.IDToken
	}
	if t.Scope != "" {
		c.Scopes = strings.Fields(t.Scope)
	}
	return nil
}

type identityClaims struct {
	Subject string `json:"sub"`
	Email   string `json:"email"`
	Nonce   string `json:"nonce"`
}

func (m *Manager) provider(ctx context.Context, client *http.Client) (*oidc.Provider, error) {
	p, err := oidc.NewProvider(oidc.ClientContext(ctx, withoutRedirects(client)), m.issuer)
	if err != nil {
		return nil, errors.New("ChatGPT OpenID discovery failed")
	}
	return p, nil
}
func (m *Manager) verifyID(ctx context.Context, client *http.Client, raw, id, nonce string) (identityClaims, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client = withoutRedirects(client)
	var claims identityClaims
	p, err := m.provider(ctx, client)
	if err != nil {
		return claims, err
	}
	t, err := p.Verifier(&oidc.Config{ClientID: id, SupportedSigningAlgs: []string{"RS256"}}).Verify(oidc.ClientContext(ctx, client), raw)
	if err != nil || t.Claims(&claims) != nil || claims.Subject == "" || (nonce != "" && subtle.ConstantTimeCompare([]byte(nonce), []byte(claims.Nonce)) != 1) {
		return identityClaims{}, errors.New("ChatGPT ID token validation failed (signature, issuer, audience, expiry or nonce)")
	}
	return claims, nil
}

func (m *Manager) access(ctx context.Context, client *http.Client, id string) (string, error) {
	var access string
	err := m.withVault(ctx, func(v *vault) (bool, error) {
		c := find(v, id)
		if c == nil || c.Access == "" {
			return false, fmt.Errorf("%w: account is signed out; open Settings", errSession)
		}
		if !c.public().PlanEnabled {
			return false, fmt.Errorf("%w: enable plan usage in Settings", errPermission)
		}
		if time.Until(c.ExpiresAt) > 60*time.Second {
			access = c.Access
			return false, nil
		}
		if c.Refresh == "" {
			c.clear()
			return true, fmt.Errorf("%w: session expired", errSession)
		}
		t, err := m.exchange(ctx, client, url.Values{"grant_type": {"refresh_token"}, "client_id": {c.ID}, "refresh_token": {c.Refresh}, "resource": {m.resource}})
		if err != nil {
			if terminalRefresh(err) {
				c.clear()
				return true, err
			}
			return false, err
		}
		if t.IDToken != "" {
			claims, err := m.verifyID(ctx, client, t.IDToken, c.ID, "")
			if err != nil {
				return false, err
			}
			if claims.Subject != c.Subject {
				return false, errors.New("ChatGPT refresh identity mismatch")
			}
		}
		if err = applyTokens(c, t, true); err != nil {
			return false, err
		}
		if !c.public().PlanEnabled {
			return true, fmt.Errorf("%w: permission is no longer granted", errPermission)
		}
		access = c.Access
		return true, nil
	})
	return access, err
}

type LogoutResult struct {
	LocalCleared  bool `json:"localCleared"`
	RemoteRevoked bool `json:"remoteRevoked"`
}

func (m *Manager) Logout(ctx context.Context, client *http.Client, id string) (LogoutResult, error) {
	m.CancelLogin()
	m.mu.Lock()
	// Fail closed even if the disk/keychain later refuses the local clear.
	if m.paused == nil {
		m.paused = make(map[string]error)
	}
	m.paused[id] = fmt.Errorf("%w: signed out locally; authorize again before use", errSession)
	for _, cancel := range m.requests[id] {
		cancel()
	}
	m.mu.Unlock()
	result := LogoutResult{}
	err := m.withVault(ctx, func(v *vault) (bool, error) {
		c := find(v, id)
		if c == nil {
			return false, errors.New("ChatGPT account registration not found")
		}
		if c.Refresh != "" {
			if p, err := m.provider(ctx, client); err == nil {
				var metadata struct {
					Revoke string `json:"revocation_endpoint"`
				}
				if p.Claims(&metadata) == nil && validEndpoint(metadata.Revoke, m.issuer) {
					for attempt := 0; attempt < 3; attempt++ {
						req, _ := http.NewRequestWithContext(ctx, "POST", metadata.Revoke, strings.NewReader(url.Values{"token": {c.Refresh}, "token_type_hint": {"refresh_token"}, "client_id": {c.ID}}.Encode()))
						req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
						resp, err := withoutRedirects(client).Do(req)
						status := 0
						if err == nil {
							status = resp.StatusCode
							resp.Body.Close()
						}
						if status == 200 {
							result.RemoteRevoked = true
							break
						}
						if status > 0 && status < 500 {
							break
						}
						select {
						case <-time.After(time.Duration(attempt+1) * 200 * time.Millisecond):
						case <-ctx.Done():
						}
					}
				}
			}
		}
		c.clear()
		result.LocalCleared = true
		return true, nil
	})
	if err != nil {
		result.LocalCleared = false
	}
	return result, err
}
func validEndpoint(endpoint, issuer string) bool {
	u, e := url.Parse(endpoint)
	i, _ := url.Parse(issuer)
	return e == nil && u.Scheme == i.Scheme && u.Host == i.Host && u.User == nil && u.Fragment == ""
}

type Model struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name"`
}

func (m *Manager) Models(ctx context.Context, client *http.Client, id string) ([]Model, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", m.resource+"/models", nil)
	resp, err := m.Do(client, id, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("ChatGPT model list HTTP %d; verify account access and try Refresh", resp.StatusCode)
	}
	var payload struct {
		Models []struct {
			Slug       string `json:"slug"`
			Name       string `json:"display_name"`
			Visibility string `json:"visibility"`
		} `json:"models"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&payload) != nil || payload.Models == nil {
		return nil, errors.New("ChatGPT model catalog format invalid")
	}
	out := []Model{}
	for _, item := range payload.Models {
		if item.Visibility == "list" && item.Slug != "" {
			name := item.Name
			if name == "" {
				name = item.Slug
			}
			out = append(out, Model{item.Slug, name})
		}
	}
	m.mu.Lock()
	delete(m.paused, id)
	m.mu.Unlock()
	return out, nil
}

// A usage cap needs user action rather than automatic retries or another biller.
// Refreshing the account catalog explicitly allows a new attempt after the user
// has reviewed their limits in ChatGPT Settings.
func (m *Manager) RecordError(id string, err error) {
	if err == nil || !strings.Contains(err.Error(), "subscription_sharing_usage_limit_exceeded") {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if errors.Is(m.paused[id], errSession) {
		return
	}
	if m.paused == nil {
		m.paused = make(map[string]error)
	}
	m.paused[id] = errors.New("ChatGPT subscription_sharing_usage_limit_exceeded: requests paused; review https://chatgpt.com/settings/usage, then refresh models in Ally")
}

type responseBody struct {
	io.ReadCloser
	release func()
}

func (b *responseBody) Close() error { defer b.release(); return b.ReadCloser.Close() }

// Do pins the credential to the official resource and tracks in-flight calls so
// local sign-out interrupts streams. No key or token is put in ConfigState.
func (m *Manager) Do(client *http.Client, id string, req *http.Request) (*http.Response, error) {
	if !(req.Method == http.MethodPost && req.URL.String() == m.resource+"/responses") &&
		!(req.Method == http.MethodGet && req.URL.String() == m.resource+"/models") {
		return nil, errors.New("ChatGPT OAuth request endpoint is not allowed")
	}
	ctx, cancel := context.WithCancel(req.Context())
	m.mu.Lock()
	if (req.URL.Path == "/v1/responses" && m.paused[id] != nil) || errors.Is(m.paused[id], errSession) {
		err := m.paused[id]
		m.mu.Unlock()
		cancel()
		return nil, err
	}
	if m.closing {
		m.mu.Unlock()
		cancel()
		return nil, errors.New("Ally is closing")
	}
	if m.requests[id] == nil {
		m.requests[id] = make(map[*http.Request]context.CancelFunc)
	}
	m.requests[id][req] = cancel
	m.mu.Unlock()
	release := func() {
		cancel()
		m.mu.Lock()
		delete(m.requests[id], req)
		if len(m.requests[id]) == 0 {
			delete(m.requests, id)
		}
		m.mu.Unlock()
	}
	access, err := m.access(ctx, client, id)
	if err != nil {
		cancelled := ctx.Err()
		release()
		if cancelled != nil {
			return nil, cancelled
		}
		return nil, err
	}
	copy := req.Clone(ctx)
	copy.Header.Set("Authorization", "Bearer "+access)
	safeClient := *client
	safeClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := safeClient.Do(copy)
	if err != nil {
		cancelled := ctx.Err()
		release()
		if cancelled != nil {
			return nil, cancelled
		}
		return nil, errors.New("ChatGPT request failed; check network and proxy settings")
	}
	resp.Body = &responseBody{resp.Body, release}
	return resp, nil
}

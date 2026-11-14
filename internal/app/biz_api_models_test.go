// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
)

// Use a local mux so deletion is exercised without changing the parent-owned routes.
func apiModelsTestHandler(a *App) http.Handler { return a.apiMux() }

func TestAPIModelsPatchPreservesRedactedAndOmittedFields(t *testing.T) {
	a := newIsolatedChatTestApp(t)
	vision := false
	original := normalizeModelEntry(ModelConfig{
		ProviderName: "p", Model: "m", DisplayName: "Friendly", APIKey: "secret-first",
		APIKeys: []string{"secret-first", "secret-second"}, BaseURL: "https://example.test/v1",
		MaxTokens: 1234, ContextWindow: 98765, TokenParam: "max_tokens", ReasoningTag: "think",
		ReasoningEffort: "high", VisionCapable: &vision, CustomHeaders: map[string]string{"X-Gateway": "secret-header"},
	})
	a.config.Models = []ModelConfig{original}
	h := apiModelsTestHandler(a)
	rec, payload := apiRequest(t, h, "GET", "/api/v1/models", "", "")
	data := apiRequireOK(t, rec, payload)
	for _, secret := range []string{"secret-first", "secret-second", "secret-header"} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Fatal("model summary leaked a secret")
		}
	}
	entry := data["models"].([]any)[0].(map[string]any)
	entry["displayName"] = "Renamed"
	body, err := json.Marshal(map[string]any{"index": 0, "model": entry})
	if err != nil {
		t.Fatal(err)
	}
	rec, payload = apiRequest(t, h, "POST", "/api/v1/models", "", string(body))
	apiRequireOK(t, rec, payload)
	original.DisplayName = "Renamed"
	if !reflect.DeepEqual(a.config.Models[0], original) {
		t.Fatal("redacted round trip changed omitted fields or credentials")
	}
	for _, tokenParam := range []string{"max_completion_tokens", "auto", "max_tokens"} {
		body, _ := json.Marshal(map[string]any{"index": 0, "model": map[string]any{"tokenParam": tokenParam, "maxTokens": 0, "visionCapable": false}})
		rec, payload = apiRequest(t, h, "POST", "/api/v1/models", "", string(body))
		apiRequireOK(t, rec, payload)
		model := a.config.Models[0]
		if model.TokenParam != tokenParam || model.MaxTokens != 0 || model.VisionCapable == nil || *model.VisionCapable {
			t.Fatal("explicit model fields lost")
		}
	}
	// Verify the atomic persistence path, not just the in-memory model.
	bytes, err := os.ReadFile(a.configPath)
	if err != nil {
		t.Fatal(err)
	}
	var disk ConfigState
	if err := json.Unmarshal(bytes, &disk); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(disk.Models, a.config.Models) {
		t.Fatal("persisted models differ")
	}
}

func TestAPIModelsExplicitCredentialsAndCollections(t *testing.T) {
	for _, tc := range []struct {
		patch   string
		keys    []string
		headers map[string]string
	}{
		{`{"apiKey":"replacement"}`, []string{"replacement"}, map[string]string{"X-Old": "value"}},
		{`{"apiKeys":[" new ","backup"],"customHeaders":{"X-New":"value"}}`, []string{"new", "backup"}, map[string]string{"X-New": "value"}},
		{`{"apiKeys":[],"customHeaders":{}}`, nil, nil},
		{`{"apiKey":"","customHeaders":null}`, nil, nil},
		{`{"apiKeys":[" "]}`, nil, map[string]string{"X-Old": "value"}},
	} {
		t.Run(tc.patch, func(t *testing.T) {
			a := newIsolatedChatTestApp(t)
			a.config.Models = []ModelConfig{{ProviderName: "p", Model: "m", APIKey: "old", APIKeys: []string{"old", "backup-old"}, CustomHeaders: map[string]string{"X-Old": "value"}}}
			rec, payload := apiRequest(t, apiModelsTestHandler(a), "POST", "/api/v1/models", "", `{"index":0,"model":`+tc.patch+`}`)
			apiRequireOK(t, rec, payload)
			m := a.config.Models[0]
			if !reflect.DeepEqual(m.APIKeys, tc.keys) || !reflect.DeepEqual(m.CustomHeaders, tc.headers) {
				t.Fatal("explicit credentials/collections not applied")
			}
			key := ""
			if len(tc.keys) > 0 {
				key = tc.keys[0]
			}
			if m.APIKey != key {
				t.Fatal("legacy key and key pool disagree")
			}
		})
	}
}

func TestAPIModelsDuplicateIdentityAndOAuthState(t *testing.T) {
	a := newIsolatedChatTestApp(t)
	h := apiModelsTestHandler(a)
	for _, account := range []string{"one", "two"} {
		rec, payload := apiRequest(t, h, "POST", "/api/v1/models", "", `{"model":{"providerName":"arbitrary label","authType":"chatgpt_oauth","oauthAccountId":"`+account+`","model":"same","apiKey":"discard-secret"}}`)
		apiRequireOK(t, rec, payload)
	}
	before := cloneConfig(a.config)
	for _, body := range []string{
		`{"model":{"providerName":"another label","authType":"chatgpt_oauth","oauthAccountId":"one","model":" SAME "}}`,
		`{"index":1,"model":{"oauthAccountId":"one"}}`,
	} {
		rec, _ := apiRequest(t, h, "POST", "/api/v1/models", "", body)
		if rec.Code != http.StatusConflict {
			t.Fatalf("duplicate status = %d", rec.Code)
		}
		if !reflect.DeepEqual(before, a.config) {
			t.Fatal("duplicate changed configuration")
		}
	}
	rec, payload := apiRequest(t, h, "POST", "/api/v1/models/activate", "", `{"index":1}`)
	apiRequireOK(t, rec, payload)
	rec, payload = apiRequest(t, h, "POST", "/api/v1/models", "", `{"index":1,"model":{"model":"renamed","displayName":"Second"}}`)
	apiRequireOK(t, rec, payload)
	if a.config.LastUsedModel.OAuthAccountID != "two" || a.config.LastUsedModel.Model != "renamed" {
		t.Fatal("active OAuth identity lost")
	}
	rec, payload = apiRequest(t, h, "GET", "/api/v1/models", "", "")
	data := apiRequireOK(t, rec, payload)
	active := data["active"].(map[string]any)
	listed := data["models"].([]any)[1].(map[string]any)
	if !reflect.DeepEqual(active, listed) {
		t.Fatal("active OAuth summary differs from listed model")
	}
	if active["authType"] != authChatGPTOAuth || active["oauthAccountId"] != "two" || active["hasApiKey"] != false || active["providerName"] != chatGPTProviderName {
		t.Fatal("incorrect OAuth summary")
	}
	if strings.Contains(rec.Body.String(), "discard-secret") {
		t.Fatal("OAuth secret leaked")
	}
	// Same model ID at a different provider remains a distinct API-key identity.
	for _, provider := range []string{"p", "q"} {
		rec, payload = apiRequest(t, h, "POST", "/api/v1/models", "", `{"model":{"providerName":"`+provider+`","model":"same"}}`)
		apiRequireOK(t, rec, payload)
	}
	rec, _ = apiRequest(t, h, "POST", "/api/v1/models", "", `{"model":{"providerName":" P ","model":"SAME"}}`)
	if rec.Code != http.StatusConflict {
		t.Fatal("case/whitespace duplicate accepted")
	}
}

func TestAPIModelsDeleteActiveAndInvalidIndices(t *testing.T) {
	a := newIsolatedChatTestApp(t)
	h := apiModelsTestHandler(a)
	a.config.Models = []ModelConfig{{ProviderName: "p", Model: "same", AuthType: authChatGPTOAuth, OAuthAccountID: "one"}, {ProviderName: "p", Model: "same", AuthType: authChatGPTOAuth, OAuthAccountID: "two"}}
	rec, payload := apiRequest(t, h, "POST", "/api/v1/models/activate", "", `{"index":1}`)
	apiRequireOK(t, rec, payload)
	before := cloneConfig(a.config)
	for _, index := range []string{"-1", "bad", "2"} {
		rec, _ := apiRequest(t, h, "DELETE", "/api/v1/models/"+index, "", "")
		if rec.Code != http.StatusBadRequest || !reflect.DeepEqual(before, a.config) {
			t.Fatal("invalid deletion changed configuration or status")
		}
	}
	rec, payload = apiRequest(t, h, "DELETE", "/api/v1/models/0", "", "")
	apiRequireOK(t, rec, payload)
	if len(a.config.Models) != 1 || a.config.LastUsedModel == nil || a.config.LastUsedModel.OAuthAccountID != "two" {
		t.Fatal("deleting other OAuth account changed active identity")
	}
	rec, payload = apiRequest(t, h, "DELETE", "/api/v1/models/0", "", "")
	apiRequireOK(t, rec, payload)
	if len(a.config.Models) != 0 || a.config.LastUsedModel != nil || expandLastUsedModel(a.config).Model != "" {
		t.Fatal("active deletion left dangling model")
	}
	bytes, err := os.ReadFile(a.configPath)
	if err != nil {
		t.Fatal(err)
	}
	var disk ConfigState
	if err := json.Unmarshal(bytes, &disk); err != nil {
		t.Fatal(err)
	}
	if len(disk.Models) != 0 || disk.LastUsedModel != nil {
		t.Fatal("deletion was not persisted")
	}
}

func TestAPIModelsRejectAlternateCasingAndUnknownFields(t *testing.T) {
	for _, patch := range []string{
		`{"APIKey":"replacement-secret"}`,
		`{"APIKey":"replacement-secret","apiKeys":["replacement-pool-secret"]}`,
		`{"apiKey":"replacement-secret","APIKeys":["replacement-pool-secret"]}`,
		`{"apiKeys":["replacement-pool-secret"],"ApiKeys":[]}`,
		`{"Model":"renamed"}`,
		`{"model":"renamed","Model":"other"}`,
		`{"apiKye":"replacement-secret"}`,
		`{"note":"unknown"}`,
	} {
		t.Run(patch, func(t *testing.T) {
			a := newIsolatedChatTestApp(t)
			if err := a.commitConfig(func(cfg *ConfigState) error {
				cfg.Models = []ModelConfig{{ProviderName: "p", Model: "original", APIKey: "previous-secret", APIKeys: []string{"previous-secret", "previous-pool-secret"}}}
				id := identityOfModel(cfg.Models[0])
				cfg.LastUsedModel = &id
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			before := cloneConfig(a.config)
			diskBefore, err := os.ReadFile(a.configPath)
			if err != nil {
				t.Fatal(err)
			}
			rec, _ := apiRequest(t, apiModelsTestHandler(a), "POST", "/api/v1/models", "", `{"index":0,"model":`+patch+`}`)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("rejected patch status = %d", rec.Code)
			}
			if !reflect.DeepEqual(a.config, before) {
				t.Fatal("rejected patch changed model or credentials")
			}
			diskAfter, err := os.ReadFile(a.configPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(diskBefore) != string(diskAfter) {
				t.Fatal("rejected patch changed persisted configuration")
			}
			for _, secret := range []string{"replacement-secret", "replacement-pool-secret", "previous-secret", "previous-pool-secret"} {
				if strings.Contains(rec.Body.String(), secret) {
					t.Fatal("validation response leaked credentials")
				}
			}
		})
	}
}

func TestAPIModelRejectsStaleIdentity(t *testing.T) {
	a := newIsolatedChatTestApp(t)
	a.config.Models = []ModelConfig{{ProviderName: "p", Model: "one"}, {ProviderName: "p", Model: "two"}}
	handler := apiModelsTestHandler(a)
	stale := modelIdentityToken(a.config.Models[0])
	a.config.Models = a.config.Models[1:]
	for _, tc := range []struct{ method, path, body string }{
		{"DELETE", "/api/v1/models/0", ""},
		{"POST", "/api/v1/models", `{"index":0,"model":{"displayName":"wrong"}}`},
		{"POST", "/api/v1/models/activate", `{"index":0}`},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("X-Model-Identity", stale)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusConflict {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
	if len(a.config.Models) != 1 || a.config.Models[0].Model != "two" || a.config.Models[0].DisplayName != "" || a.config.LastUsedModel != nil {
		t.Fatal("stale request changed configuration")
	}
}

func TestAPIModelConfigRejectsStaleIdentity(t *testing.T) {
	a := newIsolatedChatTestApp(t)
	a.config.Models = []ModelConfig{{ProviderName: "p", Model: "first"}, {ProviderName: "p", Model: "second"}}
	old := modelIdentityToken(a.config.Models[0])
	a.config.Models = a.config.Models[1:]
	index := 0
	req := httptest.NewRequest("POST", "/", nil)
	req.Header.Set("X-Model-Identity", old)
	if _, err := a.apiModelConfig(req, &index); toolErrorCode(err) != "E_MODEL_IDENTITY" {
		t.Fatalf("stale model identity accepted: %v", err)
	}
	req.Header.Set("X-Model-Identity", modelIdentityToken(a.config.Models[0]))
	cfg, err := a.apiModelConfig(req, &index)
	if err != nil || cfg.Model != "second" {
		t.Fatalf("current model not selected: %s %v", cfg.Model, err)
	}
}

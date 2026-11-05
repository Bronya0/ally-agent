// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVoiceModelListBase(t *testing.T) {
	for _, test := range []struct{ endpoint, want string }{
		{"https://api.siliconflow.cn/v1/audio/transcriptions", "https://api.siliconflow.cn/v1"},
		{"https://dashscope.aliyuncs.com/compatible-mode/v1/audio/transcriptions", "https://dashscope.aliyuncs.com/compatible-mode/v1"},
		{"https://api.openai.com/v1/audio/transcriptions/", "https://api.openai.com/v1"},
		// Chat-completions audio endpoints (Bailian Qwen3-ASR) must lose both the
		// /chat and /completions segments, not just the last one.
		{"https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions", "https://dashscope.aliyuncs.com/compatible-mode/v1"},
		{"https://example.com/v1/completions", "https://example.com/v1"},
		{"http://127.0.0.1:8786/v1/audio/transcriptions", "http://127.0.0.1:8786/v1"},
		{"https://example.com/transcribe", "https://example.com"},
	} {
		got, err := voiceModelListBase(test.endpoint)
		if err != nil {
			t.Fatalf("voiceModelListBase(%q): %v", test.endpoint, err)
		}
		if got != test.want {
			t.Errorf("voiceModelListBase(%q) = %q, want %q", test.endpoint, got, test.want)
		}
	}
	for _, bad := range []string{"", "not-a-url", "ftp://example.com/x", "https:///v1/audio/transcriptions"} {
		if _, err := voiceModelListBase(bad); err == nil {
			t.Errorf("voiceModelListBase(%q) should fail", bad)
		}
	}
}

func TestFilterVoiceModelsKeepsOnlySpeech(t *testing.T) {
	got := filterVoiceModels([]string{"Qwen/Qwen3-8B", "FunAudioLLM/SenseVoiceSmall", "FunAudioLLM/CosyVoice2-0.5B", "openai/whisper-large-v3", "BAAI/bge-m3"}, VoiceListAny, "")
	want := []string{"FunAudioLLM/SenseVoiceSmall", "FunAudioLLM/CosyVoice2-0.5B", "openai/whisper-large-v3"}
	if len(got) != len(want) {
		t.Fatalf("filterVoiceModels = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("filterVoiceModels = %v, want %v", got, want)
		}
	}
}

// The catalogue mixes realtime and batch ASR; the picker only ever shows batch
// ids, because a realtime model would need a websocket session this client does
// not speak yet.
func TestFilterVoiceModelsSplitsRealtimeAndBatch(t *testing.T) {
	models := []string{
		"qwen3-asr-flash", "qwen-audio-3.0-asr-flash", "qwen3-asr-flash-filetrans",
		"qwen3-asr-flash-realtime", "qwen-audio-3.0-asr-flash-realtime",
		"qwen-plus",
	}
	batch := filterVoiceModels(models, VoiceListNonRealtime, "")
	if len(batch) != 3 || batch[0] != "qwen3-asr-flash" || batch[1] != "qwen-audio-3.0-asr-flash" || batch[2] != "qwen3-asr-flash-filetrans" {
		t.Fatalf("batch filter = %v", batch)
	}
	// The selected protocol's model family comes first: both families stay
	// selectable because the route follows the model.
	chatFirst := filterVoiceModels(models, VoiceListNonRealtime, "chat_audio")
	if len(chatFirst) != 3 || chatFirst[0] != "qwen3-asr-flash" {
		t.Fatalf("chat-first filter = %v", chatFirst)
	}
	syncFirst := filterVoiceModels(models, VoiceListNonRealtime, "dashscope_sync_audio")
	if len(syncFirst) != 3 || syncFirst[0] != "qwen-audio-3.0-asr-flash" {
		t.Fatalf("sync-first filter = %v", syncFirst)
	}
}

func TestVoiceProtocolListFilters(t *testing.T) {
	for _, test := range []struct{ protocol, want string }{
		{"chat_audio", VoiceListNonRealtime},
		{"dashscope_sync_audio", VoiceListNonRealtime},
		{"openai_audio", VoiceListAny},
		{"multipart", VoiceListAny},
	} {
		spec, ok := VoiceProtocolSpec(test.protocol)
		if !ok {
			t.Fatalf("protocol %q is missing", test.protocol)
		}
		if spec.ListFilter != test.want {
			t.Errorf("protocol %q list filter = %q, want %q", test.protocol, spec.ListFilter, test.want)
		}
	}
}

func TestRankVoiceModelsPutsSpeechFirst(t *testing.T) {
	got := rankVoiceModels([]string{
		"Qwen/Qwen3-8B", "FunAudioLLM/SenseVoiceSmall", " ", "FunAudioLLM/SenseVoiceSmall",
		"openai/whisper-large-v3", "BAAI/bge-m3",
	})
	want := []string{"FunAudioLLM/SenseVoiceSmall", "openai/whisper-large-v3", "Qwen/Qwen3-8B", "BAAI/bge-m3"}
	if len(got) != len(want) {
		t.Fatalf("rankVoiceModels = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rankVoiceModels = %v, want %v", got, want)
		}
	}
}

// The model name can come from the service instead of memory: the derived URL,
// the speech-to-text filter, the bearer header and the ordering are all checked
// against a stub endpoint.
func TestFetchVoiceModelListAsksForSpeechModels(t *testing.T) {
	var gotPath, gotQuery, gotAuth string
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		gotPath, gotQuery, gotAuth = r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"FunAudioLLM/SenseVoiceSmall"}]}`)
	}))
	defer server.Close()

	app := NewApp()
	models, err := app.FetchVoiceModelList(VoiceModelConfig{
		Endpoint: server.URL + "/v1/audio/transcriptions",
		AuthMode: "bearer", APIKey: "test-key",
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/models" {
		t.Fatalf("requested %q, want /v1/models", gotPath)
	}
	if gotQuery != "sub_type=speech-to-text" {
		t.Fatalf("query = %q, want the speech-to-text filter", gotQuery)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want one filtered request", calls)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if len(models) != 1 || models[0] != "FunAudioLLM/SenseVoiceSmall" {
		t.Fatalf("models = %v", models)
	}
}

// A provider that rejects the capability filter must still work: the request
// falls back to the audio modality and then to local keyword filtering.
func TestFetchVoiceModelListFallsBackWhenFilterUnsupported(t *testing.T) {
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		switch r.URL.RawQuery {
		case "sub_type=speech-to-text":
			w.WriteHeader(http.StatusBadRequest)
			return
		case "type=audio":
			_, _ = io.WriteString(w, `{"data":[{"id":"FunAudioLLM/CosyVoice2-0.5B"},{"id":"FunAudioLLM/SenseVoiceSmall"}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"Qwen/Qwen3-8B"}]}`)
	}))
	defer server.Close()

	app := NewApp()
	models, err := app.FetchVoiceModelList(VoiceModelConfig{
		Endpoint: server.URL + "/v1/audio/transcriptions", AuthMode: "none",
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 2 || queries[0] != "sub_type=speech-to-text" || queries[1] != "type=audio" {
		t.Fatalf("queries = %v, want the two filters in order", queries)
	}
	if len(models) != 2 || models[0] != "FunAudioLLM/CosyVoice2-0.5B" {
		t.Fatalf("audio-modality results should be kept, got %v", models)
	}
}

// A missing /models route must cost one request, not three: the same 404 would come
// back every time, and a local server often has no list endpoint at all.
func TestFetchVoiceModelListStopsWhenTheRouteIsMissing(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	app := NewApp()
	if _, err := app.FetchVoiceModelList(VoiceModelConfig{
		Endpoint: server.URL + "/v1/audio/transcriptions", AuthMode: "none",
	}, false); err == nil {
		t.Fatal("a missing model list must fail")
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want a single probe of a missing route", calls)
	}
}

// A provider that ignores the capability filter (most OpenAI-compatible
// gateways including DashScope) still must not fill the voice picker with chat
// models, so the answer is filtered locally.
func TestFetchVoiceModelListFiltersIgnoredServerAnswer(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = io.WriteString(w, `{"data":[{"id":"qwen-plus"},{"id":"qwen3-asr-flash"},{"id":"text-embedding-v4"}]}`)
	}))
	defer server.Close()

	app := NewApp()
	models, err := app.FetchVoiceModelList(VoiceModelConfig{
		Endpoint: server.URL + "/compatible-mode/v1/audio/transcriptions", AuthMode: "none",
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want one accepted answer", calls)
	}
	if len(models) != 1 || models[0] != "qwen3-asr-flash" {
		t.Fatalf("models = %v, want only the speech model", models)
	}
}

// When nothing speech-like is advertised, the picker stays empty instead of
// offering a chat catalogue; the dialog asks for a hand-typed id or 「全部」.
func TestFetchVoiceModelListReturnsEmptyWithoutSpeechModels(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = io.WriteString(w, `{"data":[{"id":"qwen-plus"},{"id":"text-embedding-v4"}]}`)
	}))
	defer server.Close()

	app := NewApp()
	models, err := app.FetchVoiceModelList(VoiceModelConfig{
		Endpoint: server.URL + "/compatible-mode/v1/audio/transcriptions", AuthMode: "none",
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 0 {
		t.Fatalf("models = %v, want an empty voice list", models)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want the three filter attempts", calls)
	}
}

func TestVoiceModelListHintExplainsFailures(t *testing.T) {
	refused := voiceModelListHint("http://127.0.0.1:8786/v1", errors.New(`Get "http://127.0.0.1:8786/v1/models": dial tcp 127.0.0.1:8786: connectex: No connection could be made because the target machine actively refused it.`))
	if !strings.Contains(refused.Error(), "没有服务在监听") {
		t.Fatalf("refused connection hint = %q", refused)
	}
	if !strings.Contains(refused.Error(), "http://127.0.0.1:8786/v1/models") {
		t.Fatalf("hint should name the probed URL: %q", refused)
	}
	missing := voiceModelListHint("http://10.0.0.5:8786/v1", errors.New("GET http://10.0.0.5:8786/v1/models returned HTTP 404: not found"))
	if !strings.Contains(missing.Error(), "没有 /models") {
		t.Fatalf("404 hint = %q", missing)
	}
	other := errors.New("some other failure")
	if got := voiceModelListHint("http://x/v1", other); got.Error() != other.Error() {
		t.Fatalf("unrelated errors must pass through, got %q", got)
	}
}

func TestFetchVoiceModelListIncludeAllSkipsFilters(t *testing.T) {
	var query string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		_, _ = io.WriteString(w, `{"data":[{"id":"Qwen/Qwen3-8B"},{"id":"FunAudioLLM/SenseVoiceSmall"}]}`)
	}))
	defer server.Close()

	app := NewApp()
	models, err := app.FetchVoiceModelList(VoiceModelConfig{
		Endpoint: server.URL + "/v1/audio/transcriptions", AuthMode: "none",
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if query != "" {
		t.Fatalf("query = %q, want no filter", query)
	}
	if len(models) != 2 || models[0] != "FunAudioLLM/SenseVoiceSmall" {
		t.Fatalf("models = %v, want speech first and everything kept", models)
	}
}

func TestFetchVoiceModelListSendsHeaderAuth(t *testing.T) {
	var gotToken string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-Token")
		_, _ = io.WriteString(w, `{"data":[{"id":"asr-1"}]}`)
	}))
	defer server.Close()

	app := NewApp()
	if _, err := app.FetchVoiceModelList(VoiceModelConfig{
		Endpoint: server.URL + "/v1/audio/transcriptions",
		AuthMode: "header", AuthHeader: "X-Token", AuthPrefix: "Token ", APIKey: "abc",
	}, false); err != nil {
		t.Fatal(err)
	}
	if gotToken != "Token abc" {
		t.Fatalf("X-Token = %q", gotToken)
	}
}

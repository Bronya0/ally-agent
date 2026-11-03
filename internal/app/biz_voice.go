// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// VoiceModelConfig describes one speech-to-text endpoint. Endpoint is the full
// transcription URL (for example http://127.0.0.1:8786/v1/audio/transcriptions).
// The multipart protocol is deliberately configurable so both local servers
// and hosted APIs can use the same adapter without baking in vendor names.
type VoiceModelConfig struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Provider       string            `json:"provider,omitempty"`
	Deployment     string            `json:"deployment,omitempty"` // local / api
	Protocol       string            `json:"protocol,omitempty"`   // openai_audio / multipart
	Endpoint       string            `json:"endpoint"`
	Model          string            `json:"model,omitempty"`
	Language       string            `json:"language,omitempty"`
	ResponseFormat string            `json:"responseFormat,omitempty"`
	FileField      string            `json:"fileField,omitempty"`
	APIKey         string            `json:"apiKey,omitempty"`
	AuthMode       string            `json:"authMode,omitempty"` // per protocol: none / bearer / header
	AuthHeader     string            `json:"authHeader,omitempty"`
	AuthPrefix     string            `json:"authPrefix,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	ExtraFields    map[string]string `json:"extraFields,omitempty"`
	TimeoutSeconds int               `json:"timeoutSeconds,omitempty"`
}

func cloneVoiceModels(in []VoiceModelConfig) []VoiceModelConfig {
	if in == nil {
		return nil
	}
	out := make([]VoiceModelConfig, len(in))
	copy(out, in)
	for i := range out {
		out[i].Headers = maps.Clone(in[i].Headers)
		out[i].ExtraFields = maps.Clone(in[i].ExtraFields)
	}
	return out
}

func normalizeVoiceModels(in []VoiceModelConfig) []VoiceModelConfig {
	out := cloneVoiceModels(in)
	seen := make(map[string]bool, len(out))
	clean := out[:0]
	for _, m := range out {
		m.ID = strings.TrimSpace(m.ID)
		m.Name = strings.TrimSpace(m.Name)
		m.Provider = strings.TrimSpace(m.Provider)
		m.Deployment = strings.ToLower(strings.TrimSpace(m.Deployment))
		m.Protocol = strings.ToLower(strings.TrimSpace(m.Protocol))
		if m.Protocol == "" {
			m.Protocol = "openai_audio"
		}
		m.Endpoint = strings.TrimSpace(m.Endpoint)
		m.Model = strings.TrimSpace(m.Model)
		m.Language = strings.TrimSpace(m.Language)
		m.ResponseFormat = strings.TrimSpace(m.ResponseFormat)
		m.FileField = strings.TrimSpace(m.FileField)
		m.AuthMode = strings.ToLower(strings.TrimSpace(m.AuthMode))
		m.AuthHeader = strings.TrimSpace(m.AuthHeader)
		m.APIKey = strings.TrimSpace(m.APIKey)
		if m.FileField == "" {
			m.FileField = "file"
		}
		if m.Deployment == "" {
			m.Deployment = "local"
		}
		if m.AuthMode == "" {
			if spec, ok := VoiceProtocolSpec(m.Protocol); ok && len(spec.AuthModes) > 0 {
				m.AuthMode = spec.AuthModes[0]
			}
			if m.AuthMode == "" {
				m.AuthMode = "none"
			}
		}
		if m.AuthHeader == "" {
			m.AuthHeader = "Authorization"
		}
		if m.AuthMode == "bearer" && m.AuthPrefix == "" {
			m.AuthPrefix = "Bearer "
		}
		if m.TimeoutSeconds <= 0 {
			m.TimeoutSeconds = 30
		}
		m.Headers = normalizeCustomHeaders(m.Headers)
		if m.ID == "" || seen[m.ID] {
			continue
		}
		seen[m.ID] = true
		clean = append(clean, m)
	}
	return clean
}

func validateVoiceModel(m VoiceModelConfig) error {
	if strings.TrimSpace(m.ID) == "" {
		return errors.New("语音模型配置 ID 不能为空")
	}
	if m.Deployment != "local" && m.Deployment != "api" {
		return errors.New("部署类型必须是 local 或 api")
	}
	protocol, ok := VoiceProtocolSpec(m.Protocol)
	if !ok {
		return errors.New("暂不支持该语音协议")
	}
	u, err := url.Parse(strings.TrimSpace(m.Endpoint))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Fragment != "" {
		return errors.New("Endpoint 必须是有效的 http:// 或 https:// URL，且不能包含用户名密码")
	}
	if protocol.Uses(VoiceFieldFileField) && m.FileField == "" {
		return errors.New("音频文件字段名不能为空")
	}
	if !protocol.AllowsAuth(m.AuthMode) {
		return errors.New("鉴权方式必须是 " + strings.Join(protocol.AuthModes, " / "))
	}
	if (m.AuthMode == "bearer" || m.AuthMode == "header") && m.APIKey == "" {
		return errors.New("所选鉴权方式需要填写 API Key")
	}
	if m.AuthMode == "header" && !validVoiceHeaderName(m.AuthHeader) {
		return errors.New("自定义 API Key 鉴权需要有效的 Header 名称")
	}
	if strings.ContainsAny(m.AuthPrefix+m.APIKey, "\r\n") || (m.AuthMode == "header" && strings.ContainsAny(m.AuthHeader, "\r\n")) {
		return errors.New("鉴权 Header 或值无效")
	}
	if m.TimeoutSeconds < 1 || m.TimeoutSeconds > 180 {
		return errors.New("超时时间应为 1–180 秒")
	}
	for k, v := range m.Headers {
		if !validVoiceHeaderName(k) || strings.ContainsAny(v, "\r\n") {
			return errors.New("自定义 HTTP Header 无效")
		}
	}
	for k, v := range m.ExtraFields {
		if strings.TrimSpace(k) == "" || strings.ContainsAny(k+v, "\r\n") {
			return errors.New("自定义表单字段无效")
		}
	}
	return nil
}

// FetchVoiceModelList lists the models a speech endpoint advertises, asking the
// provider to return speech-to-text models only when it supports that filter.
// includeAll skips the filters and returns the whole catalogue. The base is
// derived from the transcription URL by dropping its audio path, so
// `.../v1/audio/transcriptions` asks `.../v1/models`.
func (a *App) FetchVoiceModelList(model VoiceModelConfig, includeAll bool) ([]string, error) {
	base, err := voiceModelListBase(model.Endpoint)
	if err != nil {
		return nil, err
	}
	headers := make(map[string]string, len(model.Headers))
	for k, v := range model.Headers {
		headers[k] = v
	}
	key := ""
	switch model.AuthMode {
	case "bearer":
		key = model.APIKey
	case "header":
		if model.AuthHeader != "" {
			headers[model.AuthHeader] = model.AuthPrefix + model.APIKey
		}
	}
	fetch := func(query url.Values) ([]string, error) {
		models, err := a.fetchModelListQuery(base, key, headers, query)
		if err != nil {
			return nil, errors.New(redactVoiceSecrets(voiceModelListHint(base, err).Error(), model))
		}
		return models, nil
	}
	listFilter := VoiceListAny
	if spec, ok := VoiceProtocolSpec(model.Protocol); ok {
		listFilter = spec.ListFilter
	}
	if includeAll {
		models, err := fetch(nil)
		if err != nil {
			return nil, err
		}
		return rankVoiceModels(models), nil
	}
	// Ask for the narrowest filter first, then the audio modality, then the
	// plain list. Whatever the server answers, the picker keeps only speech
	// models: a provider that ignores the filter (as the OpenAI-compatible
	// gateways do) would otherwise fill the voice dialog with chat models.
	var lastErr error
	for _, query := range []url.Values{
		{"sub_type": {"speech-to-text"}},
		{"type": {"audio"}},
		nil,
	} {
		models, err := fetch(query)
		if err != nil {
			lastErr = err
			continue
		}
		if speech := filterVoiceModels(models, listFilter, model.Protocol); len(speech) > 0 {
			return speech, nil
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	// No speech model was advertised. Returning an empty list is better than
	// offering a chat model catalogue in a voice picker; the dialog tells the
	// user to type the id or switch to 「全部」.
	return []string{}, nil
}

// voiceModelListHint turns a raw model-list failure into advice. Most local ASR
// servers expose only /v1/audio/transcriptions, and a wrong host or a stopped
// service shows up as a plain connection error, so neither case should read as
// "拉取失败" with no next step.
func voiceModelListHint(base string, err error) error {
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "connection refused"),
		strings.Contains(message, "actively refused"),
		strings.Contains(message, "connectex"),
		strings.Contains(message, "no connection could be made"):
		return fmt.Errorf("无法连接 %s：该地址没有服务在监听。请确认本地 ASR 已启动且地址/端口正确（模型列表接口是 %s/models）；本地服务通常不提供模型列表，也可以直接手动填写模型名", base, base)
	case strings.Contains(message, "timeout"), strings.Contains(message, "deadline exceeded"):
		return fmt.Errorf("连接 %s 超时：请确认服务地址和网络，或直接手动填写模型名", base)
	case strings.Contains(message, "returned http 404"):
		return fmt.Errorf("%s 没有 /models 模型列表接口：手动填写模型名即可（如 SenseVoiceSmall、whisper-1）", base)
	}
	return err
}

// voiceModelListSuffixes are the API paths trimmed to reach the model-list
// base. Longest first: `/chat/completions` must win over `/completions`, and
// `/audio/transcriptions` over `/audio`.
var voiceModelListSuffixes = []string{
	"/chat/completions",
	"/audio/transcriptions",
	"/audio/transcription",
	"/transcriptions",
	"/transcription",
	"/completions",
	"/audio",
}

// voiceModelListBase derives the model-list base URL of a transcription endpoint.
// The audio path is dropped deliberately (`/v1/audio/transcriptions` → `/v1`) and
// so is the chat path (`/v1/chat/completions` → `/v1`), because list endpoints
// live on the API root rather than under `/audio` or `/chat`.
func voiceModelListBase(endpoint string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("Endpoint 不是有效的 URL，无法推导模型列表地址")
	}
	path := strings.TrimRight(u.Path, "/")
	trimmed := false
	for _, suffix := range voiceModelListSuffixes {
		if strings.HasSuffix(path, suffix) {
			path = strings.TrimSuffix(path, suffix)
			trimmed = true
			break
		}
	}
	if !trimmed {
		if idx := strings.LastIndex(path, "/"); idx >= 0 {
			path = path[:idx]
		}
	}
	return u.Scheme + "://" + u.Host + path, nil
}

// voiceAudioModelHints are the id fragments that usually mean speech-to-text.
// They only affect ordering: a list endpoint commonly returns every model the
// account can use, and hiding the rest would break unusual naming.
var voiceAudioModelHints = []string{"asr", "stt", "gsr", "sensevoice", "whisper", "paraformer", "transcri", "speech", "voice", "audio", "funasr"}

// rankVoiceModels dedupes a model list and puts speech models first.
func rankVoiceModels(models []string) []string {
	const maxVoiceModels = 500
	seen := make(map[string]bool, len(models))
	audio := make([]string, 0, len(models))
	others := make([]string, 0, len(models))
	for _, id := range models {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if isVoiceModelID(id) {
			audio = append(audio, id)
		} else {
			others = append(others, id)
		}
		if len(audio)+len(others) >= maxVoiceModels {
			break
		}
	}
	return append(audio, others...)
}

// filterVoiceModels keeps only ids that look like speech-to-text models of the
// requested mode, in their original order, and puts the ids of the selected
// protocol's model family first. The catalogue mixes qwen3-asr (chat/completions)
// with qwen-audio and fun-asr (multimodal-generation); the request route follows
// the model, so both stay selectable, but the likely ones come first.
func filterVoiceModels(models []string, mode, protocol string) []string {
	preferred := make([]string, 0, len(models))
	others := make([]string, 0, len(models))
	seen := map[string]bool{}
	for _, id := range models {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] || !isVoiceModelID(id) {
			continue
		}
		realtime := strings.Contains(strings.ToLower(id), "realtime")
		if mode == VoiceListNonRealtime && realtime {
			continue
		}
		seen[id] = true
		if family := voiceAudioFamilyForID(id); family != "" && family == protocol {
			preferred = append(preferred, id)
			continue
		}
		others = append(others, id)
	}
	return append(preferred, others...)
}

func isVoiceModelID(id string) bool {
	lower := strings.ToLower(id)
	for _, hint := range voiceAudioModelHints {
		if strings.Contains(lower, hint) {
			return true
		}
	}
	return false
}

func validVoiceHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", r)) {
			return false
		}
	}
	return true
}

// TestNativeVoiceEndpoint probes the configured URL without uploading audio.
// A transcription route answers POST only, and some gateways answer 404 to
// HEAD/GET even though the route exists, so a HEAD miss falls back to an empty
// POST: a 400/415/422 there proves the path is routed and only the body is bad.
func (a *App) TestNativeVoiceEndpoint(model VoiceModelConfig) (string, error) {
	// Endpoint probing is intentionally independent of the display name and
	// transcription form fields. Supply a temporary name only because the
	// shared normalizer/validator is also used when saving a complete model.
	if strings.TrimSpace(model.ID) == "" {
		model.ID = "voice-endpoint-check"
	}
	if strings.TrimSpace(model.Name) == "" {
		model.Name = "endpoint-probe"
	}
	model = normalizeVoiceModels([]VoiceModelConfig{model})[0]
	if err := validateVoiceModel(model); err != nil {
		return "", err
	}
	cfg, err := a.GetConfig()
	if err != nil {
		return "", err
	}
	client := proxyHTTPClient(cfg, true, 8*time.Second)
	probe := func(method string) (int, string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, method, model.Endpoint, nil)
		if err != nil {
			return 0, "", err
		}
		applyVoiceAuth(req, model)
		resp, err := client.Do(req)
		if err != nil {
			return 0, "", fmt.Errorf("连接语音 Endpoint 失败：%w", err)
		}
		defer resp.Body.Close()
		return resp.StatusCode, resp.Status, nil
	}

	status, statusText, err := probe(http.MethodHead)
	if err != nil {
		return "", err
	}
	if status == http.StatusNotFound || status == http.StatusMethodNotAllowed || status == http.StatusNotImplemented {
		if postStatus, postText, postErr := probe(http.MethodPost); postErr == nil {
			status, statusText = postStatus, postText
		}
	}
	switch {
	case status >= 200 && status < 300:
		return fmt.Sprintf("Endpoint 可达（HTTP %d）", status), nil
	case status == http.StatusMethodNotAllowed:
		return "Endpoint 可达；该转写路径只接受 POST（HTTP 405）", nil
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "Endpoint 可达，但鉴权失败（HTTP " + statusText + "），请检查 API Key / 请求头", fmt.Errorf("语音服务鉴权失败：HTTP %d", status)
	case status == http.StatusBadRequest || status == http.StatusUnsupportedMediaType || status == http.StatusUnprocessableEntity:
		// The route exists and rejected the empty body: exactly what a
		// transcription endpoint does without an audio file.
		return fmt.Sprintf("Endpoint 可达；空请求返回 HTTP %d，说明路由存在", status), nil
	case status == http.StatusNotFound:
		return "", fmt.Errorf("Endpoint 返回 HTTP 404：HEAD/POST 都没有对应路由，请确认路径（如 /v1/audio/transcriptions）")
	default:
		return "", fmt.Errorf("Endpoint 检查返回 HTTP %d；请确认 URL 与服务路由", status)
	}
}

// Bailian serves its non-realtime ASR models through two different routes that
// share one host, so a mode switch can leave the previous route behind.
const (
	voiceAudioChatPath = "/compatible-mode/v1/chat/completions"
	voiceAudioSyncPath = "/api/v1/services/aigc/multimodal-generation/generation"
)

// voiceAudioFamilyForID maps a Bailian model id to the request shape it needs.
// The catalogue mixes two families that are not interchangeable: qwen3-asr is
// served by chat/completions, while qwen-audio and fun-asr only answer on
// multimodal-generation. Sending either family to the other route returns
// "format is empty", which names neither the route nor the model.
func voiceAudioFamilyForID(model string) string {
	id := strings.ToLower(strings.TrimSpace(model))
	// Realtime ids belong to the websocket session, not to either HTTP route.
	if id == "" || strings.Contains(id, "realtime") {
		return ""
	}
	switch {
	case strings.HasPrefix(id, "qwen3-asr"):
		return "chat_audio"
	case strings.HasPrefix(id, "qwen-audio"), strings.HasPrefix(id, "fun-asr"):
		return "dashscope_sync_audio"
	}
	return ""
}

// voiceAudioShapeFor resolves the shape actually sent: a known model family wins
// over the chosen mode, because the model decides the route on this provider. The
// mode therefore only picks the default endpoint and the list filter.
func voiceAudioShapeFor(model VoiceModelConfig) string {
	if family := voiceAudioFamilyForID(model.Model); family != "" {
		return family
	}
	if model.Protocol == "dashscope_sync_audio" {
		return "dashscope_sync_audio"
	}
	return "chat_audio"
}

// voiceAudioEndpoint returns the URL a shape must post to, correcting a Bailian
// endpoint that still points at the other route. The correction is limited to
// *.aliyuncs.com hosts: an unknown gateway keeps exactly the URL the user typed,
// because only this provider is known to split ASR across two paths.
func voiceAudioEndpoint(model VoiceModelConfig, shape string) string {
	u, err := url.Parse(strings.TrimSpace(model.Endpoint))
	if err != nil || !strings.HasSuffix(strings.ToLower(u.Hostname()), "aliyuncs.com") {
		return model.Endpoint
	}
	path := strings.TrimRight(u.Path, "/")
	switch shape {
	case "chat_audio":
		if strings.Contains(path, "/multimodal-generation/") {
			u.Path = voiceAudioChatPath
			return u.String()
		}
	case "dashscope_sync_audio":
		if strings.Contains(path, "/compatible-mode/") {
			u.Path = voiceAudioSyncPath
			return u.String()
		}
	}
	return model.Endpoint
}

// transcribeInlineAudio posts the whole recording as one inline-audio request in
// the single shape the model actually needs: chat/completions with an input_audio
// content part for qwen3-asr, multimodal-generation with input.messages and
// parameters.format for qwen-audio / fun-asr. Both Bailian routes reject a bare
// base64 payload with a misleading error ("The provided URL does not appear to be
// valid" / "format is empty"), so the audio always travels as a data URI and the
// route follows the model instead of the mode.
func (a *App) transcribeInlineAudio(cfg ConfigState, model VoiceModelConfig, audio []byte, onText func(string)) (string, error) {
	shape := voiceAudioShapeFor(model)
	dataURI := "data:audio/wav;base64," + base64.StdEncoding.EncodeToString(audio)
	payload := chatAudioPayload(model, dataURI)
	if shape == "dashscope_sync_audio" {
		payload = dashScopeSyncPayload(model, dataURI, voiceWAVSampleRate(audio))
	}
	text, err := a.postVoiceJSON(cfg, model, voiceAudioEndpoint(model, shape), payload)
	if err != nil {
		return "", err
	}
	if onText != nil {
		onText(text)
	}
	return text, nil
}

// voiceAudioPart is the inline-audio content part both routes carry. Only
// chat/completions documents an explicit format field.
func voiceAudioPart(data string, withFormat bool) map[string]any {
	audio := map[string]any{"data": data}
	if withFormat {
		audio["format"] = "wav"
	}
	return map[string]any{"type": "input_audio", "input_audio": audio}
}

// chatAudioPayload is the OpenAI-compatible chat/completions body with an audio
// content part. data must be a data URI; the format field is the one Bailian
// documents for inline audio.
func chatAudioPayload(model VoiceModelConfig, data string) map[string]any {
	payload := map[string]any{
		"model": model.Model,
		"messages": []any{map[string]any{
			"role":    "user",
			"content": []any{voiceAudioPart(data, true)},
		}},
		"stream": false,
	}
	options := map[string]any{"enable_itn": true}
	if language := strings.TrimSpace(model.Language); language != "" {
		options["language"] = language
	}
	payload["asr_options"] = options
	return payload
}

// dashScopeSyncPayload is DashScope's native synchronous ASR body: the audio
// under input.messages and the format under parameters, which that route requires
// (omitting it answers "format is empty").
func dashScopeSyncPayload(model VoiceModelConfig, data, sampleRate string) map[string]any {
	return map[string]any{
		"model": model.Model,
		"input": map[string]any{"messages": []any{map[string]any{
			"role":    "user",
			"content": []any{voiceAudioPart(data, false)},
		}}},
		"parameters": map[string]any{"format": "wav", "sample_rate": sampleRate},
	}
}

// voiceWAVSampleRate reports the rate of an accepted WAV payload so the declared
// rate matches the audio: the recorder always targets 16 kHz, but the test path
// also accepts any WAV a caller supplies.
func voiceWAVSampleRate(audio []byte) string {
	if len(audio) >= 28 && string(audio[0:4]) == "RIFF" && string(audio[8:12]) == "WAVE" {
		if rate := int(binary.LittleEndian.Uint32(audio[24:28])); rate >= 8000 && rate <= 384000 {
			return strconv.Itoa(rate)
		}
	}
	return "16000"
}

// postVoiceJSON performs one transcription request and returns its transcript.
func (a *App) postVoiceJSON(cfg ConfigState, model VoiceModelConfig, endpoint string, payload map[string]any) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	timeout := time.Duration(model.TimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	// Harmless for chat endpoints, required by DashScope's synchronous ASR.
	req.Header.Set("X-DashScope-SSE", "disable")
	applyVoiceAuth(req, model)
	resp, err := proxyHTTPClient(cfg, true, timeout).Do(req)
	if err != nil {
		return "", fmt.Errorf("请求语音服务失败：%w", err)
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		return "", readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := redactVoiceSecrets(voiceChatErrorMessage(raw), model)
		return "", fmt.Errorf("语音服务返回 HTTP %d：%s%s", resp.StatusCode, message, voiceAudioErrorHint(message))
	}
	text := parseVoiceAudioText(raw)
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("语音服务已接受音频（HTTP %d），但没有返回转写文字：「测试转写效果」默认发送空白测试音，请按住语音快捷键用真实说话验证", resp.StatusCode)
	}
	return text, nil
}

// voiceAudioErrorHint turns the misleading route rejections into the fix. Bailian
// answers both routes with "format is empty" / "Input should be a valid string"
// when the model family does not belong to the route being called, which reads
// like an audio problem when it is a model/route pairing.
func voiceAudioErrorHint(message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "format is empty"), strings.Contains(lower, "unsupported_format"):
		return "（该路由不接受这个模型：qwen3-asr 系列走 chat/completions，qwen-audio / fun-asr 系列走 multimodal-generation；请核对模型名与 Endpoint）"
	case strings.Contains(lower, "input should be a valid string"), strings.Contains(lower, "multimodalitem"):
		return "（该路由只接受 multimodal-generation 报文：qwen3-asr 系列应走 chat/completions，请核对模型名与 Endpoint）"
	case strings.Contains(lower, "does not exist or you do not have access"), strings.Contains(lower, "model_not_found"), strings.Contains(lower, "model_not_supported"):
		return "（模型名不可用：点「拉取模型」选择，或手动核对模型 ID）"
	case strings.Contains(lower, "does not appear to be valid"), strings.Contains(lower, "file_download_failed"):
		return "（音频没有被当作内联数据：该服务要求 data:audio/...;base64, 前缀的数据 URI）"
	}
	return ""
}

// parseVoiceAudioText reads the transcript from either reply shape.
func parseVoiceAudioText(data []byte) string {
	if text := parseVoiceDashScopeText(data); strings.TrimSpace(text) != "" {
		return text
	}
	return parseVoiceChatText(data)
}

// parseVoiceDashScopeText reads the text of a DashScope multimodal reply, which
// carries no choices field.
func parseVoiceDashScopeText(data []byte) string {
	var payload struct {
		Output *voiceDashScopeNode `json:"output"`
	}
	if json.Unmarshal(data, &payload) != nil {
		return ""
	}
	return payload.Output.text(0)
}

// voiceDashScopeNode is one DashScope reply node. The synchronous ASR route wraps
// its result twice (output.output.sentence.text), while other DashScope services
// answer at output.text, so the node is walked instead of hard-coded: reading only
// output.* silently lost every transcript of that route.
type voiceDashScopeNode struct {
	Text     string `json:"text"`
	Sentence struct {
		Text string `json:"text"`
	} `json:"sentence"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Output *voiceDashScopeNode `json:"output"`
}

func (n *voiceDashScopeNode) text(depth int) string {
	if n == nil || depth > 3 {
		return ""
	}
	for _, candidate := range []string{n.Text, n.Sentence.Text} {
		if text := strings.TrimSpace(candidate); text != "" {
			return text
		}
	}
	for _, choice := range n.Choices {
		if text := strings.TrimSpace(choice.Message.Content); text != "" {
			return text
		}
	}
	return n.Output.text(depth + 1)
}

// parseVoiceChatText reads the assistant text out of a chat completion.
func parseVoiceChatText(data []byte) string {
	var payload struct {
		Choices []struct {
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(data, &payload) != nil || len(payload.Choices) == 0 {
		return ""
	}
	content := payload.Choices[0].Message.Content
	var text string
	if json.Unmarshal(content, &text) == nil {
		return strings.TrimSpace(text)
	}
	// Some gateways answer with content parts instead of a string.
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &parts) == nil {
		var builder strings.Builder
		for _, part := range parts {
			builder.WriteString(part.Text)
		}
		return strings.TrimSpace(builder.String())
	}
	return ""
}

// redactVoiceSecrets removes the credential a voice model carries from a message
// that may be shown or logged.
func redactVoiceSecrets(message string, model VoiceModelConfig) string {
	if key := strings.TrimSpace(model.APIKey); key != "" {
		message = strings.ReplaceAll(message, key, "[redacted]")
	}
	return message
}

// voiceChatErrorMessage is the human part of an error body, capped so a whole
// HTML page never reaches the dialog.
func voiceChatErrorMessage(data []byte) string {
	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &payload) == nil {
		if message := strings.TrimSpace(payload.Error.Message); message != "" {
			return message
		}
		if message := strings.TrimSpace(payload.Message); message != "" {
			return message
		}
	}
	text := strings.TrimSpace(string(data))
	if len(text) > 200 {
		text = text[:200] + "…"
	}
	return text
}

func applyVoiceAuth(req *http.Request, model VoiceModelConfig) {
	switch model.AuthMode {
	case "bearer":
		req.Header.Set("Authorization", model.AuthPrefix+model.APIKey)
	case "header":
		req.Header.Set(model.AuthHeader, model.AuthPrefix+model.APIKey)
	}
	for k, v := range model.Headers {
		req.Header.Set(k, v)
	}
}

// TestNativeVoiceModelAudio sends transient recorded WAV audio to the configured endpoint.
func (a *App) TestNativeVoiceModelAudio(model VoiceModelConfig, audio []byte) (string, error) {
	return a.TestNativeVoiceModelAudioStream(model, audio, nil)
}

// TestNativeVoiceModelAudioStream reports cumulative partial text when an endpoint responds with SSE.
func (a *App) TestNativeVoiceModelAudioStream(model VoiceModelConfig, audio []byte, onText func(string)) (string, error) {
	if len(audio) < 44 || len(audio) > 20*1024*1024 || string(audio[:4]) != "RIFF" || string(audio[8:12]) != "WAVE" {
		return "", errors.New("录音 WAV 无效或超过 20 MiB")
	}
	// Testing an unsaved draft must not depend on a registry ID.
	if strings.TrimSpace(model.ID) == "" {
		model.ID = "voice-test-draft"
	}
	if strings.TrimSpace(model.Name) == "" {
		model.Name = "voice-test"
	}
	model = normalizeVoiceModels([]VoiceModelConfig{model})[0]
	if err := validateVoiceModel(model); err != nil {
		return "", err
	}
	cfg, err := a.GetConfig()
	if err != nil {
		return "", err
	}
	if model.Protocol == "chat_audio" || model.Protocol == "dashscope_sync_audio" {
		return a.transcribeInlineAudio(cfg, model, audio, onText)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if model.Model != "" {
		_ = writer.WriteField("model", model.Model)
	}
	if model.Language != "" {
		_ = writer.WriteField("language", model.Language)
	}
	if model.ResponseFormat != "" {
		_ = writer.WriteField("response_format", model.ResponseFormat)
	}
	for k, v := range model.ExtraFields {
		if k == "model" || k == "language" || k == "response_format" || k == model.FileField {
			continue
		}
		if err := writer.WriteField(k, v); err != nil {
			return "", err
		}
	}
	part, err := writer.CreateFormFile(model.FileField, "ally-voice-test.wav")
	if err != nil {
		return "", err
	}
	if _, err := part.Write(audio); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(model.TimeoutSeconds)*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, model.Endpoint, &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	applyVoiceAuth(req, model)
	client := proxyHTTPClient(cfg, true, time.Duration(model.TimeoutSeconds)*time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("请求语音服务失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		message := strings.TrimSpace(string(data))
		return "", fmt.Errorf("语音服务返回 HTTP %d：%s", resp.StatusCode, redactVoiceSecrets(message, model))
	}
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		return readVoiceSSE(resp.Body, onText)
	}
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, 64*1024+1))
	if readErr != nil {
		return "", readErr
	}
	if len(data) > 64*1024 {
		return "", errors.New("语音服务测试响应超过 64 KiB")
	}
	text := strings.TrimSpace(string(data))
	var parsed map[string]any
	if json.Unmarshal(data, &parsed) == nil {
		if value, ok := parsed["text"].(string); ok {
			text = strings.TrimSpace(value)
		}
	}
	if text == "" {
		text = "服务已接受测试音频（HTTP " + resp.Status + "），未返回转写文字。"
	} else if len(text) > 180 {
		text = text[:180] + "…"
	}
	if onText != nil {
		onText(text)
	}
	return text, nil
}

func readVoiceSSE(body io.Reader, onText func(string)) (string, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 4096), 64*1024)
	var full strings.Builder
	seen := make(map[string]bool)
	total := 0
	for scanner.Scan() {
		line := scanner.Text()
		total += len(line) + 1
		if total > 64*1024 {
			return "", errors.New("语音服务测试响应超过 64 KiB")
		}
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		var event map[string]any
		if json.Unmarshal([]byte(payload), &event) != nil {
			continue
		}
		part, isDelta := event["delta"].(string)
		if part == "" {
			isDelta = false
			part, _ = event["text"].(string)
		}
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		current := full.String()
		if isDelta {
			full.WriteString(part)
		} else if strings.HasPrefix(part, current) {
			full.Reset()
			full.WriteString(part)
		} else if !strings.HasPrefix(current, part) && !seen[part] {
			full.WriteString(part)
			seen[part] = true
		}
		if onText != nil {
			onText(full.String())
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return strings.TrimSpace(full.String()), nil
}

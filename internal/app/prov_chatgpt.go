// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
)

type chatGPTRequestError struct {
	message, code string
	status        int
}

func (e *chatGPTRequestError) Error() string { return e.message }
func isPermanentChatGPTError(err error) bool {
	var e *chatGPTRequestError
	if !errors.As(err, &e) {
		return false
	}
	return (e.status >= 400 && e.status < 500 && e.status != 429) || slices.Contains([]string{
		"subscription_sharing_user_not_eligible", "subscription_sharing_usage_limit_exceeded",
		"subscription_sharing_unsupported_capability", "subscription_sharing_route_not_supported",
		"subscription_sharing_invalid_user", "chatpass_v2_scope_not_authorized", "chatpass_v2_invalid_authorization_context",
	}, e.code)
}

// chatGPTResponsesRequest is the SIWC preview compatibility boundary. Build
// normal Responses history first, then allow only the supported wire fields.
func chatGPTResponsesRequest(body map[string]any) error {
	allowed := map[string]bool{"model": true, "input": true, "instructions": true, "tools": true, "tool_choice": true, "reasoning": true, "include": true, "parallel_tool_calls": true, "prompt_cache_key": true, "store": true, "stream": true}
	for key := range body {
		if !allowed[key] {
			delete(body, key)
		}
	}
	body["store"] = false
	body["stream"] = true
	input, ok := body["input"].([]any)
	if !ok {
		return errors.New("ChatGPT Responses requires full context as an input array")
	}
	for _, raw := range input {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if item["role"] == "system" {
			item["role"] = "developer"
		}
		if item["type"] == "function_call" {
			item["namespace"] = "ally"
		}
	}
	if tools, ok := body["tools"].([]any); ok && len(tools) > 0 {
		for _, raw := range tools {
			tool, ok := raw.(map[string]any)
			if !ok || (tool["type"] != "function" && tool["type"] != "custom") {
				return errors.New("ChatGPT plan usage does not support this hosted tool")
			}
		}
		body["tools"] = []any{map[string]any{"type": "namespace", "name": "ally", "description": "Ally local agent tools and connected MCP functions, executed by Ally.", "tools": tools}}
	}
	return nil
}

// Keep admission bodies and structured errors (including nonstandard detail)
// useful while preventing an echoed credential from reaching logs or the UI.
func chatGPTResponseError(resp *http.Response, raw []byte) error {
	text := strings.TrimSpace(string(raw))
	if req := resp.Request; req != nil {
		text = redactBearer(text, req.Header.Get("Authorization"))
	}
	if len(text) > 4096 {
		text = text[:4096]
	}
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &payload)
	return &chatGPTRequestError{message: fmt.Sprintf("ChatGPT Responses HTTP %d (request ID %s): %s", resp.StatusCode, resp.Header.Get("x-request-id"), text), status: resp.StatusCode, code: payload.Error.Code}
}
func redactBearer(text, authorization string) string {
	if token := strings.TrimPrefix(authorization, "Bearer "); token != "" && token != authorization {
		text = strings.ReplaceAll(text, token, "[redacted]")
	}
	return text
}
func chatGPTStreamError(raw []byte, authorization string) error {
	var payload struct {
		Type     string `json:"type"`
		Code     string `json:"code"`
		Message  string `json:"message"`
		Param    string `json:"param"`
		Response struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
				Param   string `json:"param"`
			} `json:"error"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return errors.New("ChatGPT Responses error event could not be decoded")
	}
	code, message, param := payload.Code, payload.Message, payload.Param
	if payload.Type == "response.failed" {
		code, message, param = payload.Response.Error.Code, payload.Response.Error.Message, payload.Response.Error.Param
	}
	return &chatGPTRequestError{message: redactBearer("ChatGPT Responses: "+responsesErrorText(code, message, param), authorization), code: code}
}

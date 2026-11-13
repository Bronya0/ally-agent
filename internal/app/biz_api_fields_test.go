// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPIDecodeBodyValidatesFieldNames(t *testing.T) {
	type request struct {
		StartServiceRequest
		Workspace   string            `json:"workspace"`
		Attachments []AttachmentInput `json:"attachments"`
	}
	for _, body := range []string{
		`{"command":"echo test","workspce":"/other"}`,
		`{"Command":"echo test"}`,
		`{"command":"echo test","Workspace":"/other"}`,
		`{"attachments":[{"dataURL":"data:test"}]}`,
		`{"attachments":[{"unknown":"ignored"}]}`,
	} {
		t.Run(body, func(t *testing.T) {
			got := request{Workspace: "original"}
			err := apiDecodeBody(httptest.NewRequest("POST", "/", strings.NewReader(body)), &got)
			if err == nil || got.Workspace != "original" || got.Command != "" || len(got.Attachments) != 0 {
				t.Fatalf("invalid fields decoded: %+v, error: %v", got, err)
			}
		})
	}
	var got request
	err := apiDecodeBody(httptest.NewRequest("POST", "/", strings.NewReader(
		`{"command":"echo test","workspace":"/work","attachments":[{"dataUrl":"data:test"}]}`)), &got)
	if err != nil || got.Command != "echo test" || got.Workspace != "/work" || len(got.Attachments) != 1 || got.Attachments[0].DataURL != "data:test" {
		t.Fatalf("canonical embedded/nested fields failed: %+v, error: %v", got, err)
	}
}

func TestAPIInvalidSessionFieldsDoNotCreateSession(t *testing.T) {
	a := newIsolatedChatTestApp(t)
	for _, body := range []string{`{"workspce":"/other"}`, `{"Title":"wrong case"}`} {
		rec, _ := apiRequest(t, a.apiMux(), "POST", "/api/v1/sessions", "", body)
		if rec.Code != 400 {
			t.Fatalf("invalid session fields accepted: %d %s", rec.Code, rec.Body.String())
		}
	}
	sessions, err := a.ListSessions()
	if err != nil || len(sessions) != 0 {
		t.Fatalf("invalid requests created sessions: %v, %v", sessions, err)
	}
}

// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
)

type apiSSHServer struct {
	ID          string           `json:"id"`
	Alias       string           `json:"alias"`
	AuthType    string           `json:"authType"`
	HasPassword bool             `json:"hasPassword"`
	HasKey      bool             `json:"hasKey"`
	Group       string           `json:"group"`
	Summary     SSHServerSummary `json:"summary"`
}

func apiSSHServerView(node SSHServerNode) apiSSHServer {
	return apiSSHServer{
		ID: node.ID, Alias: node.Alias, AuthType: normalizeSSHAuthType(node),
		HasPassword: node.Password != "", HasKey: strings.TrimSpace(node.KeyPath) != "",
		Group: node.Group,
		Summary: SSHServerSummary{
			Alias: node.Alias, Host: node.Host, Port: node.Port, Username: node.Username,
			Description: node.Description, RiskLevel: node.RiskLevel, Status: node.Status,
		},
	}
}

func apiSSHWorkspace(w http.ResponseWriter, r *http.Request) (string, bool) {
	workspace := normalizeWorkspaceKey(r.URL.Query().Get("workspace"))
	if !filepath.IsAbs(workspace) || strings.ContainsRune(workspace, '\x00') {
		apiWriteError(w, http.StatusBadRequest, "an absolute workspace path is required")
		return "", false
	}
	return workspace, true
}

// GET /api/v1/ssh/servers; workspace is optional for the global inventory.
func (a *App) apiHandleListSSHServers(w http.ResponseWriter, r *http.Request) {
	workspace := ""
	if r.URL.Query().Has("workspace") {
		var ok bool
		workspace, ok = apiSSHWorkspace(w, r)
		if !ok {
			return
		}
	}
	servers := make([]apiSSHServer, 0)
	for _, node := range a.ListAllSSHServers() {
		servers = append(servers, apiSSHServerView(node))
	}
	allowed := []string{}
	if workspace != "" {
		allowed = append(allowed, a.GetWorkspaceAllowedServers(workspace)...)
	}
	apiWriteOK(w, map[string]any{"servers": servers, "workspace": workspace, "allowed": allowed})
}

// POST /api/v1/ssh/servers accepts SSHServerNode fields. Omitted fields retain
// their values on edits; explicit empty credentials clear them. A mode change
// starts with empty credentials so a login password cannot become a key phrase.
func (a *App) apiHandleSaveSSHServer(w http.ResponseWriter, r *http.Request) {
	var raw json.RawMessage
	if err := apiDecodeBody(r, &raw); err != nil {
		apiWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		apiWriteError(w, http.StatusBadRequest, "server must be a JSON object")
		return
	}
	if err := apiValidateFieldNames(fields, SSHServerNode{}, "hasPassword", "hasKey", "summary"); err != nil {
		apiWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	for name, value := range fields {
		switch name {
		case "hasPassword", "hasKey", "summary":
			continue // Read-only metadata from a listed node is ignored.
		}
		if strings.TrimSpace(string(value)) == "null" {
			apiWriteError(w, http.StatusBadRequest, "server fields cannot be null; use an empty string to clear credentials")
			return
		}
	}
	var supplied SSHServerNode
	if err := json.Unmarshal(raw, &supplied); err != nil {
		apiWriteError(w, http.StatusBadRequest, "invalid server fields")
		return
	}
	if _, present := fields["authType"]; present {
		switch strings.ToLower(strings.TrimSpace(supplied.AuthType)) {
		case sshAuthTypeAgent, sshAuthTypeKey, sshAuthTypePassword:
		default:
			apiWriteError(w, http.StatusBadRequest, "authType must be agent, key, or password")
			return
		}
	}
	node := SSHServerNode{CreatedBy: "user", Status: "approved"}
	var expected *SSHServerNode
	found := false
	for _, existing := range a.ListAllSSHServers() {
		if (strings.TrimSpace(supplied.ID) != "" && existing.ID == strings.TrimSpace(supplied.ID)) ||
			(strings.TrimSpace(supplied.ID) == "" && strings.EqualFold(existing.Alias, strings.TrimSpace(supplied.Alias))) {
			node, found = existing, true
			expected = &existing
			break
		}
	}
	if strings.TrimSpace(supplied.ID) != "" && !found {
		apiWriteError(w, http.StatusNotFound, "SSH server not found")
		return
	}
	if _, present := fields["authType"]; present && normalizeSSHAuthType(supplied) != normalizeSSHAuthType(node) {
		node.Password, node.KeyPath = "", ""
	}
	// Approval state and attribution belong to the server: a client may list a
	// node, but cannot approve it or hand it to another author.
	status, createdBy := node.Status, node.CreatedBy
	// Decode over the existing value to preserve every omitted field, including
	// credentials. The save transaction compares the snapshot under its lock and
	// owns normalization, persistence and cache purge.
	_ = json.Unmarshal(raw, &node) // The same payload was fully validated above.
	node.Status, node.CreatedBy = status, createdBy
	if node.Port < 0 || node.Port > 65535 {
		apiWriteError(w, http.StatusBadRequest, "port must be between 1 and 65535 (or 0 for default)")
		return
	}
	saved, err := a.saveSSHServerSnapshot(node, "", expected)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, errSSHServerChanged) {
			status = http.StatusConflict
		}
		apiWriteError(w, status, err.Error())
		return
	}
	apiWriteOK(w, apiSSHServerView(saved))
}

// DELETE /api/v1/ssh/servers/{id}; only stable IDs are accepted here.
func (a *App) apiHandleDeleteSSHServer(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	found := false
	for _, node := range a.ListAllSSHServers() {
		if id != "" && node.ID == id {
			found = true
			break
		}
	}
	if !found {
		apiWriteError(w, http.StatusNotFound, "SSH server not found")
		return
	}
	if err := a.DeleteSSHServer(id); err != nil {
		apiWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	apiWriteOK(w, map[string]any{"id": id})
}

// GET /api/v1/ssh/authorizations?workspace=...
func (a *App) apiHandleGetSSHAuthorizations(w http.ResponseWriter, r *http.Request) {
	workspace, ok := apiSSHWorkspace(w, r)
	if !ok {
		return
	}
	allowed := append([]string{}, a.GetWorkspaceAllowedServers(workspace)...)
	apiWriteOK(w, map[string]any{"workspace": workspace, "allowed": allowed})
}

// PUT /api/v1/ssh/authorizations?workspace=... accepts {"allowed":[idOrAlias,...]}.
// An explicit [] revokes all grants; missing/null is never a revocation.
func (a *App) apiHandlePutSSHAuthorizations(w http.ResponseWriter, r *http.Request) {
	workspace, ok := apiSSHWorkspace(w, r)
	if !ok {
		return
	}
	var body struct {
		Allowed []string `json:"allowed"`
	}
	if err := apiDecodeBody(r, &body); err != nil {
		apiWriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Allowed == nil {
		apiWriteError(w, http.StatusBadRequest, "allowed must be an explicit array; use [] to clear")
		return
	}
	// The backend tolerates stale references for legacy callers. The API rejects
	// them before replacing grants, while still letting the backend canonicalize.
	servers := a.ListAllSSHServers()
	for _, ref := range body.Allowed {
		found := false
		for _, node := range servers {
			if strings.TrimSpace(ref) == node.ID || strings.EqualFold(strings.TrimSpace(ref), node.Alias) {
				found = true
				break
			}
		}
		if !found {
			apiWriteError(w, http.StatusBadRequest, "allowed contains an unknown SSH server reference")
			return
		}
	}
	if err := a.SetWorkspaceAllowedServers(workspace, body.Allowed); err != nil {
		apiWriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.apiHandleGetSSHAuthorizations(w, r)
}

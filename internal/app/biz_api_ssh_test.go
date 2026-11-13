// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func newAPISSHTestHandler(t *testing.T) (http.Handler, *App, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	a := newIsolatedChatTestApp(t)
	const token = "ssh-test-token"
	return apiAuthWithToken(a.apiMux(), func() string { return token }), a, token
}

func apiSSHTestJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func apiSSHTestNode(t *testing.T, a *App, alias string) SSHServerNode {
	t.Helper()
	node, ok := a.GetSSHServer(alias)
	if !ok {
		t.Fatalf("missing node %q", alias)
	}
	return node
}

func TestAPISSHAuthenticationAndRedaction(t *testing.T) {
	h, a, token := newAPISSHTestHandler(t)
	for _, route := range []struct{ method, path, body string }{
		{"GET", "/api/v1/ssh/servers", ""},
		{"POST", "/api/v1/ssh/servers", `{}`},
		{"DELETE", "/api/v1/ssh/servers/unknown", ""},
		{"GET", "/api/v1/ssh/authorizations?workspace=ws", ""},
		{"PUT", "/api/v1/ssh/authorizations?workspace=ws", `{"allowed":[]}`},
	} {
		for _, badToken := range []string{"", "incorrect"} {
			rec, _ := apiRequest(t, h, route.method, route.path, badToken, route.body)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("%s %s without auth = %d", route.method, route.path, rec.Code)
			}
		}
	}
	if len(a.ListAllSSHServers()) != 0 {
		t.Fatal("unauthenticated requests mutated inventory")
	}
	workspace := t.TempDir()
	path := "/api/v1/ssh/servers?workspace=" + url.QueryEscape(workspace)
	rec, payload := apiRequest(t, h, "POST", "/api/v1/ssh/servers", token,
		`{"alias":"key-node","host":"example.invalid","username":"deploy","description":"build host","authType":"key","keyPath":"/secret/private-key","password":"secret-passphrase"}`)
	saved := apiRequireOK(t, rec, payload)
	id := saved["id"].(string)
	if id == "" || saved["authType"] != "key" || saved["hasPassword"] != true || saved["hasKey"] != true {
		t.Fatalf("incorrect credential metadata: %v", saved)
	}
	assertRedacted := func(body string) {
		t.Helper()
		for _, secret := range []string{"/secret/private-key", "secret-passphrase", `"password"`, `"keyPath"`} {
			if strings.Contains(body, secret) {
				t.Fatalf("response leaked credential: %s", body)
			}
		}
	}
	assertRedacted(rec.Body.String())
	if err := a.SetWorkspaceAllowedServers(workspace, []string{id}); err != nil {
		t.Fatal(err)
	}
	rec, payload = apiRequest(t, h, "GET", path, token, "")
	data := apiRequireOK(t, rec, payload)
	assertRedacted(rec.Body.String())
	servers := data["servers"].([]any)
	server := servers[0].(map[string]any)
	if len(servers) != 1 || server["id"] != id || server["alias"] != "key-node" ||
		server["summary"].(map[string]any)["host"] != "example.invalid" ||
		!reflect.DeepEqual(data["allowed"], []any{id}) {
		t.Fatalf("unexpected inventory: %v", data)
	}
}

func TestAPISSHCredentialEditsAndModes(t *testing.T) {
	h, a, token := newAPISSHTestHandler(t)
	save := func(body any) {
		t.Helper()
		rec, payload := apiRequest(t, h, "POST", "/api/v1/ssh/servers", token, apiSSHTestJSON(t, body))
		apiRequireOK(t, rec, payload)
	}
	save(map[string]any{"alias": "dev", "host": "example.invalid", "username": "deploy", "description": "dev host",
		"authType": "key", "password": "passphrase", "keyPath": "/private/key"})
	node := apiSSHTestNode(t, a, "dev")
	id := node.ID
	cacheKey := sshCredentialKey(nodeSSHHost(node), "22")
	a.sshCredentials.store(cacheKey, "key", "cached", "/cached/key")
	save(map[string]any{"id": id, "description": "new purpose"})
	node = apiSSHTestNode(t, a, "dev")
	if node.Password != "passphrase" || node.KeyPath != "/private/key" || node.Description != "new purpose" {
		t.Fatalf("omitted fields lost: %+v", node)
	}
	if _, ok := a.sshCredentials.lookup(cacheKey); ok {
		t.Fatal("edit did not invalidate credential cache")
	}
	// Legacy alias upserts must also preserve omitted credentials and identity.
	save(map[string]any{"alias": "DEV", "group": "ops"})
	node = apiSSHTestNode(t, a, "dev")
	if node.ID != id || node.Password != "passphrase" || node.KeyPath != "/private/key" {
		t.Fatal("alias upsert lost identity or credentials")
	}
	save(map[string]any{"id": id, "password": ""})
	node = apiSSHTestNode(t, a, "dev")
	if node.Password != "" || node.KeyPath != "/private/key" {
		t.Fatal("explicit password clear failed")
	}
	save(map[string]any{"id": id, "keyPath": ""})
	if apiSSHTestNode(t, a, "dev").KeyPath != "" {
		t.Fatal("explicit key path clear failed")
	}
	save(map[string]any{"id": id, "authType": "password", "password": "login", "keyPath": "/hidden/key"})
	node = apiSSHTestNode(t, a, "dev")
	if node.Password != "login" || node.KeyPath != "" || node.AuthType != "password" {
		t.Fatal("password mode did not use backend normalization")
	}
	save(map[string]any{"id": id, "authType": "key", "keyPath": "/new/key"})
	node = apiSSHTestNode(t, a, "dev")
	if node.Password != "" || node.KeyPath != "/new/key" {
		t.Fatal("login password was reused as a key passphrase")
	}
	save(map[string]any{"id": id, "authType": "agent", "password": "hidden", "keyPath": "/hidden/key"})
	node = apiSSHTestNode(t, a, "dev")
	if node.AuthType != "agent" || node.Password != "" || node.KeyPath != "" {
		t.Fatal("agent mode retained hidden credentials")
	}
}

func TestAPISSHAuthorizationIdentityAndDeletion(t *testing.T) {
	h, a, token := newAPISSHTestHandler(t)
	if err := a.SaveSSHServer(SSHServerNode{Alias: "dev", Host: "example.invalid", Username: "deploy", Description: "test"}); err != nil {
		t.Fatal(err)
	}
	node := apiSSHTestNode(t, a, "dev")
	workspace := t.TempDir()
	path := "/api/v1/ssh/authorizations?workspace=" + url.QueryEscape(workspace)
	rec, payload := apiRequest(t, h, "PUT", path, token, `{"allowed":[" DEV "]}`)
	data := apiRequireOK(t, rec, payload)
	if !reflect.DeepEqual(data["allowed"], []any{node.ID}) {
		t.Fatalf("alias was not canonicalized: %v", data)
	}
	rec, payload = apiRequest(t, h, "POST", "/api/v1/ssh/servers", token,
		apiSSHTestJSON(t, map[string]any{"id": node.ID, "alias": "renamed"}))
	apiRequireOK(t, rec, payload)
	if !a.IsServerAuthorizedForWorkspace(workspace, "renamed") {
		t.Fatal("alias edit lost stable ID authorization")
	}
	for _, body := range []string{`{}`, `null`, `{"allowed":null}`, `{"allowed":[null]}`, `{"allowed":[""]}`,
		`{"allowed":["renamed","missing"]}`, `{"allowed":"renamed"}`, `{"allowed":[]} {}`} {
		rec, _ = apiRequest(t, h, "PUT", path, token, body)
		if rec.Code != http.StatusBadRequest || !a.IsServerAuthorizedForWorkspace(workspace, node.ID) {
			t.Fatalf("invalid replacement changed grants: %s (%d)", body, rec.Code)
		}
	}
	rec, payload = apiRequest(t, h, "PUT", path, token, `{"allowed":[]}`)
	data = apiRequireOK(t, rec, payload)
	if !reflect.DeepEqual(data["allowed"], []any{}) || len(a.GetWorkspaceAllowedServers(workspace)) != 0 {
		t.Fatal("explicit [] did not revoke authorization")
	}
	if err := a.SetWorkspaceAllowedServers(workspace, []string{node.ID}); err != nil {
		t.Fatal(err)
	}
	key := sshCredentialKey(nodeSSHHost(node), "22")
	a.sshCredentials.store(key, "password", "cached", "")
	rec, payload = apiRequest(t, h, "DELETE", "/api/v1/ssh/servers/"+node.ID, token, "")
	apiRequireOK(t, rec, payload)
	if len(a.ListAllSSHServers()) != 0 || len(a.GetWorkspaceAllowedServers(workspace)) != 0 {
		t.Fatal("delete left node or authorizations")
	}
	if _, ok := a.sshCredentials.lookup(key); ok {
		t.Fatal("delete left cached credentials")
	}
	rec, payload = apiRequest(t, h, "GET", path, token, "")
	data = apiRequireOK(t, rec, payload)
	if !reflect.DeepEqual(data["allowed"], []any{}) {
		t.Fatal("empty authorizations should be an array")
	}
}

func TestAPISSHValidation(t *testing.T) {
	h, a, token := newAPISSHTestHandler(t)
	for _, query := range []string{"", "?workspace=", "?workspace=%20%20", "?workspace=%00", "?workspace=relative/path", "?workspace=.", "?workspace=../other"} {
		for _, method := range []string{"GET", "PUT"} {
			rec, _ := apiRequest(t, h, method, "/api/v1/ssh/authorizations"+query, token, `{"allowed":[]}`)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s workspace %q = %d", method, query, rec.Code)
			}
		}
	}
	for _, query := range []string{"?workspace=relative/path", "?workspace=", "?workspace=%00"} {
		rec, _ := apiRequest(t, h, "GET", "/api/v1/ssh/servers"+query, token, "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("inventory workspace %q = %d", query, rec.Code)
		}
	}
	for _, body := range []string{`{}`, `null`, `[]`, `{`, `{} {}`, `{"alias":"x"}`, `{"authType":"tls"}`, `{"password":null}`,
		`{"id":"missing","alias":"x","host":"example.invalid","username":"deploy","description":"test"}`} {
		rec, _ := apiRequest(t, h, "POST", "/api/v1/ssh/servers", token, body)
		if rec.Code != http.StatusBadRequest && rec.Code != http.StatusNotFound {
			t.Fatalf("invalid server %s = %d", body, rec.Code)
		}
	}
	for _, port := range []int{-1, 65536} {
		rec, _ := apiRequest(t, h, "POST", "/api/v1/ssh/servers", token,
			apiSSHTestJSON(t, map[string]any{"alias": "x", "host": "example.invalid", "username": "deploy", "description": "test", "port": port}))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid port %d = %d", port, rec.Code)
		}
	}
	if len(a.ListAllSSHServers()) != 0 {
		t.Fatal("invalid input created nodes")
	}
	rec, _ := apiRequest(t, h, "DELETE", "/api/v1/ssh/servers/missing", token, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown delete = %d", rec.Code)
	}
}

func TestAPISSHAbsoluteWorkspaceNeedNotExist(t *testing.T) {
	h, a, token := newAPISSHTestHandler(t)
	workspace := filepath.Join(t.TempDir(), "not-created", "workspace")
	path := "/api/v1/ssh/authorizations?workspace=" + url.QueryEscape(workspace)
	rec, payload := apiRequest(t, h, "PUT", path, token, `{"allowed":[]}`)
	data := apiRequireOK(t, rec, payload)
	if data["workspace"] != normalizeWorkspaceKey(workspace) {
		t.Fatalf("unexpected workspace: %v", data)
	}
	if _, err := os.Stat(workspace); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("workspace should not exist: %v", err)
	}
	if len(a.GetWorkspaceAllowedServers(workspace)) != 0 {
		t.Fatal("unexpected grants")
	}
}

func TestAPISSHStalePatchCannotOverwriteNewerEdit(t *testing.T) {
	for _, edit := range []string{"credential", "alias", "deletion"} {
		t.Run(edit, func(t *testing.T) {
			_, a, _ := newAPISSHTestHandler(t)
			original := SSHServerNode{Alias: "dev", Host: "example.invalid", Username: "deploy",
				Description: "original", AuthType: "password", Password: "old-password"}
			if err := a.SaveSSHServer(original); err != nil {
				t.Fatal(err)
			}
			// Two callers read the same node. The first finishes its edit before
			// the second submits its merged patch to the shared save transaction.
			snapshot := apiSSHTestNode(t, a, "dev")
			newer := snapshot
			switch edit {
			case "credential":
				newer.Password = "new-password"
			case "alias":
				newer.Alias = "renamed"
			}
			if edit == "deletion" {
				if err := a.DeleteSSHServer(snapshot.ID); err != nil {
					t.Fatal(err)
				}
			} else if err := a.SaveSSHServer(newer); err != nil {
				t.Fatal(err)
			}
			before := a.ListAllSSHServers()
			diskBefore, err := os.ReadFile(a.sshClustersFilePath())
			if err != nil {
				t.Fatal(err)
			}
			key := sshCredentialKey(nodeSSHHost(snapshot), "22")
			a.sshCredentials.store(key, "password", "current-cache", "")
			stalePatch := snapshot
			stalePatch.Description = "stale patch"
			if err := a.saveSSHServer(stalePatch, "", &snapshot); !errors.Is(err, errSSHServerChanged) {
				t.Fatalf("stale patch = %v, want errSSHServerChanged", err)
			}
			if !reflect.DeepEqual(a.ListAllSSHServers(), before) {
				t.Fatal("stale patch overwrote the newer node state")
			}
			diskAfter, err := os.ReadFile(a.sshClustersFilePath())
			if err != nil || string(diskAfter) != string(diskBefore) {
				t.Fatalf("stale patch changed persistence: %v", err)
			}
			if entry, ok := a.sshCredentials.lookup(key); !ok || entry.password != "current-cache" {
				t.Fatal("stale patch invalidated current credentials")
			}
		})
	}
}

func TestAPISSHExpectedSnapshotCreateAndLegacyUpsert(t *testing.T) {
	_, a, _ := newAPISSHTestHandler(t)
	node := SSHServerNode{Alias: "dev", Host: "example.invalid", Username: "deploy", Description: "initial"}
	if err := a.saveSSHServer(node, "", nil); err != nil {
		t.Fatalf("expected-create failed: %v", err)
	}
	snapshot := apiSSHTestNode(t, a, "dev")
	node.Description = "concurrent create"
	if err := a.saveSSHServer(node, "", nil); !errors.Is(err, errSSHServerChanged) {
		t.Fatalf("concurrent create overwrote alias: %v", err)
	}
	if got := apiSSHTestNode(t, a, "dev"); !reflect.DeepEqual(got, snapshot) {
		t.Fatal("failed create changed existing node")
	}
	patch := snapshot
	patch.Description = "matching snapshot"
	if err := a.saveSSHServer(patch, "", &snapshot); err != nil {
		t.Fatalf("matching snapshot rejected: %v", err)
	}
	// Callers omitting the optional argument keep legacy alias-based upserts.
	node.Description = "legacy upsert"
	if err := a.SaveSSHServer(node); err != nil {
		t.Fatalf("legacy upsert failed: %v", err)
	}
	if got := apiSSHTestNode(t, a, "dev"); got.ID != snapshot.ID || got.Description != node.Description {
		t.Fatal("legacy upsert behavior changed")
	}
}

func TestAPISSHRejectsUnknownFields(t *testing.T) {
	h, a, token := newAPISSHTestHandler(t)
	if err := a.SaveSSHServer(SSHServerNode{Alias: "dev", Host: "example.invalid", Username: "deploy", Description: "test"}); err != nil {
		t.Fatal(err)
	}
	before := apiSSHTestNode(t, a, "dev")
	for _, patch := range []map[string]any{
		{"id": before.ID, "summery": map[string]any{"host": "other.invalid"}},
		{"id": before.ID, "Summary": map[string]any{"host": "other.invalid"}},
	} {
		rec, _ := apiRequest(t, h, "POST", "/api/v1/ssh/servers", token, apiSSHTestJSON(t, patch))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("unknown field accepted: %v (%d)", patch, rec.Code)
		}
		if got := apiSSHTestNode(t, a, "dev"); !reflect.DeepEqual(got, before) {
			t.Fatal("rejected field changed the node")
		}
	}
	// Read-only view flags a listed node carries stay readable.
	rec, payload := apiRequest(t, h, "POST", "/api/v1/ssh/servers", token,
		apiSSHTestJSON(t, map[string]any{"id": before.ID, "hasPassword": true, "hasKey": false, "description": "updated purpose"}))
	apiRequireOK(t, rec, payload)
	if got := apiSSHTestNode(t, a, "dev"); got.Description != "updated purpose" {
		t.Fatal("readable view fields blocked the edit")
	}
}

func TestAPISSHListedNodeRoundTripPreservesCredentials(t *testing.T) {
	h, a, token := newAPISSHTestHandler(t)
	if err := a.SaveSSHServer(SSHServerNode{Alias: "dev", Host: "example.invalid", Username: "deploy",
		Description: "test", AuthType: "password", Password: "saved-password"}); err != nil {
		t.Fatal(err)
	}
	before := apiSSHTestNode(t, a, "dev")
	rec, payload := apiRequest(t, h, "GET", "/api/v1/ssh/servers", token, "")
	listed := apiRequireOK(t, rec, payload)["servers"].([]any)[0].(map[string]any)
	listed["group"] = "ops"
	listed["summary"].(map[string]any)["host"] = "ignored.invalid"
	rec, payload = apiRequest(t, h, "POST", "/api/v1/ssh/servers", token, apiSSHTestJSON(t, listed))
	apiRequireOK(t, rec, payload)
	before.Group = "ops"
	got := apiSSHTestNode(t, a, "dev")
	// Saving updates this timestamp; two writes can fall in different milliseconds.
	before.UpdatedAtMS = got.UpdatedAtMS
	if !reflect.DeepEqual(got, before) {
		t.Fatal("listed node round trip changed omitted fields or credentials")
	}
}

func TestAPISSHApprovalStateIsServerOwned(t *testing.T) {
	h, a, token := newAPISSHTestHandler(t)
	rec, payload := apiRequest(t, h, "POST", "/api/v1/ssh/servers", token,
		`{"alias":"dev","host":"example.invalid","username":"deploy","description":"test","status":"pending_approval","createdBy":"agent"}`)
	apiRequireOK(t, rec, payload)
	node := apiSSHTestNode(t, a, "dev")
	if node.Status != "approved" || node.CreatedBy != "user" {
		t.Fatalf("client changed approval state: %+v", node)
	}
}

func TestAPISSHRejectsAlternateFieldCasing(t *testing.T) {
	h, a, token := newAPISSHTestHandler(t)
	if err := a.SaveSSHServer(SSHServerNode{Alias: "dev", Host: "example.invalid", Username: "deploy",
		Description: "test", AuthType: "password", Password: "login-password"}); err != nil {
		t.Fatal(err)
	}
	before := apiSSHTestNode(t, a, "dev")
	for _, patch := range []map[string]any{
		{"AuthType": "key", "keyPath": "/new/key"},
		{"AUTHTYPE": "key", "keyPath": "/new/key"},
		{"authType": "password", "AuthType": "key", "keyPath": "/new/key"},
		{"Password": "changed"},
		{"KeyPath": "/new/key"},
	} {
		patch["id"] = before.ID
		rec, _ := apiRequest(t, h, "POST", "/api/v1/ssh/servers", token, apiSSHTestJSON(t, patch))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("alternate casing accepted: %v (%d)", patch, rec.Code)
		}
		if got := apiSSHTestNode(t, a, "dev"); !reflect.DeepEqual(got, before) {
			t.Fatal("rejected field casing changed credentials or node")
		}
	}
}

type apiSSHTestEventSink func(string, any)

func (sink apiSSHTestEventSink) Emit(name string, payload any) { sink(name, payload) }

func TestAPISSHResponseUsesCommittedSnapshot(t *testing.T) {
	for _, change := range []string{"rename", "delete", "replace-alias"} {
		t.Run(change, func(t *testing.T) {
			h, a, token := newAPISSHTestHandler(t)
			var committed SSHServerNode
			fired := false
			// The existing event boundary runs after commit but before the API
			// response. Interleave another save here without timing assumptions.
			a.events = apiSSHTestEventSink(func(name string, _ any) {
				if name != "ssh:clusters-changed" || fired {
					return
				}
				fired = true
				committed = apiSSHTestNode(t, a, "dev")
				if change == "delete" {
					if err := a.DeleteSSHServer(committed.ID); err != nil {
						t.Fatal(err)
					}
					return
				}
				newer := committed
				newer.Alias, newer.AuthType = "renamed", "agent"
				if err := a.SaveSSHServer(newer); err != nil {
					t.Fatal(err)
				}
				if change == "replace-alias" {
					replacement := SSHServerNode{Alias: "dev", Host: "other.invalid", Username: "other", Description: "replacement"}
					if err := a.SaveSSHServer(replacement); err != nil {
						t.Fatal(err)
					}
				}
			})
			rec, payload := apiRequest(t, h, "POST", "/api/v1/ssh/servers", token,
				`{"alias":" dev ","host":"example.invalid","username":"deploy","description":" test ","authType":"password","password":"login"}`)
			data := apiRequireOK(t, rec, payload)
			if !fired || committed.ID == "" || committed.Port != 22 || committed.Description != "test" || committed.CreatedAtMS == 0 {
				t.Fatal("commit snapshot lacks normalized storage fields")
			}
			var expected map[string]any
			if err := json.Unmarshal([]byte(apiSSHTestJSON(t, apiSSHServerView(committed))), &expected); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(data, expected) {
				t.Fatalf("response used later inventory state: got %v, want %v", data, expected)
			}
		})
	}
}

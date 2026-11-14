// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	toolshared "ally-dev/internal/tools/shared"
)

func TestRemoteAliasContractRejectsLegacyCalls(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		request func() any
	}{
		{"remote_read", `"files":[{"path":"/srv/a"}]`, func() any { return &RemoteReadFileRequest{} }},
		{"remote_edit", `"path":"/srv/a","version":"abc123","changes":[{"oldText":"a","newText":"b"}]`, func() any { return &RemoteEditRequest{} }},
		{"remote_create_file", `"path":"/srv/a","content":"a"`, func() any { return &RemoteCreateFileRequest{} }},
		{"remote_delete_path", `"paths":["/srv/a"]`, func() any { return &RemoteDeletePathRequest{} }},
		{"remote_run_command", `"command":"pwd"`, func() any { return &RemoteRunCommandRequest{} }},
		{"remote_transfer", `"action":"upload","localPath":"a","remotePath":"/srv/a"`, func() any { return &RemoteTransferRequest{} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema, ok := toolshared.BuiltinSchema(tc.name)
			if !ok {
				t.Fatal("missing schema")
			}
			props := schema["properties"].(map[string]any)
			if _, exists := props["target"]; exists {
				t.Fatal("new schema exposes legacy target")
			}
			if _, exists := props["alias"]; !exists {
				t.Fatal("schema missing alias")
			}
			for _, key := range []string{"alias"} {
				req := tc.request()
				raw := []byte(`{"` + key + `":"生产 / 分析:一",` + tc.body + `}`)
				if err, warnings := decodeToolArguments(tc.name, raw, req); err != nil || len(warnings) != 0 {
					t.Fatalf("%s: error=%v warnings=%v", key, err, warnings)
				}
				encoded, _ := json.Marshal(req)
				var got map[string]any
				_ = json.Unmarshal(encoded, &got)
				if got["alias"] != "生产 / 分析:一" {
					t.Fatalf("lost server selection: %s", encoded)
				}
			}
			if err, warnings := decodeToolArguments(tc.name, []byte(`{"alias":"dev","target":"prod",`+tc.body+`}`), tc.request()); err != nil || len(warnings) == 0 {
				t.Fatalf("obsolete field did not follow unknown-field handling: %v %v", err, warnings)
			}
			for _, prefix := range []string{"", `"alias":null,`, `"alias":"",`, `"target":"dev",`} {
				if err, _ := decodeToolArguments(tc.name, []byte(`{`+prefix+tc.body+`}`), tc.request()); err == nil {
					t.Fatalf("accepted missing or conflicting server: %s", prefix)
				}
			}
		})
	}

}

func TestRemoteAliasMutationIdentity(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"remote_edit", `"path":"/srv/a"`},
		{"remote_create_file", `"path":"/srv/a"`},
		{"remote_delete_path", `"paths":["/srv/a"]`},
		{"remote_transfer", `"action":"upload","remotePath":"/srv/a"`},
	} {
		current := fileMutationTargets(ConfigState{}, tc.name, `{"alias":"dev",`+tc.body+`}`)
		other := fileMutationTargets(ConfigState{}, tc.name, `{"alias":"prod",`+tc.body+`}`)
		if len(current) != 1 || reflect.DeepEqual(current, other) {
			t.Fatalf("%s: current=%v other=%v", tc.name, current, other)
		}
	}
}

func TestRemoteAliasResolvesOpaqueNicknameAndAuthorization(t *testing.T) {
	a := newSSHIdentityTestApp(t)
	workspace := t.TempDir()
	alias := "生产 / 分析:一"
	if err := a.SaveSSHServerForWorkspace(SSHServerNode{Alias: alias, Host: "192.0.2.1", Port: 2222, Username: "analyst", Description: "test", AuthType: sshAuthTypePassword, Password: "fixture"}, workspace); err != nil {
		t.Fatal(err)
	}
	rt, err := a.resolveAndAuthorizeRemoteTarget(withToolWorkspace(context.Background(), workspace), alias)
	if err != nil {
		t.Fatal(err)
	}
	if rt.Host != "analyst@192.0.2.1" || rt.Port != "2222" || rt.credential == nil || rt.credential.password != "fixture" {
		t.Fatal("alias did not resolve registered connection settings")
	}
	if _, err := a.resolveAndAuthorizeRemoteTarget(withToolWorkspace(context.Background(), t.TempDir()), alias); err == nil {
		t.Fatal("alias bypassed workspace authorization")
	}
}

func TestRemoteAliasSubagentFileSummary(t *testing.T) {
	for _, key := range []string{"alias"} {
		var reads, edits []string
		seenReads, seenEdits := map[string]bool{}, map[string]bool{}
		args := `{"` + key + `":"dev","path":"/srv/a"}`
		trackFileFromToolResult("remote_read", args, &toolResult{OK: true, Data: BatchReadResult{Files: []BatchReadResultItem{{Path: "/srv/a"}}}}, &reads, &edits, seenReads, seenEdits)
		for _, name := range []string{"remote_edit", "remote_create_file"} {
			trackFileFromToolResult(name, args, &toolResult{OK: true}, &reads, &edits, seenReads, seenEdits)
		}
		if !reflect.DeepEqual(reads, []string{"dev :: /srv/a"}) || !reflect.DeepEqual(edits, reads) {
			t.Fatalf("%s: reads=%v edits=%v", key, reads, edits)
		}
	}
}

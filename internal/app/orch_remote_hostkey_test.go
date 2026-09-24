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
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"ally-dev/internal/tools/sshclient"
)

// hostKeyTestServer 是只完成握手、不产出远端 helper JSON 的临时 SSH 服务器。
// 指纹不匹配发生在握手阶段，所以驱动「换记录 + 重连」链路不需要远端真的跑 python。
type hostKeyTestServer struct {
	port        string
	connections atomic.Int32
}

func startHostKeyTestServer(t *testing.T, hostSigner ssh.Signer) *hostKeyTestServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	config := &ssh.ServerConfig{
		PasswordCallback: func(_ ssh.ConnMetadata, _ []byte) (*ssh.Permissions, error) { return nil, nil },
	}
	config.AddHostKey(hostSigner)

	server := &hostKeyTestServer{port: fmt.Sprint(listener.Addr().(*net.TCPAddr).Port)}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			server.connections.Add(1)
			go serveHostKeyTestConnection(conn, config)
		}
	}()
	return server
}

func serveHostKeyTestConnection(conn net.Conn, config *ssh.ServerConfig) {
	serverConn, channels, requests, err := ssh.NewServerConn(conn, config)
	if err != nil {
		_ = conn.Close()
		return
	}
	go ssh.DiscardRequests(requests)
	defer serverConn.Close()
	for newChannel := range channels {
		if newChannel.ChannelType() != "session" {
			_ = newChannel.Reject(ssh.UnknownChannelType, "only sessions are supported")
			continue
		}
		channel, channelRequests, err := newChannel.Accept()
		if err != nil {
			continue
		}
		go func(channel ssh.Channel, requests <-chan *ssh.Request) {
			defer channel.Close()
			for request := range requests {
				if request.Type != "exec" {
					_ = request.Reply(false, nil)
					continue
				}
				_ = request.Reply(true, nil)
				_, _ = io.ReadAll(channel)
				// 故意不输出 helper 的 marker JSON：重试只要走到这里，就说明
				// 记录替换已经生效、连接真的重新建立了。
				_, _ = io.WriteString(channel, "no remote helper here")
				_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{Status: 0}))
				return
			}
		}(channel, channelRequests)
	}
}

func hostKeyTestSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatalf("host signer: %v", err)
	}
	return signer
}

// hostKeyTestApp 把指纹记录文件放进临时目录：测试绝不能读写用户真实的
// ~/.ssh/known_hosts（见 AGENTS.md 单测隔离约定）。
func hostKeyTestApp(t *testing.T, recordedKey ssh.PublicKey, address string) *App {
	t.Helper()
	dir := t.TempDir()
	app := NewApp()
	app.configPath = filepath.Join(dir, "config.json")
	app.sshKnownHostsPath = filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(app.sshKnownHostsPath, []byte(knownhosts.Line([]string{address}, recordedKey)+"\n"), 0o600); err != nil {
		t.Fatalf("write known_hosts: %v", err)
	}
	return app
}

// pendingAskCount 报告当前有多少条待回答的提问。指纹不一致的处理不允许产生任何
// 一条：一旦产生就意味着有人得去点确认，而这条链路按策略不询问任何人。
func pendingAskCount(app *App) int {
	app.askMu.Lock()
	defer app.askMu.Unlock()
	return len(app.pendingAsks)
}

func readKnownHosts(t *testing.T, app *App) string {
	t.Helper()
	data, err := os.ReadFile(app.sshKnownHostsPath)
	if err != nil {
		t.Fatalf("read known_hosts: %v", err)
	}
	return string(data)
}

func publicKeyBlob(key ssh.PublicKey) string {
	return base64.StdEncoding.EncodeToString(key.Marshal())
}

// hostKeyTestCall 组装一次“记录里的指纹与服务器实际提供的指纹不一致”的远端调用。
type hostKeyTestCall struct {
	app       *App
	target    remoteTarget
	ctx       context.Context
	sessionID string
	recorded  ssh.Signer
	offered   ssh.Signer
	server    *hostKeyTestServer
}

func newHostKeyTestCall(t *testing.T, sessionID string) *hostKeyTestCall {
	t.Helper()
	recordedSigner := hostKeyTestSigner(t)
	offeredSigner := hostKeyTestSigner(t)
	server := startHostKeyTestServer(t, offeredSigner)
	address := net.JoinHostPort("127.0.0.1", server.port)

	app := hostKeyTestApp(t, recordedSigner.PublicKey(), address)
	app.sshCredentials.store(sshCredentialKey("127.0.0.1", server.port), sshAuthTypePassword, "pw", "")

	ctx := context.Background()
	if sessionID != "" {
		ctx = context.WithValue(ctx, toolExecutionMetaContextKey{}, toolExecutionMeta{sessionID: sessionID})
	}
	return &hostKeyTestCall{
		app:       app,
		target:    remoteTarget{Raw: "127.0.0.1:" + server.port, Host: "127.0.0.1", Port: server.port, WorkspaceRoot: "/srv/app"},
		ctx:       ctx,
		sessionID: sessionID,
		recorded:  recordedSigner,
		offered:   offeredSigner,
		server:    server,
	}
}

// runRemoteCall 在后台跑一次远端调用，返回结果通道。
func runRemoteCall(call *hostKeyTestCall) <-chan error {
	done := make(chan error, 1)
	go func() {
		var out map[string]any
		done <- call.app.invokeRemotePython(call.ctx, call.target, map[string]any{"op": "read"}, 10*time.Second, &out)
	}()
	return done
}

func TestRemoteHostKeyChangeUpdatesRecordAndRetries(t *testing.T) {
	call := newHostKeyTestCall(t, "sess-hostkey-update")
	err := <-runRemoteCall(call)

	if got := pendingAskCount(call.app); got != 0 {
		t.Errorf("pending asks = %d, want none: a mismatched host key is updated without asking", got)
	}
	var mismatch *sshclient.HostKeyMismatchError
	if errors.As(err, &mismatch) {
		t.Fatalf("host key mismatch was reported instead of being handled: %v", err)
	}
	if err == nil {
		t.Fatal("expected the retry to fail on the fake remote helper output, got nil")
	}
	if !strings.Contains(err.Error(), "remote helper returned no JSON result") {
		t.Fatalf("retry error = %v, want the remote-helper decode failure", err)
	}
	if got := call.server.connections.Load(); got != 2 {
		t.Errorf("server connections = %d, want 2 (initial attempt + automatic retry)", got)
	}

	record := readKnownHosts(t, call.app)
	if strings.Contains(record, publicKeyBlob(call.recorded.PublicKey())) {
		t.Errorf("old fingerprint survived the automatic replacement: %s", record)
	}
	if !strings.Contains(record, publicKeyBlob(call.offered.PublicKey())) {
		t.Errorf("new fingerprint was not recorded: %s", record)
	}
	// 换钥匙是静默发生的，出问题时只能靠这份备份回溯。
	backup, readErr := os.ReadFile(call.app.sshKnownHostsPath + ".old")
	if readErr != nil {
		t.Fatalf("read known_hosts.old: %v", readErr)
	}
	if !strings.Contains(string(backup), publicKeyBlob(call.recorded.PublicKey())) {
		t.Errorf("backup = %q, want the replaced record", backup)
	}
}

// TestTestSSHServerUpdatesChangedHostKey 锁定面板「测试」按钮的口径：指纹不一致时
// 同样不询问用户，直接换记录后重连，所以按钮不会再停在一个需要人工核对的失败上。
// （面板场景本来就没有可用的提问通道，恢复一致的唯一办法就是自动替换。）
func TestTestSSHServerUpdatesChangedHostKey(t *testing.T) {
	recorded := hostKeyTestSigner(t)
	offered := hostKeyTestSigner(t)
	server := startHostKeyTestServer(t, offered)
	address := net.JoinHostPort("127.0.0.1", server.port)
	app := hostKeyTestApp(t, recorded.PublicKey(), address)
	port, err := strconv.Atoi(server.port)
	if err != nil {
		t.Fatalf("parse test server port: %v", err)
	}

	res := app.TestSSHServer(SSHServerNode{
		Alias:       "tested",
		Host:        "127.0.0.1",
		Port:        port,
		Username:    "testuser",
		Description: "host key change on the panel path",
		AuthType:    sshAuthTypePassword,
		Password:    "pw",
	})
	if !res.OK {
		t.Fatalf("test failed instead of updating the changed host key: %+v", res)
	}
	if got := server.connections.Load(); got != 2 {
		t.Errorf("server connections = %d, want 2 (initial attempt + automatic retry)", got)
	}
	if got := pendingAskCount(app); got != 0 {
		t.Errorf("pending asks = %d, want none", got)
	}
	record := readKnownHosts(t, app)
	if strings.Contains(record, publicKeyBlob(recorded.PublicKey())) {
		t.Errorf("old fingerprint survived the automatic replacement: %s", record)
	}
	if !strings.Contains(record, publicKeyBlob(offered.PublicKey())) {
		t.Errorf("new fingerprint was not recorded: %s", record)
	}
}

func TestRemoteHostKeyChangeWithoutSessionAlsoUpdatesRecord(t *testing.T) {
	call := newHostKeyTestCall(t, "")

	err := <-runRemoteCall(call)
	var mismatch *sshclient.HostKeyMismatchError
	if errors.As(err, &mismatch) {
		t.Fatalf("host key mismatch was reported instead of being handled: %v", err)
	}
	if err == nil {
		t.Fatal("expected the retry to fail on the fake remote helper output, got nil")
	}
	if !strings.Contains(err.Error(), "remote helper returned no JSON result") {
		t.Fatalf("retry error = %v, want the remote-helper decode failure", err)
	}
	if got := call.server.connections.Load(); got != 2 {
		t.Errorf("server connections = %d, want 2 (a missing session must not change the policy)", got)
	}
	if got := pendingAskCount(call.app); got != 0 {
		t.Errorf("pending asks = %d, want none", got)
	}
	if record := readKnownHosts(t, call.app); !strings.Contains(record, publicKeyBlob(call.offered.PublicKey())) {
		t.Errorf("call without a session did not record the new fingerprint: %s", record)
	}
}

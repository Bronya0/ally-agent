// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package sshclient

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func TestRunPasswordAuthAndKnownHosts(t *testing.T) {
	server := startTestServer(t, func(config *ssh.ServerConfig) {
		config.PasswordCallback = func(_ ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if string(password) != "test-password" {
				return nil, errors.New("bad password")
			}
			return nil, nil
		}
	})
	knownHostsPath := filepath.Join(t.TempDir(), ".ssh", "known_hosts")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := Run(ctx, Config{
		Host:           "testuser@" + server.host,
		Port:           server.port,
		AuthMode:       AuthModePassword,
		Password:       "test-password",
		KnownHostsPath: knownHostsPath,
	}, "cat", strings.NewReader("remote input\n"))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := string(result.Stdout), "received:remote input\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := string(result.Stderr); got != "test stderr" {
		t.Fatalf("stderr = %q, want test stderr", got)
	}
	data, err := os.ReadFile(knownHostsPath)
	if err != nil {
		t.Fatalf("known_hosts was not created: %v", err)
	}
	if !strings.Contains(string(data), knownhosts.Normalize(net.JoinHostPort(server.host, server.port))) {
		t.Fatalf("known_hosts does not contain server endpoint: %q", data)
	}
}

func TestSSHClientTestConnectivity(t *testing.T) {
	server := startTestServer(t, func(config *ssh.ServerConfig) {
		config.PasswordCallback = func(_ ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if string(password) != "test-password" {
				return nil, errors.New("bad password")
			}
			return nil, nil
		}
	})
	knownHostsPath := filepath.Join(t.TempDir(), ".ssh", "known_hosts")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	duration, err := Test(ctx, Config{
		Host:           "testuser@" + server.host,
		Port:           server.port,
		AuthMode:       AuthModePassword,
		Password:       "test-password",
		KnownHostsPath: knownHostsPath,
	})
	if err != nil {
		t.Fatalf("Test() error = %v", err)
	}
	if duration <= 0 {
		t.Fatalf("expected positive duration, got %v", duration)
	}

	// Test with bad password should return ErrAuthentication
	_, badErr := Test(ctx, Config{
		Host:           "testuser@" + server.host,
		Port:           server.port,
		AuthMode:       AuthModePassword,
		Password:       "wrong-password",
		KnownHostsPath: knownHostsPath,
	})
	if !errors.Is(badErr, ErrAuthentication) {
		t.Fatalf("expected ErrAuthentication, got %v", badErr)
	}
}

func TestRunEncryptedPrivateKeyAuth(t *testing.T) {
	clientKey := testPrivateKey(t)
	clientSigner, err := ssh.NewSignerFromKey(clientKey)
	if err != nil {
		t.Fatalf("create client signer: %v", err)
	}
	passphrase := []byte("key-passphrase")
	block, err := ssh.MarshalPrivateKeyWithPassphrase(clientKey, "test", passphrase)
	if err != nil {
		t.Fatalf("MarshalPrivateKeyWithPassphrase: %v", err)
	}
	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("write private key: %v", err)
	}
	server := startTestServer(t, func(config *ssh.ServerConfig) {
		config.PublicKeyCallback = func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if conn.User() != "testuser" || !bytes.Equal(key.Marshal(), clientSigner.PublicKey().Marshal()) {
				return nil, errors.New("unexpected public key")
			}
			return nil, nil
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := Run(ctx, Config{
		Host:           "testuser@" + server.host,
		Port:           server.port,
		AuthMode:       AuthModeKey,
		KeyPath:        keyPath,
		KeyPassphrase:  string(passphrase),
		KnownHostsPath: filepath.Join(t.TempDir(), "known_hosts"),
	}, "cat", strings.NewReader("key auth works"))
	if err != nil {
		t.Fatalf("Run() key authentication error = %v", err)
	}
	if got, want := string(result.Stdout), "received:key auth works"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestKeyPassphraseIsNeverSentAsAccountPassword(t *testing.T) {
	privateKey := testPrivateKey(t)
	passphrase := []byte("key-passphrase")
	block, err := ssh.MarshalPrivateKeyWithPassphrase(privateKey, "test", passphrase)
	if err != nil {
		t.Fatalf("MarshalPrivateKeyWithPassphrase: %v", err)
	}
	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("write private key: %v", err)
	}
	var passwordAttempts atomic.Int32
	server := startTestServer(t, func(config *ssh.ServerConfig) {
		config.PasswordCallback = func(_ ssh.ConnMetadata, _ []byte) (*ssh.Permissions, error) {
			passwordAttempts.Add(1)
			return nil, nil
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = Run(ctx, Config{
		Host:           "testuser@" + server.host,
		Port:           server.port,
		AuthMode:       AuthModeKey,
		KeyPath:        keyPath,
		KeyPassphrase:  string(passphrase),
		KnownHostsPath: filepath.Join(t.TempDir(), "known_hosts"),
	}, "cat", strings.NewReader("must not authenticate with passphrase"))
	if !errors.Is(err, ErrAuthentication) {
		t.Fatalf("Run() error = %v, want ErrAuthentication", err)
	}
	if attempts := passwordAttempts.Load(); attempts != 0 {
		t.Fatalf("key passphrase was sent as an account password %d times", attempts)
	}
}

func TestHostKeyCallbackAcceptsNewAndRejectsChangedKeys(t *testing.T) {
	for _, test := range []struct {
		name       string
		port       string
		remotePort int
	}{
		{name: "default port", port: "22", remotePort: 22},
		{name: "non-default port", port: "2222", remotePort: 2222},
	} {
		t.Run(test.name, func(t *testing.T) {
			knownHostsPath := filepath.Join(t.TempDir(), "known_hosts")
			first := testSigner(t)
			second := testSigner(t)
			callback, err := newHostKeyCallback("example.test", test.port, knownHostsPath)
			if err != nil {
				t.Fatalf("newHostKeyCallback: %v", err)
			}
			remote := &net.TCPAddr{IP: net.ParseIP("192.0.2.4"), Port: test.remotePort}
			if err := callback("ignored", remote, first.PublicKey()); err != nil {
				t.Fatalf("first host key should be accepted: %v", err)
			}
			if err := callback("ignored", remote, first.PublicKey()); err != nil {
				t.Fatalf("recorded host key should be accepted: %v", err)
			}
			data, err := os.ReadFile(knownHostsPath)
			if err != nil {
				t.Fatalf("read known_hosts: %v", err)
			}
			wantAddress := knownhosts.Normalize(net.JoinHostPort("example.test", test.port))
			if !strings.Contains(string(data), wantAddress) {
				t.Fatalf("known_hosts does not contain %q: %q", wantAddress, data)
			}
			if err := callback("ignored", remote, second.PublicKey()); err == nil {
				t.Fatal("changed host key was accepted")
			} else {
				var mismatch *HostKeyMismatchError
				if !errors.As(err, &mismatch) || len(mismatch.OldKeys) == 0 {
					t.Fatalf("changed host key should return a host key mismatch, got %T: %v", err, err)
				}
			}
		})
	}
}

func TestConcurrentFirstContactPinsOnlyOneHostKey(t *testing.T) {
	knownHostsPath := filepath.Join(t.TempDir(), "known_hosts")
	callback, err := newHostKeyCallback("example.test", "2222", knownHostsPath)
	if err != nil {
		t.Fatalf("newHostKeyCallback: %v", err)
	}
	keys := []ssh.Signer{testSigner(t), testSigner(t)}
	remote := &net.TCPAddr{IP: net.ParseIP("192.0.2.4"), Port: 2222}
	start := make(chan struct{})
	results := make(chan error, len(keys))
	var workers sync.WaitGroup
	for _, signer := range keys {
		workers.Add(1)
		go func(signer ssh.Signer) {
			defer workers.Done()
			<-start
			results <- callback("ignored", remote, signer.PublicKey())
		}(signer)
	}
	close(start)
	workers.Wait()
	close(results)

	accepted := 0
	rejected := 0
	for err := range results {
		if err == nil {
			accepted++
		} else {
			rejected++
		}
	}
	if accepted != 1 || rejected != 1 {
		t.Fatalf("concurrent first contact accepted=%d rejected=%d; want one each", accepted, rejected)
	}
	data, err := os.ReadFile(knownHostsPath)
	if err != nil {
		t.Fatalf("read known_hosts: %v", err)
	}
	if lines := strings.Count(string(data), "\n"); lines != 1 {
		t.Fatalf("known_hosts contains %d lines, want exactly one pinned key: %q", lines, data)
	}
}

func TestLoadPrivateKeyWithPassphrase(t *testing.T) {
	privateKey := testPrivateKey(t)
	passphrase := []byte("key-passphrase")
	block, err := ssh.MarshalPrivateKeyWithPassphrase(privateKey, "test", passphrase)
	if err != nil {
		t.Fatalf("MarshalPrivateKeyWithPassphrase: %v", err)
	}
	keyPath := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("write private key: %v", err)
	}
	if _, err := loadPrivateKey(keyPath, string(passphrase)); err != nil {
		t.Fatalf("load encrypted key with passphrase: %v", err)
	}
	if _, err := loadPrivateKey(keyPath, "wrong-passphrase"); !errors.Is(err, ErrPrivateKey) {
		t.Fatalf("wrong passphrase error = %v, want ErrPrivateKey", err)
	}
}

func TestResolveEndpoint(t *testing.T) {
	t.Setenv("USER", "localuser")
	t.Setenv("USERNAME", "localuser")
	cases := []struct {
		name string
		cfg  Config
		want endpoint
	}{
		{"user and default port", Config{Host: "deploy@example.test"}, endpoint{host: "example.test", port: "22", user: "deploy"}},
		{"explicit port and IPv6", Config{Host: "[2001:db8::1]", Port: "2222"}, endpoint{host: "2001:db8::1", port: "2222", user: "localuser"}},
		{"configured port name", Config{Host: "example.test", Port: "ssh"}, endpoint{host: "example.test", port: "22", user: "localuser"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveEndpoint(tc.cfg)
			if err != nil {
				t.Fatalf("resolveEndpoint: %v", err)
			}
			if got != tc.want {
				t.Fatalf("resolveEndpoint = %+v, want %+v", got, tc.want)
			}
		})
	}
	if _, err := resolveEndpoint(Config{Host: "example.test", Port: "0"}); err == nil {
		t.Fatal("port zero should be rejected")
	}
}

func TestRunContextDeadlineInterruptsSessionOpen(t *testing.T) {
	server := startStalledChannelServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()

	_, err := Run(ctx, Config{
		Host:           "testuser@" + server.host,
		Port:           server.port,
		AuthMode:       AuthModePassword,
		Password:       "test-password",
		KnownHostsPath: filepath.Join(t.TempDir(), "known_hosts"),
	}, "sleep", nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() error = %v, want context deadline", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("session open ignored cancellation for %s", elapsed)
	}
}

func TestLimitedWriterReportsTruncation(t *testing.T) {
	truncateCalls := 0
	writer := &limitedWriter{limit: 4, onTruncate: func() { truncateCalls++ }}
	if n, err := writer.Write([]byte("abcdef")); n != 6 || err != nil {
		t.Fatalf("Write() = (%d, %v), want (6, nil)", n, err)
	}
	if n, err := writer.Write([]byte("later")); n != 5 || err != nil {
		t.Fatalf("second Write() = (%d, %v), want (5, nil)", n, err)
	}
	if got := string(writer.Bytes()); got != "abcd" {
		t.Fatalf("captured bytes = %q, want abcd", got)
	}
	if !writer.Truncated() {
		t.Fatal("writer did not record truncation")
	}
	if truncateCalls != 1 {
		t.Fatalf("onTruncate was called %d times, want once", truncateCalls)
	}
}

func startStalledChannelServer(t *testing.T) testSSHServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	config := &ssh.ServerConfig{
		PasswordCallback: func(_ ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if string(password) != "test-password" {
				return nil, errors.New("bad password")
			}
			return nil, nil
		},
	}
	signer, err := ssh.NewSignerFromKey(testPrivateKey(t))
	if err != nil {
		t.Fatalf("create host signer: %v", err)
	}
	config.AddHostKey(signer)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				serverConn, _, requests, err := ssh.NewServerConn(conn, config)
				if err != nil {
					_ = conn.Close()
					return
				}
				go ssh.DiscardRequests(requests)
				_ = serverConn.Wait()
				_ = serverConn.Close()
			}(conn)
		}
	}()
	addr := listener.Addr().(*net.TCPAddr)
	return testSSHServer{host: "127.0.0.1", port: fmt.Sprint(addr.Port)}
}

type testSSHServer struct {
	host string
	port string
}

func startTestServer(t *testing.T, configure func(*ssh.ServerConfig)) testSSHServer {
	t.Helper()
	signer, err := ssh.NewSignerFromKey(testPrivateKey(t))
	if err != nil {
		t.Fatalf("create host signer: %v", err)
	}
	return startTestServerWithHostKey(t, signer, configure)
}

// runEcho 对测试服务器执行一次 cat，把 stdin 原样回显，用于验证整条链路连通。
func runEcho(t *testing.T, server testSSHServer, stdin string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := Run(ctx, Config{
		Host:           "testuser@" + server.host,
		Port:           server.port,
		AuthMode:       AuthModePassword,
		Password:       "test-password",
		KnownHostsPath: filepath.Join(t.TempDir(), "known_hosts"),
	}, "cat", strings.NewReader(stdin))
	return string(result.Stdout), err
}

func TestRunToleratesSlowServerAuthentication(t *testing.T) {
	// 回归：建连与“密钥交换+认证”必须各用各的预算。老实现共用一个十秒预算，
	// 只要服务端应答认证比它慢，就会在建连与密钥交换都成功的情况下报“握手超时”。
	oldDial, oldHandshake := dialTimeout, handshakeTimeout
	t.Cleanup(func() { dialTimeout, handshakeTimeout = oldDial, oldHandshake })
	dialTimeout = 50 * time.Millisecond
	handshakeTimeout = 10 * time.Second

	server := startTestServer(t, func(config *ssh.ServerConfig) {
		config.PasswordCallback = func(_ ssh.ConnMetadata, _ []byte) (*ssh.Permissions, error) {
			time.Sleep(300 * time.Millisecond) // 远慢于建连预算
			return nil, nil
		}
	})
	stdout, err := runEcho(t, server, "slow-auth")
	if err != nil {
		t.Fatalf("Run() error = %v, want success: the authentication budget must not be the dial budget", err)
	}
	if want := "received:slow-auth"; stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
}

func TestRunReportsAuthenticationTimeoutNotHandshakeFailure(t *testing.T) {
	oldHandshake := handshakeTimeout
	t.Cleanup(func() { handshakeTimeout = oldHandshake })
	handshakeTimeout = 200 * time.Millisecond

	// 服务端完成密钥交换后一直不应答认证：超时要说清是认证阶段的问题，
	// 不能笼统报“握手失败”，否则会被误读成协议不兼容。
	server := startTestServer(t, func(config *ssh.ServerConfig) {
		config.PasswordCallback = func(_ ssh.ConnMetadata, _ []byte) (*ssh.Permissions, error) {
			time.Sleep(5 * time.Second)
			return nil, nil
		}
	})
	_, err := runEcho(t, server, "stall")
	if err == nil {
		t.Fatal("Run() succeeded against a server that never answers authentication")
	}
	if !strings.Contains(err.Error(), "authentication") {
		t.Errorf("error %q should name the authentication phase", err)
	}
	if strings.Contains(err.Error(), "SSH key exchange with") {
		t.Errorf("error %q must not blame the key exchange, which already completed", err)
	}
}

func TestHandshakeTimeoutErrorDistinguishesPhases(t *testing.T) {
	kexErr := handshakeTimeoutError("example.test:22", false)
	if !strings.Contains(kexErr.Error(), "key exchange") {
		t.Errorf("key-exchange timeout error = %q, want it to mention the key exchange", kexErr)
	}
	authErr := handshakeTimeoutError("example.test:22", true)
	if !strings.Contains(authErr.Error(), "authentication") {
		t.Errorf("authentication timeout error = %q, want it to mention authentication", authErr)
	}
	if kexErr.Error() == authErr.Error() {
		t.Error("timeouts in different phases must not produce the same message")
	}
}

// startTestServerWithHostKey 与 startTestServer 相同，但由调用方指定主机密钥，
// 用于制造“记录里的指纹与服务器实际提供的指纹不一致”的场景。
func startTestServerWithHostKey(t *testing.T, hostSigner ssh.Signer, configure func(*ssh.ServerConfig)) testSSHServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	config := &ssh.ServerConfig{}
	configure(config)
	config.AddHostKey(hostSigner)

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go serveTestConnection(conn, config)
		}
	}()
	addr := listener.Addr().(*net.TCPAddr)
	return testSSHServer{host: "127.0.0.1", port: fmt.Sprint(addr.Port)}
}

func serveTestConnection(conn net.Conn, config *ssh.ServerConfig) {
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
		go serveTestSession(channel, channelRequests)
	}
}

func serveTestSession(channel ssh.Channel, requests <-chan *ssh.Request) {
	defer channel.Close()
	for request := range requests {
		if request.Type != "exec" {
			_ = request.Reply(false, nil)
			continue
		}
		_ = request.Reply(true, nil)
		input, err := io.ReadAll(channel)
		if err != nil {
			return
		}
		_, _ = io.WriteString(channel, "received:"+string(input))
		_, _ = io.WriteString(channel.Stderr(), "test stderr")
		_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{Status: 0}))
		return
	}
}

func testSigner(t *testing.T) ssh.Signer {
	t.Helper()
	signer, err := ssh.NewSignerFromKey(testPrivateKey(t))
	if err != nil {
		t.Fatalf("NewSignerFromKey: %v", err)
	}
	return signer
}

func testPrivateKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return privateKey
}

// hostKeyBlob 取出公钥在 known_hosts 里的 "<类型> <base64>" 形式，便于拼装带
// @revoked 标记或哈希化主机名的记录行。
func hostKeyBlob(t *testing.T, key ssh.PublicKey) string {
	t.Helper()
	_, blob, found := strings.Cut(knownhosts.Line([]string{"placeholder"}, key), " ")
	if !found {
		t.Fatalf("knownhosts.Line(%s) has no key blob", key.Type())
	}
	return blob
}

func TestHostKeyMismatchErrorCarriesBothFingerprints(t *testing.T) {
	knownHostsPath := filepath.Join(t.TempDir(), "known_hosts")
	recorded := testSigner(t)
	offered := testSigner(t)
	callback, err := newHostKeyCallback("example.test", "2222", knownHostsPath)
	if err != nil {
		t.Fatalf("newHostKeyCallback: %v", err)
	}
	remote := &net.TCPAddr{IP: net.ParseIP("192.0.2.4"), Port: 2222}
	if err := callback("ignored", remote, recorded.PublicKey()); err != nil {
		t.Fatalf("first host key should be accepted: %v", err)
	}

	err = callback("ignored", remote, offered.PublicKey())
	var mismatch *HostKeyMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("changed host key error = %T (%v), want *HostKeyMismatchError", err, err)
	}
	if !errors.Is(err, ErrHostKeyChanged) {
		t.Error("errors.Is(err, ErrHostKeyChanged) = false, want true")
	}
	if got, want := mismatch.Address, "example.test:2222"; got != want {
		t.Errorf("Address = %q, want %q", got, want)
	}
	if !strings.HasSuffix(mismatch.KnownHostsPath, "known_hosts") {
		t.Errorf("KnownHostsPath = %q, want the record file that was just pinned", mismatch.KnownHostsPath)
	}
	if len(mismatch.OldKeys) != 1 || !bytes.Equal(mismatch.OldKeys[0].Marshal(), recorded.PublicKey().Marshal()) {
		t.Errorf("OldKeys = %v, want the recorded key only", mismatch.OldKeys)
	}
	if mismatch.NewKey == nil || !bytes.Equal(mismatch.NewKey.Marshal(), offered.PublicKey().Marshal()) {
		t.Errorf("NewKey = %v, want the key offered by the server", mismatch.NewKey)
	}

	wantOld := recorded.PublicKey().Type() + " " + ssh.FingerprintSHA256(recorded.PublicKey())
	wantNew := offered.PublicKey().Type() + " " + ssh.FingerprintSHA256(offered.PublicKey())
	if got := mismatch.OldFingerprints(); len(got) != 1 || got[0] != wantOld {
		t.Errorf("OldFingerprints() = %v, want [%s]", got, wantOld)
	}
	if got := mismatch.NewFingerprint(); got != wantNew {
		t.Errorf("NewFingerprint() = %q, want %q", got, wantNew)
	}
	for _, print := range []string{wantOld, wantNew} {
		if !strings.Contains(err.Error(), print) {
			t.Errorf("error %q does not mention %q", err, print)
		}
	}

	// 不匹配绝不能顺手写进记录：旧指纹原样留着，等用户核对身份后再说。
	data, readErr := os.ReadFile(knownHostsPath)
	if readErr != nil {
		t.Fatalf("read known_hosts: %v", readErr)
	}
	if lines := strings.Count(string(data), "\n"); lines != 1 {
		t.Errorf("known_hosts has %d lines, want only the recorded key: %q", lines, data)
	}
	if !bytes.Contains(data, []byte(hostKeyBlob(t, recorded.PublicKey()))) {
		t.Errorf("recorded key disappeared from known_hosts: %q", data)
	}
	if bytes.Contains(data, []byte(hostKeyBlob(t, offered.PublicKey()))) {
		t.Errorf("mismatched key must not be pinned without approval: %q", data)
	}
}

func TestReplaceHostKeyDropsTargetRecordsAndKeepsTheRest(t *testing.T) {
	recordedA := testSigner(t)
	recordedB := testSigner(t)
	other := testSigner(t)
	replacement := testSigner(t)

	for _, test := range []struct {
		name    string
		address string
		content string
		dropped []string
		kept    []string
	}{
		{
			name:    "plain host on the default port",
			address: net.JoinHostPort("target.test", "22"),
			content: strings.Join([]string{
				"# pinned by hand",
				knownhosts.Line([]string{"other.test"}, other.PublicKey()),
				"@revoked target.test " + hostKeyBlob(t, other.PublicKey()),
				knownhosts.Line([]string{"target.test"}, recordedA.PublicKey()),
				knownhosts.Line([]string{"target.test"}, recordedB.PublicKey()),
				knownhosts.Line([]string{"[target.test]:2222"}, other.PublicKey()),
			}, "\n") + "\n",
			dropped: []string{hostKeyBlob(t, recordedA.PublicKey()), hostKeyBlob(t, recordedB.PublicKey())},
			kept:    []string{"# pinned by hand", "@revoked", hostKeyBlob(t, other.PublicKey()), "[target.test]:2222"},
		},
		{
			name:    "hashed host on a non-default port",
			address: net.JoinHostPort("target.test", "2222"),
			content: strings.Join([]string{
				"# pinned by hand",
				knownhosts.HashHostname(knownhosts.Normalize(net.JoinHostPort("target.test", "2222"))) + " " + hostKeyBlob(t, recordedA.PublicKey()),
				knownhosts.Line([]string{"target.test"}, recordedB.PublicKey()),
			}, "\n") + "\n",
			dropped: []string{hostKeyBlob(t, recordedA.PublicKey())},
			kept:    []string{"# pinned by hand", hostKeyBlob(t, recordedB.PublicKey())},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "known_hosts")
			if err := os.WriteFile(path, []byte(test.content), 0o600); err != nil {
				t.Fatalf("write known_hosts: %v", err)
			}

			if err := ReplaceHostKey(path, test.address, replacement.PublicKey()); err != nil {
				t.Fatalf("ReplaceHostKey: %v", err)
			}

			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read known_hosts: %v", err)
			}
			for _, blob := range test.dropped {
				if strings.Contains(string(data), blob) {
					t.Errorf("replaced key survived: %q in %q", blob, data)
				}
			}
			for _, want := range test.kept {
				if !strings.Contains(string(data), want) {
					t.Errorf("unrelated record %q was dropped: %q", want, data)
				}
			}
			if !strings.Contains(string(data), hostKeyBlob(t, replacement.PublicKey())) {
				t.Errorf("replacement key was not recorded: %q", data)
			}

			backup, err := os.ReadFile(path + ".old")
			if err != nil {
				t.Fatalf("known_hosts.old backup missing: %v", err)
			}
			if string(backup) != test.content {
				t.Errorf("backup = %q, want the original content %q", backup, test.content)
			}
		})
	}
}

func TestRunReportsHostKeyMismatchThenAcceptsAfterReplace(t *testing.T) {
	recorded := testSigner(t)
	offered := testSigner(t)
	server := startTestServerWithHostKey(t, offered, func(config *ssh.ServerConfig) {
		config.PasswordCallback = func(_ ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if string(password) != "test-password" {
				return nil, errors.New("bad password")
			}
			return nil, nil
		}
	})

	knownHostsPath := filepath.Join(t.TempDir(), ".ssh", "known_hosts")
	if err := os.MkdirAll(filepath.Dir(knownHostsPath), 0o700); err != nil {
		t.Fatalf("create known_hosts directory: %v", err)
	}
	address := net.JoinHostPort(server.host, server.port)
	if err := os.WriteFile(knownHostsPath, []byte(knownhosts.Line([]string{address}, recorded.PublicKey())+"\n"), 0o600); err != nil {
		t.Fatalf("write known_hosts: %v", err)
	}

	cfg := Config{
		Host:           "testuser@" + server.host,
		Port:           server.port,
		AuthMode:       AuthModePassword,
		Password:       "test-password",
		KnownHostsPath: knownHostsPath,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := Run(ctx, cfg, "cat", strings.NewReader("before"))
	var mismatch *HostKeyMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("Run() over a changed host key = %T (%v), want *HostKeyMismatchError", err, err)
	}
	if mismatch.NewKey == nil || !bytes.Equal(mismatch.NewKey.Marshal(), offered.PublicKey().Marshal()) {
		t.Fatal("mismatch did not carry the key offered by the server")
	}
	if len(mismatch.OldKeys) != 1 || !bytes.Equal(mismatch.OldKeys[0].Marshal(), recorded.PublicKey().Marshal()) {
		t.Fatal("mismatch did not carry the recorded key")
	}

	if err := ReplaceHostKey(mismatch.KnownHostsPath, mismatch.Address, mismatch.NewKey); err != nil {
		t.Fatalf("ReplaceHostKey: %v", err)
	}

	result, err := Run(ctx, cfg, "cat", strings.NewReader("after"))
	if err != nil {
		t.Fatalf("Run() after replacing the record error = %v", err)
	}
	if got, want := string(result.Stdout), "received:after"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

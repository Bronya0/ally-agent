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
				var keyErr *knownhosts.KeyError
				if !errors.As(err, &keyErr) || len(keyErr.Want) == 0 {
					t.Fatalf("changed host key should return a known-host mismatch, got %T: %v", err, err)
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
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	config := &ssh.ServerConfig{}
	configure(config)
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

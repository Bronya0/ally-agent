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
	"encoding/binary"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// 回归：一个活着但没有身份的 ssh-agent 不能把本机默认私钥（~/.ssh）挡死。
// x/crypto 的客户端按认证方法名去重，agent 回调与默认私钥若各自注册成一个
// "publickey" 方法，前一个失败就会把整类标成已试过，后面的私钥再也轮不到。
// 所以 agent 模式的密钥来源必须合并进同一个认证方法。

// fakeAgent 只实现最小 agent 协议：被问身份时回一份给定的公钥列表，其余请求
// 一律失败。帧格式与 x/crypto 一致（4 字节大端长度 + 负载，负载首字节是类型）。
type fakeAgent struct {
	socketPath string
	keys       []ssh.PublicKey
}

func startFakeAgent(t *testing.T, keys ...ssh.PublicKey) *fakeAgent {
	t.Helper()
	// socket 路径必须短：macOS 的 sun_path 上限约 104 字节，而 t.TempDir() 会带上
	// 测试名与子测试名，名字一长就直接 bind 失败（invalid argument）。
	dir, err := os.MkdirTemp("", "ally-agent")
	if err != nil {
		t.Fatalf("mkdir temp agent dir: %v", err)
	}
	socketPath := filepath.Join(dir, "s")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
		_ = os.RemoveAll(dir)
	})
	fake := &fakeAgent{socketPath: socketPath, keys: keys}
	go fake.serve(listener)
	t.Setenv("SSH_AUTH_SOCK", socketPath)
	return fake
}

func (f *fakeAgent) serve(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go f.serveConn(conn)
	}
}

// agent 协议里用到的三个消息类型。
const (
	agentMsgFailure           = 5
	agentMsgRequestIdentities = 11
	agentMsgIdentitiesAnswer  = 12
)

func (f *fakeAgent) serveConn(conn net.Conn) {
	defer conn.Close()
	for {
		var sizeBuf [4]byte
		if _, err := io.ReadFull(conn, sizeBuf[:]); err != nil {
			return
		}
		request := make([]byte, binary.BigEndian.Uint32(sizeBuf[:]))
		if _, err := io.ReadFull(conn, request); err != nil {
			return
		}
		if len(request) == 0 {
			return
		}
		reply := []byte{agentMsgFailure}
		if request[0] == agentMsgRequestIdentities {
			reply = f.identitiesAnswer()
		}
		frame := make([]byte, 4+len(reply))
		binary.BigEndian.PutUint32(frame, uint32(len(reply)))
		copy(frame[4:], reply)
		if _, err := conn.Write(frame); err != nil {
			return
		}
	}
}

// identitiesAnswer 渲染 SSH_AGENT_IDENTITIES_ANSWER：类型 + 条目数 + 每条
// “公钥 blob + 注释”。
func (f *fakeAgent) identitiesAnswer() []byte {
	reply := make([]byte, 5)
	reply[0] = agentMsgIdentitiesAnswer
	binary.BigEndian.PutUint32(reply[1:], uint32(len(f.keys)))
	for _, key := range f.keys {
		blob := key.Marshal()
		reply = append(reply, 0, 0, 0, 0)
		binary.BigEndian.PutUint32(reply[len(reply)-4:], uint32(len(blob)))
		reply = append(reply, blob...)
		reply = append(reply, 0, 0, 0, 0) // 注释（空）
	}
	return reply
}

// signers 用 x/crypto 的正式客户端读一次身份，用来确认假 agent 的协议是对的：
// 否则“代理为空”可能只是因为假 agent 讲坏了协议，回归测试就失去意义。
func (f *fakeAgent) signers() ([]ssh.Signer, error) {
	conn, err := net.Dial("unix", f.socketPath)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	return agent.NewClient(conn).Signers()
}

// setFakeHome 把「家目录」指向 dir：类 Unix 的 os.UserHomeDir() 读 HOME，Windows 读
// USERPROFILE——只设 HOME 的话，Windows 上 sshclient 会去**真实**用户目录里找 ~/.ssh，
// 「临时家目录」这份隔离就静默失效了（表现为认证失败，而不是环境错误，很难往隔离上想）。
// 仓库里其它包（internal/app 的 redirectAppStateDir、internal/tools/command）也是两个
// 都设，见 LESSONS 的 home-isolation。
func setFakeHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}

// writeDefaultPrivateKey 在临时 HOME 下放一份无口令私钥，等价于本机已配好的
// ~/.ssh/id_ed25519，并把它作为默认私钥的来源。
func writeDefaultPrivateKey(t *testing.T) ssh.Signer {
	t.Helper()
	privateKey := testPrivateKey(t)
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatalf("NewSignerFromKey: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(privateKey, "test")
	if err != nil {
		t.Fatalf("MarshalPrivateKey: %v", err)
	}
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatalf("mkdir .ssh: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "id_ed25519"), pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("write id_ed25519: %v", err)
	}
	setFakeHome(t, home)
	return signer
}

func TestFakeAgentSpeaksTheProtocol(t *testing.T) {
	for _, test := range []struct {
		name string
		keys []ssh.PublicKey
	}{
		{"empty", nil},
		{"with-key", []ssh.PublicKey{testSigner(t).PublicKey()}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := startFakeAgent(t, test.keys...)
			signers, err := fake.signers()
			if err != nil {
				t.Fatalf("fake agent signers: %v", err)
			}
			if len(signers) != len(test.keys) {
				t.Fatalf("fake agent offered %d signers, want %d", len(signers), len(test.keys))
			}
		})
	}
}

// agent 模式下无论代理里有没有身份，都只能有一个 publickey 认证方法。
func TestAgentModeKeepsOnePublicKeyMethod(t *testing.T) {
	writeDefaultPrivateKey(t)
	for _, test := range []struct {
		name string
		keys []ssh.PublicKey
	}{
		{"empty-agent", nil},
		{"agent-with-key", []ssh.PublicKey{testSigner(t).PublicKey()}},
	} {
		t.Run(test.name, func(t *testing.T) {
			startFakeAgent(t, test.keys...)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			methods, closeAgent, err := authMethods(ctx, Config{AuthMode: AuthModeAgent})
			if err != nil {
				t.Fatalf("authMethods: %v", err)
			}
			defer closeAgent()

			if len(methods) != 1 {
				t.Fatalf("authMethods() registered %d methods, want 1: two same-named publickey methods let the first failure skip the other", len(methods))
			}
		})
	}
}

// 本机 agent 活着但没有身份时，~/.ssh 里的免密私钥仍然要能用上。
func TestEmptyAgentStillUsesDefaultPrivateKeys(t *testing.T) {
	defaultSigner := writeDefaultPrivateKey(t)
	startFakeAgent(t)

	server := startTestServer(t, func(config *ssh.ServerConfig) {
		config.PublicKeyCallback = func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if conn.User() != "testuser" || !bytes.Equal(key.Marshal(), defaultSigner.PublicKey().Marshal()) {
				return nil, errors.New("unexpected public key")
			}
			return nil, nil
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := Run(ctx, Config{
		Host:           "testuser@" + server.host,
		Port:           server.port,
		AuthMode:       AuthModeAgent,
		KnownHostsPath: filepath.Join(t.TempDir(), "known_hosts"),
	}, "cat", strings.NewReader("agent fallback works"))
	if err != nil {
		t.Fatalf("Run() error = %v, want success with the default private key: an empty ssh-agent must not shadow it", err)
	}
	if got, want := string(result.Stdout), "received:agent fallback works"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

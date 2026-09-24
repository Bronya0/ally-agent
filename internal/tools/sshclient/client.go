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
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

var (
	ErrAuthentication  = errors.New("SSH authentication failed")
	ErrHostKeyChanged  = errors.New("SSH host key changed")
	ErrPrivateKey      = errors.New("SSH private key error")
	ErrOutputTooLarge  = errors.New("SSH command output exceeded the capture limit")
	knownHostsFileLock sync.Mutex

	// dialTimeout 只覆盖 TCP 建连（含主机名解析）。
	dialTimeout = 10 * time.Second
	// handshakeTimeout 覆盖密钥交换与认证，与建连预算独立：老 sshd 可能在首次
	// 认证前做名称服务查询（例如 UseDNS 触发的反向解析），实测能拖住十秒以上，
	// 若与建连共用一个十秒预算，就会卡在服务端回包前的一瞬间被误报成“握手失败”。
	// 声明为变量（而非常量）是为了让测试能收紧预算，快速验证两段确实独立。
	handshakeTimeout = 30 * time.Second
)

const (
	defaultPort        = "22"
	maxStdoutBytes     = 64 * 1024 * 1024
	maxStderrBytes     = 64 * 1024
	maxPrivateKeyBytes = 1 * 1024 * 1024
	maxHostKeyFileMode = 0o600
)

// AuthMode selects exactly one authentication mode. In key mode, KeyPassphrase
// decrypts the private key and is never sent as an SSH account password.
type AuthMode string

const (
	AuthModeAgent    AuthMode = "agent"
	AuthModeKey      AuthMode = "key"
	AuthModePassword AuthMode = "password"
)

// Config describes a direct SSH connection. Host may include a user prefix
// (user@host).
type Config struct {
	Host           string
	Port           string
	AuthMode       AuthMode
	KeyPath        string
	KeyPassphrase  string
	Password       string
	KnownHostsPath string
}

// Result contains output captured from one remote command.
type Result struct {
	Stdout []byte
	Stderr []byte
}

// Run connects to a host, executes one remote command, and returns its output.
// The remote command's exit status is returned as an error, just like ssh.Session.Run.
func Run(ctx context.Context, cfg Config, command string, stdin io.Reader) (Result, error) {
	if ctx == nil {
		return Result{}, errors.New("SSH context is required")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(command) == "" {
		return Result{}, errors.New("SSH command is required")
	}
	endpoint, err := resolveEndpoint(cfg)
	if err != nil {
		return Result{}, err
	}

	methods, closeAgent, err := authMethods(ctx, cfg)
	if err != nil {
		return Result{}, err
	}
	defer closeAgent()

	knownHostsPath := cfg.KnownHostsPath
	if knownHostsPath == "" {
		knownHostsPath, err = DefaultKnownHostsPath()
		if err != nil {
			return Result{}, fmt.Errorf("locate SSH known_hosts: %w", err)
		}
	}
	callback, err := newHostKeyCallback(endpoint.host, endpoint.port, knownHostsPath)
	if err != nil {
		return Result{}, err
	}

	client, err := dial(ctx, endpoint, methods, callback)
	if err != nil {
		return Result{}, err
	}
	defer client.Close()

	stopCancel := make(chan struct{})
	cancelWatcherDone := make(chan struct{})
	go func() {
		defer close(cancelWatcherDone)
		select {
		case <-ctx.Done():
			_ = client.Close()
		case <-stopCancel:
		}
	}()

	session, err := client.NewSession()
	if err != nil {
		close(stopCancel)
		<-cancelWatcherDone
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		return Result{}, fmt.Errorf("open SSH session: %w", err)
	}
	defer session.Close()

	stdout := &limitedWriter{limit: maxStdoutBytes, onTruncate: func() { _ = client.Close() }}
	stderr := &limitedWriter{limit: maxStderrBytes, onTruncate: func() { _ = client.Close() }}
	session.Stdout = stdout
	session.Stderr = stderr
	session.Stdin = stdin

	runErr := session.Run(command)
	close(stopCancel)
	<-cancelWatcherDone
	result := Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if stdout.Truncated() || stderr.Truncated() {
		return result, ErrOutputTooLarge
	}
	if runErr != nil {
		return result, runErr
	}
	return result, nil
}

// Test verifies the SSH connection and authentication with the remote host.
// It establishes a connection, verifies host keys and credentials, opens
// a test session channel to verify shell/session readiness, and returns the elapsed latency.
func Test(ctx context.Context, cfg Config) (time.Duration, error) {
	if ctx == nil {
		return 0, errors.New("SSH context is required")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	endpoint, err := resolveEndpoint(cfg)
	if err != nil {
		return 0, err
	}

	methods, closeAgent, err := authMethods(ctx, cfg)
	if err != nil {
		return 0, err
	}
	defer closeAgent()

	knownHostsPath := cfg.KnownHostsPath
	if knownHostsPath == "" {
		knownHostsPath, err = DefaultKnownHostsPath()
		if err != nil {
			return 0, fmt.Errorf("locate SSH known_hosts: %w", err)
		}
	}
	callback, err := newHostKeyCallback(endpoint.host, endpoint.port, knownHostsPath)
	if err != nil {
		return 0, err
	}

	start := time.Now()
	client, err := dial(ctx, endpoint, methods, callback)
	if err != nil {
		return time.Since(start), err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return time.Since(start), fmt.Errorf("open SSH session: %w", err)
	}
	_ = session.Close()

	return time.Since(start), nil
}

// DefaultKnownHostsPath returns the OpenSSH-compatible per-user known_hosts path.
func DefaultKnownHostsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ssh", "known_hosts"), nil
}

type endpoint struct {
	host string
	port string
	user string
}

func resolveEndpoint(cfg Config) (endpoint, error) {
	hostValue := strings.TrimSpace(cfg.Host)
	if hostValue == "" {
		return endpoint{}, errors.New("SSH host is required")
	}
	userName := ""
	if at := strings.LastIndex(hostValue, "@"); at >= 0 {
		userName = strings.TrimSpace(hostValue[:at])
		hostValue = strings.TrimSpace(hostValue[at+1:])
	}
	if strings.HasPrefix(hostValue, "[") && strings.HasSuffix(hostValue, "]") {
		hostValue = strings.TrimSuffix(strings.TrimPrefix(hostValue, "["), "]")
	}
	if hostValue == "" {
		return endpoint{}, errors.New("SSH host is required")
	}

	port := strings.TrimSpace(cfg.Port)
	if port == "" {
		port = defaultPort
	}
	portNumber, err := net.LookupPort("tcp", port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return endpoint{}, fmt.Errorf("invalid SSH port %q", port)
	}
	port = fmt.Sprint(portNumber)
	if userName == "" {
		userName = defaultUser()
	}
	if userName == "" {
		return endpoint{}, errors.New("SSH username is required")
	}
	return endpoint{host: hostValue, port: port, user: userName}, nil
}

func defaultUser() string {
	name := os.Getenv("USER")
	if runtime.GOOS == "windows" {
		name = os.Getenv("USERNAME")
	}
	if name == "" {
		current, err := user.Current()
		if err == nil {
			name = current.Username
		}
	}
	if i := strings.LastIndexAny(name, `/\\`); i >= 0 {
		name = name[i+1:]
	}
	return strings.TrimSpace(name)
}

// handshakeTimeoutError 把密钥交换与认证两阶段的超时分开描述，并给出认证阶段最常见
// 的服务端原因：认证要等服务端先完成自己的名称服务查询，而不是协议不兼容。
func handshakeTimeoutError(address string, keyExchangeDone bool) error {
	if !keyExchangeDone {
		return fmt.Errorf("SSH key exchange with %s timed out after %s", address, handshakeTimeout)
	}
	return fmt.Errorf("SSH authentication with %s timed out after %s: the server completed the key exchange but never answered authentication; a slow name-service lookup on the server (for example an sshd UseDNS reverse DNS query) is a common cause", address, handshakeTimeout)
}

func dial(ctx context.Context, target endpoint, methods []ssh.AuthMethod, callback ssh.HostKeyCallback) (*ssh.Client, error) {
	address := net.JoinHostPort(target.host, target.port)
	dialCtx, cancelDial := context.WithTimeout(ctx, dialTimeout)
	defer cancelDial()

	netConn, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", address)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(dialCtx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("SSH connection to %s timed out after %s", address, dialTimeout)
		}
		return nil, fmt.Errorf("connect to SSH host %s: %w", address, err)
	}

	handshakeCtx, cancelHandshake := context.WithTimeout(ctx, handshakeTimeout)
	defer cancelHandshake()
	if deadline, ok := handshakeCtx.Deadline(); ok {
		_ = netConn.SetDeadline(deadline)
	}
	stopCancel := make(chan struct{})
	cancelWatcherDone := make(chan struct{})
	go func() {
		defer close(cancelWatcherDone)
		select {
		case <-handshakeCtx.Done():
			_ = netConn.Close()
		case <-stopCancel:
		}
	}()

	// 指纹校验回调在密钥交换阶段被调用，用它区分超时是卡在密钥交换还是认证。
	var keyExchangeDone atomic.Bool
	clientConfig := &ssh.ClientConfig{
		User: target.user,
		Auth: methods,
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			keyExchangeDone.Store(true)
			return callback(hostname, remote, key)
		},
		// 只有 ssh.Dial 会用到该字段；此处连接已建立，预算由上面的 context 控制。
		Timeout: dialTimeout,
	}
	sshConn, channels, requests, handshakeErr := ssh.NewClientConn(netConn, address, clientConfig)
	handshakeErrCtx := handshakeCtx.Err()
	close(stopCancel)
	<-cancelWatcherDone
	if ctx.Err() != nil {
		_ = netConn.Close()
		return nil, ctx.Err()
	}
	if errors.Is(handshakeErrCtx, context.DeadlineExceeded) {
		_ = netConn.Close()
		return nil, handshakeTimeoutError(address, keyExchangeDone.Load())
	}
	if handshakeErr != nil {
		_ = netConn.Close()
		// 指纹不匹配由校验回调构造成携带新旧公钥的结构化错误（见
		// HostKeyMismatchError），这里原样上抛，交由审批闸门展示对比。
		var mismatch *HostKeyMismatchError
		if errors.As(handshakeErr, &mismatch) {
			return nil, mismatch
		}
		if strings.Contains(strings.ToLower(handshakeErr.Error()), "unable to authenticate") {
			return nil, fmt.Errorf("%w for %s: %v", ErrAuthentication, address, handshakeErr)
		}
		return nil, fmt.Errorf("SSH handshake with %s: %w", address, handshakeErr)
	}
	_ = netConn.SetDeadline(time.Time{})
	return ssh.NewClient(sshConn, channels, requests), nil
}

func authMethods(ctx context.Context, cfg Config) ([]ssh.AuthMethod, func(), error) {
	methods := make([]ssh.AuthMethod, 0, 2)
	closeAgent := func() {}
	mode := cfg.AuthMode
	if mode == "" {
		switch {
		case strings.TrimSpace(cfg.KeyPath) != "":
			mode = AuthModeKey
		case cfg.Password != "":
			mode = AuthModePassword
		default:
			mode = AuthModeAgent
		}
	}

	switch mode {
	case AuthModeKey:
		if strings.TrimSpace(cfg.KeyPath) == "" || cfg.Password != "" {
			return nil, closeAgent, errors.New("key authentication requires keyPath and does not accept an SSH account password")
		}
		signer, err := loadPrivateKey(cfg.KeyPath, cfg.KeyPassphrase)
		if err != nil {
			return nil, closeAgent, err
		}
		methods = append(methods, ssh.PublicKeys(signer))
	case AuthModePassword:
		if cfg.Password == "" || cfg.KeyPath != "" || cfg.KeyPassphrase != "" {
			return nil, closeAgent, errors.New("password authentication requires only a non-empty account password")
		}
		methods = append(methods, ssh.Password(cfg.Password))
	case AuthModeAgent:
		if cfg.KeyPath != "" || cfg.KeyPassphrase != "" || cfg.Password != "" {
			return nil, closeAgent, errors.New("agent authentication does not accept a private key or password")
		}
		agentCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		conn, agentErr := dialAgent(agentCtx)
		cancel()
		if agentErr == nil {
			agentClient := agent.NewClient(conn)
			methods = append(methods, ssh.PublicKeysCallback(agentClient.Signers))
			closeAgent = func() { _ = conn.Close() }
		}
		if signers := defaultSigners(); len(signers) > 0 {
			methods = append(methods, ssh.PublicKeys(signers...))
		}
	default:
		return nil, closeAgent, fmt.Errorf("unsupported SSH authentication mode %q", mode)
	}
	return methods, closeAgent, nil
}

func loadPrivateKey(keyPath, passphrase string) (ssh.Signer, error) {
	path, err := expandHome(keyPath)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPrivateKey, err)
	}
	data, err := readPrivateKey(path)
	if err != nil {
		return nil, fmt.Errorf("%w: read %q: %v", ErrPrivateKey, path, err)
	}
	defer clear(data)
	signer, err := ssh.ParsePrivateKey(data)
	if err == nil {
		return signer, nil
	}
	if passphrase != "" {
		pass := []byte(passphrase)
		defer clear(pass)
		signer, passErr := ssh.ParsePrivateKeyWithPassphrase(data, pass)
		if passErr == nil {
			return signer, nil
		}
		err = passErr
	}
	return nil, fmt.Errorf("%w: could not parse %q (for an encrypted key, verify its passphrase): %v", ErrPrivateKey, path, err)
}

func readPrivateKey(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, int64(maxPrivateKeyBytes+1)))
	closeErr := file.Close()
	if readErr != nil {
		clear(data)
		return nil, readErr
	}
	if closeErr != nil {
		clear(data)
		return nil, closeErr
	}
	if len(data) > maxPrivateKeyBytes {
		clear(data)
		return nil, fmt.Errorf("private key exceeds %d-byte limit", maxPrivateKeyBytes)
	}
	return data, nil
}

func defaultSigners() []ssh.Signer {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var signers []ssh.Signer
	for _, name := range []string{"id_ed25519", "id_ecdsa", "id_rsa", "id_dsa"} {
		path := filepath.Join(home, ".ssh", name)
		data, err := readPrivateKey(path)
		if err != nil {
			continue
		}
		signer, err := ssh.ParsePrivateKey(data)
		clear(data)
		if err == nil {
			signers = append(signers, signer)
		}
	}
	return signers
}

func expandHome(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" || path[0] != '~' || (len(path) > 1 && path[1] != '/' && path[1] != '\\') {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if len(path) == 1 {
		return home, nil
	}
	return filepath.Join(home, path[2:]), nil
}

// HostKeyMismatchError 描述一次主机指纹不匹配：记录中已保存的公钥、本次连接实际
// 收到的公钥，以及记录所在文件。替换记录与否是上层策略：本项目 app 层收到它就直接
// 调用 ReplaceHostKey 换新公钥并重连一次（不询问用户，见 orch_remote.go 的
// refreshHostKeyAndRetry）；两次都对不上才把新旧指纹报给用户人工核对。
type HostKeyMismatchError struct {
	// Address 是 host:port 形式（net.JoinHostPort），既用于匹配记录行也用于展示。
	Address        string
	KnownHostsPath string
	OldKeys        []ssh.PublicKey
	NewKey         ssh.PublicKey
}

func (e *HostKeyMismatchError) Error() string {
	recorded := strings.Join(e.OldFingerprints(), ", ")
	if recorded == "" {
		recorded = "<none>"
	}
	return fmt.Sprintf("host key verification failed for %s: recorded %s, server offered %s",
		e.Address, recorded, e.NewFingerprint())
}

// Is 让 errors.Is(err, ErrHostKeyChanged) 对指纹不匹配继续成立。
func (e *HostKeyMismatchError) Is(target error) bool { return target == ErrHostKeyChanged }

// OldFingerprints 返回记录中已保存公钥的指纹，与 ssh-keygen -l 的口径一致。
func (e *HostKeyMismatchError) OldFingerprints() []string {
	prints := make([]string, 0, len(e.OldKeys))
	for _, key := range e.OldKeys {
		prints = append(prints, describeHostKey(key))
	}
	return prints
}

// NewFingerprint 返回本次连接收到的公钥指纹。
func (e *HostKeyMismatchError) NewFingerprint() string { return describeHostKey(e.NewKey) }

// describeHostKey 渲染成 "<算法> SHA256:<指纹>"，便于用户与 ssh-keygen -l 输出核对。
func describeHostKey(key ssh.PublicKey) string {
	if key == nil {
		return "<unknown>"
	}
	return key.Type() + " " + ssh.FingerprintSHA256(key)
}

// known_hosts 里的两个标记：带它们的行承载 CA 信任与撤销信息，与具体主机指纹
// 无关，替换记录时必须原样保留。
const (
	knownHostsMarkerCertAuthority = "@cert-authority"
	knownHostsMarkerRevoked       = "@revoked"
)

// resolveKnownHostsPath 展开 ~、转绝对路径、建父目录、确保文件存在并解析符号链接。
// 连接校验与指纹替换共用它：两处若各自解析，相对路径或符号链接会让它们在不同
// 路径上加锁，从而并发覆盖对方的写入。
func resolveKnownHostsPath(knownHostsPath string) (string, error) {
	path, err := expandHome(knownHostsPath)
	if err != nil {
		return "", fmt.Errorf("resolve SSH known_hosts path: %w", err)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve SSH known_hosts path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create SSH known_hosts directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, maxHostKeyFileMode)
	if err != nil {
		return "", fmt.Errorf("open SSH known_hosts file: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close SSH known_hosts file: %w", err)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve SSH known_hosts file: %w", err)
	}
	return path, nil
}

// withKnownHostsLock 串行化对同一份 known_hosts 的读改写：进程内互斥 + 跨进程文件锁
// (<path>.lock)。首次连接固化指纹与用户确认后的替换共用它，否则两个连接会各自
// 读入旧内容再各自写回，后写的一方悄悄丢掉前者的记录。
func withKnownHostsLock(path string, fn func() error) error {
	knownHostsFileLock.Lock()
	defer knownHostsFileLock.Unlock()

	lockFile, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, maxHostKeyFileMode)
	if err != nil {
		return fmt.Errorf("open SSH known_hosts lock: %w", err)
	}
	defer lockFile.Close()
	unlock, err := lockKnownHostsFile(lockFile)
	if err != nil {
		return fmt.Errorf("lock SSH known_hosts file: %w", err)
	}
	defer unlock()

	return fn()
}

// ReplaceHostKey 用本次连接收到的公钥替换记录中该地址的旧公钥。是否先问人由调用方
// 决定：地址被别的机器占用与服务器重装，在协议层长得一模一样，本项目 app 层选择
// 直接替换（等价 StrictHostKeyChecking=no），只保留 <path>.old 备份供人工回溯。
//
// 替换前把原文件备份为 <path>.old（与 ssh-keygen -R 的习惯一致），便于回滚。
// 带 @cert-authority / @revoked 标记的行一律原样保留：一次“信任新指纹”不应该
// 顺手抹掉撤销标记或 CA 信任。
func ReplaceHostKey(knownHostsPath, address string, key ssh.PublicKey) error {
	if key == nil {
		return errors.New("SSH host key is required")
	}
	host, port, err := splitKnownHostsAddress(address)
	if err != nil {
		return err
	}
	path, err := resolveKnownHostsPath(knownHostsPath)
	if err != nil {
		return err
	}

	return withKnownHostsLock(path, func() error {
		existing, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read SSH known_hosts file: %w", err)
		}
		if len(existing) > 0 {
			if err := os.WriteFile(path+".old", existing, maxHostKeyFileMode); err != nil {
				return fmt.Errorf("back up SSH known_hosts file: %w", err)
			}
		}
		next := appendHostKeyRecord(dropKnownHostsLines(existing, host, port), address, key)
		return writeFileAtomically(path, next)
	})
}

// splitKnownHostsAddress 把 host:port 拆成匹配记录用的两段；缺端口时按 22 处理。
func splitKnownHostsAddress(address string) (string, string, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return "", "", errors.New("SSH host is required")
	}
	if host, port, err := net.SplitHostPort(address); err == nil {
		return host, port, nil
	}
	return strings.Trim(address, "[]"), defaultPort, nil
}

// dropKnownHostsLines 去掉描述该地址的普通记录行，其余内容（注释、空行、带标记的
// 行、其它主机）含各自换行符原样保留。
func dropKnownHostsLines(content []byte, host, port string) []byte {
	if len(content) == 0 {
		return nil
	}
	kept := make([]byte, 0, len(content))
	for _, line := range strings.SplitAfter(string(content), "\n") {
		if knownHostsLineCoversAddress(strings.TrimRight(line, "\r\n"), host, port) {
			continue
		}
		kept = append(kept, line...)
	}
	return kept
}

// appendHostKeyRecord 追加一行记录；文件末尾缺换行时先补一个，避免新记录粘在上一
// 行尾部而变成无效行。
func appendHostKeyRecord(content []byte, address string, key ssh.PublicKey) []byte {
	if len(content) > 0 && content[len(content)-1] != '\n' {
		content = append(content, '\n')
	}
	return append(content, hostKeyRecordLine(address, key)...)
}

// hostKeyRecordLine 渲染一行记录（含换行），地址按 OpenSSH 惯例规范化。
func hostKeyRecordLine(address string, key ssh.PublicKey) string {
	return knownhosts.Line([]string{address}, key) + "\n"
}

// writeFileAtomically 把整份内容原子地安装到 path：先写同目录临时文件再整体替换，
// 避免中途失败留下半份记录文件（那会让之后所有连接都读不出已保存的指纹）。
func writeFileAtomically(path string, content []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".known_hosts-*.tmp")
	if err != nil {
		return fmt.Errorf("create SSH known_hosts temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if err := tmp.Chmod(maxHostKeyFileMode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("restrict SSH known_hosts temp file: %w", err)
	}
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write SSH known_hosts temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close SSH known_hosts temp file: %w", err)
	}
	if err := replaceFileAtomically(tmpName, path); err != nil {
		return fmt.Errorf("install SSH known_hosts file: %w", err)
	}
	return nil
}

// replaceFileAtomically 安装同目录下已写好的临时文件；Windows 上覆盖已存在的目标
// 可能被拒绝，此时把旧文件挪到 <dst>.bak、装入新文件、失败则回滚。与 internal/app
// 里的同形 helper 不能共用：tools 层不得依赖 app 层。
func replaceFileAtomically(tmp, dst string) error {
	if err := os.Rename(tmp, dst); err == nil {
		return nil
	}
	backup := dst + ".bak"
	_ = os.Remove(backup)
	if err := os.Rename(dst, backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Rename(backup, dst)
		return err
	}
	_ = os.Remove(backup)
	return nil
}

// pinHostKey 记录首次连接见到的公钥，等价于 OpenSSH 的
// StrictHostKeyChecking=accept-new：只固化，绝不替换已有记录。
func pinHostKey(path, address string, key ssh.PublicKey) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, maxHostKeyFileMode)
	if err != nil {
		return fmt.Errorf("open SSH known_hosts file: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect SSH known_hosts file: %w", err)
	}
	if info.Size() > 0 {
		var last [1]byte
		if _, err := file.ReadAt(last[:], info.Size()-1); err != nil {
			return fmt.Errorf("read SSH known_hosts file: %w", err)
		}
		if last[0] != '\n' {
			if _, err := io.WriteString(file, "\n"); err != nil {
				return fmt.Errorf("save new SSH host key: %w", err)
			}
		}
	}
	if _, err := io.WriteString(file, hostKeyRecordLine(address, key)); err != nil {
		return fmt.Errorf("save new SSH host key: %w", err)
	}
	return nil
}

// knownKeysOf 取出记录里匹配该地址的已保存公钥。
func knownKeysOf(keyErr *knownhosts.KeyError) []ssh.PublicKey {
	keys := make([]ssh.PublicKey, 0, len(keyErr.Want))
	for _, want := range keyErr.Want {
		keys = append(keys, want.Key)
	}
	return keys
}

// knownHostsLineCoversAddress 判断一行记录是否描述了该地址。语义对齐 OpenSSH 与
// golang.org/x/crypto：主机名支持 * 与 ? 通配，可写 [host]:port 指定非默认端口，
// 可用逗号列多个模式，前缀 ! 表示排除；以 | 开头的是哈希化主机名，比对 HMAC-SHA1。
// 注释、空行与带 @cert-authority / @revoked 标记的行都返回 false（替换时保留）。
func knownHostsLineCoversAddress(line, host, port string) bool {
	fields := strings.Fields(line)
	if len(fields) < 2 || strings.HasPrefix(fields[0], "#") {
		return false
	}
	// marker 行承载撤销与 CA 信任，与具体主机指纹无关，一律不认领；普通行的
	// 第一个字段就是主机模式（key 类型与公钥在其后）。
	if fields[0] == knownHostsMarkerCertAuthority || fields[0] == knownHostsMarkerRevoked {
		return false
	}
	matched := false
	for _, pattern := range strings.Split(fields[0], ",") {
		negate := strings.HasPrefix(pattern, "!")
		pattern = strings.TrimPrefix(pattern, "!")
		if pattern == "" || !knownHostsPatternCoversAddress(pattern, host, port) {
			continue
		}
		if negate {
			return false
		}
		matched = true
	}
	return matched
}

// knownHostsPatternCoversAddress 判断单个主机模式是否覆盖该地址。
func knownHostsPatternCoversAddress(pattern, host, port string) bool {
	if strings.HasPrefix(pattern, "|") {
		salt, hash, err := decodeKnownHostsHash(pattern)
		if err != nil {
			// 无法解析的哈希形式保守处理：不认领这一行，宁可留下旧记录也不要
			// 误删别人的指纹。新记录会追加在后面，连接照样能建立。
			return false
		}
		return bytes.Equal(knownHostsHostHash(knownhosts.Normalize(net.JoinHostPort(host, port)), salt), hash)
	}
	patternHost, patternPort := splitKnownHostsPattern(pattern)
	return wildcardHostMatch(patternHost, host) && patternPort == port
}

// splitKnownHostsPattern 拆出模式里的主机与端口；无端口视为 22。
func splitKnownHostsPattern(pattern string) (string, string) {
	if host, port, err := net.SplitHostPort(pattern); err == nil {
		return host, port
	}
	return pattern, defaultPort
}

// decodeKnownHostsHash 解析 |1|salt|hash 形式的哈希化主机名（OpenSSH 的
// HashKnownHosts 开关，Debian/Ubuntu 默认开启）。类型字段固定为 1（HMAC-SHA1），
// 其它类型无法比对，返回错误由调用方保守处理。
func decodeKnownHostsHash(pattern string) (salt, hash []byte, err error) {
	parts := strings.Split(pattern, "|")
	if len(parts) != 4 || parts[1] != "1" {
		return nil, nil, errors.New("unsupported known_hosts host hash")
	}
	salt, err = base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, nil, err
	}
	hash, err = base64.StdEncoding.DecodeString(parts[3])
	if err != nil {
		return nil, nil, err
	}
	return salt, hash, nil
}

// knownHostsHostHash 复现 OpenSSH 的哈希化主机名：HMAC-SHA1(规范化后的 host:port, salt)。
func knownHostsHostHash(address string, salt []byte) []byte {
	mac := hmac.New(sha1.New, salt)
	mac.Write([]byte(address))
	return mac.Sum(nil)
}

// wildcardHostMatch 复现 OpenSSH addrmatch.c 的通配语义：* 可跨分隔符匹配任意长度，
// ? 匹配单个字符（与 filepath.Match 不同，后者不跨分隔符）。
func wildcardHostMatch(pattern, name string) bool {
	for {
		switch {
		case pattern == "":
			return name == ""
		case name == "":
			return false
		case pattern[0] == '*':
			if len(pattern) == 1 {
				return true
			}
			for i := range name {
				if wildcardHostMatch(pattern[1:], name[i:]) {
					return true
				}
			}
			return false
		case pattern[0] == '?' || pattern[0] == name[0]:
			pattern, name = pattern[1:], name[1:]
		default:
			return false
		}
	}
}

func newHostKeyCallback(host, port, knownHostsPath string) (ssh.HostKeyCallback, error) {
	path, err := resolveKnownHostsPath(knownHostsPath)
	if err != nil {
		return nil, err
	}

	address := net.JoinHostPort(host, port)

	return func(_ string, remote net.Addr, key ssh.PublicKey) error {
		return withKnownHostsLock(path, func() error {
			verify, err := knownhosts.New(path)
			if err != nil {
				return fmt.Errorf("read SSH known_hosts file: %w", err)
			}
			if err := verify(address, remote, key); err == nil {
				return nil
			} else {
				var keyErr *knownhosts.KeyError
				if !errors.As(err, &keyErr) {
					return err
				}
				if len(keyErr.Want) > 0 {
					// 已有记录但对不上：把新旧公钥一并交给上层，由调用方决定是否
					// 覆盖。客户端本身绝不偷偷改写记录 —— 服务器重装与地址被别的
					// 机器占用在协议层无法区分，替换与否只能是上层策略。
					return &HostKeyMismatchError{
						Address:        address,
						KnownHostsPath: path,
						OldKeys:        knownKeysOf(keyErr),
						NewKey:         key,
					}
				}
			}
			// Match OpenSSH StrictHostKeyChecking=accept-new: pin first-seen keys,
			// but never replace a previously trusted key after a mismatch.
			return pinHostKey(path, address, key)
		})
	}, nil
}

type limitedWriter struct {
	buffer     bytes.Buffer
	limit      int
	truncated  bool
	onTruncate func()
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	remaining := w.limit - w.buffer.Len()
	if remaining <= 0 {
		if len(p) > 0 && !w.truncated {
			w.truncated = true
			if w.onTruncate != nil {
				w.onTruncate()
			}
		}
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = w.buffer.Write(p[:remaining])
		w.truncated = true
		if w.onTruncate != nil {
			w.onTruncate()
		}
	} else {
		_, _ = w.buffer.Write(p)
	}
	return len(p), nil
}

func (w *limitedWriter) Bytes() []byte   { return w.buffer.Bytes() }
func (w *limitedWriter) Truncated() bool { return w.truncated }

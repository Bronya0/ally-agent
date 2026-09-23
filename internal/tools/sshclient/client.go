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
)

const (
	defaultPort        = "22"
	connectTimeout     = 10 * time.Second
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

func dial(ctx context.Context, target endpoint, methods []ssh.AuthMethod, callback ssh.HostKeyCallback) (*ssh.Client, error) {
	address := net.JoinHostPort(target.host, target.port)
	connectCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()

	netConn, err := (&net.Dialer{}).DialContext(connectCtx, "tcp", address)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(connectCtx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("SSH connection to %s timed out after %s", address, connectTimeout)
		}
		return nil, fmt.Errorf("connect to SSH host %s: %w", address, err)
	}

	if deadline, ok := connectCtx.Deadline(); ok {
		_ = netConn.SetDeadline(deadline)
	}
	stopCancel := make(chan struct{})
	cancelWatcherDone := make(chan struct{})
	go func() {
		defer close(cancelWatcherDone)
		select {
		case <-connectCtx.Done():
			_ = netConn.Close()
		case <-stopCancel:
		}
	}()

	clientConfig := &ssh.ClientConfig{
		User:            target.user,
		Auth:            methods,
		HostKeyCallback: callback,
		Timeout:         connectTimeout,
	}
	sshConn, channels, requests, handshakeErr := ssh.NewClientConn(netConn, address, clientConfig)
	connectErr := connectCtx.Err()
	close(stopCancel)
	<-cancelWatcherDone
	cancel()
	if ctx.Err() != nil {
		_ = netConn.Close()
		return nil, ctx.Err()
	}
	if errors.Is(connectErr, context.DeadlineExceeded) {
		_ = netConn.Close()
		return nil, fmt.Errorf("SSH handshake with %s timed out after %s", address, connectTimeout)
	}
	if handshakeErr != nil {
		_ = netConn.Close()
		var keyErr *knownhosts.KeyError
		if errors.As(handshakeErr, &keyErr) && len(keyErr.Want) > 0 {
			return nil, fmt.Errorf("%w for %s: %v", ErrHostKeyChanged, address, handshakeErr)
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

func newHostKeyCallback(host, port, knownHostsPath string) (ssh.HostKeyCallback, error) {
	path, err := expandHome(knownHostsPath)
	if err != nil {
		return nil, fmt.Errorf("resolve SSH known_hosts path: %w", err)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve SSH known_hosts path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create SSH known_hosts directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, maxHostKeyFileMode)
	if err != nil {
		return nil, fmt.Errorf("open SSH known_hosts file: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close SSH known_hosts file: %w", err)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return nil, fmt.Errorf("resolve SSH known_hosts file: %w", err)
	}

	address := net.JoinHostPort(host, port)
	knownHostsAddress := knownhosts.Normalize(address)

	return func(_ string, remote net.Addr, key ssh.PublicKey) error {
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

		verify, err := knownhosts.New(path)
		if err != nil {
			return fmt.Errorf("read SSH known_hosts file: %w", err)
		}
		if err := verify(address, remote, key); err == nil {
			return nil
		} else {
			var keyErr *knownhosts.KeyError
			if !errors.As(err, &keyErr) || len(keyErr.Want) != 0 {
				return err
			}
		}

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
		// Match OpenSSH StrictHostKeyChecking=accept-new: pin first-seen keys,
		// but never replace a previously trusted key after a mismatch.
		line := knownhosts.Line([]string{knownHostsAddress}, key) + "\n"
		if _, err := io.WriteString(file, line); err != nil {
			return fmt.Errorf("save new SSH host key: %w", err)
		}
		return nil
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

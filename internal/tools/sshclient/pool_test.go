package sshclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestPoolReuseAndInvalidate(t *testing.T) {
	var auth atomic.Int32
	server := startTestServer(t, func(c *ssh.ServerConfig) {
		c.PasswordCallback = func(_ ssh.ConnMetadata, _ []byte) (*ssh.Permissions, error) { auth.Add(1); return nil, nil }
	})
	cfg := Config{Host: "test@" + server.host, Port: server.port, AuthMode: AuthModePassword, Password: "pw", KnownHostsPath: filepath.Join(t.TempDir(), "known_hosts")}
	p := NewPool()
	defer p.Close()
	ctx := context.Background()
	for _, v := range []string{"one", "two"} {
		r, err := p.Run(ctx, "node", 0, cfg, "cat", strings.NewReader(v))
		if err != nil || string(r.Stdout) != "received:"+v {
			t.Fatalf("run %q: %q %v", v, r.Stdout, err)
		}
	}
	if n := auth.Load(); n != 1 {
		t.Fatalf("authentications = %d, want 1", n)
	}
	p.Invalidate("node")
	if _, err := p.Run(ctx, "node", 0, cfg, "cat", strings.NewReader("old")); err == nil {
		t.Fatal("stale node version accepted")
	}
	r, err := p.Run(ctx, "node", p.Version("node"), cfg, "cat", strings.NewReader("new"))
	if err != nil || string(r.Stdout) != "received:new" {
		t.Fatalf("after invalidation: %q %v", r.Stdout, err)
	}
	if n := auth.Load(); n != 2 {
		t.Fatalf("authentications = %d, want 2", n)
	}
}

func TestPoolControlledCancellationEOFAndReuse(t *testing.T) {
	const script = "finite script"
	ready := make(chan struct{})
	var auth atomic.Int32
	server := startTestServerWithHostKey(t, testSigner(t), func(c *ssh.ServerConfig) {
		c.PasswordCallback = func(_ ssh.ConnMetadata, _ []byte) (*ssh.Permissions, error) { auth.Add(1); return nil, nil }
	}, func(channel ssh.Channel, requests <-chan *ssh.Request) {
		defer channel.Close()
		for request := range requests {
			if request.Type != "exec" {
				_ = request.Reply(false, nil)
				continue
			}
			var execRequest struct{ Command string }
			if ssh.Unmarshal(request.Payload, &execRequest) != nil {
				return
			}
			_ = request.Reply(true, nil)
			var input []byte
			if execRequest.Command == "controlled" {
				input = make([]byte, len(script))
				if _, err := io.ReadFull(channel, input); err != nil {
					return
				}
				close(ready)
			}
			remaining, err := io.ReadAll(channel)
			if err != nil {
				return
			}
			_, _ = io.WriteString(channel, "received:"+string(append(input, remaining...)))
			_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{Status: 0}))
			return
		}
	})
	cfg := Config{Host: "test@" + server.host, Port: server.port, AuthMode: AuthModePassword, Password: "pw", KnownHostsPath: filepath.Join(t.TempDir(), "known_hosts")}
	p := NewPool()
	defer p.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		result Result
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := p.RunControlled(ctx, "node", 0, cfg, "controlled", script)
		done <- outcome{result, err}
	}()
	select {
	case <-ready:
	case result := <-done:
		t.Fatalf("controlled call ended before script arrived: %+v", result)
	case <-time.After(3 * time.Second):
		t.Fatal("controlled script did not arrive")
	}
	cancel()
	select {
	case result := <-done:
		if !errors.Is(result.err, context.Canceled) || string(result.result.Stdout) != "received:"+script {
			t.Fatalf("cancellation did not deliver EOF after the script: %+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("EOF cancellation fell through to session-close fallback")
	}
	result, err := p.Run(context.Background(), "node", 0, cfg, "cat", strings.NewReader("after"))
	if err != nil || string(result.Stdout) != "received:after" || auth.Load() != 1 {
		t.Fatalf("connection not reusable: result=%q err=%v authentications=%d", result.Stdout, err, auth.Load())
	}
}

func TestPoolCancelDoesNotCloseOtherSession(t *testing.T) {
	var auth atomic.Int32
	server := startTestServer(t, func(c *ssh.ServerConfig) {
		c.PasswordCallback = func(_ ssh.ConnMetadata, _ []byte) (*ssh.Permissions, error) { auth.Add(1); return nil, nil }
	})
	cfg := Config{Host: "test@" + server.host, Port: server.port, AuthMode: AuthModePassword, Password: "pw", KnownHostsPath: filepath.Join(t.TempDir(), "known_hosts")}
	p := NewPool()
	defer p.Close()
	// Keep one session open waiting for input while another is cancelled.
	reader, writer := io.Pipe()
	defer writer.Close()
	defer reader.Close()
	done := make(chan error, 1)
	go func() { _, err := p.Run(context.Background(), "node", 0, cfg, "cat", reader); done <- err }()
	// Wait for session admission rather than relying on timing.
	for {
		p.mu.Lock()
		e := p.entries["node"]
		ready := e != nil && !e.dialing && e.active == 1
		ch := p.changed
		p.mu.Unlock()
		if ready {
			break
		}
		select {
		case <-ch:
		case err := <-done:
			t.Fatalf("first session ended before admission: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader2, writer2 := io.Pipe()
	defer writer2.Close()
	cancelled := make(chan error, 1)
	go func() { _, err := p.Run(ctx, "node", 0, cfg, "cat", reader2); cancelled <- err }()
	for {
		p.mu.Lock()
		e := p.entries["node"]
		ready := e != nil && e.active == 2
		ch := p.changed
		p.mu.Unlock()
		if ready {
			break
		}
		select {
		case <-ch:
		case err := <-done:
			t.Fatalf("first session ended before second admission: %v", err)
		case err := <-cancelled:
			t.Fatalf("second session ended before admission: %v", err)
		}
	}
	cancel()
	_ = reader2.Close()
	if err := <-cancelled; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled call: %v", err)
	}

	_, _ = writer.Write([]byte("one"))
	_ = writer.Close()
	if err := <-done; err != nil {
		t.Fatalf("other session: %v", err)
	}
	if auth.Load() != 1 {
		t.Fatalf("authentications = %d", auth.Load())
	}
}
func TestPoolInvalidationDuringDial(t *testing.T) {
	started := make(chan struct{})
	unblock := make(chan struct{})
	defer close(unblock)
	server := startTestServer(t, func(c *ssh.ServerConfig) {
		c.PasswordCallback = func(_ ssh.ConnMetadata, _ []byte) (*ssh.Permissions, error) {
			close(started)
			<-unblock
			return nil, nil
		}
	})
	cfg := Config{Host: "test@" + server.host, Port: server.port, AuthMode: AuthModePassword, Password: "pw", KnownHostsPath: filepath.Join(t.TempDir(), "known_hosts")}
	p := NewPool()
	defer p.Close()
	done := make(chan error, 1)
	go func() {
		_, err := p.Run(context.Background(), "node", 0, cfg, "cat", strings.NewReader("old"))
		done <- err
	}()
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("dial ended before authentication: %v", err)
	}
	p.Invalidate("node")
	if err := <-done; err == nil {
		t.Fatal("in-flight connection survived invalidation")
	}
	p.mu.Lock()
	_, exists := p.entries["node"]
	p.mu.Unlock()
	if exists {
		t.Fatal("invalidated connection entered pool")
	}
}

func TestPoolCanceledUnansweredOpenIsRetiredAndClosed(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	config := &ssh.ServerConfig{PasswordCallback: func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) { return nil, nil }}
	config.AddHostKey(testSigner(t))
	opened := make(chan struct{})
	go func() {
		for i := 0; ; i++ {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			if i > 0 {
				go serveTestConnection(conn, config)
				continue
			}
			go func() {
				server, channels, requests, err := ssh.NewServerConn(conn, config)
				if err != nil {
					return
				}
				defer server.Close()
				go ssh.DiscardRequests(requests)
				for range channels {
					close(opened)
					_ = server.Wait()
					return
				}
			}()
		}
	}()
	addr := listener.Addr().(*net.TCPAddr)
	cfg := Config{Host: "test@127.0.0.1", Port: fmt.Sprint(addr.Port), AuthMode: AuthModePassword, Password: "pw", KnownHostsPath: filepath.Join(t.TempDir(), "known_hosts")}
	p := NewPool()
	p.openGrace = 25 * time.Millisecond
	defer p.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	defer cancel()
	go func() { _, err := p.Run(ctx, "node", 0, cfg, "cat", nil); done <- err }()
	select {
	case <-opened:
	case err := <-done:
		t.Fatalf("session ended before channel open: %v", err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled open: %v", err)
	}
	result, err := p.Run(context.Background(), "node", 0, cfg, "cat", strings.NewReader("fresh"))
	if err != nil || string(result.Stdout) != "received:fresh" {
		t.Fatalf("replacement connection: %q %v", result.Stdout, err)
	}
	deadline, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	for {
		p.mu.Lock()
		remaining, changed := len(p.all), p.changed
		p.mu.Unlock()
		if remaining == 1 {
			break
		}
		select {
		case <-changed:
		case <-deadline.Done():
			t.Fatal("stalled transport remained in pool")
		}
	}
}

func TestPoolCancelUnansweredExecDoesNotReplay(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	config := &ssh.ServerConfig{PasswordCallback: func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) { return nil, nil }}
	config.AddHostKey(testSigner(t))
	started := make(chan struct{})
	var commands atomic.Int32
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				server, channels, requests, err := ssh.NewServerConn(conn, config)
				if err != nil {
					return
				}
				defer server.Close()
				go ssh.DiscardRequests(requests)
				for incoming := range channels {
					_, requests, err := incoming.Accept()
					if err != nil {
						continue
					}
					go func() {
						for req := range requests {
							if req.Type == "exec" {
								if commands.Add(1) == 1 {
									close(started)
								}
								return
							}
						}
					}()
				}
			}()
		}
	}()
	addr := listener.Addr().(*net.TCPAddr)
	cfg := Config{Host: "test@127.0.0.1", Port: fmt.Sprint(addr.Port), AuthMode: AuthModePassword, Password: "pw", KnownHostsPath: filepath.Join(t.TempDir(), "known_hosts")}
	p := NewPool()
	p.openGrace = 25 * time.Millisecond
	defer p.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	defer cancel()
	go func() { _, err := p.Run(ctx, "node", 0, cfg, "side-effect", nil); done <- err }()
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("session ended before exec: %v", err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, ErrResultUnknown) {
			t.Fatalf("exec cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("unanswered exec did not unblock after cancellation")
	}
	if got := commands.Load(); got != 1 {
		t.Fatalf("executed %d times", got)
	}
}

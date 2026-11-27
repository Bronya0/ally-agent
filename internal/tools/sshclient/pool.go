// SPDX-License-Identifier: GPL-3.0-only

package sshclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"
)

// Pool holds authenticated transports; every operation gets a separate session.
type Pool struct {
	mu        sync.Mutex
	entries   map[string]*poolEntry
	all       map[*poolEntry]bool
	versions  map[string]uint64
	changed   chan struct{}
	closed    bool
	openGrace time.Duration
}
type poolEntry struct {
	key        string
	client     *ssh.Client
	dialing    bool
	active     int
	idle       *time.Timer
	idleSince  time.Time
	dialCancel context.CancelFunc
}

var ErrResultUnknown = errors.New("SSH operation may have executed; inspect the remote state before retrying")

const poolLimit = 16
const sessionsPerConnection = 4
const poolIdle = 5 * time.Minute
const canceledOpenGrace = 5 * time.Second
const controlledCancelGrace = 2 * time.Second

func NewPool() *Pool {
	return &Pool{entries: make(map[string]*poolEntry), all: make(map[*poolEntry]bool), versions: make(map[string]uint64), changed: make(chan struct{}), openGrace: canceledOpenGrace}
}
func (p *Pool) signal()                   { close(p.changed); p.changed = make(chan struct{}) }
func (p *Pool) Version(key string) uint64 { p.mu.Lock(); defer p.mu.Unlock(); return p.versions[key] }

// Invalidate removes a transport from admission; existing sessions finish normally.
func (p *Pool) Invalidate(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.versions[key]++
	if e := p.entries[key]; e != nil {
		delete(p.entries, key)
		if e.idle != nil {
			e.idle.Stop()
		}
		if e.dialCancel != nil {
			e.dialCancel()
		}
		if e.active == 0 && e.client != nil {
			delete(p.all, e)
			_ = e.client.Close()
		}
	}
	p.signal()
}
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.closed = true
	for e := range p.all {
		if e.dialCancel != nil {
			e.dialCancel()
		}
		if e.idle != nil {
			e.idle.Stop()
		}
		if e.client != nil {
			_ = e.client.Close()
		}
	}
	p.entries = make(map[string]*poolEntry)
	p.signal()
}
func connect(ctx context.Context, cfg Config) (*ssh.Client, error) {
	ep, err := resolveEndpoint(cfg)
	if err != nil {
		return nil, err
	}
	methods, closeAgent, err := authMethods(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer closeAgent()
	known := cfg.KnownHostsPath
	if known == "" {
		known, err = DefaultKnownHostsPath()
		if err != nil {
			return nil, err
		}
	}
	callback, err := newHostKeyCallback(ep.host, ep.port, known)
	if err != nil {
		return nil, err
	}
	return dial(ctx, ep, methods, callback)
}
func (p *Pool) acquire(ctx context.Context, key string, version uint64, cfg Config) (*poolEntry, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return nil, errors.New("SSH connection pool is closed")
		}
		if p.versions[key] != version {
			p.mu.Unlock()
			return nil, errors.New("SSH node changed; retry the operation")
		}
		e := p.entries[key]
		if e != nil && !e.dialing && e.active < sessionsPerConnection {
			e.active++
			p.signal()
			if e.idle != nil {
				e.idle.Stop()
				e.idle = nil
			}
			p.mu.Unlock()
			return e, nil
		}
		if e == nil && len(p.all) >= poolLimit {
			var oldest *poolEntry
			for _, v := range p.entries {
				if !v.dialing && v.active == 0 && (oldest == nil || v.idleSince.Before(oldest.idleSince)) {
					oldest = v
				}
			}
			if oldest != nil {
				delete(p.entries, oldest.key)
				delete(p.all, oldest)
				if oldest.idle != nil {
					oldest.idle.Stop()
				}
				_ = oldest.client.Close()
			}
		}
		if e == nil && len(p.all) < poolLimit {
			e = &poolEntry{key: key, dialing: true, active: 1}
			p.entries[key] = e
			p.all[e] = true
			dialCtx, cancel := context.WithCancel(ctx)
			e.dialCancel = cancel
			p.mu.Unlock()
			client, err := connect(dialCtx, cfg)
			cancel()
			p.mu.Lock()
			e.dialCancel = nil
			valid := !p.closed && p.versions[key] == version && p.entries[key] == e
			if err != nil || !valid {
				delete(p.all, e)
				if p.entries[key] == e {
					delete(p.entries, key)
				}
				if client != nil {
					_ = client.Close()
				}
				p.signal()
				p.mu.Unlock()
				if err != nil {
					return nil, err
				}
				return nil, errors.New("SSH node changed during connection")
			}
			e.client = client
			e.dialing = false
			p.signal()
			p.mu.Unlock()
			go func() {
				_ = client.Wait()
				p.mu.Lock()
				if p.entries[key] == e {
					delete(p.entries, key)
					if e.active == 0 {
						delete(p.all, e)
					}
					p.signal()
				}
				p.mu.Unlock()
			}()
			return e, nil
		}
		ch := p.changed
		p.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ch:
		}
	}
}
func (p *Pool) release(e *poolEntry) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e.active--
	if e.active == 0 {
		if p.entries[e.key] != e || p.closed {
			delete(p.all, e)
			_ = e.client.Close()
		} else {
			e.idleSince = time.Now()
			var timer *time.Timer
			timer = time.AfterFunc(poolIdle, func() {
				p.mu.Lock()
				defer p.mu.Unlock()
				if p.entries[e.key] == e && e.active == 0 && e.idle == timer {
					delete(p.entries, e.key)
					delete(p.all, e)
					_ = e.client.Close()
					p.signal()
				}
			})
			e.idle = timer
		}
	}
	p.signal()
}

// retireCanceledOpen gives other sessions a chance to finish before closing a
// transport whose canceled channel operation is unresponsive.
func (p *Pool) retireCanceledOpen(e *poolEntry, done <-chan struct{}, grace time.Duration) {
	p.mu.Lock()
	if p.entries[e.key] == e {
		delete(p.entries, e.key)
		p.signal()
	}
	p.mu.Unlock()
	go func() {
		timer := time.NewTimer(grace)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
			_ = e.client.Close()
		}
	}()
}

// run opens a fresh session, with cancellation scoped to that session only.
func (p *Pool) run(ctx context.Context, key string, version uint64, cfg Config, command string, stdin io.Reader, stdout, stderr io.Writer) error {
	return p.runOnce(ctx, key, version, cfg, command, stdin, stdout, stderr, true, false)
}
func (p *Pool) runOnce(ctx context.Context, key string, version uint64, cfg Config, command string, stdin io.Reader, stdout, stderr io.Writer, retry, controlled bool) error {
	if ctx == nil {
		return errors.New("SSH context is required")
	}
	if strings.TrimSpace(command) == "" {
		return errors.New("SSH command is required")
	}
	e, err := p.acquire(ctx, key, version, cfg)
	if err != nil {
		return err
	}
	type opened struct {
		s   *ssh.Session
		err error
	}
	ch := make(chan opened)
	openDone := make(chan struct{})
	go func() {
		defer close(openDone)
		s, err := e.client.NewSession()
		select {
		case ch <- opened{s, err}:
		case <-ctx.Done():
			if s != nil {
				_ = s.Close()
			}
			p.release(e)
		}
	}()
	var op opened
	select {
	case op = <-ch:
	case <-ctx.Done():
		// A channel-open request cannot be canceled independently by SSH. Stop
		// admitting work to this transport and close it if the server never
		// responds, so canceled opens cannot exhaust the pool indefinitely.
		p.retireCanceledOpen(e, openDone, p.openGrace)
		return ctx.Err()
	}
	if op.err != nil {
		var rejected *ssh.OpenChannelError
		if retry && ctx.Err() == nil && !errors.As(op.err, &rejected) {
			p.mu.Lock()
			if p.entries[key] == e {
				delete(p.entries, key)
				p.signal()
			}
			p.mu.Unlock()
			p.release(e)
			return p.runOnce(ctx, key, version, cfg, command, stdin, stdout, stderr, false, controlled)
		}
		p.release(e)
		return fmt.Errorf("open SSH session: %w", op.err)
	}
	defer p.release(e)
	defer op.s.Close()
	if err := ctx.Err(); err != nil {
		return err
	}
	var control io.WriteCloser
	var controlMu sync.Mutex
	var controlCloseOnce sync.Once
	closeControl := func() {
		controlMu.Lock()
		defer controlMu.Unlock()
		controlCloseOnce.Do(func() { _ = control.Close() })
	}
	if controlled {
		control, err = op.s.StdinPipe()
		if err != nil {
			return fmt.Errorf("open SSH control stdin: %w", err)
		}
		defer closeControl()
	} else {
		op.s.Stdin = stdin
	}
	operationDone := make(chan struct{})
	defer close(operationDone)
	stop := context.AfterFunc(ctx, func() {
		if controlled {
			// EOF tells the helper to stop its command group. Keep the session
			// open for its cleanup and retain the shared transport on success.
			// Arm the fallback before Close: a broken transport can block writes.
			timer := time.AfterFunc(controlledCancelGrace, func() {
				select {
				case <-operationDone:
					return
				default:
				}
				p.retireCanceledOpen(e, operationDone, p.openGrace)
				_ = op.s.Close()
			})
			closeControl()
			<-operationDone
			timer.Stop()
			return
		}
		p.retireCanceledOpen(e, operationDone, p.openGrace)
		_ = op.s.Close()
	})
	defer stop()
	op.s.Stdout = stdout
	op.s.Stderr = stderr
	// Start can fail after the request was sent, so never replay it.
	if err = op.s.Start(command); err != nil {
		return errors.Join(ErrResultUnknown, err)
	}
	var inputDone chan error
	if controlled {
		inputDone = make(chan error, 1)
		go func() {
			// SSH CloseWrite must not race a script write. Cancellation waits
			// for the finite script, with the same transport fallback above.
			controlMu.Lock()
			copyErr := ctx.Err()
			if copyErr == nil {
				_, copyErr = io.Copy(control, stdin)
			}
			controlMu.Unlock()
			if copyErr != nil {
				_ = op.s.Close()
			}
			// Deliberately keep stdin open after the finite script is sent.
			inputDone <- copyErr
		}()
	}
	err = op.s.Wait()
	if inputDone != nil {
		// Closing the channel also unblocks a script write if the helper exited
		// early. Join the copier before releasing the pooled connection.
		_ = op.s.Close()
		if copyErr := <-inputDone; copyErr != nil {
			err = errors.Join(err, copyErr)
		}
	}
	if ctx.Err() != nil {
		return errors.Join(ErrResultUnknown, ctx.Err())
	}
	var exit *ssh.ExitError
	if err != nil && !errors.As(err, &exit) {
		return errors.Join(ErrResultUnknown, err)
	}
	return err
}
func (p *Pool) Run(ctx context.Context, key string, version uint64, cfg Config, command string, stdin io.Reader) (Result, error) {
	return p.runCaptured(ctx, key, version, cfg, command, stdin, false)
}

// RunControlled sends a finite script, keeping stdin open until cancellation.
// The remote command must read its script by length and treat EOF as a stop
// request. Cancellation allows bounded cleanup before closing the session.
func (p *Pool) RunControlled(ctx context.Context, key string, version uint64, cfg Config, command, script string) (Result, error) {
	if script == "" {
		return Result{}, errors.New("SSH control script is required")
	}
	return p.runCaptured(ctx, key, version, cfg, command, strings.NewReader(script), true)
}

func (p *Pool) runCaptured(ctx context.Context, key string, version uint64, cfg Config, command string, stdin io.Reader, controlled bool) (Result, error) {
	if ctx == nil {
		return Result{}, errors.New("SSH context is required")
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	out := &limitedWriter{limit: maxStdoutBytes, onTruncate: cancel}
	errout := &limitedWriter{limit: maxStderrBytes, onTruncate: cancel}
	err := p.runOnce(runCtx, key, version, cfg, command, stdin, out, errout, true, controlled)
	if out.Truncated() || errout.Truncated() {
		return Result{Stdout: out.Bytes(), Stderr: errout.Bytes()}, ErrOutputTooLarge
	}
	return Result{Stdout: out.Bytes(), Stderr: errout.Bytes()}, err
}

// ErrStreamIdle means no file bytes moved for the stream's idle budget.
var ErrStreamIdle = errors.New("SSH stream made no progress")

// streamIdleTimeout bounds a transfer's silence, not its duration. It only
// applies to RunStream, whose traffic is file bytes, so a command that is
// legitimately quiet for longer belongs on Run instead. Connection setup never
// eats into it: dial and handshake carry their own shorter budgets.
const streamIdleTimeout = 2 * time.Minute

type streamActivity struct {
	started time.Time
	last    atomic.Int64 // monotonic time since started
}

func (a *streamActivity) progress() {
	a.last.Store(int64(time.Since(a.started)))
}

func (a *streamActivity) watch(ctx context.Context, cancel context.CancelCauseFunc, idle time.Duration) {
	timer := time.NewTimer(idle)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			last := time.Duration(a.last.Load())
			remaining := idle - (time.Since(a.started) - last)
			if remaining <= 0 {
				cancel(fmt.Errorf("%w for %s", ErrStreamIdle, idle))
				return
			}
			timer.Reset(remaining)
		}
	}
}

type streamReader struct {
	io.Reader
	activity *streamActivity
}

func (r streamReader) Read(b []byte) (int, error) {
	n, err := r.Reader.Read(b)
	if n > 0 {
		r.activity.progress()
	}
	return n, err
}

type streamWriter struct {
	io.Writer
	activity *streamActivity
	cancel   context.CancelCauseFunc
}

func (w streamWriter) Write(b []byte) (int, error) {
	n, err := w.Writer.Write(b)
	if n > 0 {
		w.activity.progress()
	}
	if err == nil && n != len(b) {
		err = io.ErrShortWrite
	}
	if err != nil {
		// Wait alone may block forever: the peer is waiting to send bytes
		// that the failed local writer will no longer consume.
		w.cancel(fmt.Errorf("write SSH stream output: %w", err))
	}
	return n, err
}

// RunStream allows long transfers as long as bytes continue to move.
func (p *Pool) RunStream(ctx context.Context, key string, version uint64, cfg Config, command string, stdin io.Reader, stdout io.Writer) ([]byte, error) {
	if ctx == nil {
		return nil, errors.New("SSH context is required")
	}
	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	activity := &streamActivity{started: time.Now()}
	go activity.watch(runCtx, cancel, streamIdleTimeout)
	if stdin != nil {
		stdin = streamReader{Reader: stdin, activity: activity}
	}
	if stdout == nil {
		stdout = io.Discard
	} else {
		stdout = streamWriter{Writer: stdout, activity: activity, cancel: cancel}
	}
	stderr := &limitedWriter{limit: maxStderrBytes, onTruncate: func() { cancel(ErrOutputTooLarge) }}
	err := p.run(runCtx, key, version, cfg, command, stdin, stdout, stderr)
	if stderr.Truncated() {
		return stderr.Bytes(), ErrOutputTooLarge
	}
	if cause := context.Cause(runCtx); err != nil && cause != nil && ctx.Err() == nil {
		if errors.Is(err, ErrResultUnknown) {
			cause = errors.Join(cause, ErrResultUnknown)
		}
		err = cause
	}
	return stderr.Bytes(), err
}

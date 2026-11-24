// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Use the production loader and keep its stdin open after the finite script.
func startControlledRemoteHelper(t *testing.T, payload map[string]any, home string) (io.WriteCloser, <-chan error, *bytes.Buffer) {
	t.Helper()
	script, err := buildRemoteScript(payload)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(pickRemoteHelperPython(t), "-u", "-c", remoteRunLoader(script))
	cmd.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stdin.Close(); _ = cmd.Process.Kill() })
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	if _, err := io.WriteString(stdin, script); err != nil {
		t.Fatal(err)
	}
	return stdin, done, &output
}

func controlledRemoteResult(t *testing.T, done <-chan error, output *bytes.Buffer) map[string]any {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("helper process: %v; output: %s", err, output)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("controlled helper did not finish")
	}
	_, raw, found := strings.Cut(output.String(), remotePythonMarker)
	var response remotePythonResponse
	if !found || json.Unmarshal([]byte(strings.TrimSpace(raw)), &response) != nil {
		t.Fatalf("invalid helper response: %s", output)
	}
	if !response.OK {
		t.Fatalf("helper rejected controlled execution: %s", response.Error)
	}
	var data map[string]any
	if err := json.Unmarshal(response.Data, &data); err != nil {
		t.Fatal(err)
	}
	return data
}

func TestRemoteRunControlledOpenStdin(t *testing.T) {
	home := t.TempDir()
	_, done, output := startControlledRemoteHelper(t, map[string]any{
		"op": "run", "command": "echo ally-controlled", "timeoutSeconds": 2,
	}, home)
	data := controlledRemoteResult(t, done, output)
	if data["exitCode"] != float64(0) || !strings.Contains(data["output"].(string), "ally-controlled") {
		t.Fatalf("command result: %+v", data)
	}
}

func TestRemoteRunControlledEOFStopsCommandTree(t *testing.T) {
	home := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	// A grandchild holds this socket while blocked, without producing output.
	// Socket EOF proves it was stopped along with the command shell.
	child := fmt.Sprintf("import socket; s=socket.socket(); s.connect(('127.0.0.1', %d)); s.recv(1)", port)
	runner := fmt.Sprintf("import subprocess, sys\np = subprocess.Popen([sys.executable, '-c', %q])\np.wait()\n", child)
	runnerPath := filepath.Join(home, "runner.py")
	if err := os.WriteFile(runnerPath, []byte(runner), 0o600); err != nil {
		t.Fatal(err)
	}
	command := fmt.Sprintf(`"%s" "%s"`, pickRemoteHelperPython(t), runnerPath)
	stdin, done, output := startControlledRemoteHelper(t, map[string]any{
		"op": "run", "command": command, "timeoutSeconds": 5,
	}, home)
	if err := listener.(*net.TCPListener).SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	conn, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	data := controlledRemoteResult(t, done, output)
	if data["exitCode"] != float64(-1) || data["timedOut"] != false {
		t.Fatalf("EOF did not cancel the command: %+v", data)
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	// Windows reports a killed peer as WSAECONNRESET (10054).
	if n, err := conn.Read(make([]byte, 1)); n != 0 || (!errors.Is(err, io.EOF) && !errors.Is(err, syscall.ECONNRESET) && !errors.Is(err, syscall.Errno(10054))) {
		t.Fatalf("command grandchild survived cancellation: n=%d err=%v", n, err)
	}
}

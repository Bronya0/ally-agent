// SPDX-License-Identifier: GPL-3.0-only
package screenshot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if handled, err := RunCaptureHelper(os.Args[1:]); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestCaptureCommandProcess(t *testing.T) {
	mode := os.Getenv("ALLY_SCREENSHOT_TEST_PROCESS")
	if mode == "" {
		t.Skip("subprocess fixture")
	}
	switch mode {
	case "hang":
		if err := os.WriteFile(os.Getenv("ALLY_SCREENSHOT_TEST_READY"), []byte("ready"), 0600); err != nil {
			os.Exit(2)
		}
		time.Sleep(time.Hour)
	case "success":
		img, err := EncodeForModel(image.NewRGBA(image.Rect(0, 0, 16, 8)))
		if err != nil {
			os.Exit(2)
		}
		_ = json.NewEncoder(os.Stdout).Encode(captureHelperResult{
			Mime: img.Mime, Data: img.Data, Scaled: true,
			Source: CaptureSource{Kind: TargetWindow, Window: &Window{ID: 7}, OriginalWidth: 32, OriginalHeight: 16},
		})
	case "failure":
		fmt.Fprintln(os.Stderr, "capture fixture failed")
		os.Exit(2)
	}
	os.Exit(0)
}

func captureTestCommand(t *testing.T, ctx context.Context, mode string) (*exec.Cmd, string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestCaptureCommandProcess$")
	cmd.Env = append(os.Environ(), "HOME="+dir, "USERPROFILE="+dir,
		"ALLY_SCREENSHOT_TEST_PROCESS="+mode, "ALLY_SCREENSHOT_TEST_READY="+ready)
	cmd.WaitDelay = time.Second
	return cmd, ready
}

func TestCaptureCommandCancellationReapsBlockedHelper(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd, ready := captureTestCommand(t, ctx, "hang")
	done := make(chan error, 1)
	go func() {
		_, _, err := runCaptureCommand(ctx, cmd)
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("helper did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			t.Fatalf("cancel must kill and reap the helper: state=%v error=%v", cmd.ProcessState, err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation left the capture blocked")
	}
}

func TestCaptureCommandDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cmd, _ := captureTestCommand(t, ctx, "hang")
	_, _, err := runCaptureCommand(ctx, cmd)
	if !errors.Is(err, context.DeadlineExceeded) || cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
		t.Fatalf("deadline must kill and reap the helper: state=%v error=%v", cmd.ProcessState, err)
	}
}

func TestCaptureCommandResultAndFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd, _ := captureTestCommand(t, ctx, "success")
	img, source, err := runCaptureCommand(ctx, cmd)
	if err != nil || img.Image.Bounds().Dx() != 16 || !img.Scaled || source.Window.ID != 7 || source.OriginalWidth != 32 {
		t.Fatalf("helper lost capture metadata: image=%+v source=%+v error=%v", img, source, err)
	}
	cmd, _ = captureTestCommand(t, ctx, "failure")
	_, _, err = runCaptureCommand(ctx, cmd)
	if !errors.Is(err, ErrCaptureFailed) || !strings.Contains(err.Error(), "capture fixture failed") {
		t.Fatalf("helper failure lost its diagnostic: %v", err)
	}
}

// A Windows HWND is a signed pointer: hosts such as WebView2 report handles
// with the high bit set, which arrive here as negative ints. The helper
// response must accept them, otherwise a window the list just returned can
// never be captured.
func TestCaptureHelperAcceptsSignedWindowHandle(t *testing.T) {
	img, err := EncodeForModel(image.NewRGBA(image.Rect(0, 0, 4, 3)))
	if err != nil {
		t.Fatal(err)
	}
	const signedID = -989130788
	data, err := json.Marshal(captureHelperResult{
		Mime: img.Mime, Data: img.Data,
		Source: CaptureSource{Kind: TargetWindow, Window: &Window{ID: signedID}, OriginalWidth: 4, OriginalHeight: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, source, err := decodeCaptureHelperResult(data)
	if err != nil || source.Window == nil || source.Window.ID != signedID {
		t.Fatalf("signed window handle rejected: source=%+v err=%v", source, err)
	}
	if _, _, err := decodeCaptureHelperResult(mustJSON(t, captureHelperResult{
		Mime: img.Mime, Data: img.Data,
		Source: CaptureSource{Kind: TargetWindow, Window: &Window{ID: 0}, OriginalWidth: 4, OriginalHeight: 3},
	})); !errors.Is(err, ErrCaptureFailed) {
		t.Fatalf("zero id must stay invalid, got %v", err)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestCaptureHelperPipeAndImageLimits(t *testing.T) {
	output := &captureOutputBuffer{limit: 17}
	_, _ = io.Copy(output, strings.NewReader(strings.Repeat("x", 65536)))
	if output.buffer.Len() != 17 || !output.overflow {
		t.Fatal("helper pipe must remain bounded even when io.Copy is used")
	}
	for _, result := range []captureHelperResult{
		{},
		{Data: make([]byte, MaxModelImageBytes+1)},
		{Mime: "image/png", Data: []byte("not an image")},
	} {
		data, _ := json.Marshal(result)
		if _, _, err := decodeCaptureHelperResult(data); !errors.Is(err, ErrCaptureFailed) {
			t.Fatalf("invalid helper response accepted: %v", err)
		}
	}
	data, _ := json.Marshal(captureHelperResult{Error: "gone", Kind: "not-found"})
	if _, _, err := decodeCaptureHelperResult(data); !errors.Is(err, ErrWindowNotFound) {
		t.Fatalf("window-not-found identity lost across helper: %v", err)
	}
}

func TestCaptureContextCancelledBeforeDispatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := CaptureContext(ctx, CaptureOptions{WindowID: 1}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled request must not start a capture: %v", err)
	}
	if _, _, err := ListWindowsContext(ctx, ListOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled request must not enumerate windows: %v", err)
	}
}

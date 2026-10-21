// SPDX-License-Identifier: GPL-3.0-only
package screenshot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"os"
	"os/exec"
	"time"
)

const captureHelperFlag = "--ally-screenshot-helper"

type captureHelperResult struct {
	Source CaptureSource
	Mime   string
	Data   []byte
	Scaled bool
	Error  string
	Kind   string
}

// RunCaptureHelper is called before desktop/server startup. The helper owns
// its GDI resources and exits after one capture; killing it releases them even
// if another application's PrintWindow handler never returns.
func RunCaptureHelper(args []string) (bool, error) {
	if len(args) == 0 || args[0] != captureHelperFlag {
		return false, nil
	}
	if len(args) != 2 {
		return true, errors.New("screenshot helper requires one capture request")
	}
	var opts CaptureOptions
	if err := json.Unmarshal([]byte(args[1]), &opts); err != nil {
		return true, err
	}
	kind, _, _, _, err := opts.Target()
	if err != nil || kind != TargetWindow {
		return true, errors.New("screenshot helper requires a window target")
	}
	// A hard watchdog also bounds an orphaned helper if its parent exits
	// before CommandContext can kill an unresponsive PrintWindow call.
	watchdog := time.AfterFunc(captureTimeout+time.Second, func() { os.Exit(1) })
	defer watchdog.Stop()
	img, source, err := captureDirect(context.Background(), opts)
	result := captureHelperResult{Source: source, Mime: img.Mime, Data: img.Data, Scaled: img.Scaled}
	if err != nil {
		result.Error = err.Error()
		switch {
		case errors.Is(err, ErrWindowNotFound):
			result.Kind = "not-found"
		case errors.Is(err, ErrAmbiguousWindow):
			result.Kind = "ambiguous"
		case errors.Is(err, ErrBadRegion):
			result.Kind = "bad-region"
		case errors.Is(err, ErrCaptureUnsupported):
			result.Kind = "unsupported"
		}
	}
	return true, json.NewEncoder(os.Stdout).Encode(result)
}

type captureOutputBuffer struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
}

func (b *captureOutputBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if room := b.limit - b.buffer.Len(); n > room {
		p = p[:max(0, room)]
		b.overflow = true
	}
	_, _ = b.buffer.Write(p)
	return n, nil
}

func runCaptureCommand(ctx context.Context, cmd *exec.Cmd) (ModelImage, CaptureSource, error) {
	// Data is base64 in JSON. Bound both pipes even if the executable emits
	// unrelated output or a future helper accidentally returns an oversized image.
	output := &captureOutputBuffer{limit: MaxModelImageBytes*4/3 + 64*1024}
	stderr := &captureOutputBuffer{limit: 4096}
	cmd.Stdout, cmd.Stderr = output, stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ModelImage{}, CaptureSource{}, ctx.Err()
		}
		return ModelImage{}, CaptureSource{}, fmt.Errorf("%w: screenshot helper: %v: %s", ErrCaptureFailed, err, stderr.buffer.String())
	}
	if err := ctx.Err(); err != nil {
		return ModelImage{}, CaptureSource{}, err
	}
	if output.overflow {
		return ModelImage{}, CaptureSource{}, fmt.Errorf("%w: screenshot helper response exceeds limit", ErrCaptureFailed)
	}
	return decodeCaptureHelperResult(output.buffer.Bytes())
}

func decodeCaptureHelperResult(data []byte) (ModelImage, CaptureSource, error) {
	var result captureHelperResult
	if err := json.Unmarshal(data, &result); err != nil {
		return ModelImage{}, CaptureSource{}, fmt.Errorf("%w: invalid screenshot helper response: %v", ErrCaptureFailed, err)
	}
	if result.Error != "" {
		kind := ErrCaptureFailed
		switch result.Kind {
		case "not-found":
			kind = ErrWindowNotFound
		case "ambiguous":
			kind = ErrAmbiguousWindow
		case "bad-region":
			kind = ErrBadRegion
		case "unsupported":
			kind = ErrCaptureUnsupported
		}
		return ModelImage{}, result.Source, fmt.Errorf("%w: %s", kind, result.Error)
	}
	if len(result.Data) == 0 || len(result.Data) > MaxModelImageBytes {
		return ModelImage{}, result.Source, fmt.Errorf("%w: invalid screenshot helper image size", ErrCaptureFailed)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(result.Data))
	if err != nil || (format != "png" && format != "jpeg") || config.Width <= 0 || config.Height <= 0 || max(config.Width, config.Height) > MaxModelImageEdgePx || result.Mime != "image/"+format {
		return ModelImage{}, result.Source, fmt.Errorf("%w: invalid screenshot helper image", ErrCaptureFailed)
	}
	if result.Source.Kind != TargetWindow || result.Source.Window == nil || result.Source.Window.ID <= 0 || result.Source.OriginalWidth <= 0 || result.Source.OriginalHeight <= 0 {
		return ModelImage{}, result.Source, fmt.Errorf("%w: invalid screenshot helper source", ErrCaptureFailed)
	}
	frame, _, err := image.Decode(bytes.NewReader(result.Data))
	if err != nil {
		return ModelImage{}, result.Source, fmt.Errorf("%w: decode screenshot helper image: %v", ErrCaptureFailed, err)
	}
	return ModelImage{Image: frame, Mime: result.Mime, Data: result.Data, Scaled: result.Scaled}, result.Source, nil
}

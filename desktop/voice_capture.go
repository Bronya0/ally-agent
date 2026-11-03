// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"encoding/binary"
	"errors"
	"math"
	"time"
)

var errVoiceCaptureDeviceUnavailable = errors.New("所选录音设备不可用")

type voiceCaptureDevice struct {
	ID   string
	Name string
}

// nativeAudioRecording is the platform-neutral contract used by the recorder
// dialog and the hold-to-talk hotkey. Platform backends only open a device,
// ask the OS audio engine for the target format when it can convert on our
// behalf, and publish PCM plus a normalized level; buffering, resampling, WAV
// framing and lifecycle stay shared so every platform behaves the same.
//
// Stop returns a 16 kHz mono 16-bit WAV, which is the wire format the
// transcription endpoint expects.
type nativeAudioRecording interface {
	Start() error
	Stop() ([]byte, error)
	Cancel()
	Levels() <-chan float32
}

// voiceCaptureSampleRate is the single capture target. Asking the OS engine to
// deliver it avoids carrying 48 kHz stereo float32 (384 kB/s) through memory
// and skips the resampling pass at the end of every recording.
const voiceCaptureSampleRate = 16000

// capturePCMFormat describes the PCM layout a backend delivers.
type capturePCMFormat struct {
	SampleRate     uint32
	Channels       int
	BytesPerSample int
	BlockAlign     int
	Float          bool // 32-bit IEEE float container
}

func (f capturePCMFormat) valid() bool {
	return f.SampleRate >= 8000 && f.SampleRate <= 384000 &&
		f.Channels >= 1 && f.Channels <= 8 &&
		f.BytesPerSample >= 1 && f.BytesPerSample <= 4 &&
		f.BlockAlign >= f.Channels*f.BytesPerSample
}

func (f capturePCMFormat) bytesPerSecond() int { return int(f.SampleRate) * f.BlockAlign }

// isTarget reports whether the backend already delivers the wire format, which
// turns the WAV pass into a plain copy instead of a mix-and-resample.
func (f capturePCMFormat) isTarget() bool {
	return f.SampleRate == voiceCaptureSampleRate && f.Channels == 1 && f.BytesPerSample == 2 && !f.Float
}

// pcmBudgetBytes is the exact buffer a full capture window needs, used to size
// the collector once instead of regrowing a bytes.Buffer on every chunk.
func (f capturePCMFormat) pcmBudgetBytes(window time.Duration) int {
	if !f.valid() || window <= 0 {
		return 0
	}
	return f.bytesPerSecond() * int(window/time.Second)
}

// pcmCollector accumulates captured PCM into a preallocated, fixed budget. A
// recording can never exceed voiceCaptureMaxDuration, so dropping the tail is
// the correct overflow policy and no reallocation ever happens.
type pcmCollector struct {
	buf []byte
	max int
}

func newPCMCollector(maxBytes int) *pcmCollector {
	if maxBytes < 0 {
		maxBytes = 0
	}
	return &pcmCollector{buf: make([]byte, 0, maxBytes), max: maxBytes}
}

// Append keeps as much of src as fits and returns the number of bytes kept.
func (c *pcmCollector) Append(src []byte) int {
	remaining := c.max - len(c.buf)
	if remaining <= 0 || len(src) == 0 {
		return 0
	}
	if len(src) > remaining {
		src = src[:remaining]
	}
	c.buf = append(c.buf, src...)
	return len(src)
}

func (c *pcmCollector) Bytes() []byte { return c.buf }

func (c *pcmCollector) Len() int { return len(c.buf) }

// pcmSample decodes one sample starting at p into [-1, 1].
func pcmSample(p []byte, format capturePCMFormat) float64 {
	if format.Float && format.BytesPerSample == 4 {
		return float64(math.Float32frombits(binary.LittleEndian.Uint32(p)))
	}
	switch format.BytesPerSample {
	case 1:
		return float64(int(p[0])-128) / 128
	case 2:
		return float64(int16(binary.LittleEndian.Uint16(p))) / 32768
	case 3:
		x := int32(p[0]) | int32(p[1])<<8 | int32(p[2])<<16
		if x&0x800000 != 0 {
			x |= ^0xffffff
		}
		return float64(x) / 8388608
	case 4:
		return float64(int32(binary.LittleEndian.Uint32(p))) / 2147483648
	}
	return 0
}

// pcmRMS reports a normalized level over a bounded sample of the buffer, so the
// per-chunk cost stays flat regardless of how much audio one poll returned.
func pcmRMS(raw []byte, format capturePCMFormat) float32 {
	if !format.valid() {
		return 0
	}
	frames := len(raw) / format.BlockAlign
	stride := max(1, frames/256)
	sum, count := 0.0, 0
	for frame := 0; frame < frames; frame += stride {
		base := frame * format.BlockAlign
		for channel := 0; channel < format.Channels; channel++ {
			off := base + channel*format.BytesPerSample
			if off+format.BytesPerSample > len(raw) {
				continue
			}
			sample := pcmSample(raw[off:off+format.BytesPerSample], format)
			sum += sample * sample
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return float32(math.Min(1, math.Sqrt(sum/float64(count))*3))
}

// encodeVoiceWAV wraps captured PCM as a 16 kHz mono 16-bit WAV. Backends that
// already deliver the target format take the copy path; anything else is mixed
// down and resampled by this shared implementation, so a backend that cannot
// negotiate the format (or a platform added later) still produces identical
// audio for the transcription endpoint.
func encodeVoiceWAV(raw []byte, format capturePCMFormat) ([]byte, error) {
	if !format.valid() {
		return nil, errUnsupportedCaptureFormat
	}
	frames := len(raw) / format.BlockAlign
	targetFrames := frames * voiceCaptureSampleRate / int(format.SampleRate)
	if targetFrames <= 0 {
		return nil, errUnsupportedCaptureFormat
	}
	out := make([]byte, 44+targetFrames*2)
	data := out[44:]
	if format.isTarget() {
		copy(data, raw[:targetFrames*2])
	} else {
		for outFrame := 0; outFrame < targetFrames; outFrame++ {
			sourceFrame := outFrame * int(format.SampleRate) / voiceCaptureSampleRate
			base := sourceFrame * format.BlockAlign
			sum := 0.0
			for channel := 0; channel < format.Channels; channel++ {
				off := base + channel*format.BytesPerSample
				if off+format.BytesPerSample > len(raw) {
					break
				}
				sum += pcmSample(raw[off:off+format.BytesPerSample], format)
			}
			value := sum / float64(format.Channels)
			value = math.Max(-1, math.Min(1, value))
			binary.LittleEndian.PutUint16(data[outFrame*2:], uint16(int16(value*32767)))
		}
	}
	writeVoiceWAVHeader(out, uint32(targetFrames*2))
	return out, nil
}

var errUnsupportedCaptureFormat = errors.New("麦克风音频格式不受支持")

func writeVoiceWAVHeader(out []byte, dataSize uint32) {
	copy(out[0:4], "RIFF")
	binary.LittleEndian.PutUint32(out[4:8], 36+dataSize)
	copy(out[8:12], "WAVE")
	copy(out[12:16], "fmt ")
	binary.LittleEndian.PutUint32(out[16:20], 16)
	binary.LittleEndian.PutUint16(out[20:22], 1)
	binary.LittleEndian.PutUint16(out[22:24], 1)
	binary.LittleEndian.PutUint32(out[24:28], voiceCaptureSampleRate)
	binary.LittleEndian.PutUint32(out[28:32], voiceCaptureSampleRate*2)
	binary.LittleEndian.PutUint16(out[32:34], 2)
	binary.LittleEndian.PutUint16(out[34:36], 16)
	copy(out[36:40], "data")
	binary.LittleEndian.PutUint32(out[40:44], dataSize)
}

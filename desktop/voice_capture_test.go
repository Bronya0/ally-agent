// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"encoding/binary"
	"testing"
	"time"
)

// These tests cover the shared capture pipeline every platform backend feeds,
// so they run on Windows, macOS and Linux alike.
func TestEncodeVoiceWAVResamplesAndMixesToMono(t *testing.T) {
	format := capturePCMFormat{SampleRate: 44100, Channels: 2, BytesPerSample: 2, BlockAlign: 4}
	raw := make([]byte, 441*4) // 10 ms stereo capture.
	for i := 0; i < 441; i++ {
		binary.LittleEndian.PutUint16(raw[i*4:i*4+2], uint16(int16(16384)))
		binary.LittleEndian.PutUint16(raw[i*4+2:i*4+4], uint16(int16(16384)))
	}
	wav, err := encodeVoiceWAV(raw, format)
	if err != nil {
		t.Fatal(err)
	}
	if string(wav[:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
		t.Fatal("encoded audio is not a RIFF/WAVE payload")
	}
	if rate := binary.LittleEndian.Uint32(wav[24:28]); rate != voiceCaptureSampleRate {
		t.Fatalf("sample rate = %d, want %d", rate, voiceCaptureSampleRate)
	}
	if size := binary.LittleEndian.Uint32(wav[40:44]); size != 320 {
		t.Fatalf("data chunk = %d bytes, want 320", size)
	}
	if level := int16(binary.LittleEndian.Uint16(wav[44:46])); level != 16383 {
		t.Fatalf("mixed sample = %d, want 16383", level)
	}
}

func TestEncodeVoiceWAVCopiesTargetFormat(t *testing.T) {
	format := capturePCMFormat{SampleRate: voiceCaptureSampleRate, Channels: 1, BytesPerSample: 2, BlockAlign: 2}
	raw := make([]byte, 320) // 10 ms mono capture.
	for i := range raw {
		raw[i] = byte(i)
	}
	wav, err := encodeVoiceWAV(raw, format)
	if err != nil {
		t.Fatal(err)
	}
	if len(wav) != 44+len(raw) {
		t.Fatalf("encoded length = %d, want %d", len(wav), 44+len(raw))
	}
	for i := range raw {
		if wav[44+i] != raw[i] {
			t.Fatalf("target format should be copied verbatim, byte %d differs", i)
		}
	}
}

func TestEncodeVoiceWAVRejectsInvalidInput(t *testing.T) {
	if _, err := encodeVoiceWAV([]byte{0, 0}, capturePCMFormat{SampleRate: 0, Channels: 1, BytesPerSample: 2, BlockAlign: 2}); err == nil {
		t.Fatal("invalid sample rate must fail")
	}
	target := capturePCMFormat{SampleRate: voiceCaptureSampleRate, Channels: 1, BytesPerSample: 2, BlockAlign: 2}
	if _, err := encodeVoiceWAV(nil, target); err == nil {
		t.Fatal("empty capture must fail instead of emitting an empty WAV")
	}
}

func TestVoiceClipHasSignal(t *testing.T) {
	format := capturePCMFormat{SampleRate: voiceCaptureSampleRate, Channels: 1, BytesPerSample: 2, BlockAlign: 2}
	quiet, err := encodeVoiceWAV(make([]byte, voiceMinClipPCMBytes), format)
	if err != nil {
		t.Fatal(err)
	}
	if voiceClipHasSignal(quiet) {
		t.Fatal("digital silence must not be sent as a transcription request")
	}
	pcm := make([]byte, voiceMinClipPCMBytes)
	for i := 0; i < len(pcm); i += 2 {
		binary.LittleEndian.PutUint16(pcm[i:], uint16(int16(256)))
	}
	speechLike, err := encodeVoiceWAV(pcm, format)
	if err != nil {
		t.Fatal(err)
	}
	if !voiceClipHasSignal(speechLike) {
		t.Fatal("low-level non-silent audio should pass the signal gate")
	}
	if voiceClipHasSignal([]byte("not a WAV")) {
		t.Fatal("invalid WAV must not pass the signal gate")
	}
}

func TestPCMCollectorDropsOverflowTail(t *testing.T) {
	collector := newPCMCollector(8)
	if kept := collector.Append([]byte{1, 2, 3, 4, 5, 6}); kept != 6 {
		t.Fatalf("first append kept %d bytes, want 6", kept)
	}
	if kept := collector.Append([]byte{7, 8, 9}); kept != 2 {
		t.Fatalf("overflowing append kept %d bytes, want 2", kept)
	}
	if collector.Len() != 8 || collector.max != 8 {
		t.Fatalf("collector holds %d/%d bytes, want 8/8", collector.Len(), collector.max)
	}
	if kept := collector.Append([]byte{9}); kept != 0 {
		t.Fatalf("full collector kept %d bytes, want 0", kept)
	}
}

func TestPCMBudgetMatchesCaptureWindow(t *testing.T) {
	target := capturePCMFormat{SampleRate: voiceCaptureSampleRate, Channels: 1, BytesPerSample: 2, BlockAlign: 2}
	if got := target.pcmBudgetBytes(voiceCaptureMaxDuration); got != 640000 {
		t.Fatalf("16 kHz mono budget = %d bytes, want 640000", got)
	}
	mix := capturePCMFormat{SampleRate: 48000, Channels: 2, BytesPerSample: 4, BlockAlign: 8}
	if got := mix.pcmBudgetBytes(voiceCaptureMaxDuration); got != 7680000 {
		t.Fatalf("48 kHz stereo float budget = %d bytes, want 7680000", got)
	}
	if got := (capturePCMFormat{}).pcmBudgetBytes(20 * time.Second); got != 0 {
		t.Fatalf("invalid format budget = %d, want 0", got)
	}
}

func TestPCMRMSReportsLevel(t *testing.T) {
	format := capturePCMFormat{SampleRate: voiceCaptureSampleRate, Channels: 1, BytesPerSample: 2, BlockAlign: 2}
	if level := pcmRMS(make([]byte, 320), format); level != 0 {
		t.Fatalf("silence level = %v, want 0", level)
	}
	raw := make([]byte, 320)
	for i := 0; i < 160; i++ {
		binary.LittleEndian.PutUint16(raw[i*2:], uint16(int16(8192)))
	}
	if level := pcmRMS(raw, format); level < 0.70 || level > 0.80 {
		t.Fatalf("quarter-scale level = %v, want about 0.75", level)
	}
}

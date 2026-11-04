//go:build !windows

// SPDX-License-Identifier: GPL-3.0-only
package desktop

import "errors"

func startNativeVoiceHotkey(string, bool, bool, func() bool, func(), func()) (func(), error) {
	return nil, errors.New("语音快捷键目前仅支持 Windows 桌面版")
}

func startNativeHotkeyCapture(func([]string, string), func(string)) (func(), error) {
	return nil, errors.New("语音快捷键目前仅支持 Windows 桌面版")
}

func voiceHotkeyHookLive() bool { return false }

// voiceInputSupported reports whether this build can capture audio and install a
// keyboard hook, so the settings page can stay out of the way instead of offering
// a switch that fails on use.
func voiceInputSupported() bool { return false }

type unavailableAudioRecording struct{}

func startNativeAudioRecording(...string) (nativeAudioRecording, error) {
	return nil, errors.New("实时麦克风测试目前仅支持 Windows 桌面版")
}
func listNativeAudioInputDevices() ([]voiceCaptureDevice, error) {
	return nil, errors.New("语音输入设备选择目前仅支持 Windows 桌面版")
}
func (*unavailableAudioRecording) Start() error {
	return errors.New("实时麦克风测试目前仅支持 Windows 桌面版")
}
func (*unavailableAudioRecording) Stop() ([]byte, error) {
	return nil, errors.New("实时麦克风测试目前仅支持 Windows 桌面版")
}
func (*unavailableAudioRecording) Cancel() {}
func (*unavailableAudioRecording) Levels() <-chan float32 {
	ch := make(chan float32)
	close(ch)
	return ch
}

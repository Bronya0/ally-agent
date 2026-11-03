//go:build !windows

// SPDX-License-Identifier: GPL-3.0-only
package desktop

import "errors"

func startNativeVoiceHotkey(string, bool, bool, func(), func()) (func(), error) {
	return nil, errors.New("语音快捷键目前仅支持 Windows 桌面版")
}

func startNativeHotkeyCapture(func([]string, string), func(string)) (func(), error) {
	return nil, errors.New("语音快捷键目前仅支持 Windows 桌面版")
}

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

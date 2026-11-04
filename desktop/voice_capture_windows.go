//go:build windows

// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
)

type captureResult struct {
	wav      []byte
	err      error
	reusable bool
}

type windowsAudioRecording struct {
	stop       chan struct{}
	begin      chan struct{}
	cancel     chan struct{}
	done       chan struct{}
	started    chan error
	result     chan captureResult
	levels     chan float32
	mu         sync.Mutex
	reusable   bool
	cancelOnce sync.Once
	deviceID   string
	// collector is created on the first take and reset afterwards, so a warm
	// session holds no audio buffer until it is needed.
	collector *pcmCollector
}

const audclntSBufferEmpty = uintptr(0x08890001)

func startNativeAudioRecording(deviceIDs ...string) (nativeAudioRecording, error) {
	deviceID := ""
	if len(deviceIDs) > 0 {
		deviceID = deviceIDs[0]
	}
	r := &windowsAudioRecording{stop: make(chan struct{}, 1), begin: make(chan struct{}, 1), cancel: make(chan struct{}), done: make(chan struct{}), started: make(chan error, 1), result: make(chan captureResult, 1), levels: make(chan float32, 1), deviceID: deviceID}
	ready := make(chan error, 1)
	go r.capture(ready)
	if err := <-ready; err != nil {
		return nil, err
	}
	return r, nil
}

func (r *windowsAudioRecording) Start() error {
	select {
	case <-r.done:
		return errors.New("录音设备会话已关闭")
	case r.begin <- struct{}{}:
	}
	select {
	case err := <-r.started:
		return err
	case <-r.done:
		select {
		case err := <-r.started:
			return err
		default:
			return errors.New("录音设备会话已关闭")
		}
	}
}

func (r *windowsAudioRecording) Stop() ([]byte, error) {
	if result, ok := r.takeResult(); ok {
		return result.wav, result.err
	}
	select {
	case <-r.done:
		if result, ok := r.takeResult(); ok {
			return result.wav, result.err
		}
		return nil, errors.New("录音设备会话已关闭")
	case r.stop <- struct{}{}:
	}
	select {
	case result := <-r.result:
		return result.wav, result.err
	case <-r.done:
		if result, ok := r.takeResult(); ok {
			return result.wav, result.err
		}
		return nil, errors.New("录音设备会话已关闭")
	}
}

func (r *windowsAudioRecording) takeResult() (captureResult, bool) {
	select {
	case result := <-r.result:
		r.setReusable(result.reusable)
		return result, true
	default:
		return captureResult{}, false
	}
}

func (r *windowsAudioRecording) Levels() <-chan float32 { return r.levels }
func (r *windowsAudioRecording) Done() <-chan struct{}  { return r.done }
func (r *windowsAudioRecording) CanReuse() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reusable
}

func (r *windowsAudioRecording) Cancel() {
	r.cancelOnce.Do(func() { close(r.cancel) })
	<-r.done
}

func (r *windowsAudioRecording) capture(ready chan<- error) {
	defer close(r.done)
	defer close(r.levels)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		ready <- err
		return
	}
	defer ole.CoUninitialize()
	var enumerator *wca.IMMDeviceEnumerator
	if err := wca.CoCreateInstance(wca.CLSID_MMDeviceEnumerator, 0, wca.CLSCTX_ALL, wca.IID_IMMDeviceEnumerator, &enumerator); err != nil {
		ready <- err
		return
	}
	defer enumerator.Release()
	device, deviceID, err := resolveVoiceCaptureDevice(enumerator, r.deviceID)
	if err != nil {
		ready <- err
		return
	}
	defer device.Release()
	var client *wca.IAudioClient
	if err := device.Activate(wca.IID_IAudioClient, wca.CLSCTX_ALL, nil, &client); err != nil {
		ready <- err
		return
	}
	defer client.Release()
	pcmFormat, freeFormat, err := initializeVoiceCaptureClient(client)
	if err != nil {
		ready <- err
		return
	}
	defer freeFormat()
	var capture *wca.IAudioCaptureClient
	if err := client.GetService(wca.IID_IAudioCaptureClient, &capture); err != nil {
		ready <- err
		return
	}
	defer capture.Release()
	r.setReusable(true)
	ready <- nil

	for {
		select {
		case <-r.begin:
		case <-r.cancel:
			return
		}
		// Start capture immediately: re-resolving the endpoint here cost tens of
		// milliseconds between the hotkey press and the first sample. Whether the
		// session is still reusable is decided once the take has been captured,
		// where the same check costs the user nothing.
		if err := client.Start(); err != nil {
			r.setReusable(false)
			r.started <- err
			return
		}
		r.started <- nil
		drainFloat32(r.levels)

		// The take buffer is allocated on first use: a warm session sits idle
		// between dictations, and reserving a full 20 s buffer up front kept
		// several megabytes resident for a microphone nobody was talking into.
		if r.collector == nil {
			r.collector = newPCMCollector(pcmFormat.pcmBudgetBytes(voiceCaptureMaxDuration))
		}
		collector := r.collector
		collector.Reset()
		var captureErr error
		// silence is reused for silent buffers so a muted device never allocates
		// on every poll.
		var silence []byte
		ticker := time.NewTicker(20 * time.Millisecond)
		collecting := true
		for collecting {
			select {
			case <-r.stop:
				collecting = false
			case <-r.cancel:
				ticker.Stop()
				client.Stop()
				return
			case <-ticker.C:
				for {
					var data *byte
					var frames, flags uint32
					var devicePos, qpcPos uint64
					if err := capture.GetBuffer(&data, &frames, &flags, &devicePos, &qpcPos); err != nil {
						if isAudioBufferEmpty(err) {
							break
						}
						captureErr = err
						collecting = false
						break
					}
					if frames == 0 {
						break
					}
					remaining := collector.max - collector.Len()
					keepFrames := min(int(frames), remaining/pcmFormat.BlockAlign)
					if keepFrames > 0 {
						n := keepFrames * pcmFormat.BlockAlign
						if flags&wca.AUDCLNT_BUFFERFLAGS_SILENT != 0 {
							if len(silence) < n {
								silence = make([]byte, n)
							}
							collector.Append(silence[:n])
							r.publishLevel(0)
						} else if data != nil {
							raw := unsafe.Slice(data, n)
							collector.Append(raw)
							r.publishLevel(pcmRMS(raw, pcmFormat))
						}
					}
					if err := capture.ReleaseBuffer(frames); err != nil {
						captureErr = err
						collecting = false
						break
					}
				}
			}
		}
		ticker.Stop()
		stopErr := client.Stop()
		if captureErr == nil {
			captureErr = stopErr
		}
		r.setReusable(true)
		if captureErr != nil {
			r.setReusable(false)
		} else {
			if err := client.Reset(); err != nil {
				captureErr = err
				r.setReusable(false)
			}
		}
		if captureErr != nil {
			r.result <- captureResult{err: captureErr, reusable: false}
			return
		}
		// A device switch keeps this take intact; it only retires the session so
		// the next press reinitializes it on the new endpoint.
		result := captureResult{reusable: !r.captureDeviceChanged(enumerator, deviceID)}
		if !result.reusable {
			r.setReusable(false)
		}
		switch {
		case collector.Len() == 0:
			result.err = errors.New("没有录到音频，请检查麦克风权限和输入设备")
		default:
			result.wav, result.err = encodeVoiceWAV(collector.Bytes(), pcmFormat)
		}
		r.result <- result
		if !result.reusable {
			return
		}
	}
}

func isAudioBufferEmpty(err error) bool {
	var oleErr *ole.OleError
	return errors.As(err, &oleErr) && oleErr.Code() == audclntSBufferEmpty
}

// captureDeviceChanged reports whether this recording ended on a different
// endpoint than the one the session was opened with, which retires the session
// so the next press reinitializes it. It runs after the capture has been
// delivered to the caller, never on the start path.
func (r *windowsAudioRecording) captureDeviceChanged(enumerator *wca.IMMDeviceEnumerator, initializedID string) bool {
	current, currentID, err := resolveVoiceCaptureDevice(enumerator, r.deviceID)
	if current != nil {
		current.Release()
	}
	return err != nil || voiceCaptureDeviceChanged(initializedID, currentID)
}

func listNativeAudioInputDevices() ([]voiceCaptureDevice, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		return nil, err
	}
	defer ole.CoUninitialize()
	var enumerator *wca.IMMDeviceEnumerator
	if err := wca.CoCreateInstance(wca.CLSID_MMDeviceEnumerator, 0, wca.CLSCTX_ALL, wca.IID_IMMDeviceEnumerator, &enumerator); err != nil {
		return nil, err
	}
	defer enumerator.Release()
	var collection *wca.IMMDeviceCollection
	if err := enumerator.EnumAudioEndpoints(wca.ECapture, wca.DEVICE_STATE_ACTIVE, &collection); err != nil {
		return nil, err
	}
	defer collection.Release()
	var count uint32
	if err := collection.GetCount(&count); err != nil {
		return nil, err
	}
	devices := make([]voiceCaptureDevice, 0, count)
	for i := uint32(0); i < count; i++ {
		var device *wca.IMMDevice
		if err := collection.Item(i, &device); err != nil {
			continue
		}
		var id string
		idErr := device.GetId(&id)
		var name string
		var props *wca.IPropertyStore
		if err := device.OpenPropertyStore(wca.STGM_READ, &props); err == nil {
			var value wca.PROPVARIANT
			if err := props.GetValue(&wca.PKEY_Device_FriendlyName, &value); err == nil {
				name = value.String()
			}
			value.VARIANT.Clear()
			props.Release()
		}
		device.Release()
		if idErr == nil && id != "" && name != "" {
			devices = append(devices, voiceCaptureDevice{ID: id, Name: name})
		}
	}
	return devices, nil
}

func resolveVoiceCaptureDevice(enumerator *wca.IMMDeviceEnumerator, requestedID string) (*wca.IMMDevice, string, error) {
	if requestedID == "" {
		var device *wca.IMMDevice
		if err := enumerator.GetDefaultAudioEndpoint(wca.ECapture, wca.EConsole, &device); err != nil {
			return nil, "", err
		}
		var id string
		if err := device.GetId(&id); err != nil {
			device.Release()
			return nil, "", err
		}
		return device, id, nil
	}
	var collection *wca.IMMDeviceCollection
	if err := enumerator.EnumAudioEndpoints(wca.ECapture, wca.DEVICE_STATE_ACTIVE, &collection); err != nil {
		return nil, "", err
	}
	defer collection.Release()
	var count uint32
	if err := collection.GetCount(&count); err != nil {
		return nil, "", err
	}
	for i := uint32(0); i < count; i++ {
		var device *wca.IMMDevice
		if err := collection.Item(i, &device); err != nil {
			continue
		}
		var id string
		err := device.GetId(&id)
		if err == nil && id == requestedID {
			return device, id, nil
		}
		device.Release()
		if err != nil {
			return nil, "", err
		}
	}
	return nil, "", errVoiceCaptureDeviceUnavailable
}

func voiceCaptureDeviceChanged(initializedID, currentID string) bool {
	return initializedID != currentID
}

func (r *windowsAudioRecording) setReusable(value bool) {
	r.mu.Lock()
	r.reusable = value
	r.mu.Unlock()
}

func drainFloat32(ch <-chan float32) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

func (r *windowsAudioRecording) publishLevel(level float32) {
	select {
	case r.levels <- level:
	default:
		select {
		case <-r.levels:
		default:
		}
		select {
		case r.levels <- level:
		default:
		}
	}
}

// initializeVoiceCaptureClient asks the engine to deliver the shared wire format
// directly. AUTOCONVERTPCM lets every Windows endpoint resample in the audio
// engine, which keeps our PCM buffer at 32 kB/s instead of the mix format's
// 384 kB/s and removes the closing resample pass. Endpoints or drivers that
// refuse the custom format fall back to the mix format, which the shared
// encoder converts exactly as macOS and Linux backends will.
func initializeVoiceCaptureClient(client *wca.IAudioClient) (capturePCMFormat, func(), error) {
	target := &wca.WAVEFORMATEX{
		WFormatTag:      1,
		NChannels:       1,
		NSamplesPerSec:  voiceCaptureSampleRate,
		NAvgBytesPerSec: voiceCaptureSampleRate * 2,
		NBlockAlign:     2,
		WBitsPerSample:  16,
	}
	flags := uint32(wca.AUDCLNT_STREAMFLAGS_AUTOCONVERTPCM | wca.AUDCLNT_STREAMFLAGS_SRC_DEFAULT_QUALITY)
	if err := client.Initialize(wca.AUDCLNT_SHAREMODE_SHARED, flags, 0, 0, target, nil); err == nil {
		return capturePCMFormat{SampleRate: voiceCaptureSampleRate, Channels: 1, BytesPerSample: 2, BlockAlign: 2}, func() {}, nil
	}
	var format *wca.WAVEFORMATEX
	if err := client.GetMixFormat(&format); err != nil {
		return capturePCMFormat{}, func() {}, err
	}
	free := func() { ole.CoTaskMemFree(uintptr(unsafe.Pointer(format))) }
	converted, err := captureFormatOf(format)
	if err != nil {
		free()
		return capturePCMFormat{}, func() {}, err
	}
	if err := client.Initialize(wca.AUDCLNT_SHAREMODE_SHARED, 0, 0, 0, format, nil); err != nil {
		free()
		return capturePCMFormat{}, func() {}, err
	}
	return converted, free, nil
}

// captureFormatOf describes the mix format the engine reported.
func captureFormatOf(format *wca.WAVEFORMATEX) (capturePCMFormat, error) {
	if format.NSamplesPerSec == 0 || format.NChannels == 0 || format.NBlockAlign == 0 {
		return capturePCMFormat{}, errors.New("麦克风返回了无效音频格式")
	}
	floating := isFloatWaveFormat(format)
	if !floating && format.WFormatTag != 1 && format.WFormatTag != 0xfffe {
		return capturePCMFormat{}, fmt.Errorf("不支持麦克风音频格式 %d", format.WFormatTag)
	}
	out := capturePCMFormat{
		SampleRate:     format.NSamplesPerSec,
		Channels:       int(format.NChannels),
		BytesPerSample: (int(format.WBitsPerSample) + 7) / 8,
		BlockAlign:     int(format.NBlockAlign),
		Float:          floating,
	}
	if !out.valid() {
		return capturePCMFormat{}, errUnsupportedCaptureFormat
	}
	return out, nil
}

func isFloatWaveFormat(format *wca.WAVEFORMATEX) bool {
	if format.WFormatTag == 3 {
		return true
	}
	if format.WFormatTag != 0xfffe || format.CbSize < 22 {
		return false
	}
	// WAVEFORMATEXTENSIBLE.SubFormat GUID begins at byte offset 24; Data1=3 is IEEE float.
	return *(*uint32)(unsafe.Add(unsafe.Pointer(format), 24)) == 3
}

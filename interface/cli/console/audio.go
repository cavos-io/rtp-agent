package console

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/gordonklaus/portaudio"
)

// AudioIO manages bidirectional audio interfacing with the host OS's microphone and speakers.
type AudioIO struct {
	stream *portaudio.Stream
	ctx    context.Context
	cancel context.CancelFunc

	// Mic to Worker
	audioOutCh chan *model.AudioFrame

	// Worker to Speakers
	audioInCh chan *model.AudioFrame

	mu            sync.Mutex
	started       bool
	inputAttached bool
	outputPaused  bool

	sampleRate      int
	channels        int
	framesPerBuffer int
	inputDevice     string
	outputDevice    string

	speakerBuffer []int16
}

func NewAudioIO() *AudioIO {
	return &AudioIO{
		audioOutCh:      make(chan *model.AudioFrame, 100),
		audioInCh:       make(chan *model.AudioFrame, 100),
		sampleRate:      24000,
		channels:        1,
		framesPerBuffer: 2400, // 100ms at 24kHz, chunked into 10ms frames.
		inputAttached:   true,
	}
}

func (a *AudioIO) SetDevices(inputDevice, outputDevice string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.inputDevice = inputDevice
	a.outputDevice = outputDevice
}

func (a *AudioIO) Start(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.started {
		return nil
	}

	err := portaudio.Initialize()
	if err != nil {
		return fmt.Errorf("failed to init portaudio: %w", err)
	}

	a.ctx, a.cancel = context.WithCancel(ctx)

	devices, err := portaudio.Devices()
	if err != nil {
		portaudio.Terminate()
		return fmt.Errorf("failed to list audio devices: %w", err)
	}
	defaultInput, _ := portaudio.DefaultInputDevice()
	defaultOutput, _ := portaudio.DefaultOutputDevice()
	inputDevice, err := selectConsoleAudioDevice(devices, a.inputDevice, defaultInput, true)
	if err != nil {
		portaudio.Terminate()
		return err
	}
	outputDevice, err := selectConsoleAudioDevice(devices, a.outputDevice, defaultOutput, false)
	if err != nil {
		portaudio.Terminate()
		return err
	}

	params := portaudio.StreamParameters{
		Input: portaudio.StreamDeviceParameters{
			Device:   inputDevice,
			Channels: a.channels,
			Latency:  inputDevice.DefaultLowInputLatency,
		},
		Output: portaudio.StreamDeviceParameters{
			Device:   outputDevice,
			Channels: a.channels,
			Latency:  outputDevice.DefaultLowOutputLatency,
		},
		SampleRate:      float64(a.sampleRate),
		FramesPerBuffer: a.framesPerBuffer,
	}

	stream, err := portaudio.OpenStream(params,
		func(in, out []int16) {
			a.pushMicSamples(in)
			a.fillSpeakerOutput(out)
		})

	if err != nil {
		portaudio.Terminate()
		return fmt.Errorf("failed to open audio stream: %w", err)
	}

	if err := stream.Start(); err != nil {
		stream.Close()
		portaudio.Terminate()
		return fmt.Errorf("failed to start audio stream: %w", err)
	}

	a.stream = stream
	a.started = true

	go a.receiveLoop()

	return nil
}

func selectConsoleAudioDevice(devices []*portaudio.DeviceInfo, request string, defaultDevice *portaudio.DeviceInfo, input bool) (*portaudio.DeviceInfo, error) {
	kind := "output"
	if input {
		kind = "input"
	}

	if request == "" {
		if defaultDevice == nil {
			return nil, fmt.Errorf("no default %s device available", kind)
		}
		if !consoleAudioDeviceSupports(defaultDevice, input) {
			return nil, fmt.Errorf("default %s device %q does not support %s", kind, defaultDevice.Name, kind)
		}
		return defaultDevice, nil
	}

	var match *portaudio.DeviceInfo
	if index, err := strconv.Atoi(request); err == nil {
		for _, device := range devices {
			if device != nil && device.Index == index {
				match = device
				break
			}
		}
	} else {
		request = strings.ToLower(request)
		for _, device := range devices {
			if device != nil && strings.Contains(strings.ToLower(device.Name), request) {
				match = device
				break
			}
		}
	}

	if match == nil {
		return nil, fmt.Errorf("%s device %q was not found", kind, request)
	}
	if !consoleAudioDeviceSupports(match, input) {
		return nil, fmt.Errorf("%s device %q does not support %s", kind, match.Name, kind)
	}
	return match, nil
}

func consoleAudioDeviceSupports(device *portaudio.DeviceInfo, input bool) bool {
	if device == nil {
		return false
	}
	if input {
		return device.MaxInputChannels > 0
	}
	return device.MaxOutputChannels > 0
}

func (a *AudioIO) Stop() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.started {
		return nil
	}

	a.cancel()
	a.stream.Stop()
	a.stream.Close()
	portaudio.Terminate()
	a.started = false
	return nil
}

func (a *AudioIO) SetInputAttached(attached bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.inputAttached = attached
}

func (a *AudioIO) SetOutputPaused(paused bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.outputPaused = paused
}

func (a *AudioIO) OutputPaused() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.outputPaused
}

func (a *AudioIO) PushMicFrame(frame *model.AudioFrame) bool {
	if frame == nil {
		return false
	}

	a.mu.Lock()
	attached := a.inputAttached
	a.mu.Unlock()
	if !attached {
		return false
	}

	select {
	case a.audioOutCh <- frame:
		return true
	default:
		return false
	}
}

func (a *AudioIO) pushMicSamples(samples []int16) {
	const frameSamples = 240 // 10ms at 24kHz.
	for start := 0; start+frameSamples <= len(samples); start += frameSamples {
		chunk := samples[start : start+frameSamples]
		data := make([]byte, len(chunk)*2)
		for i, v := range chunk {
			data[i*2] = byte(v)
			data[i*2+1] = byte(v >> 8)
		}

		a.PushMicFrame(&model.AudioFrame{
			Data:              data,
			SampleRate:        uint32(a.sampleRate),
			NumChannels:       uint32(a.channels),
			SamplesPerChannel: uint32(len(chunk)),
		})
	}
}

// PushFrame takes audio from the Agent and queues it for the speakers
func (a *AudioIO) PushFrame(frame *model.AudioFrame) {
	if frame == nil {
		return
	}

	buffer := audio.NewAudioArrayBuffer(int(frame.SamplesPerChannel), frame.SampleRate)
	if _, err := buffer.PushFrame(frame); err != nil {
		return
	}
	pcm := buffer.Read()

	a.mu.Lock()
	a.speakerBuffer = append(a.speakerBuffer, pcm...)
	a.mu.Unlock()
}

func (a *AudioIO) fillSpeakerOutput(out []int16) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.outputPaused {
		for i := range out {
			out[i] = 0
		}
		return
	}

	if len(a.speakerBuffer) >= len(out) {
		copy(out, a.speakerBuffer[:len(out)])
		a.speakerBuffer = a.speakerBuffer[len(out):]
		return
	}

	copy(out[:len(a.speakerBuffer)], a.speakerBuffer)
	for i := len(a.speakerBuffer); i < len(out); i++ {
		out[i] = 0
	}
	a.speakerBuffer = a.speakerBuffer[:0]
}

func (a *AudioIO) ClearOutputBuffer() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.speakerBuffer = a.speakerBuffer[:0]
}

func (a *AudioIO) MicFrames() <-chan *model.AudioFrame {
	return a.audioOutCh
}

func (a *AudioIO) receiveLoop() {
	for {
		select {
		case <-a.ctx.Done():
			return
		case frame := <-a.audioInCh:
			a.PushFrame(frame)
		}
	}
}

// Write for pipe integration
func (a *AudioIO) Write(frame *model.AudioFrame) error {
	select {
	case <-a.ctx.Done():
		return a.ctx.Err()
	case a.audioInCh <- frame:
		return nil
	default:
		// wait a bit
		select {
		case <-a.ctx.Done():
			return a.ctx.Err()
		case <-time.After(10 * time.Millisecond):
			return fmt.Errorf("audio buffer full")
		case a.audioInCh <- frame:
			return nil
		}
	}
}

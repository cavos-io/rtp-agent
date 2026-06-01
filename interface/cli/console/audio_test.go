package console

import (
	"strings"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/gordonklaus/portaudio"
)

func TestAudioIOInputAttachmentControlsMicFrames(t *testing.T) {
	audioIO := NewAudioIO()
	frame := &model.AudioFrame{
		Data:              []byte{0x01, 0x00},
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: 1,
	}

	audioIO.SetInputAttached(false)
	if audioIO.PushMicFrame(frame) {
		t.Fatal("PushMicFrame() = true while input detached, want false")
	}
	select {
	case got := <-audioIO.MicFrames():
		t.Fatalf("MicFrames() received %#v while detached", got)
	default:
	}

	audioIO.SetInputAttached(true)
	if !audioIO.PushMicFrame(frame) {
		t.Fatal("PushMicFrame() = false while input attached, want true")
	}
	select {
	case got := <-audioIO.MicFrames():
		if got != frame {
			t.Fatal("MicFrames() returned a different frame")
		}
	case <-time.After(time.Second):
		t.Fatal("MicFrames() did not receive attached frame")
	}
}

func TestAudioIOPushMicSamplesEmitsTenMillisecondFrames(t *testing.T) {
	audioIO := NewAudioIO()
	samples := make([]int16, 480)
	for i := range samples {
		samples[i] = int16(i + 1)
	}

	audioIO.pushMicSamples(samples)

	for frameIndex := 0; frameIndex < 2; frameIndex++ {
		select {
		case frame := <-audioIO.MicFrames():
			if frame.SampleRate != 24000 {
				t.Fatalf("frame %d SampleRate = %d, want 24000", frameIndex, frame.SampleRate)
			}
			if frame.NumChannels != 1 {
				t.Fatalf("frame %d NumChannels = %d, want 1", frameIndex, frame.NumChannels)
			}
			if frame.SamplesPerChannel != 240 {
				t.Fatalf("frame %d SamplesPerChannel = %d, want 240", frameIndex, frame.SamplesPerChannel)
			}
			if len(frame.Data) != 480 {
				t.Fatalf("frame %d data len = %d, want 480", frameIndex, len(frame.Data))
			}
			firstSample := int16(frame.Data[0]) | int16(frame.Data[1])<<8
			wantFirstSample := int16(frameIndex*240 + 1)
			if firstSample != wantFirstSample {
				t.Fatalf("frame %d first sample = %d, want %d", frameIndex, firstSample, wantFirstSample)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for frame %d", frameIndex)
		}
	}

	select {
	case frame := <-audioIO.MicFrames():
		t.Fatalf("unexpected extra frame: %#v", frame)
	default:
	}
}

func TestSelectConsoleAudioDeviceSupportsIDAndNameSubstring(t *testing.T) {
	devices := []*portaudio.DeviceInfo{
		{Index: 0, Name: "Built-in Microphone", MaxInputChannels: 1},
		{Index: 1, Name: "Built-in Speakers", MaxOutputChannels: 2},
		{Index: 2, Name: "USB Headset", MaxInputChannels: 1, MaxOutputChannels: 2},
	}

	input, err := selectConsoleAudioDevice(devices, "usb headset", devices[0], true)
	if err != nil {
		t.Fatalf("selectConsoleAudioDevice(input by name) error = %v", err)
	}
	if input.Index != 2 {
		t.Fatalf("input device index = %d, want 2", input.Index)
	}

	output, err := selectConsoleAudioDevice(devices, "1", devices[2], false)
	if err != nil {
		t.Fatalf("selectConsoleAudioDevice(output by ID) error = %v", err)
	}
	if output.Index != 1 {
		t.Fatalf("output device index = %d, want 1", output.Index)
	}

	defaultInput, err := selectConsoleAudioDevice(devices, "", devices[0], true)
	if err != nil {
		t.Fatalf("selectConsoleAudioDevice(default input) error = %v", err)
	}
	if defaultInput.Index != 0 {
		t.Fatalf("default input device index = %d, want 0", defaultInput.Index)
	}
}

func TestSelectConsoleAudioDeviceRejectsWrongDirection(t *testing.T) {
	devices := []*portaudio.DeviceInfo{
		{Index: 0, Name: "Built-in Microphone", MaxInputChannels: 1},
		{Index: 1, Name: "Built-in Speakers", MaxOutputChannels: 2},
	}

	_, err := selectConsoleAudioDevice(devices, "1", devices[0], true)
	if err == nil {
		t.Fatal("selectConsoleAudioDevice() error = nil, want wrong-direction error")
	}
	if !strings.Contains(err.Error(), "input") {
		t.Fatalf("selectConsoleAudioDevice() error = %q, want input context", err)
	}
}

func TestAudioIOClearOutputBufferDropsQueuedSpeakerAudio(t *testing.T) {
	audioIO := NewAudioIO()
	audioIO.PushFrame(&model.AudioFrame{
		Data:              []byte{0x01, 0x00, 0x02, 0x00},
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: 2,
	})

	audioIO.mu.Lock()
	buffered := len(audioIO.speakerBuffer)
	audioIO.mu.Unlock()
	if buffered != 2 {
		t.Fatalf("speakerBuffer len after PushFrame = %d, want 2", buffered)
	}

	audioIO.ClearOutputBuffer()

	audioIO.mu.Lock()
	buffered = len(audioIO.speakerBuffer)
	audioIO.mu.Unlock()
	if buffered != 0 {
		t.Fatalf("speakerBuffer len after ClearOutputBuffer = %d, want 0", buffered)
	}
}

func TestAudioIOOutputPausePreservesQueuedSpeakerAudio(t *testing.T) {
	audioIO := NewAudioIO()
	if audioIO.OutputPaused() {
		t.Fatal("OutputPaused() = true for new AudioIO, want false")
	}
	audioIO.PushFrame(&model.AudioFrame{
		Data:              []byte{0x03, 0x00, 0x04, 0x00},
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: 2,
	})

	audioIO.SetOutputPaused(true)
	if !audioIO.OutputPaused() {
		t.Fatal("OutputPaused() = false after SetOutputPaused(true)")
	}
	out := []int16{9, 9}
	audioIO.fillSpeakerOutput(out)
	if out[0] != 0 || out[1] != 0 {
		t.Fatalf("paused speaker output = %v, want silence", out)
	}
	audioIO.mu.Lock()
	buffered := len(audioIO.speakerBuffer)
	audioIO.mu.Unlock()
	if buffered != 2 {
		t.Fatalf("speakerBuffer len while paused = %d, want 2", buffered)
	}

	audioIO.SetOutputPaused(false)
	if audioIO.OutputPaused() {
		t.Fatal("OutputPaused() = true after SetOutputPaused(false)")
	}
	audioIO.fillSpeakerOutput(out)
	if out[0] != 3 || out[1] != 4 {
		t.Fatalf("resumed speaker output = %v, want queued samples", out)
	}
	audioIO.mu.Lock()
	buffered = len(audioIO.speakerBuffer)
	audioIO.mu.Unlock()
	if buffered != 0 {
		t.Fatalf("speakerBuffer len after resumed output = %d, want 0", buffered)
	}
}

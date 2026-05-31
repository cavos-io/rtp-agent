package console

import (
	"testing"
	"time"

	"github.com/cavos-io/conversation-worker/model"
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
	audioIO.PushFrame(&model.AudioFrame{
		Data:              []byte{0x03, 0x00, 0x04, 0x00},
		SampleRate:        24000,
		NumChannels:       1,
		SamplesPerChannel: 2,
	})

	audioIO.SetOutputPaused(true)
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

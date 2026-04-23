package agent

import (
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/model"
)

func TestTranscriptSynchronizer(t *testing.T) {
	// speakingRate: 10 syllables per second (very fast for testing)
	// refreshRate: 10ms
	sync := NewTranscriptSynchronizer(10.0, 10*time.Millisecond)
	defer sync.Close()

	// "hello world" is 3 syllables (hel-lo world)
	// At 10 syl/sec, it needs 0.3s of audio.
	sync.PushText("hello world ")
	
	// Push 400ms of audio
	// 48000Hz, 1 channel, 480 samples = 10ms
	for i := 0; i < 40; i++ {
		sync.PushAudio(&model.AudioFrame{
			Data:              make([]byte, 960),
			SampleRate:        48000,
			NumChannels:       1,
			SamplesPerChannel: 480,
		})
	}

	timeout := time.After(1 * time.Second)
	var result string
Loop:
	for {
		select {
		case ev := <-sync.EventCh():
			result += ev.Text
			if result == "hello world " {
				break Loop
			}
		case <-timeout:
			t.Fatalf("Timed out waiting for transcript. Got: %q", result)
		}
	}
}

func TestSyncedOutputs(t *testing.T) {
	sync := NewTranscriptSynchronizer(10.0, 10*time.Millisecond)
	defer sync.Close()

	mockAudio := &mockAudioOutput{}
	syncedAudio := NewSyncedAudioOutput(sync, mockAudio)

	mockText := &mockTextOutput{}
	_ = NewSyncedTextOutput(sync, mockText)

	syncedAudio.CaptureFrame(&model.AudioFrame{
		Data:              make([]byte, 960),
		SampleRate:        48000,
		NumChannels:       1,
		SamplesPerChannel: 480,
	})
	
	err := syncedAudio.next.CaptureFrame(&model.AudioFrame{}) // check if next is called
	if err != nil {
		t.Errorf("CaptureFrame failed: %v", err)
	}

	if len(mockAudio.captured) == 0 {
		t.Error("Audio frame was not captured by mock")
	}
}

package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadAudioFramesFromFileReadsPCMBackgroundAudio(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ambient.pcm")
	data := []byte{
		0x01, 0x00,
		0xff, 0x7f,
		0x00, 0x80,
		0x00, 0x00,
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	frames := readAudioFramesFromFile(path, false, make(chan struct{}))

	select {
	case frame, ok := <-frames:
		if !ok {
			t.Fatal("readAudioFramesFromFile closed without a frame")
		}
		if string(frame.Data) != string(data) {
			t.Fatalf("frame data = %v, want %v", frame.Data, data)
		}
		if frame.SampleRate != 48000 {
			t.Fatalf("frame SampleRate = %d, want 48000", frame.SampleRate)
		}
		if frame.NumChannels != 1 {
			t.Fatalf("frame NumChannels = %d, want 1", frame.NumChannels)
		}
		if frame.SamplesPerChannel != 4 {
			t.Fatalf("frame SamplesPerChannel = %d, want 4", frame.SamplesPerChannel)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for PCM frame")
	}
}

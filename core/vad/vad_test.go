package vad

import (
	"context"
	"testing"

	"github.com/cavos-io/rtp-agent/model"
)

func TestSimpleVAD(t *testing.T) {
	v := NewSimpleVAD(0.01)
	stream, _ := v.Stream(context.Background())

	// Push 4 frames of silence
	silenceFrame := &model.AudioFrame{
		Data: make([]byte, 320), // 160 samples of zero
	}
	for i := 0; i < 4; i++ {
		stream.PushFrame(silenceFrame)
	}

	// Push 4 frames of loud audio
	loudData := make([]byte, 320)
	for i := range loudData {
		loudData[i] = 0x7F
	}
	loudFrame := &model.AudioFrame{
		Data: loudData,
	}
	for i := 0; i < 4; i++ {
		stream.PushFrame(loudFrame)
	}

	// Should trigger StartOfSpeech (debounce 3 frames)
	ev, err := stream.Next()
	if err != nil {
		t.Fatalf("Next failed: %v", err)
	}
	if ev.Type != VADEventStartOfSpeech {
		t.Errorf("Expected StartOfSpeech, got %v", ev.Type)
	}

	// Push 51 frames of silence to trigger EndOfSpeech (debounce 50 frames)
	for i := 0; i < 51; i++ {
		stream.PushFrame(silenceFrame)
	}

	ev, err = stream.Next()
	if err != nil {
		t.Fatalf("Next failed: %v", err)
	}
	if ev.Type != VADEventEndOfSpeech {
		t.Errorf("Expected EndOfSpeech, got %v", ev.Type)
	}
}

package silero

import (
	"context"
	"testing"

	"github.com/cavos-io/rtp-agent/model"
)

func TestSileroVAD_SpeechDetection(t *testing.T) {
	v := NewSileroVAD(WithActivationThreshold(0.5))
	stream, _ := v.Stream(context.Background())

	// 1. Test Silence (All zeros)
	silence := &model.AudioFrame{
		Data:        make([]byte, 3200), // 100ms at 16k
		SampleRate:  16000,
		NumChannels: 1,
	}
	
	err := stream.PushFrame(silence)
	if err != nil {
		t.Fatalf("PushFrame failed: %v", err)
	}

	// 2. Test Speech (Loud noise)
	speechData := make([]byte, 3200)
	for i := range speechData {
		speechData[i] = 0x7F // Max value
	}
	speech := &model.AudioFrame{
		Data:        speechData,
		SampleRate:  16000,
		NumChannels: 1,
	}

	err = stream.PushFrame(speech)
	if err != nil {
		t.Fatalf("PushFrame failed: %v", err)
	}

	// Note: VAD detection is usually asynchronous via events if implemented that way,
	// but SileroVAD/SimpleVAD might expose it differently.
	// In core/vad/vad.go, VADStream usually has a way to get events.
}

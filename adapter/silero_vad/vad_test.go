package silero_vad

import (
	"context"
	"testing"

	"github.com/cavos-io/rtp-agent/model"
)

func TestSileroVAD(t *testing.T) {
	v := NewSileroVAD(SileroVADOptions{
		ActivationThreshold: 0.5,
		MinSpeechDuration:   100,
		MinSilenceDuration:  500,
	})

	stream, err := v.Stream(context.Background())
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}
	defer stream.Close()

	// Push a silent frame
	silentFrame := &model.AudioFrame{
		Data: make([]byte, 640), // 20ms of 16kHz audio
	}

	err = stream.PushFrame(silentFrame)
	if err != nil {
		t.Errorf("PushFrame failed: %v", err)
	}
}

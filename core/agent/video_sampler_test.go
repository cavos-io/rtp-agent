package agent

import (
	"context"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/library/utils/images"
)

func TestVoiceActivityVideoSampler(t *testing.T) {
	sampler := NewVoiceActivityVideoSampler(nil, 2.0, images.EncodeOptions{})
	
	ctx := context.Background()
	frame := &images.VideoFrame{}
	
	// Not speaking, should return false
	if sampler.OnVideoFrame(ctx, frame) {
		t.Error("Expected false when not speaking")
	}
	
	sampler.SetSpeaking(true)
	
	// First frame while speaking should return true
	if !sampler.OnVideoFrame(ctx, frame) {
		t.Error("Expected true for first frame while speaking")
	}
	
	// Immediate second frame should return false (sample rate is 2fps)
	if sampler.OnVideoFrame(ctx, frame) {
		t.Error("Expected false for rapid second frame")
	}
	
	// Wait 0.6s (> 1/2s interval)
	time.Sleep(600 * time.Millisecond)
	if !sampler.OnVideoFrame(ctx, frame) {
		t.Error("Expected true after waiting for interval")
	}
}

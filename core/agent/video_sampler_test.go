package agent

import (
	"context"
	"testing"
	"time"

	"github.com/cavos-io/rtp-agent/library/utils/images"
)

func TestVoiceActivityVideoSamplerSamplesFirstSilentFrame(t *testing.T) {
	sampler := NewVoiceActivityVideoSampler(nil, 1.0, images.EncodeOptions{})

	if !sampler.OnVideoFrame(context.Background(), &images.VideoFrame{}) {
		t.Fatal("first silent frame was not sampled")
	}
}

func TestVoiceActivityVideoSamplerUsesSilentFPS(t *testing.T) {
	sampler := NewVoiceActivityVideoSampler(nil, 1.0, images.EncodeOptions{})
	if !sampler.OnVideoFrame(context.Background(), &images.VideoFrame{}) {
		t.Fatal("first silent frame was not sampled")
	}
	if sampler.OnVideoFrame(context.Background(), &images.VideoFrame{}) {
		t.Fatal("second immediate silent frame was sampled before silent interval")
	}

	sampler.mu.Lock()
	sampler.lastTime = time.Now().Add(-4 * time.Second)
	sampler.mu.Unlock()

	if !sampler.OnVideoFrame(context.Background(), &images.VideoFrame{}) {
		t.Fatal("silent frame after silent interval was not sampled")
	}
}

func TestVoiceActivityVideoSamplerUsesSpeakingFPS(t *testing.T) {
	sampler := NewVoiceActivityVideoSampler(nil, 2.0, images.EncodeOptions{})
	sampler.SetSpeaking(true)
	if !sampler.OnVideoFrame(context.Background(), &images.VideoFrame{}) {
		t.Fatal("first speaking frame was not sampled")
	}
	if sampler.OnVideoFrame(context.Background(), &images.VideoFrame{}) {
		t.Fatal("second immediate speaking frame was sampled before speaking interval")
	}

	sampler.mu.Lock()
	sampler.lastTime = time.Now().Add(-600 * time.Millisecond)
	sampler.mu.Unlock()

	if !sampler.OnVideoFrame(context.Background(), &images.VideoFrame{}) {
		t.Fatal("speaking frame after speaking interval was not sampled")
	}
}

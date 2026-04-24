package silero

import (
	"context"
	"fmt"

	"github.com/cavos-io/rtp-agent/core/vad"
	speech "github.com/streamer45/silero-vad-go/speech"
)

type VADOptions struct {
	MinSpeechDuration   float64
	MinSilenceDuration  float64
	ActivationThreshold float64
	SampleRate          int
	ModelPath           string
	SpeechPadMs         int
}

func DefaultVADOptions() VADOptions {
	return VADOptions{
		MinSpeechDuration:   0.05,
		MinSilenceDuration:  0.3, // Updated from 0.25
		ActivationThreshold: 0.5,
		SampleRate:          16000,
		ModelPath:           "/models/silero_vad.onnx",
		SpeechPadMs:         30,
	}
}

type SileroVAD struct {
	options VADOptions
}

type VADOption func(*VADOptions)

func WithMinSpeechDuration(d float64) VADOption {
	return func(o *VADOptions) {
		o.MinSpeechDuration = d
	}
}

func WithMinSilenceDuration(d float64) VADOption {
	return func(o *VADOptions) {
		o.MinSilenceDuration = d
	}
}

func WithActivationThreshold(t float64) VADOption {
	return func(o *VADOptions) {
		o.ActivationThreshold = t
	}
}

func WithSampleRate(r int) VADOption {
	return func(o *VADOptions) {
		o.SampleRate = r
	}
}

func WithModelPath(p string) VADOption {
	return func(o *VADOptions) {
		o.ModelPath = p
	}
}

func WithSpeechPadMs(ms int) VADOption {
	return func(o *VADOptions) {
		o.SpeechPadMs = ms
	}
}

func NewSileroVAD(opts ...VADOption) (*SileroVAD, error) {
	options := DefaultVADOptions()
	for _, opt := range opts {
		opt(&options)
	}

	// Validate model path is accessible
	if options.ModelPath == "" {
		return nil, fmt.Errorf("silero VAD model path is required")
	}

	return &SileroVAD{
		options: options,
	}, nil
}

func (v *SileroVAD) PreWarm() error {
	// Create a temporary detector to warm up the ONNX runtime
	sd, err := speech.NewDetector(speech.DetectorConfig{
		ModelPath:            v.options.ModelPath,
		SampleRate:           v.options.SampleRate,
		Threshold:            float32(v.options.ActivationThreshold),
		MinSilenceDurationMs: int(v.options.MinSilenceDuration * 1000),
		SpeechPadMs:          v.options.SpeechPadMs,
	})
	if err != nil {
		return fmt.Errorf("pre-warm: failed to create detector: %w", err)
	}
	defer sd.Destroy()

	// Run a dummy inference with silence
	// 1536 is sileroChunkSize from stream.go
	dummyPCM := make([]float32, 1536)
	_, err = sd.Detect(dummyPCM)
	if err != nil {
		return fmt.Errorf("pre-warm: failed to run dummy inference: %w", err)
	}

	return nil
}

func (v *SileroVAD) Stream(ctx context.Context) (vad.VADStream, error) {
	// Create a new ONNX-backed Silero detector per stream
	sd, err := speech.NewDetector(speech.DetectorConfig{
		ModelPath:            v.options.ModelPath,
		SampleRate:           v.options.SampleRate,
		Threshold:            float32(v.options.ActivationThreshold),
		MinSilenceDurationMs: int(v.options.MinSilenceDuration * 1000),
		SpeechPadMs:          v.options.SpeechPadMs,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Silero VAD detector: %w", err)
	}

	return newSileroVADStream(ctx, sd, v.options.SampleRate), nil
}

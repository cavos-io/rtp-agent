package silero

import (
	"context"

	"github.com/cavos-io/rtp-agent/core/vad"
)

type VADOptions struct {
	MinSpeechDuration   float64
	MinSilenceDuration  float64
	ActivationThreshold float64
	SampleRate          int
}

func DefaultVADOptions() VADOptions {
	return VADOptions{
		MinSpeechDuration:   0.05,
		MinSilenceDuration:  0.25,
		ActivationThreshold: 0.5,
		SampleRate:          16000,
	}
}

type SileroVAD struct {
	options VADOptions
	inner   vad.VAD
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

func NewSileroVAD(opts ...VADOption) *SileroVAD {
	options := DefaultVADOptions()
	for _, opt := range opts {
		opt(&options)
	}

	// Fallback to simple VAD for now to provide out-of-the-box working plugin
	// without requiring CGO/ONNX dependencies in the base install.
	inner := vad.NewSimpleVAD(options.ActivationThreshold / 10.0) // Scale threshold for RMS vs Probability

	return &SileroVAD{
		options: options,
		inner:   inner,
	}
}

func (v *SileroVAD) Stream(ctx context.Context) (vad.VADStream, error) {
	return v.inner.Stream(ctx)
}

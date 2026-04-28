package silero_vad

import (
	"context"

	"github.com/cavos-io/rtp-agent/core/vad"
)

type SileroVADOptions struct {
	MinSpeechDuration   float64
	MinSilenceDuration  float64
	SpeechPadMs         int
	ActivationThreshold float64
}

type SileroVAD struct {
	Options SileroVADOptions
}

func NewSileroVAD(opts SileroVADOptions) *SileroVAD {
	return &SileroVAD{
		Options: opts,
	}
}

func (v *SileroVAD) Stream(ctx context.Context) (vad.VADStream, error) {
	backend := vad.NewEnhancedVAD(vad.EnhancedVADOptions{
		ActivationThreshold: v.Options.ActivationThreshold,
		MinSpeechDuration:   v.Options.MinSpeechDuration,
		MinSilenceDuration:  v.Options.MinSilenceDuration,
	})
	return backend.Stream(ctx)
}

func (v *SileroVAD) PreWarm() error {
	return nil
}

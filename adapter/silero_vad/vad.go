package silero_vad

import (
	"context"
	"fmt"

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
	return nil, fmt.Errorf("native silero onnx vad is unsupported in this go port; use simple_vad")
}


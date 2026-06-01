package silero_vad

import (
	"context"
	"fmt"

	"github.com/cavos-io/conversation-worker/core/vad"
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

func (v *SileroVAD) Label() string {
	return "silero_vad.VAD"
}

func (v *SileroVAD) Model() string {
	return "silero"
}

func (v *SileroVAD) Provider() string {
	return "ONNX"
}

func (v *SileroVAD) Capabilities() vad.VADCapabilities {
	return vad.VADCapabilities{UpdateInterval: 0.032}
}

func (v *SileroVAD) OnMetricsCollected(vad.VADMetricsHandler) {}

func (v *SileroVAD) Stream(ctx context.Context) (vad.VADStream, error) {
	return nil, fmt.Errorf("native silero onnx vad is unsupported in this go port; use simple_vad")
}

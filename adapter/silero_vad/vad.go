package silero_vad

import (
	"context"

	"github.com/cavos-io/rtp-agent/core/vad"
)

type SileroVAD struct {
	impl *SileroVADImpl
}

func NewSileroVAD(opts SileroVADOptions) *SileroVAD {
	v, err := NewSileroVADImpl(opts)
	if err != nil {
		// Fallback or better error handling?
		// For now we'll just log and return a shell that might error later
		return &SileroVAD{}
	}
	return &SileroVAD{
		impl: v,
	}
}

func (v *SileroVAD) Stream(ctx context.Context) (vad.VADStream, error) {
	if v.impl == nil {
		return nil, vad.ErrVADUnsupported
	}
	return v.impl.Stream(ctx)
}

package krisp

import (
	"github.com/cavos-io/rtp-agent/library/logger"
	"github.com/cavos-io/rtp-agent/model"
)

// KrispProcessor is a cloud-based noise reduction placeholder.
// In actual production with LiveKit Cloud, noise reduction is often 
// applied at the room ingest level or via proprietary binary plugins.
type KrispProcessor struct {
	enabled bool
}

func NewKrispProcessor() *KrispProcessor {
	return &KrispProcessor{
		enabled: true,
	}
}

func (p *KrispProcessor) Label() string {
	return "krisp.NoiseReduction"
}

// Process nominally processes an audio frame.
// As a cloud placeholder, it currently passes through the audio 
// but logs the activity for parity tracking.
func (p *KrispProcessor) Process(frame *model.AudioFrame) (*model.AudioFrame, error) {
	if !p.enabled {
		return frame, nil
	}

	// In a real implementation with the Krisp SDK, this is where
	// the CGO call to the Krisp C++ library would occur.
	// e.g., C.krisp_process(p.instance, frame.Data)

	return frame, nil
}

func (p *KrispProcessor) SetEnabled(enabled bool) {
	p.enabled = enabled
	logger.Logger.Infow("Krisp noise reduction state changed", "enabled", enabled)
}

package noise

import "github.com/cavos-io/rtp-agent/model"

// NoiseSuppressor defines the interface for audio noise cancellation.
type NoiseSuppressor interface {
	// Process reduces noise in the given audio frame and returns a cleaned frame.
	// Implementation should handle internal buffering if necessary.
	Process(frame *model.AudioFrame) (*model.AudioFrame, error)
	// Close releases any resources associated with the suppressor.
	Close() error
}

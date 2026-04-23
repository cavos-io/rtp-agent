package audio

import "github.com/cavos-io/conversation-worker/model"

// NoiseFilter is an optional audio pre-processing port.
// Implementations are provided by consumers (e.g. go-agent-worker).
// If no filter is set on the Agent, the pipeline skips filtering entirely.
type NoiseFilter interface {
	Label() string
	Process(frame *model.AudioFrame) (*model.AudioFrame, error)
	Close() error
}

// NoopNoiseFilter passes audio through unmodified.
// Used as a safe default when noise filtering is not needed.
type NoopNoiseFilter struct{}

func (f *NoopNoiseFilter) Label() string { return "noop" }
func (f *NoopNoiseFilter) Process(frame *model.AudioFrame) (*model.AudioFrame, error) {
	return frame, nil
}
func (f *NoopNoiseFilter) Close() error { return nil }

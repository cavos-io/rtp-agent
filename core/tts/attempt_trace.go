package tts

import (
	"context"
	"sync"

	"github.com/cavos-io/rtp-agent/library/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Fallback owns each opaque provider attempt; providers with internal tracing
// retain ownership of their finer-grained retries.
func (*FallbackAdapter) TracesRequestAttempts() bool { return true }

type ttsAttemptTrace struct {
	span   trace.Span
	cancel context.CancelFunc
	once   sync.Once
}

func (t *ttsAttemptTrace) finish(err error) {
	t.once.Do(func() { telemetry.RecordAttemptError(t.span, err); t.span.End(); t.cancel() })
}
func (t *ttsAttemptTrace) observe(audio *SynthesizedAudio) {
	if audio != nil && audio.RequestID != "" {
		t.span.SetAttributes(attribute.StringSlice(telemetry.AttrProviderRequestIDs, []string{audio.RequestID}))
	}
}

type tracedChunkedAttempt struct {
	ChunkedStream
	attempt *ttsAttemptTrace
}

func (s *tracedChunkedAttempt) Next() (*SynthesizedAudio, error) {
	audio, err := s.ChunkedStream.Next()
	s.attempt.observe(audio)
	if err != nil {
		s.attempt.finish(err)
	}
	return audio, err
}
func (s *tracedChunkedAttempt) Close() error {
	err := s.ChunkedStream.Close()
	s.attempt.finish(err)
	return err
}

type tracedSynthesizeAttempt struct {
	SynthesizeStream
	attempt *ttsAttemptTrace
}

func (s *tracedSynthesizeAttempt) Next() (*SynthesizedAudio, error) {
	audio, err := s.SynthesizeStream.Next()
	s.attempt.observe(audio)
	if err != nil {
		s.attempt.finish(err)
	}
	return audio, err
}
func (s *tracedSynthesizeAttempt) Close() error {
	err := s.SynthesizeStream.Close()
	s.attempt.finish(err)
	return err
}

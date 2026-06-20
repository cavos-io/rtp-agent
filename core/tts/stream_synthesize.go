package tts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	"github.com/cavos-io/rtp-agent/core/audio"
	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/core/llm"
	cavosmath "github.com/cavos-io/rtp-agent/library/math"
	"github.com/cavos-io/rtp-agent/library/telemetry"
	"go.opentelemetry.io/otel/trace"
)

func SynthesizeWithStream(ctx context.Context, provider TTS, text string) (ChunkedStream, error) {
	ctx, span := telemetry.NewTTSStreamSpan(ctx, Model(provider), Provider(provider))
	stream, err := provider.Stream(ctx)
	if err != nil {
		span.End()
		emitTTSError(provider, err, false)
		return nil, err
	}
	if isNilSynthesizeStream(stream) {
		err := fmt.Errorf("TTS returned nil synthesize stream")
		span.End()
		emitTTSError(provider, err, false)
		return nil, err
	}
	if text != "" {
		if err := stream.PushText(text); err != nil {
			_ = stream.Close()
			span.End()
			emitTTSError(provider, err, false)
			return nil, err
		}
	}
	if err := endSynthesizeStreamInput(stream); err != nil {
		_ = stream.Close()
		span.End()
		emitTTSError(provider, err, false)
		return nil, err
	}
	return &chunkedStreamFromSynthesizeStream{
		provider:  provider,
		stream:    stream,
		text:      text,
		requestID: cavosmath.ShortUUID(""),
		startedAt: time.Now(),
		span:      span,
	}, nil
}

func isNilSynthesizeStream(stream SynthesizeStream) bool {
	if stream == nil {
		return true
	}
	value := reflect.ValueOf(stream)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

type inputEndingSynthesizeStream interface {
	EndInput() error
}

func endSynthesizeStreamInput(stream SynthesizeStream) error {
	return EndSynthesizeStreamInput(stream)
}

func EndSynthesizeStreamInput(stream SynthesizeStream) error {
	if isNilSynthesizeStream(stream) {
		return fmt.Errorf("TTS returned nil synthesize stream")
	}
	if ending, ok := stream.(inputEndingSynthesizeStream); ok {
		return ending.EndInput()
	}
	return stream.Flush()
}

type chunkedStreamFromSynthesizeStream struct {
	provider    TTS
	stream      SynthesizeStream
	text        string
	requestID   string
	pending     *SynthesizedAudio
	pendingTail bool
	audioSeen   bool
	closed      bool
	errEmitted  bool
	metricsSent bool
	startedAt   time.Time
	ttfb        float64
	ttfbSet     bool
	audioDur    float64
	done        bool
	exception   error
	span        trace.Span
	spanEnded   bool
}

func (s *chunkedStreamFromSynthesizeStream) Next() (*SynthesizedAudio, error) {
	for {
		audio, err := s.stream.Next()
		clientClosed := isClientClosedStatus(err)
		if clientClosed {
			err = io.EOF
		}
		if err != nil {
			_ = s.Close()
			if errors.Is(err, io.EOF) {
				if s.pending != nil {
					pending := cloneSynthesizedAudio(s.pending)
					pending.RequestID = s.requestID
					pending.SegmentID = ""
					pending.IsFinal = true
					s.pending = nil
					s.markDone(nil)
					s.emitMetrics()
					return pending, nil
				}
				if !clientClosed && !s.audioSeen && strings.TrimSpace(s.text) != "" {
					err := fmt.Errorf("no audio frames were pushed for text: %s", s.text)
					s.markDone(err)
					s.emitError(err)
					return nil, err
				}
				s.markDone(nil)
				s.emitMetrics()
			}
			if !errors.Is(err, io.EOF) {
				s.markDone(err)
				s.emitError(err)
			}
			return nil, err
		}
		if audio == nil {
			continue
		}
		s.audioSeen = true
		s.observeAudio(audio)
		if s.pending != nil {
			combined, combineErr := combineAudioFrames(s.pending.Frame, audio.Frame)
			if s.pendingTail && combineErr == nil {
				audio = cloneSynthesizedAudio(audio)
				audio.Frame = combined
			} else {
				pending := s.stampAudio(s.pending, false)
				s.pending = audio
				s.pendingTail = false
				return pending, nil
			}
		}
		head, tail, ok := splitSynthesizedAudioTail(audio)
		if ok {
			s.pending = tail
			s.pendingTail = true
			return s.stampAudio(head, false), nil
		}
		s.pending = tail
		s.pendingTail = false
	}
}

func isClientClosedStatus(err error) bool {
	var statusErr *llm.APIStatusError
	return errors.As(err, &statusErr) && statusErr.StatusCode == 499
}

func (s *chunkedStreamFromSynthesizeStream) observeAudio(audio *SynthesizedAudio) {
	if audio == nil || audio.Frame == nil {
		return
	}
	if !s.ttfbSet {
		s.ttfb = time.Since(s.startedAt).Seconds()
		s.ttfbSet = true
	}
	s.audioDur += audioFrameDurationSeconds(audio.Frame)
}

func (s *chunkedStreamFromSynthesizeStream) emitError(err error) {
	if s.errEmitted {
		return
	}
	s.errEmitted = true
	emitTTSError(s.provider, err, false)
}

func (s *chunkedStreamFromSynthesizeStream) emitMetrics() {
	if s.metricsSent || s.errEmitted {
		return
	}
	s.metricsSent = true

	emitter, ok := s.provider.(metricsEmitterTTS)
	if !ok {
		return
	}

	ttfb := -1.0
	if s.ttfbSet {
		ttfb = s.ttfb
	}
	emitter.EmitMetricsCollected(&telemetry.TTSMetrics{
		Label:           s.provider.Label(),
		RequestID:       s.requestID,
		Timestamp:       time.Now(),
		TTFB:            ttfb,
		Duration:        time.Since(s.startedAt).Seconds(),
		AudioDuration:   s.audioDur,
		CharactersCount: len(s.text),
		Streamed:        false,
		Metadata: &telemetry.Metadata{
			ModelName:     Model(s.provider),
			ModelProvider: Provider(s.provider),
		},
	})
}

func (s *chunkedStreamFromSynthesizeStream) stampAudio(audio *SynthesizedAudio, isFinal bool) *SynthesizedAudio {
	audio = cloneSynthesizedAudio(audio)
	audio.RequestID = s.requestID
	audio.SegmentID = ""
	audio.IsFinal = isFinal
	return audio
}

func (s *chunkedStreamFromSynthesizeStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	s.markDone(nil)
	s.endSpan()
	return s.stream.Close()
}

func (s *chunkedStreamFromSynthesizeStream) Done() bool {
	return s.done
}

func (s *chunkedStreamFromSynthesizeStream) Exception() error {
	return s.exception
}

func (s *chunkedStreamFromSynthesizeStream) markDone(err error) {
	s.done = true
	if err != nil && !errors.Is(err, io.EOF) {
		s.exception = err
	}
}

func (s *chunkedStreamFromSynthesizeStream) endSpan() {
	if s.spanEnded || s.span == nil {
		return
	}
	s.spanEnded = true
	s.span.End()
}

func audioFrameDurationSeconds(frame *model.AudioFrame) float64 {
	return audio.CalculateFrameDuration(frame)
}

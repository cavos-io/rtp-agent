package tts

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/cavos-io/rtp-agent/core/audio/model"
	"github.com/cavos-io/rtp-agent/library/telemetry"
)

func TestTTSMetadataDefaultsUnknown(t *testing.T) {
	provider := &metadataDefaultsTTS{}

	if got := Model(provider); got != "unknown" {
		t.Fatalf("Model = %q, want unknown", got)
	}
	if got := Provider(provider); got != "unknown" {
		t.Fatalf("Provider = %q, want unknown", got)
	}
}

func TestTTSPrewarmDefaultNoop(t *testing.T) {
	provider := &metadataDefaultsTTS{}

	Prewarm(provider)
}

func TestTTSCloseDefaultNoop(t *testing.T) {
	provider := &metadataDefaultsTTS{}

	if err := Close(provider); err != nil {
		t.Fatalf("Close error = %v", err)
	}
}

func TestTTSCloseDelegatesWhenSupported(t *testing.T) {
	provider := &closableMetadataTTS{}

	if err := Close(provider); err != nil {
		t.Fatalf("Close error = %v", err)
	}
	if !provider.closed {
		t.Fatal("Close did not delegate to provider")
	}
}

func TestTTSMetricsEmitterEmitsToHandlers(t *testing.T) {
	var emitter MetricsEmitter
	metrics := &telemetry.TTSMetrics{Label: "tts", RequestID: "req"}
	received := make(chan *telemetry.TTSMetrics, 1)

	unsubscribe := emitter.OnMetricsCollected(func(got *telemetry.TTSMetrics) {
		received <- got
	})
	defer unsubscribe()

	emitter.EmitMetricsCollected(metrics)

	select {
	case got := <-received:
		if got != metrics {
			t.Fatalf("metrics pointer = %p, want %p", got, metrics)
		}
	default:
		t.Fatal("metrics handler was not called")
	}
}

func TestTTSMetricsEmitterCanUnsubscribe(t *testing.T) {
	var emitter MetricsEmitter
	received := make(chan *telemetry.TTSMetrics, 1)
	unsubscribe := emitter.OnMetricsCollected(func(metrics *telemetry.TTSMetrics) {
		received <- metrics
	})
	unsubscribe()
	unsubscribe()

	emitter.EmitMetricsCollected(&telemetry.TTSMetrics{Label: "tts"})

	select {
	case metrics := <-received:
		t.Fatalf("received metrics after unsubscribe: %#v", metrics)
	default:
	}
}

func TestTTSMetricsEmitterIgnoresNilHandler(t *testing.T) {
	var emitter MetricsEmitter
	unsubscribe := emitter.OnMetricsCollected(nil)
	unsubscribe()

	emitter.EmitMetricsCollected(&telemetry.TTSMetrics{Label: "tts"})
}

func TestTTSErrorEmitterEmitsToHandlers(t *testing.T) {
	var emitter ErrorEmitter
	cause := context.Canceled
	received := make(chan TTSError, 1)

	unsubscribe := emitter.OnError(func(err TTSError) {
		received <- err
	})
	defer unsubscribe()

	emitter.EmitError(TTSError{
		Label:       "tts",
		Err:         cause,
		Recoverable: true,
	})

	select {
	case got := <-received:
		if got.Type != TTSErrorType {
			t.Fatalf("Type = %q, want %q", got.Type, TTSErrorType)
		}
		if got.Label != "tts" {
			t.Fatalf("Label = %q, want tts", got.Label)
		}
		if got.Err != cause {
			t.Fatalf("Err = %v, want %v", got.Err, cause)
		}
		if !got.Recoverable {
			t.Fatal("Recoverable = false, want true")
		}
		if got.Timestamp.IsZero() {
			t.Fatal("Timestamp is zero")
		}
	default:
		t.Fatal("error handler was not called")
	}
}

func TestTTSErrorEmitterCanUnsubscribe(t *testing.T) {
	var emitter ErrorEmitter
	received := make(chan TTSError, 1)
	unsubscribe := emitter.OnError(func(err TTSError) {
		received <- err
	})
	unsubscribe()
	unsubscribe()

	emitter.EmitError(TTSError{Label: "tts", Err: context.Canceled})

	select {
	case err := <-received:
		t.Fatalf("received error after unsubscribe: %#v", err)
	default:
	}
}

func TestTTSErrorEmitterIgnoresNilHandler(t *testing.T) {
	var emitter ErrorEmitter
	unsubscribe := emitter.OnError(nil)
	unsubscribe()

	emitter.EmitError(TTSError{Label: "tts", Err: context.Canceled})
}

func TestCollectCombinesChunkedStreamFrames(t *testing.T) {
	stream := &collectChunkedStream{events: []*SynthesizedAudio{
		{Frame: &model.AudioFrame{
			Data:              []byte{1, 0, 2, 0},
			SampleRate:        24000,
			NumChannels:       1,
			SamplesPerChannel: 2,
		}},
		{Frame: &model.AudioFrame{
			Data:              []byte{3, 0, 4, 0},
			SampleRate:        24000,
			NumChannels:       1,
			SamplesPerChannel: 2,
		}},
	}}

	frame, err := Collect(stream)
	if err != nil {
		t.Fatalf("Collect error = %v", err)
	}
	if frame.SampleRate != 24000 {
		t.Fatalf("SampleRate = %d, want 24000", frame.SampleRate)
	}
	if frame.SamplesPerChannel != 4 {
		t.Fatalf("SamplesPerChannel = %d, want 4", frame.SamplesPerChannel)
	}
	if got := string(frame.Data); got != string([]byte{1, 0, 2, 0, 3, 0, 4, 0}) {
		t.Fatalf("Data = %v, want concatenated PCM data", frame.Data)
	}
}

func TestCollectWithTimedTranscriptPreservesTranscript(t *testing.T) {
	timed := TimedString{Text: "aligned chunk", StartTime: 0.25, EndTime: 0.5}
	stream := &collectChunkedStream{events: []*SynthesizedAudio{
		{
			Frame: &model.AudioFrame{
				Data:              []byte{1, 0},
				SampleRate:        24000,
				NumChannels:       1,
				SamplesPerChannel: 1,
			},
			TimedTranscript: []TimedString{timed},
		},
	}}

	frame, got, err := CollectWithTimedTranscript(stream)
	if err != nil {
		t.Fatalf("CollectWithTimedTranscript error = %v", err)
	}
	if frame == nil {
		t.Fatal("frame = nil, want combined audio")
	}
	if len(got) != 1 || got[0] != timed {
		t.Fatalf("timed transcript = %#v, want %#v", got, []TimedString{timed})
	}
}

func TestCollectReturnsStreamError(t *testing.T) {
	wantErr := errors.New("provider failed")
	stream := &collectChunkedStream{err: wantErr}

	_, err := Collect(stream)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Collect error = %v, want %v", err, wantErr)
	}
}

func TestCollectClosesStreamAfterEOF(t *testing.T) {
	stream := &collectChunkedStream{}

	_, err := Collect(stream)
	if err != nil {
		t.Fatalf("Collect error = %v", err)
	}
	if !stream.closed {
		t.Fatal("Collect did not close stream after EOF")
	}
}

type metadataDefaultsTTS struct{}

func (m *metadataDefaultsTTS) Label() string {
	return "metadata-defaults"
}

func (m *metadataDefaultsTTS) Capabilities() TTSCapabilities {
	return TTSCapabilities{}
}

func (m *metadataDefaultsTTS) SampleRate() int {
	return 24000
}

func (m *metadataDefaultsTTS) NumChannels() int {
	return 1
}

func (m *metadataDefaultsTTS) Synthesize(context.Context, string) (ChunkedStream, error) {
	return nil, nil
}

func (m *metadataDefaultsTTS) Stream(context.Context) (SynthesizeStream, error) {
	return nil, nil
}

type closableMetadataTTS struct {
	metadataDefaultsTTS
	closed bool
}

func (m *closableMetadataTTS) Close() error {
	m.closed = true
	return nil
}

type collectChunkedStream struct {
	events []*SynthesizedAudio
	err    error
	closed bool
}

func (s *collectChunkedStream) Next() (*SynthesizedAudio, error) {
	if s.err != nil {
		return nil, s.err
	}
	if len(s.events) == 0 {
		return nil, io.EOF
	}
	event := s.events[0]
	s.events = s.events[1:]
	return event, nil
}

func (s *collectChunkedStream) Close() error {
	s.closed = true
	return nil
}

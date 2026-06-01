package tts

import (
	"context"
	"testing"

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

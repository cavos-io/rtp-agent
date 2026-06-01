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

package tts

import (
	"context"
	"strings"
	"testing"
)

func TestFallbackAdapterAggregatesProviderMetadata(t *testing.T) {
	adapter := NewFallbackAdapter([]TTS{
		&metadataTTS{label: "low", sampleRate: 16000, numChannels: 1, capabilities: TTSCapabilities{}},
		&metadataTTS{label: "high", sampleRate: 24000, numChannels: 1, capabilities: TTSCapabilities{Streaming: true}},
	})

	if got := adapter.SampleRate(); got != 24000 {
		t.Fatalf("SampleRate = %d, want max provider sample rate", got)
	}
	if got := adapter.NumChannels(); got != 1 {
		t.Fatalf("NumChannels = %d, want provider channel count", got)
	}
	if !adapter.Capabilities().Streaming {
		t.Fatal("Capabilities().Streaming = false, want true when any provider streams")
	}
}

func TestFallbackAdapterRejectsMixedChannelCounts(t *testing.T) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("NewFallbackAdapter did not panic")
		}
		if !strings.Contains(recovered.(string), "same number of channels") {
			t.Fatalf("panic = %q, want channel-count error", recovered)
		}
	}()

	NewFallbackAdapter([]TTS{
		&metadataTTS{label: "mono", sampleRate: 24000, numChannels: 1},
		&metadataTTS{label: "stereo", sampleRate: 24000, numChannels: 2},
	})
}

type metadataTTS struct {
	label        string
	sampleRate   int
	numChannels  int
	capabilities TTSCapabilities
}

func (m *metadataTTS) Label() string {
	return m.label
}

func (m *metadataTTS) Capabilities() TTSCapabilities {
	return m.capabilities
}

func (m *metadataTTS) SampleRate() int {
	return m.sampleRate
}

func (m *metadataTTS) NumChannels() int {
	return m.numChannels
}

func (m *metadataTTS) Synthesize(context.Context, string) (ChunkedStream, error) {
	return nil, nil
}

func (m *metadataTTS) Stream(context.Context) (SynthesizeStream, error) {
	return nil, nil
}

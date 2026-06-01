package tts

import (
	"context"
	"testing"
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

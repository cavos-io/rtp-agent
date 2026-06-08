package upliftai

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestUpliftAIPluginMetadataUsesRTPAgentNamespace(t *testing.T) {
	if PluginTitle != "rtp-agent.plugins.upliftai" {
		t.Fatalf("PluginTitle = %q, want rtp-agent.plugins.upliftai", PluginTitle)
	}
	if PluginVersion != "1.5.15" {
		t.Fatalf("PluginVersion = %q, want reference version 1.5.15", PluginVersion)
	}
	if PluginPackage != "rtp-agent.plugins.upliftai" {
		t.Fatalf("PluginPackage = %q, want rtp-agent.plugins.upliftai", PluginPackage)
	}
}

func TestUpliftAITTSReferenceDefaultsAndCapabilities(t *testing.T) {
	tts := NewUpliftAITTS("secret", "")
	if tts.apiKey != "secret" {
		t.Fatalf("apiKey = %q, want secret", tts.apiKey)
	}
	if got, want := tts.voice, "v_meklc281"; got != want {
		t.Fatalf("voice = %q, want reference default voice %q", got, want)
	}
	if got, want := tts.Label(), "upliftai.TTS"; got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
	if caps := tts.Capabilities(); !caps.Streaming || caps.AlignedTranscript {
		t.Fatalf("Capabilities() = %+v, want reference streaming without aligned transcript", caps)
	}
	if got, want := tts.SampleRate(), 22050; got != want {
		t.Fatalf("SampleRate() = %d, want reference sample rate %d", got, want)
	}
	if got, want := tts.NumChannels(), 1; got != want {
		t.Fatalf("NumChannels() = %d, want %d", got, want)
	}

	if _, err := tts.Stream(context.Background()); err == nil || !strings.Contains(err.Error(), "streaming tts not natively supported") {
		t.Fatalf("Stream() error = %v, want explicit unsupported streaming error", err)
	}
}

func TestUpliftAITTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("UPLIFTAI_API_KEY", "env-secret")

	tts := NewUpliftAITTS("", "")

	if got, want := tts.apiKey, "env-secret"; got != want {
		t.Fatalf("apiKey = %q, want environment key %q", got, want)
	}
}

func TestUpliftAITTSChunkedStreamFramesAudio(t *testing.T) {
	body := io.NopCloser(strings.NewReader("\x01\x02\x03\x04"))
	stream := &upliftAITTSChunkedStream{resp: &http.Response{Body: body}}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() error = %v, want nil", err)
	}
	if got, want := audio.Frame.Data, []byte{1, 2, 3, 4}; string(got) != string(want) {
		t.Fatalf("Frame.Data = %v, want %v", got, want)
	}
	if got, want := audio.Frame.SampleRate, uint32(22050); got != want {
		t.Fatalf("SampleRate = %d, want %d", got, want)
	}
	if got, want := audio.Frame.NumChannels, uint32(1); got != want {
		t.Fatalf("NumChannels = %d, want %d", got, want)
	}
	if got, want := audio.Frame.SamplesPerChannel, uint32(2); got != want {
		t.Fatalf("SamplesPerChannel = %d, want %d", got, want)
	}
	if _, err := stream.Next(); err != io.EOF {
		t.Fatalf("second Next() error = %v, want EOF", err)
	}
}

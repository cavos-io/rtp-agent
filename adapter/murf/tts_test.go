package murf

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestMurfTTSDefaultsMatchReference(t *testing.T) {
	provider := NewMurfTTS("test-key", "")

	if provider.baseURL != "https://global.api.murf.ai" {
		t.Fatalf("base URL = %q, want reference base URL", provider.baseURL)
	}
	if provider.model != "FALCON" {
		t.Fatalf("model = %q, want FALCON", provider.model)
	}
	if provider.voice != "en-US-matthew" {
		t.Fatalf("voice = %q, want reference default voice", provider.voice)
	}
	if provider.style != "Conversation" {
		t.Fatalf("style = %q, want reference default style", provider.style)
	}
	if provider.encoding != "pcm" {
		t.Fatalf("encoding = %q, want pcm", provider.encoding)
	}
	if provider.SampleRate() != 24000 {
		t.Fatalf("sample rate = %d, want 24000", provider.SampleRate())
	}
}

func TestMurfTTSSynthesizeRequestUsesReferencePayload(t *testing.T) {
	provider := NewMurfTTS("test-key", "")

	req, err := buildMurfTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://global.api.murf.ai/v1/speech/stream" {
		t.Fatalf("url = %q, want speech stream endpoint", req.URL.String())
	}
	if got := req.Header.Get("api-key"); got != "test-key" {
		t.Fatalf("api-key = %q, want test key", got)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertMurfPayload(t, payload, "text", "hello")
	assertMurfPayload(t, payload, "model", "FALCON")
	assertMurfPayload(t, payload, "voice_id", "en-US-matthew")
	assertMurfPayload(t, payload, "style", "Conversation")
	assertMurfPayload(t, payload, "format", "pcm")
	if payload["sample_rate"] != float64(24000) {
		t.Fatalf("sample_rate = %#v, want 24000", payload["sample_rate"])
	}
	if payload["multiNativeLocale"] != nil {
		t.Fatalf("multiNativeLocale = %#v, want nil by default", payload["multiNativeLocale"])
	}
}

func TestMurfTTSOptionsMatchReference(t *testing.T) {
	provider := NewMurfTTS("test-key", "",
		WithMurfTTSBaseURL("https://murf.example/"),
		WithMurfTTSModel("GEN2"),
		WithMurfTTSVoice("en-US-natalie"),
		WithMurfTTSLocale("en-US"),
		WithMurfTTSStyle("Promo"),
		WithMurfTTSSpeed(12),
		WithMurfTTSPitch(-4),
		WithMurfTTSSampleRate(44100),
	)

	req, err := buildMurfTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.String() != "https://murf.example/v1/speech/stream" {
		t.Fatalf("url = %q, want custom speech stream endpoint", req.URL.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertMurfPayload(t, payload, "model", "GEN2")
	assertMurfPayload(t, payload, "voice_id", "en-US-natalie")
	assertMurfPayload(t, payload, "multiNativeLocale", "en-US")
	assertMurfPayload(t, payload, "style", "Promo")
	if payload["rate"] != float64(12) {
		t.Fatalf("rate = %#v, want 12", payload["rate"])
	}
	if payload["pitch"] != float64(-4) {
		t.Fatalf("pitch = %#v, want -4", payload["pitch"])
	}
	if payload["sample_rate"] != float64(44100) {
		t.Fatalf("sample_rate = %#v, want 44100", payload["sample_rate"])
	}
}

func TestMurfTTSChunkedStreamUsesConfiguredSampleRate(t *testing.T) {
	stream := &murfTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader([]byte{0x01, 0x02}))},
		sampleRate: 44100,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if audio.Frame.SampleRate != 44100 {
		t.Fatalf("sample rate = %d, want configured sample rate", audio.Frame.SampleRate)
	}
}

func assertMurfPayload(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}

package spitch

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	corestt "github.com/cavos-io/rtp-agent/core/stt"
	coretts "github.com/cavos-io/rtp-agent/core/tts"
)

func TestSpitchTTSDefaultsMatchReference(t *testing.T) {
	provider := NewSpitchTTS("test-key", "")

	if provider.voice != "lina" {
		t.Fatalf("voice = %q, want reference default voice", provider.voice)
	}
	if provider.language != "en" {
		t.Fatalf("language = %q, want en", provider.language)
	}
	if provider.outputFormat != "mp3" {
		t.Fatalf("format = %q, want mp3", provider.outputFormat)
	}
	if provider.SampleRate() != 24000 {
		t.Fatalf("sample rate = %d, want 24000", provider.SampleRate())
	}
	if got := coretts.Model(provider); got != "unknown" {
		t.Fatalf("model metadata = %q, want unknown", got)
	}
	if got := coretts.Provider(provider); got != "Spitch" {
		t.Fatalf("provider metadata = %q, want Spitch", got)
	}
}

func TestNewSpitchSTTUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("SPITCH_API_KEY", "env-key")

	provider := NewSpitchSTT("")

	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}
	if got := corestt.Model(provider); got != "unknown" {
		t.Fatalf("model metadata = %q, want unknown", got)
	}
	if got := corestt.Provider(provider); got != "Spitch" {
		t.Fatalf("provider metadata = %q, want Spitch", got)
	}

	explicit := NewSpitchSTT("explicit-key")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestNewSpitchTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("SPITCH_API_KEY", "env-key")

	provider := NewSpitchTTS("", "")

	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}

	explicit := NewSpitchTTS("explicit-key", "")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestSpitchTTSSynthesizeRequestUsesReferencePayload(t *testing.T) {
	provider := NewSpitchTTS("test-key", "")

	req, err := buildSpitchTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://api.spitch.ai/tts/v1/synthesize" {
		t.Fatalf("url = %q, want synthesize endpoint", req.URL.String())
	}
	if got := req.Header.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("authorization = %q, want bearer token", got)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertSpitchPayload(t, payload, "text", "hello")
	assertSpitchPayload(t, payload, "voice", "lina")
	assertSpitchPayload(t, payload, "language", "en")
	assertSpitchPayload(t, payload, "format", "mp3")
}

func TestSpitchTTSOptionsMatchReference(t *testing.T) {
	provider := NewSpitchTTS("test-key", "",
		WithSpitchTTSBaseURL("https://spitch.example/"),
		WithSpitchTTSVoice("amina"),
		WithSpitchTTSLanguage("fr"),
		WithSpitchTTSOutputFormat("wav"),
	)

	req, err := buildSpitchTTSRequest(context.Background(), provider, "bonjour")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.String() != "https://spitch.example/tts/v1/synthesize" {
		t.Fatalf("url = %q, want custom synthesize endpoint", req.URL.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertSpitchPayload(t, payload, "voice", "amina")
	assertSpitchPayload(t, payload, "language", "fr")
	assertSpitchPayload(t, payload, "format", "wav")
}

func TestSpitchTTSChunkedStreamUsesConfiguredSampleRate(t *testing.T) {
	stream := &spitchTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader([]byte{0x01, 0x02}))},
		sampleRate: 16000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if audio.Frame.SampleRate != 16000 {
		t.Fatalf("sample rate = %d, want configured sample rate", audio.Frame.SampleRate)
	}
}

func assertSpitchPayload(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}

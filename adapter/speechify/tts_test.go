package speechify

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestSpeechifyTTSDefaultsMatchReference(t *testing.T) {
	provider := NewSpeechifyTTS("test-key", "")

	if provider.baseURL != "https://api.sws.speechify.com/v1" {
		t.Fatalf("base URL = %q, want reference API base", provider.baseURL)
	}
	if provider.voice != "jack" {
		t.Fatalf("voice = %q, want jack", provider.voice)
	}
	if provider.encoding != "ogg_24000" {
		t.Fatalf("encoding = %q, want ogg_24000", provider.encoding)
	}
	if provider.sampleRate != 24000 {
		t.Fatalf("sample rate = %d, want 24000", provider.sampleRate)
	}
}

func TestNewSpeechifyTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("SPEECHIFY_API_KEY", "env-key")

	provider := NewSpeechifyTTS("", "")

	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}

	explicit := NewSpeechifyTTS("explicit-key", "")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestSpeechifyTTSRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("SPEECHIFY_API_KEY", "")
	provider := NewSpeechifyTTS("", "", WithSpeechifyTTSBaseURL("://bad-url"))

	_, err := provider.Synthesize(context.Background(), "hello")

	if err == nil || !strings.Contains(err.Error(), "SPEECHIFY_API_KEY") {
		t.Fatalf("Synthesize error = %v, want missing API key error", err)
	}
}

func TestSpeechifyTTSSynthesizeRequestUsesReferencePayload(t *testing.T) {
	provider := NewSpeechifyTTS("test-key", "")

	req, err := buildSpeechifyTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://api.sws.speechify.com/v1/audio/stream" {
		t.Fatalf("url = %q, want audio stream endpoint", req.URL.String())
	}
	if got := req.Header.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("authorization = %q, want bearer token", got)
	}
	if got := req.Header.Get("Accept"); got != "audio/ogg" {
		t.Fatalf("accept = %q, want audio/ogg", got)
	}
	if got := req.Header.Get("x-caller"); got != "livekit" {
		t.Fatalf("x-caller = %q, want livekit", got)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertSpeechifyPayload(t, payload, "input", "hello")
	assertSpeechifyPayload(t, payload, "voice_id", "jack")
	assertSpeechifyPayload(t, payload, "audio_format", "ogg")
	options, ok := payload["options"].(map[string]any)
	if !ok {
		t.Fatalf("options = %#v, want object", payload["options"])
	}
	if options["loudness_normalization"] != nil {
		t.Fatalf("loudness_normalization = %#v, want nil", options["loudness_normalization"])
	}
	if options["text_normalization"] != nil {
		t.Fatalf("text_normalization = %#v, want nil", options["text_normalization"])
	}
}

func TestSpeechifyTTSAuthorizationHeaderPreservesBearerToken(t *testing.T) {
	provider := NewSpeechifyTTS("Bearer existing-token", "")

	req, err := buildSpeechifyTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer existing-token" {
		t.Fatalf("authorization = %q, want existing bearer token without duplicate prefix", got)
	}
}

func TestSpeechifyTTSOptionsMatchReference(t *testing.T) {
	provider := NewSpeechifyTTS("test-key", "",
		WithSpeechifyTTSBaseURL("https://speechify.example/v1"),
		WithSpeechifyTTSVoice("cliff"),
		WithSpeechifyTTSEncoding("mp3_24000"),
		WithSpeechifyTTSLanguage("en-US"),
		WithSpeechifyTTSModel("simba-english"),
		WithSpeechifyTTSLoudnessNormalization(true),
		WithSpeechifyTTSTextNormalization(false),
	)

	req, err := buildSpeechifyTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.String() != "https://speechify.example/v1/audio/stream" {
		t.Fatalf("url = %q, want custom base URL audio stream endpoint", req.URL.String())
	}
	if got := req.Header.Get("Accept"); got != "audio/mpeg" {
		t.Fatalf("accept = %q, want audio/mpeg", got)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertSpeechifyPayload(t, payload, "voice_id", "cliff")
	assertSpeechifyPayload(t, payload, "language", "en-US")
	assertSpeechifyPayload(t, payload, "model", "simba-english")
	assertSpeechifyPayload(t, payload, "audio_format", "mp3")
	options := payload["options"].(map[string]any)
	if options["loudness_normalization"] != true {
		t.Fatalf("loudness_normalization = %#v, want true", options["loudness_normalization"])
	}
	if options["text_normalization"] != false {
		t.Fatalf("text_normalization = %#v, want false", options["text_normalization"])
	}
}

func TestSpeechifyTTSChunkedStreamUsesConfiguredSampleRate(t *testing.T) {
	stream := &speechifyTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader([]byte{0x01, 0x02}))},
		sampleRate: 48000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if audio.Frame.SampleRate != 48000 {
		t.Fatalf("sample rate = %d, want 48000", audio.Frame.SampleRate)
	}
}

func assertSpeechifyPayload(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}

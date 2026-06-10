package lmnt

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	coretts "github.com/cavos-io/rtp-agent/core/tts"
)

func TestLMNTTTSDefaultsMatchReference(t *testing.T) {
	provider := NewLMNTTTS("test-key", "")

	if provider.voice != "leah" {
		t.Fatalf("voice = %q, want leah", provider.voice)
	}
	if provider.model != "blizzard" {
		t.Fatalf("model = %q, want blizzard", provider.model)
	}
	if provider.language != "auto" {
		t.Fatalf("language = %q, want auto for blizzard", provider.language)
	}
	if provider.format != "mp3" {
		t.Fatalf("format = %q, want mp3", provider.format)
	}
	if provider.sampleRate != 24000 {
		t.Fatalf("sample rate = %d, want 24000", provider.sampleRate)
	}
	if provider.temperature != 1.0 {
		t.Fatalf("temperature = %v, want 1.0", provider.temperature)
	}
	if provider.topP != 0.8 {
		t.Fatalf("topP = %v, want 0.8", provider.topP)
	}
	if got := coretts.Model(provider); got != "blizzard" {
		t.Fatalf("model metadata = %q, want blizzard", got)
	}
	if got := coretts.Provider(provider); got != "LMNT" {
		t.Fatalf("provider metadata = %q, want LMNT", got)
	}
}

func TestNewLMNTTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("LMNT_API_KEY", "env-key")

	provider := NewLMNTTTS("", "")
	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want env key", provider.apiKey)
	}
	req, err := buildLMNTTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if got := req.Header.Get("X-API-Key"); got != "env-key" {
		t.Fatalf("X-API-Key = %q, want env key", got)
	}

	explicit := NewLMNTTTS("explicit-key", "")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestLMNTTTSRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("LMNT_API_KEY", "")
	provider := NewLMNTTTS("", "")

	_, err := provider.Synthesize(context.Background(), "hello")

	if err == nil || !strings.Contains(err.Error(), "LMNT_API_KEY") {
		t.Fatalf("Synthesize error = %v, want missing API key error", err)
	}
}

func TestLMNTTTSSynthesizeRequestUsesReferencePayload(t *testing.T) {
	provider := NewLMNTTTS("test-key", "")

	req, err := buildLMNTTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://api.lmnt.com/v1/ai/speech/bytes" {
		t.Fatalf("url = %q, want bytes endpoint", req.URL.String())
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q, want application/json", got)
	}
	if got := req.Header.Get("X-API-Key"); got != "test-key" {
		t.Fatalf("X-API-Key = %q, want test-key", got)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertLMNTPayload(t, payload, "text", "hello")
	assertLMNTPayload(t, payload, "voice", "leah")
	assertLMNTPayload(t, payload, "language", "auto")
	assertLMNTPayload(t, payload, "model", "blizzard")
	assertLMNTPayload(t, payload, "format", "mp3")
	if got := payload["sample_rate"]; got != float64(24000) {
		t.Fatalf("sample_rate = %#v, want 24000", got)
	}
	if got := payload["temperature"]; got != 1.0 {
		t.Fatalf("temperature = %#v, want 1.0", got)
	}
	if got := payload["top_p"]; got != 0.8 {
		t.Fatalf("top_p = %#v, want 0.8", got)
	}
}

func TestLMNTTTSOptionsMatchReference(t *testing.T) {
	provider := NewLMNTTTS("test-key", "",
		WithLMNTTTSModel("aurora"),
		WithLMNTTTSVoice("ava"),
		WithLMNTTTSLanguage("en"),
		WithLMNTTTSFormat("wav"),
		WithLMNTTTSSampleRate(16000),
		WithLMNTTTSTemperature(0.4),
		WithLMNTTTSTopP(0.6),
	)

	req, err := buildLMNTTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertLMNTPayload(t, payload, "voice", "ava")
	assertLMNTPayload(t, payload, "language", "en")
	assertLMNTPayload(t, payload, "model", "aurora")
	assertLMNTPayload(t, payload, "format", "wav")
	if got := payload["sample_rate"]; got != float64(16000) {
		t.Fatalf("sample_rate = %#v, want 16000", got)
	}
	if got := payload["temperature"]; got != 0.4 {
		t.Fatalf("temperature = %#v, want 0.4", got)
	}
	if got := payload["top_p"]; got != 0.6 {
		t.Fatalf("top_p = %#v, want 0.6", got)
	}
}

func TestLMNTTTSDefaultsLanguageToEnglishForNonBlizzard(t *testing.T) {
	provider := NewLMNTTTS("test-key", "", WithLMNTTTSModel("aurora"))

	if provider.language != "en" {
		t.Fatalf("language = %q, want en for non-blizzard model", provider.language)
	}
}

func TestLMNTTTSChunkedStreamUsesConfiguredSampleRate(t *testing.T) {
	stream := &lmntTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader([]byte{0x01, 0x02}))},
		sampleRate: 16000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if audio.Frame.SampleRate != 16000 {
		t.Fatalf("sample rate = %d, want 16000", audio.Frame.SampleRate)
	}
}

func assertLMNTPayload(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}

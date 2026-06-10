package deepgram

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/tts"
)

func TestDeepgramTTSDefaultsMatchReference(t *testing.T) {
	provider := NewDeepgramTTS("test-key", "")

	if provider.model != "aura-2-andromeda-en" {
		t.Fatalf("model = %q, want aura-2-andromeda-en", provider.model)
	}
	if provider.sampleRate != 24000 {
		t.Fatalf("sample rate = %d, want 24000", provider.sampleRate)
	}
	if provider.encoding != "linear16" {
		t.Fatalf("encoding = %q, want linear16", provider.encoding)
	}
	if got := tts.Model(provider); got != "aura-2-andromeda-en" {
		t.Fatalf("model metadata = %q, want aura-2-andromeda-en", got)
	}
	if got := tts.Provider(provider); got != "Deepgram" {
		t.Fatalf("provider metadata = %q, want Deepgram", got)
	}
}

func TestDeepgramTTSConstructorOptionsMatchReference(t *testing.T) {
	t.Setenv("DEEPGRAM_API_KEY", "env-key")

	provider := NewDeepgramTTS("", "aura-custom",
		WithDeepgramTTSAudioFormat("mulaw", 8000),
	)
	if provider.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env key", provider.apiKey)
	}
	if provider.model != "aura-custom" {
		t.Fatalf("model = %q, want aura-custom", provider.model)
	}
	if provider.encoding != "mulaw" || provider.sampleRate != 8000 {
		t.Fatalf("audio format = %s/%d, want mulaw/8000", provider.encoding, provider.sampleRate)
	}

	requestURL, _ := buildDeepgramTTSSynthesizeRequest(provider, "hello")
	parsed, err := url.Parse(requestURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	query := parsed.Query()
	assertDeepgramTTSQuery(t, query, "model", "aura-custom")
	assertDeepgramTTSQuery(t, query, "encoding", "mulaw")
	assertDeepgramTTSQuery(t, query, "sample_rate", "8000")

	provider = NewDeepgramTTS("explicit-key", "")
	if provider.apiKey != "explicit-key" {
		t.Fatalf("apiKey = %q, want explicit key", provider.apiKey)
	}
}

func TestDeepgramTTSRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("DEEPGRAM_API_KEY", "")
	provider := NewDeepgramTTS("", "", WithDeepgramTTSBaseURL("://bad-url"))

	_, synthErr := provider.Synthesize(context.Background(), "hello")
	if synthErr == nil || !strings.Contains(synthErr.Error(), "DEEPGRAM_API_KEY") {
		t.Fatalf("Synthesize error = %v, want missing API key error", synthErr)
	}

	_, streamErr := provider.Stream(context.Background())
	if streamErr == nil || !strings.Contains(streamErr.Error(), "DEEPGRAM_API_KEY") {
		t.Fatalf("Stream error = %v, want missing API key error", streamErr)
	}
}

func TestDeepgramTTSSynthesizeRequestUsesReferenceOptions(t *testing.T) {
	provider := NewDeepgramTTS("test-key", "",
		WithDeepgramTTSMipOptOut(true),
	)

	requestURL, body := buildDeepgramTTSSynthesizeRequest(provider, "hello")
	parsed, err := url.Parse(requestURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	query := parsed.Query()
	assertDeepgramTTSQuery(t, query, "model", "aura-2-andromeda-en")
	assertDeepgramTTSQuery(t, query, "encoding", "linear16")
	assertDeepgramTTSQuery(t, query, "sample_rate", "24000")
	assertDeepgramTTSQuery(t, query, "container", "none")
	assertDeepgramTTSQuery(t, query, "mip_opt_out", "true")

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if payload["text"] != "hello" {
		t.Fatalf("text = %#v, want hello", payload["text"])
	}
}

func TestDeepgramTTSSynthesizeRequestUsesConfiguredBaseURL(t *testing.T) {
	provider := NewDeepgramTTS("test-key", "",
		WithDeepgramTTSBaseURL("https://deepgram.example/v1/speak"),
	)

	requestURL, _ := buildDeepgramTTSSynthesizeRequest(provider, "hello")
	parsed, err := url.Parse(requestURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if parsed.Scheme != "https" || parsed.Host != "deepgram.example" || parsed.Path != "/v1/speak" {
		t.Fatalf("url = %q, want configured HTTP base URL", requestURL)
	}
}

func TestDeepgramTTSStreamURLUsesReferenceOptions(t *testing.T) {
	provider := NewDeepgramTTS("test-key", "")

	streamURL := buildDeepgramTTSStreamURL(provider)
	parsed, err := url.Parse(streamURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if parsed.Scheme != "wss" {
		t.Fatalf("scheme = %q, want wss", parsed.Scheme)
	}
	query := parsed.Query()
	assertDeepgramTTSQuery(t, query, "model", "aura-2-andromeda-en")
	assertDeepgramTTSQuery(t, query, "encoding", "linear16")
	assertDeepgramTTSQuery(t, query, "sample_rate", "24000")
	assertDeepgramTTSQuery(t, query, "mip_opt_out", "false")
}

func TestDeepgramTTSStreamURLUsesConfiguredBaseURL(t *testing.T) {
	provider := NewDeepgramTTS("test-key", "",
		WithDeepgramTTSBaseURL("https://deepgram.example/v1/speak"),
	)

	streamURL := buildDeepgramTTSStreamURL(provider)
	parsed, err := url.Parse(streamURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if parsed.Scheme != "wss" || parsed.Host != "deepgram.example" || parsed.Path != "/v1/speak" {
		t.Fatalf("url = %q, want configured websocket base URL", streamURL)
	}
}

func assertDeepgramTTSQuery(t *testing.T, query url.Values, key string, want string) {
	t.Helper()
	if got := query.Get(key); got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}

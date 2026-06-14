package simplismart

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	coretts "github.com/cavos-io/rtp-agent/core/tts"
)

func TestNewSimplismartTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("SIMPLISMART_API_KEY", "env-key")

	provider := NewSimplismartTTS("", "")
	if provider.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env key", provider.apiKey)
	}

	provider = NewSimplismartTTS("explicit-key", "")
	if provider.apiKey != "explicit-key" {
		t.Fatalf("apiKey = %q, want explicit key", provider.apiKey)
	}
}

func TestSimplismartTTSRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("SIMPLISMART_API_KEY", "")
	provider := NewSimplismartTTS("", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := provider.Synthesize(ctx, "hello")
	if err == nil {
		t.Fatal("Synthesize returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "SIMPLISMART_API_KEY") {
		t.Fatalf("Synthesize error = %q, want SIMPLISMART_API_KEY guidance", err)
	}
}

func TestSimplismartTTSDefaultsAndRequestMatchReference(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: simplismartRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://api.simplismart.live/tts" {
			t.Fatalf("url = %q, want reference Orpheus endpoint", r.URL.String())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization = %q, want bearer token", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("content type = %q, want application/json", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		assertSimplismartTTSPayload(t, payload, "prompt", "hello")
		assertSimplismartTTSPayload(t, payload, "voice", "tara")
		assertSimplismartTTSPayload(t, payload, "model", "canopylabs/orpheus-3b-0.1-ft")
		if got := payload["temperature"]; got != 0.7 {
			t.Fatalf("temperature = %#v, want 0.7", got)
		}
		if got := payload["top_p"]; got != 0.9 {
			t.Fatalf("top_p = %#v, want 0.9", got)
		}
		if got := payload["repetition_penalty"]; got != 1.5 {
			t.Fatalf("repetition_penalty = %#v, want 1.5", got)
		}
		if got := payload["max_tokens"]; got != float64(1000) {
			t.Fatalf("max_tokens = %#v, want 1000", got)
		}
		if _, ok := payload["text"]; ok {
			t.Fatalf("text = %#v, want omitted for Orpheus reference payload", payload["text"])
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("pcm")),
			Request:    r,
		}, nil
	})}

	provider := NewSimplismartTTS("test-key", "")
	if got := coretts.Model(provider); got != "canopylabs/orpheus-3b-0.1-ft" {
		t.Fatalf("model metadata = %q, want reference model", got)
	}
	if got := coretts.Provider(provider); got != "SimpliSmart" {
		t.Fatalf("provider metadata = %q, want SimpliSmart", got)
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize returned error: %v", err)
	}
	defer stream.Close()
}

func TestSimplismartTTSOptionsMatchReference(t *testing.T) {
	provider := NewSimplismartTTS("test-key", "leo",
		WithSimplismartTTSBaseURL("https://simplismart.example/tts"),
		WithSimplismartTTSModel("canopylabs/orpheus-3b-test"),
		WithSimplismartTTSSampleRate(16000),
		WithSimplismartTTSTemperature(0.4),
		WithSimplismartTTSTopP(0.6),
		WithSimplismartTTSRepetitionPenalty(1.2),
		WithSimplismartTTSMaxTokens(256),
	)

	req, err := buildSimplismartTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.String() != "https://simplismart.example/tts" {
		t.Fatalf("url = %q, want custom Simplismart endpoint", req.URL.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	assertSimplismartTTSPayload(t, payload, "voice", "leo")
	assertSimplismartTTSPayload(t, payload, "model", "canopylabs/orpheus-3b-test")
	if got := payload["temperature"]; got != 0.4 {
		t.Fatalf("temperature = %#v, want 0.4", got)
	}
	if got := payload["top_p"]; got != 0.6 {
		t.Fatalf("top_p = %#v, want 0.6", got)
	}
	if got := payload["repetition_penalty"]; got != 1.2 {
		t.Fatalf("repetition_penalty = %#v, want 1.2", got)
	}
	if got := payload["max_tokens"]; got != float64(256) {
		t.Fatalf("max_tokens = %#v, want 256", got)
	}
	if got := provider.SampleRate(); got != 16000 {
		t.Fatalf("sample rate = %d, want configured sample rate", got)
	}
}

func assertSimplismartTTSPayload(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}

type simplismartRoundTripFunc func(*http.Request) (*http.Response, error)

func (f simplismartRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

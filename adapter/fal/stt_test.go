package fal

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/llm"
	"github.com/cavos-io/rtp-agent/core/stt"
)

func TestFalSTTDefaultsMatchReference(t *testing.T) {
	provider := NewFalSTT("test-key")

	if provider.language != "en" {
		t.Fatalf("language = %q, want en", provider.language)
	}
	if provider.task != "transcribe" {
		t.Fatalf("task = %q, want transcribe", provider.task)
	}
	if provider.chunkLevel != "segment" {
		t.Fatalf("chunk level = %q, want segment", provider.chunkLevel)
	}
	if provider.version != "3" {
		t.Fatalf("version = %q, want 3", provider.version)
	}
	if got := stt.Model(provider); got != "Wizper" {
		t.Fatalf("model metadata = %q, want Wizper", got)
	}
	if got := stt.Provider(provider); got != "Fal" {
		t.Fatalf("provider metadata = %q, want Fal", got)
	}
	if !provider.Capabilities().InterimResults {
		t.Fatalf("interim results = false, want true to match reference capabilities")
	}
}

func TestNewFalSTTUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("FAL_KEY", "env-key")
	t.Setenv("FAL_API_KEY", "fallback-env-key")

	provider := NewFalSTT("")

	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want primary env key", provider.apiKey)
	}

	explicit := NewFalSTT("explicit-key")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestNewFalSTTUsesFallbackEnvironmentAPIKey(t *testing.T) {
	t.Setenv("FAL_KEY", "")
	t.Setenv("FAL_API_KEY", "fallback-env-key")

	provider := NewFalSTT("")

	if provider.apiKey != "fallback-env-key" {
		t.Fatalf("api key = %q, want fallback env key", provider.apiKey)
	}
}

func TestFalSTTRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("FAL_KEY", "")
	t.Setenv("FAL_API_KEY", "")
	provider := NewFalSTT("")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := provider.Recognize(ctx, nil, "")
	if err == nil {
		t.Fatal("Recognize returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "FAL_KEY") {
		t.Fatalf("Recognize error = %q, want FAL_KEY guidance", err)
	}
}

func TestFalSTTRecognizeRequestUsesReferencePayload(t *testing.T) {
	provider := NewFalSTT("test-key")

	req, err := buildFalSTTRequest(context.Background(), provider, []byte{0x01, 0x02}, "")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.URL.String() != "https://fal.run/fal-ai/wizper" {
		t.Fatalf("url = %q, want wizper endpoint", req.URL.String())
	}
	if got := req.Header.Get("Authorization"); got != "Key test-key" {
		t.Fatalf("authorization = %q, want key header", got)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if payload["audio_url"] != "data:audio/x-wav;base64,AQI=" {
		t.Fatalf("audio_url = %#v, want wav data URI", payload["audio_url"])
	}
	assertFalSTTPayload(t, payload, "task", "transcribe")
	assertFalSTTPayload(t, payload, "language", "en")
	assertFalSTTPayload(t, payload, "chunk_level", "segment")
	assertFalSTTPayload(t, payload, "version", "3")
}

func TestFalSTTOptionsAndRecognizeLanguageOverrideMatchReference(t *testing.T) {
	provider := NewFalSTT("test-key",
		WithFalSTTLanguage("fr"),
		WithFalSTTTask("translate"),
		WithFalSTTChunkLevel("word"),
		WithFalSTTVersion("2"),
	)

	req, err := buildFalSTTRequest(context.Background(), provider, []byte{0x01}, "de")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertFalSTTPayload(t, payload, "language", "de")
	assertFalSTTPayload(t, payload, "task", "translate")
	assertFalSTTPayload(t, payload, "chunk_level", "word")
	assertFalSTTPayload(t, payload, "version", "2")
}

func TestFalSTTRecognizeLanguageOverridePersistsLikeReference(t *testing.T) {
	provider := NewFalSTT("test-key", WithFalSTTLanguage("fr"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _ = provider.Recognize(ctx, nil, "de")

	if provider.language != "de" {
		t.Fatalf("provider language = %q, want recognize override to persist like reference", provider.language)
	}
}

func TestFalSTTUpdateOptionsMatchesReferenceLanguageOnly(t *testing.T) {
	provider := NewFalSTT("test-key")

	provider.UpdateOptions(
		WithFalSTTLanguage("fr"),
		WithFalSTTTask("translate"),
		WithFalSTTChunkLevel("word"),
		WithFalSTTVersion("2"),
	)

	req, err := buildFalSTTRequest(context.Background(), provider, []byte{0x01}, "")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertFalSTTPayload(t, payload, "language", "fr")
	assertFalSTTPayload(t, payload, "task", "transcribe")
	assertFalSTTPayload(t, payload, "chunk_level", "segment")
	assertFalSTTPayload(t, payload, "version", "3")
}

func TestFalSTTResponsePreservesReferenceLanguage(t *testing.T) {
	event := falSTTResponseToEvent(falSTTResponse{Text: "bonjour"}, "fr")

	if event.Type != stt.SpeechEventFinalTranscript || len(event.Alternatives) != 1 {
		t.Fatalf("event = %#v, want one final transcript", event)
	}
	got := event.Alternatives[0]
	if got.Text != "bonjour" || got.Language != "fr" {
		t.Fatalf("alternative = %+v, want reference text and language", got)
	}
}

func TestFalSTTRecognizeReturnsAPIConnectionErrorOnTransportFailure(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: falRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial failed")
	})}

	provider := NewFalSTT("test-key")
	event, err := provider.Recognize(context.Background(), nil, "")
	if err == nil {
		t.Fatalf("Recognize returned event %+v, want APIConnectionError", event)
	}
	var connErr *llm.APIConnectionError
	if !errors.As(err, &connErr) {
		t.Fatalf("Recognize error = %T %v, want APIConnectionError", err, err)
	}
}

func TestFalSTTRecognizeReturnsAPIConnectionErrorOnProviderStatus(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: falRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadGateway,
			Body:       io.NopCloser(strings.NewReader(`{"error":"bad gateway"}`)),
			Request:    r,
		}, nil
	})}

	provider := NewFalSTT("test-key")
	event, err := provider.Recognize(context.Background(), nil, "")
	if err == nil {
		t.Fatalf("Recognize returned event %+v, want APIConnectionError", event)
	}
	var connErr *llm.APIConnectionError
	if !errors.As(err, &connErr) {
		t.Fatalf("Recognize error = %T %v, want APIConnectionError", err, err)
	}
	if !strings.Contains(err.Error(), "bad gateway") {
		t.Fatalf("Recognize error = %q, want provider body", err)
	}
}

func TestFalSTTRecognizeCallerCancelReturnsContextCanceled(t *testing.T) {
	originalClient := http.DefaultClient
	t.Cleanup(func() { http.DefaultClient = originalClient })
	http.DefaultClient = &http.Client{Transport: falRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, context.Canceled
	})}

	provider := NewFalSTT("test-key")
	event, err := provider.Recognize(context.Background(), nil, "")
	if err == nil {
		t.Fatalf("Recognize returned event %+v, want context.Canceled", event)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Recognize error = %v, want context.Canceled", err)
	}
}

func assertFalSTTPayload(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}

type falRoundTripFunc func(*http.Request) (*http.Response, error)

func (f falRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

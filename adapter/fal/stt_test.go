package fal

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
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

func assertFalSTTPayload(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}

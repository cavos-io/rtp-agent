package groq

import (
	"context"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/stt"
)

func TestNewGroqSTTDefaultsMatchReference(t *testing.T) {
	t.Setenv("GROQ_API_KEY", "env-key")

	provider, err := NewGroqSTT("", "")
	if err != nil {
		t.Fatalf("NewGroqSTT error = %v", err)
	}

	if provider.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env key", provider.apiKey)
	}
	if provider.model != "whisper-large-v3-turbo" {
		t.Fatalf("model = %q, want whisper-large-v3-turbo", provider.model)
	}
	if provider.baseURL != "https://api.groq.com/openai/v1" {
		t.Fatalf("baseURL = %q, want Groq OpenAI-compatible endpoint", provider.baseURL)
	}
	if provider.language != "en" {
		t.Fatalf("language = %q, want en", provider.language)
	}
	if provider.detectLanguage {
		t.Fatalf("detectLanguage = true, want false")
	}
	if provider.Label() != "groq.STT" {
		t.Fatalf("label = %q, want groq.STT", provider.Label())
	}
	if stt.Provider(provider) != "groq" {
		t.Fatalf("provider = %q, want groq", stt.Provider(provider))
	}
	if stt.Model(provider) != "whisper-large-v3-turbo" {
		t.Fatalf("stt model = %q, want whisper-large-v3-turbo", stt.Model(provider))
	}
	caps := provider.Capabilities()
	if !caps.OfflineRecognize || caps.Streaming || caps.InterimResults {
		t.Fatalf("capabilities = %+v, want offline-only STT", caps)
	}
}

func TestNewGroqSTTRequiresAPIKey(t *testing.T) {
	t.Setenv("GROQ_API_KEY", "")

	_, err := NewGroqSTT("", "")
	if err == nil {
		t.Fatal("NewGroqSTT error = nil, want missing API key error")
	}
	if !strings.Contains(err.Error(), "GROQ_API_KEY") {
		t.Fatalf("NewGroqSTT error = %q, want GROQ_API_KEY guidance", err)
	}
}

func TestGroqSTTOptionsMatchReference(t *testing.T) {
	provider, err := NewGroqSTT("test-key", "whisper-large-v3",
		WithGroqSTTBaseURL("https://groq.example/openai/v1/"),
		WithGroqSTTLanguage("id"),
		WithGroqSTTPrompt("domain words"),
		WithGroqSTTDetectLanguage(true),
	)
	if err != nil {
		t.Fatalf("NewGroqSTT error = %v", err)
	}

	if provider.apiKey != "test-key" {
		t.Fatalf("apiKey = %q, want explicit key", provider.apiKey)
	}
	if provider.model != "whisper-large-v3" {
		t.Fatalf("model = %q, want whisper-large-v3", provider.model)
	}
	if provider.baseURL != "https://groq.example/openai/v1" {
		t.Fatalf("baseURL = %q, want trimmed custom endpoint", provider.baseURL)
	}
	if provider.language != "" {
		t.Fatalf("language = %q, want empty when detect language is enabled", provider.language)
	}
	if !provider.detectLanguage {
		t.Fatalf("detectLanguage = false, want true")
	}
	if provider.prompt != "domain words" {
		t.Fatalf("prompt = %q, want domain words", provider.prompt)
	}
}

func TestGroqSTTStreamUnsupportedLikeReferenceOfflineMode(t *testing.T) {
	provider, err := NewGroqSTT("test-key", "")
	if err != nil {
		t.Fatalf("NewGroqSTT error = %v", err)
	}

	_, err = provider.Stream(context.Background(), "en")
	if err == nil {
		t.Fatal("Stream error = nil, want unsupported realtime error")
	}
	if !strings.Contains(err.Error(), "realtime stt is not enabled") {
		t.Fatalf("Stream error = %q, want offline-mode guidance", err)
	}
}

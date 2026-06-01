package cartesia

import (
	"encoding/json"
	"net/url"
	"testing"
)

func TestCartesiaTTSDefaultsMatchReference(t *testing.T) {
	provider := NewCartesiaTTS("test-key", "", "")

	if provider.voiceID != "f786b574-daa5-4673-aa0c-cbe3e8534c02" {
		t.Fatalf("voiceID = %q, want reference default", provider.voiceID)
	}
	if provider.model != "sonic-3" {
		t.Fatalf("model = %q, want sonic-3", provider.model)
	}
	if provider.language != "en" {
		t.Fatalf("language = %q, want en", provider.language)
	}
	if provider.apiVersion != "2025-04-16" {
		t.Fatalf("api version = %q, want 2025-04-16", provider.apiVersion)
	}
}

func TestCartesiaSynthesizeRequestUsesReferenceOptions(t *testing.T) {
	provider := NewCartesiaTTS("test-key", "", "",
		WithCartesiaSpeed(1.2),
		WithCartesiaEmotion("Happy"),
		WithCartesiaVolume(1.1),
		WithCartesiaPronunciationDictID("dict-1"),
	)

	requestURL, body, err := buildCartesiaSynthesizeRequest(provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	parsed, err := url.Parse(requestURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if parsed.Path != "/tts/bytes" {
		t.Fatalf("path = %q, want /tts/bytes", parsed.Path)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if payload["model_id"] != "sonic-3" {
		t.Fatalf("model_id = %#v, want sonic-3", payload["model_id"])
	}
	if payload["transcript"] != "hello" {
		t.Fatalf("transcript = %#v, want hello", payload["transcript"])
	}
	if payload["language"] != "en" {
		t.Fatalf("language = %#v, want en", payload["language"])
	}
	if payload["pronunciation_dict_id"] != "dict-1" {
		t.Fatalf("pronunciation_dict_id = %#v, want dict-1", payload["pronunciation_dict_id"])
	}

	generationConfig, ok := payload["generation_config"].(map[string]any)
	if !ok {
		t.Fatalf("generation_config = %#v, want map", payload["generation_config"])
	}
	if generationConfig["speed"] != 1.2 {
		t.Fatalf("speed = %#v, want 1.2", generationConfig["speed"])
	}
	if generationConfig["emotion"] != "Happy" {
		t.Fatalf("emotion = %#v, want Happy", generationConfig["emotion"])
	}
	if generationConfig["volume"] != 1.1 {
		t.Fatalf("volume = %#v, want 1.1", generationConfig["volume"])
	}
}

func TestCartesiaSynthesizeRequestUsesConfiguredBaseURL(t *testing.T) {
	provider := NewCartesiaTTS("test-key", "", "",
		WithCartesiaBaseURL("https://cartesia.example"),
	)

	requestURL, _, err := buildCartesiaSynthesizeRequest(provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	parsed, err := url.Parse(requestURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if parsed.Scheme != "https" || parsed.Host != "cartesia.example" {
		t.Fatalf("url = %q, want configured base URL", requestURL)
	}
	if parsed.Path != "/tts/bytes" {
		t.Fatalf("path = %q, want /tts/bytes", parsed.Path)
	}
}

func TestCartesiaStreamInitMessageUsesReferenceOptions(t *testing.T) {
	provider := NewCartesiaTTS("test-key", "", "")

	msg := buildCartesiaStreamInitMessage(provider)

	if msg["model_id"] != "sonic-3" {
		t.Fatalf("model_id = %#v, want sonic-3", msg["model_id"])
	}
	if msg["language"] != "en" {
		t.Fatalf("language = %#v, want en", msg["language"])
	}
	if msg["add_timestamps"] != true {
		t.Fatalf("add_timestamps = %#v, want true", msg["add_timestamps"])
	}
}

func TestCartesiaWebsocketURLAndHeadersMatchReference(t *testing.T) {
	provider := NewCartesiaTTS("test-key", "", "",
		WithCartesiaBaseURL("https://cartesia.example"),
	)

	streamURL := buildCartesiaStreamURL(provider)
	parsed, err := url.Parse(streamURL)
	if err != nil {
		t.Fatalf("parse stream url: %v", err)
	}
	if parsed.Scheme != "wss" || parsed.Host != "cartesia.example" {
		t.Fatalf("stream URL = %q, want configured websocket host", streamURL)
	}
	if parsed.Path != "/tts/websocket" {
		t.Fatalf("path = %q, want /tts/websocket", parsed.Path)
	}
	if parsed.Query().Get("cartesia_version") != "2025-04-16" {
		t.Fatalf("cartesia_version = %q, want 2025-04-16", parsed.Query().Get("cartesia_version"))
	}
	if parsed.Query().Get("api_key") != "" {
		t.Fatalf("api_key query = %q, want API key in header only", parsed.Query().Get("api_key"))
	}

	headers := buildCartesiaStreamHeaders(provider)
	if headers.Get("X-API-Key") != "test-key" {
		t.Fatalf("X-API-Key = %q, want test-key", headers.Get("X-API-Key"))
	}
	if headers.Get("Cartesia-Version") != "2025-04-16" {
		t.Fatalf("Cartesia-Version = %q, want 2025-04-16", headers.Get("Cartesia-Version"))
	}
	if headers.Get("User-Agent") == "" {
		t.Fatal("User-Agent header missing")
	}
}

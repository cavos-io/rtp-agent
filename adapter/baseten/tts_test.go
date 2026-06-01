package baseten

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestBasetenTTSDefaultsMatchReferenceOptions(t *testing.T) {
	provider := NewBasetenTTS("test-key", "model-id")

	if provider.modelEndpoint != "https://model-model-id.api.baseten.co/environments/production/predict" {
		t.Fatalf("endpoint = %q, want generated model predict endpoint", provider.modelEndpoint)
	}
	if provider.voice != "tara" {
		t.Fatalf("voice = %q, want tara", provider.voice)
	}
	if provider.language != "en" {
		t.Fatalf("language = %q, want en", provider.language)
	}
	if provider.temperature != 0.6 {
		t.Fatalf("temperature = %.1f, want 0.6", provider.temperature)
	}
	if provider.Capabilities().Streaming {
		t.Fatalf("streaming = true for https endpoint, want false")
	}
}

func TestBasetenTTSWebSocketEndpointReportsStreamingCapability(t *testing.T) {
	provider := NewBasetenTTS("test-key", "",
		WithBasetenTTSModelEndpoint("wss://model.example/websocket"),
	)

	if !provider.Capabilities().Streaming {
		t.Fatal("streaming = false for websocket endpoint, want true")
	}
}

func TestBuildBasetenTTSRequestMatchesReferencePayload(t *testing.T) {
	provider := NewBasetenTTS("test-key", "",
		WithBasetenTTSModelEndpoint("https://model.example/predict"),
		WithBasetenTTSVoice("emma"),
		WithBasetenTTSLanguage("es"),
		WithBasetenTTSTemperature(0.8),
	)

	req, err := buildBasetenTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.Method != http.MethodPost {
		t.Fatalf("method = %s, want POST", req.Method)
	}
	if req.URL.String() != "https://model.example/predict" {
		t.Fatalf("URL = %q, want configured endpoint", req.URL.String())
	}
	if req.Header.Get("Authorization") != "Api-Key test-key" {
		t.Fatalf("Authorization = %q, want Api-Key header", req.Header.Get("Authorization"))
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	assertBasetenPayload(t, payload, "prompt", "hello")
	assertBasetenPayload(t, payload, "voice", "emma")
	assertBasetenPayload(t, payload, "language", "es")
	assertBasetenPayload(t, payload, "temperature", float64(0.8))
	if _, ok := payload["text"]; ok {
		t.Fatalf("payload still uses legacy text field: %+v", payload)
	}
}

func TestBasetenTTSChunkedStreamReturnsRawAudioChunks(t *testing.T) {
	stream := &basetenTTSChunkedStream{
		body:       io.NopCloser(strings.NewReader("abcdef")),
		sampleRate: 24000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("first chunk: %v", err)
	}
	if string(audio.Frame.Data) != "abcdef" {
		t.Fatalf("audio data = %q, want raw chunk", string(audio.Frame.Data))
	}
	if audio.Frame.SampleRate != 24000 || audio.Frame.NumChannels != 1 {
		t.Fatalf("frame = %+v, want 24 kHz mono", audio.Frame)
	}

	_, err = stream.Next()
	if err != io.EOF {
		t.Fatalf("second chunk err = %v, want EOF", err)
	}
}

func assertBasetenPayload(t *testing.T, payload map[string]any, key string, want any) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %#v", key, got, want)
	}
}

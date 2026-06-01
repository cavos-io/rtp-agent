package smallestai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestSmallestAITTSDefaultsMatchReference(t *testing.T) {
	provider := NewSmallestAITTS("test-key", "")

	if provider.baseURL != "https://api.smallest.ai/waves/v1" {
		t.Fatalf("base URL = %q, want reference base URL", provider.baseURL)
	}
	if provider.model != "lightning_v3.1_pro" {
		t.Fatalf("model = %q, want lightning_v3.1_pro", provider.model)
	}
	if provider.voice != "meher" {
		t.Fatalf("voice = %q, want pro default voice", provider.voice)
	}
	if provider.sampleRate != 24000 {
		t.Fatalf("sample rate = %d, want 24000", provider.sampleRate)
	}
	if provider.speed != 1.0 {
		t.Fatalf("speed = %f, want 1.0", provider.speed)
	}
	if provider.language != "en" {
		t.Fatalf("language = %q, want en", provider.language)
	}
	if provider.outputFormat != "pcm" {
		t.Fatalf("output format = %q, want pcm", provider.outputFormat)
	}
}

func TestSmallestAITTSSynthesizeRequestUsesReferencePayload(t *testing.T) {
	provider := NewSmallestAITTS("test-key", "")

	req, err := buildSmallestAITTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://api.smallest.ai/waves/v1/tts" {
		t.Fatalf("url = %q, want reference tts endpoint", req.URL.String())
	}
	if got := req.Header.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("authorization = %q, want bearer token", got)
	}
	if got := req.Header.Get("X-Source"); got != "livekit" {
		t.Fatalf("X-Source = %q, want livekit", got)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertSmallestAIPayload(t, payload, "text", "hello")
	assertSmallestAIPayload(t, payload, "model", "lightning_v3.1_pro")
	assertSmallestAIPayload(t, payload, "voice_id", "meher")
	assertSmallestAIPayload(t, payload, "language", "en")
	assertSmallestAIPayload(t, payload, "output_format", "pcm")
	if payload["sample_rate"] != float64(24000) {
		t.Fatalf("sample_rate = %#v, want 24000", payload["sample_rate"])
	}
	if payload["speed"] != float64(1.0) {
		t.Fatalf("speed = %#v, want 1.0", payload["speed"])
	}
}

func TestSmallestAITTSOptionsMatchReference(t *testing.T) {
	provider := NewSmallestAITTS("test-key", "",
		WithSmallestAITTSBaseURL("https://smallest.example/waves/v1/"),
		WithSmallestAITTSModel("lightning_v3.1"),
		WithSmallestAITTSVoice("sophia"),
		WithSmallestAITTSSampleRate(44100),
		WithSmallestAITTSSpeed(1.4),
		WithSmallestAITTSLanguage("auto"),
		WithSmallestAITTSOutputFormat("wav"),
	)

	req, err := buildSmallestAITTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.String() != "https://smallest.example/waves/v1/tts" {
		t.Fatalf("url = %q, want custom tts endpoint", req.URL.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertSmallestAIPayload(t, payload, "model", "lightning_v3.1")
	assertSmallestAIPayload(t, payload, "voice_id", "sophia")
	assertSmallestAIPayload(t, payload, "language", "auto")
	assertSmallestAIPayload(t, payload, "output_format", "wav")
	if payload["sample_rate"] != float64(44100) {
		t.Fatalf("sample_rate = %#v, want 44100", payload["sample_rate"])
	}
	if payload["speed"] != float64(1.4) {
		t.Fatalf("speed = %#v, want 1.4", payload["speed"])
	}
}

func TestSmallestAITTSChunkedStreamUsesConfiguredSampleRate(t *testing.T) {
	stream := &smallestaiTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader([]byte{0x01, 0x02}))},
		sampleRate: 44100,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if audio.Frame.SampleRate != 44100 {
		t.Fatalf("sample rate = %d, want configured sample rate", audio.Frame.SampleRate)
	}
}

func assertSmallestAIPayload(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}

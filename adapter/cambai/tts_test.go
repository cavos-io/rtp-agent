package cambai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestCambaiTTSDefaultsMatchReference(t *testing.T) {
	provider := NewCambaiTTS("test-key", "")

	if provider.baseURL != "https://client.camb.ai/apis" {
		t.Fatalf("base URL = %q, want reference base URL", provider.baseURL)
	}
	if provider.voiceID != 147320 {
		t.Fatalf("voice id = %d, want default voice", provider.voiceID)
	}
	if provider.language != "en-us" {
		t.Fatalf("language = %q, want en-us", provider.language)
	}
	if provider.model != "mars-flash" {
		t.Fatalf("model = %q, want mars-flash", provider.model)
	}
	if provider.outputFormat != "pcm_s16le" {
		t.Fatalf("output format = %q, want pcm_s16le", provider.outputFormat)
	}
	if provider.SampleRate() != 22050 {
		t.Fatalf("sample rate = %d, want mars-flash sample rate", provider.SampleRate())
	}
}

func TestCambaiTTSSynthesizeRequestUsesReferencePayload(t *testing.T) {
	provider := NewCambaiTTS("test-key", "")

	req, err := buildCambaiTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://client.camb.ai/apis/tts-stream" {
		t.Fatalf("url = %q, want tts-stream endpoint", req.URL.String())
	}
	if got := req.Header.Get("x-api-key"); got != "test-key" {
		t.Fatalf("x-api-key = %q, want API key", got)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertCambaiPayload(t, payload, "text", "hello")
	if payload["voice_id"] != float64(147320) {
		t.Fatalf("voice_id = %#v, want default voice", payload["voice_id"])
	}
	assertCambaiPayload(t, payload, "language", "en-us")
	assertCambaiPayload(t, payload, "speech_model", "mars-flash")
	if payload["enhance_named_entities_pronunciation"] != false {
		t.Fatalf("enhance_named_entities_pronunciation = %#v, want false", payload["enhance_named_entities_pronunciation"])
	}
	outputConfig := payload["output_configuration"].(map[string]any)
	assertCambaiPayload(t, outputConfig, "format", "pcm_s16le")
	if _, ok := payload["user_instructions"]; ok {
		t.Fatalf("user_instructions present, want omitted by default")
	}
}

func TestCambaiTTSOptionsMatchReference(t *testing.T) {
	provider := NewCambaiTTS("test-key", "",
		WithCambaiTTSBaseURL("https://cambai.example/apis/"),
		WithCambaiTTSVoiceID(42),
		WithCambaiTTSLanguage("fr-fr"),
		WithCambaiTTSModel("mars-pro"),
		WithCambaiTTSOutputFormat("wav"),
		WithCambaiTTSUserInstructions("warm and concise"),
		WithCambaiTTSEnhanceNamedEntities(true),
	)

	req, err := buildCambaiTTSRequest(context.Background(), provider, "bonjour")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.String() != "https://cambai.example/apis/tts-stream" {
		t.Fatalf("url = %q, want custom tts-stream endpoint", req.URL.String())
	}
	if provider.SampleRate() != 48000 {
		t.Fatalf("sample rate = %d, want mars-pro sample rate", provider.SampleRate())
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if payload["voice_id"] != float64(42) {
		t.Fatalf("voice_id = %#v, want custom voice", payload["voice_id"])
	}
	assertCambaiPayload(t, payload, "language", "fr-fr")
	assertCambaiPayload(t, payload, "speech_model", "mars-pro")
	assertCambaiPayload(t, payload, "user_instructions", "warm and concise")
	if payload["enhance_named_entities_pronunciation"] != true {
		t.Fatalf("enhance_named_entities_pronunciation = %#v, want true", payload["enhance_named_entities_pronunciation"])
	}
	outputConfig := payload["output_configuration"].(map[string]any)
	assertCambaiPayload(t, outputConfig, "format", "wav")
}

func TestCambaiTTSChunkedStreamUsesConfiguredSampleRate(t *testing.T) {
	stream := &cambaiTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader([]byte{0x01, 0x02}))},
		sampleRate: 48000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if audio.Frame.SampleRate != 48000 {
		t.Fatalf("sample rate = %d, want configured sample rate", audio.Frame.SampleRate)
	}
}

func assertCambaiPayload(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}

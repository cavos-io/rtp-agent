package cambai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type cambaiRoundTripperFunc func(*http.Request) (*http.Response, error)

func (f cambaiRoundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestCambaiTTSDefaultsMatchReference(t *testing.T) {
	provider, err := NewCambaiTTS("test-key", "")
	if err != nil {
		t.Fatalf("NewCambaiTTS error = %v", err)
	}

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
	if provider.Label() != "cambai.TTS" {
		t.Fatalf("Label = %q, want cambai.TTS", provider.Label())
	}
	if provider.Model() != "mars-flash" {
		t.Fatalf("Model = %q, want mars-flash", provider.Model())
	}
	if provider.Provider() != "Camb.ai" {
		t.Fatalf("Provider = %q, want Camb.ai", provider.Provider())
	}
	if provider.NumChannels() != 1 {
		t.Fatalf("NumChannels = %d, want 1", provider.NumChannels())
	}
	if provider.Capabilities().Streaming {
		t.Fatal("Streaming = true, want false")
	}
}

func TestCambaiTTSSynthesizeRequestUsesReferencePayload(t *testing.T) {
	provider, err := NewCambaiTTS("test-key", "")
	if err != nil {
		t.Fatalf("NewCambaiTTS error = %v", err)
	}

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

func TestCambaiTTSFallsBackToEnvironmentAPIKey(t *testing.T) {
	t.Setenv(cambaiAPIKeyEnv, "env-key")

	provider, err := NewCambaiTTS("", "")
	if err != nil {
		t.Fatalf("NewCambaiTTS error = %v, want nil from env key", err)
	}

	if provider.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env-key", provider.apiKey)
	}
}

func TestCambaiTTSRequiresAPIKey(t *testing.T) {
	t.Setenv(cambaiAPIKeyEnv, "")

	_, err := NewCambaiTTS("", "")

	if err == nil || !strings.Contains(err.Error(), "CAMB_API_KEY") {
		t.Fatalf("NewCambaiTTS error = %v, want API key error", err)
	}
}

func TestCambaiTTSOptionsMatchReference(t *testing.T) {
	provider, err := NewCambaiTTS("test-key", "",
		WithCambaiTTSBaseURL("https://cambai.example/apis/"),
		WithCambaiTTSVoiceID(42),
		WithCambaiTTSLanguage("fr-fr"),
		WithCambaiTTSModel("mars-pro"),
		WithCambaiTTSOutputFormat("wav"),
		WithCambaiTTSUserInstructions("warm and concise"),
		WithCambaiTTSEnhanceNamedEntities(true),
	)
	if err != nil {
		t.Fatalf("NewCambaiTTS error = %v", err)
	}

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

func TestCambaiTTSSynthesizeUsesConfiguredClient(t *testing.T) {
	provider, err := NewCambaiTTS("test-key", "", WithCambaiTTSModel("mars-pro"))
	if err != nil {
		t.Fatalf("NewCambaiTTS error = %v", err)
	}
	provider.httpClient = &http.Client{
		Transport: cambaiRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			if req.Header.Get("x-api-key") != "test-key" {
				t.Fatalf("x-api-key = %q, want test-key", req.Header.Get("x-api-key"))
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader([]byte{0x01, 0x02})),
				Header:     make(http.Header),
				Request:    req,
			}, nil
		}),
	}

	stream, err := provider.Synthesize(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Synthesize error = %v", err)
	}
	defer stream.Close()

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next error = %v", err)
	}
	if audio.Frame.SampleRate != 48000 {
		t.Fatalf("sample rate = %d, want mars-pro sample rate", audio.Frame.SampleRate)
	}
}

func TestCambaiTTSStreamReportsUnsupported(t *testing.T) {
	provider, err := NewCambaiTTS("test-key", "")
	if err != nil {
		t.Fatalf("NewCambaiTTS error = %v", err)
	}

	_, err = provider.Stream(context.Background())

	if err == nil || !strings.Contains(err.Error(), "not natively supported") {
		t.Fatalf("Stream error = %v, want unsupported streaming error", err)
	}
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
	if err := stream.Close(); err != nil {
		t.Fatalf("Close error = %v", err)
	}
}

func assertCambaiPayload(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}

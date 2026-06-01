package respeecher

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestRespeecherTTSDefaultsMatchReference(t *testing.T) {
	provider := NewRespeecherTTS("test-key", "")

	if provider.baseURL != "https://api.respeecher.com/v1" {
		t.Fatalf("base URL = %q, want reference base URL", provider.baseURL)
	}
	if provider.model != "/public/tts/en-rt" {
		t.Fatalf("model = %q, want English public model", provider.model)
	}
	if provider.voiceID != "samantha" {
		t.Fatalf("voice id = %q, want model default voice", provider.voiceID)
	}
	if provider.encoding != "pcm_s16le" {
		t.Fatalf("encoding = %q, want pcm_s16le", provider.encoding)
	}
	if provider.SampleRate() != 24000 {
		t.Fatalf("sample rate = %d, want 24000", provider.SampleRate())
	}
}

func TestRespeecherTTSSynthesizeRequestUsesReferencePayload(t *testing.T) {
	provider := NewRespeecherTTS("test-key", "")

	req, err := buildRespeecherTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://api.respeecher.com/v1/public/tts/en-rt/tts/bytes" {
		t.Fatalf("url = %q, want bytes endpoint", req.URL.String())
	}
	if got := req.Header.Get("X-API-Key"); got != "test-key" {
		t.Fatalf("X-API-Key = %q, want test key", got)
	}
	if got := req.Header.Get("LiveKit-Plugin-Respeecher-Version"); got != "1.5.15" {
		t.Fatalf("version header = %q, want reference plugin version", got)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertRespeecherPayload(t, payload, "transcript", "hello")
	voice := payload["voice"].(map[string]any)
	assertRespeecherPayload(t, voice, "id", "samantha")
	output := payload["output_format"].(map[string]any)
	assertRespeecherPayload(t, output, "encoding", "pcm_s16le")
	if output["sample_rate"] != float64(24000) {
		t.Fatalf("sample_rate = %#v, want 24000", output["sample_rate"])
	}
}

func TestRespeecherTTSOptionsMatchReference(t *testing.T) {
	provider := NewRespeecherTTS("test-key", "",
		WithRespeecherTTSBaseURL("https://respeecher.example/v1/"),
		WithRespeecherTTSModel("/public/tts/ua-rt"),
		WithRespeecherTTSVoice("olesia-conversation"),
		WithRespeecherTTSSampleRate(48000),
		WithRespeecherTTSSamplingParams(map[string]any{"temperature": 0.4}),
	)

	req, err := buildRespeecherTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.String() != "https://respeecher.example/v1/public/tts/ua-rt/tts/bytes" {
		t.Fatalf("url = %q, want custom bytes endpoint", req.URL.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	voice := payload["voice"].(map[string]any)
	assertRespeecherPayload(t, voice, "id", "olesia-conversation")
	samplingParams := voice["sampling_params"].(map[string]any)
	if samplingParams["temperature"] != float64(0.4) {
		t.Fatalf("temperature = %#v, want 0.4", samplingParams["temperature"])
	}
	output := payload["output_format"].(map[string]any)
	if output["sample_rate"] != float64(48000) {
		t.Fatalf("sample_rate = %#v, want 48000", output["sample_rate"])
	}
}

func TestRespeecherTTSChunkedStreamUsesConfiguredSampleRate(t *testing.T) {
	stream := &respeecherTTSChunkedStream{
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

func assertRespeecherPayload(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}

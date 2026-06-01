package neuphonic

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestNeuphonicTTSDefaultsMatchReference(t *testing.T) {
	provider := NewNeuphonicTTS("test-key", "")

	if provider.baseURL != "https://api.neuphonic.com" {
		t.Fatalf("base URL = %q, want reference API base", provider.baseURL)
	}
	if provider.voice != "8e9c4bc8-3979-48ab-8626-df53befc2090" {
		t.Fatalf("voice = %q, want reference voice id", provider.voice)
	}
	if provider.langCode != "en" {
		t.Fatalf("lang code = %q, want en", provider.langCode)
	}
	if provider.encoding != "pcm_linear" {
		t.Fatalf("encoding = %q, want pcm_linear", provider.encoding)
	}
	if provider.sampleRate != 22050 {
		t.Fatalf("sample rate = %d, want 22050", provider.sampleRate)
	}
	if provider.speed == nil || *provider.speed != 1.0 {
		t.Fatalf("speed = %v, want 1.0", provider.speed)
	}
}

func TestNeuphonicTTSSynthesizeRequestUsesReferencePayload(t *testing.T) {
	provider := NewNeuphonicTTS("test-key", "")

	req, err := buildNeuphonicTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://api.neuphonic.com/sse/speak/en" {
		t.Fatalf("url = %q, want SSE speak endpoint", req.URL.String())
	}
	if got := req.Header.Get("x-api-key"); got != "test-key" {
		t.Fatalf("x-api-key = %q, want test-key", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q, want application/json", got)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertNeuphonicPayload(t, payload, "text", "hello")
	assertNeuphonicPayload(t, payload, "voice_id", "8e9c4bc8-3979-48ab-8626-df53befc2090")
	assertNeuphonicPayload(t, payload, "lang_code", "en")
	assertNeuphonicPayload(t, payload, "encoding", "pcm_linear")
	if got := payload["sampling_rate"]; got != float64(22050) {
		t.Fatalf("sampling_rate = %#v, want 22050", got)
	}
	if got := payload["speed"]; got != 1.0 {
		t.Fatalf("speed = %#v, want 1.0", got)
	}
}

func TestNeuphonicTTSOptionsMatchReference(t *testing.T) {
	provider := NewNeuphonicTTS("test-key", "",
		WithNeuphonicTTSBaseURL("https://neuphonic.example"),
		WithNeuphonicTTSVoice("voice-2"),
		WithNeuphonicTTSLangCode("es"),
		WithNeuphonicTTSEncoding("pcm_mulaw"),
		WithNeuphonicTTSSampleRate(16000),
		WithNeuphonicTTSSpeed(0.75),
	)

	req, err := buildNeuphonicTTSRequest(context.Background(), provider, "hola")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.String() != "https://neuphonic.example/sse/speak/es" {
		t.Fatalf("url = %q, want custom base SSE speak endpoint", req.URL.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertNeuphonicPayload(t, payload, "voice_id", "voice-2")
	assertNeuphonicPayload(t, payload, "lang_code", "es")
	assertNeuphonicPayload(t, payload, "encoding", "pcm_mulaw")
	if got := payload["sampling_rate"]; got != float64(16000) {
		t.Fatalf("sampling_rate = %#v, want 16000", got)
	}
	if got := payload["speed"]; got != 0.75 {
		t.Fatalf("speed = %#v, want 0.75", got)
	}
}

func TestNeuphonicTTSChunkedStreamDecodesSSEAudio(t *testing.T) {
	stream := &neuphonicTTSChunkedStream{
		resp: &http.Response{Body: io.NopCloser(bytes.NewReader([]byte(
			"event: message\n" +
				"data: {\"status_code\":200,\"data\":{\"audio\":\"AQI=\"}}\n\n",
		)))},
		sampleRate: 16000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if string(audio.Frame.Data) != string([]byte{0x01, 0x02}) {
		t.Fatalf("audio data = %#v, want decoded base64 bytes", audio.Frame.Data)
	}
	if audio.Frame.SampleRate != 16000 {
		t.Fatalf("sample rate = %d, want 16000", audio.Frame.SampleRate)
	}
}

func assertNeuphonicPayload(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}

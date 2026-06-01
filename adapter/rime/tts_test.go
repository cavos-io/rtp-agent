package rime

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestRimeTTSDefaultsMatchReference(t *testing.T) {
	provider := NewRimeTTS("test-key", "")

	if provider.baseURL != "https://users.rime.ai/v1/rime-tts" {
		t.Fatalf("base URL = %q, want reference HTTP endpoint", provider.baseURL)
	}
	if provider.model != "arcana" {
		t.Fatalf("model = %q, want arcana", provider.model)
	}
	if provider.voice != "astra" {
		t.Fatalf("voice = %q, want astra", provider.voice)
	}
	if provider.lang != "eng" {
		t.Fatalf("lang = %q, want eng", provider.lang)
	}
	if provider.sampleRate != 22050 {
		t.Fatalf("sample rate = %d, want 22050", provider.sampleRate)
	}
}

func TestRimeTTSSynthesizeRequestUsesReferenceDefaults(t *testing.T) {
	provider := NewRimeTTS("test-key", "")

	req, err := buildRimeTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://users.rime.ai/v1/rime-tts" {
		t.Fatalf("url = %q, want reference endpoint", req.URL.String())
	}
	if got := req.Header.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("authorization = %q, want bearer token", got)
	}
	if got := req.Header.Get("Accept"); got != "audio/pcm" {
		t.Fatalf("accept = %q, want audio/pcm", got)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertRimePayload(t, payload, "speaker", "astra")
	assertRimePayload(t, payload, "text", "hello")
	assertRimePayload(t, payload, "modelId", "arcana")
	assertRimePayload(t, payload, "lang", "eng")
	if got := payload["samplingRate"]; got != float64(22050) {
		t.Fatalf("samplingRate = %#v, want 22050", got)
	}
	if _, ok := payload["audioFormat"]; ok {
		t.Fatalf("audioFormat = %#v, want omitted for HTTP reference payload", payload["audioFormat"])
	}
}

func TestRimeTTSOptionsMatchReferenceModels(t *testing.T) {
	provider := NewRimeTTS("test-key", "",
		WithRimeTTSModel("coda"),
		WithRimeTTSSampleRate(24000),
		WithRimeTTSBaseURL("https://rime.example/v1/rime-tts"),
		WithRimeTTSLang("spa"),
		WithRimeTTSTimeScaleFactor(1.1),
	)

	if provider.voice != "lyra" {
		t.Fatalf("voice = %q, want coda default lyra", provider.voice)
	}

	req, err := buildRimeTTSRequest(context.Background(), provider, "hola")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.String() != "https://rime.example/v1/rime-tts" {
		t.Fatalf("url = %q, want custom base URL", req.URL.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertRimePayload(t, payload, "speaker", "lyra")
	assertRimePayload(t, payload, "modelId", "coda")
	assertRimePayload(t, payload, "lang", "spa")
	if got := payload["samplingRate"]; got != float64(24000) {
		t.Fatalf("samplingRate = %#v, want 24000", got)
	}
	if got := payload["timeScaleFactor"]; got != 1.1 {
		t.Fatalf("timeScaleFactor = %#v, want 1.1", got)
	}
}

func TestRimeTTSChunkedStreamUsesConfiguredSampleRate(t *testing.T) {
	stream := &rimeTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader([]byte{0x01, 0x02}))},
		sampleRate: 24000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if audio.Frame.SampleRate != 24000 {
		t.Fatalf("sample rate = %d, want 24000", audio.Frame.SampleRate)
	}
}

func assertRimePayload(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}

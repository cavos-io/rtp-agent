package sarvam

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/cavos-io/conversation-worker/core/stt"
)

func TestSarvamSTTDefaultsMatchReference(t *testing.T) {
	provider := NewSarvamSTT("test-key")

	if provider.baseURL != "https://api.sarvam.ai/speech-to-text" {
		t.Fatalf("base URL = %q, want reference STT endpoint", provider.baseURL)
	}
	if provider.streamingURL != "wss://api.sarvam.ai/speech-to-text/ws" {
		t.Fatalf("streaming URL = %q, want reference STT websocket endpoint", provider.streamingURL)
	}
	if provider.model != "saarika:v2.5" {
		t.Fatalf("model = %q, want saarika:v2.5", provider.model)
	}
	if provider.language != "en-IN" {
		t.Fatalf("language = %q, want en-IN", provider.language)
	}
	if provider.mode != "transcribe" {
		t.Fatalf("mode = %q, want transcribe", provider.mode)
	}
	if provider.sampleRate != 16000 {
		t.Fatalf("sample rate = %d, want 16000", provider.sampleRate)
	}

	caps := provider.Capabilities()
	if !caps.Streaming || !caps.InterimResults || !caps.OfflineRecognize {
		t.Fatalf("capabilities = %+v, want streaming, interim, and offline recognize", caps)
	}
}

func TestSarvamSTTModelURLAndValidationReference(t *testing.T) {
	provider := NewSarvamSTT("test-key", WithSarvamSTTModel("saaras:v2.5"))
	if provider.baseURL != "https://api.sarvam.ai/speech-to-text-translate" {
		t.Fatalf("base URL = %q, want translate endpoint for saaras:v2.5", provider.baseURL)
	}
	if provider.streamingURL != "wss://api.sarvam.ai/speech-to-text-translate/ws" {
		t.Fatalf("streaming URL = %q, want translate websocket for saaras:v2.5", provider.streamingURL)
	}

	_, err := NewSarvamSTTWithError("test-key", WithSarvamSTTModel("saarika:v2.5"), WithSarvamSTTMode("translate"))
	if err == nil || !strings.Contains(err.Error(), "mode is not supported") {
		t.Fatalf("error = %v, want unsupported mode error", err)
	}

	_, err = NewSarvamSTTWithError("test-key", WithSarvamSTTModel("saarika:v2.5"), WithSarvamSTTLanguage("as-IN"))
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("error = %v, want unsupported language error", err)
	}
}

func TestBuildSarvamSTTRecognizeRequestMatchesReference(t *testing.T) {
	provider := NewSarvamSTT("test-key",
		WithSarvamSTTBaseURL("https://sarvam.example/stt"),
		WithSarvamSTTModel("saaras:v3"),
		WithSarvamSTTLanguage("ta-IN"),
		WithSarvamSTTMode("translate"),
	)

	req, err := buildSarvamSTTRecognizeRequest(context.Background(), provider, []byte("pcm"), "")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.Method != http.MethodPost {
		t.Fatalf("method = %s, want POST", req.Method)
	}
	if req.URL.String() != "https://sarvam.example/stt" {
		t.Fatalf("URL = %q, want configured base URL", req.URL.String())
	}
	if req.Header.Get("api-subscription-key") != "test-key" {
		t.Fatalf("api-subscription-key = %q, want test-key", req.Header.Get("api-subscription-key"))
	}
	if req.Header.Get("User-Agent") == "" {
		t.Fatal("User-Agent missing")
	}

	fields := readMultipartFields(t, req)
	if fields["language_code"] != "ta-IN" {
		t.Fatalf("language_code = %q, want ta-IN", fields["language_code"])
	}
	if fields["model"] != "saaras:v3" {
		t.Fatalf("model = %q, want saaras:v3", fields["model"])
	}
	if fields["mode"] != "translate" {
		t.Fatalf("mode = %q, want translate for saaras:v3", fields["mode"])
	}
	if fields["file"] != "pcm" {
		t.Fatalf("file = %q, want audio payload", fields["file"])
	}
}

func TestSarvamSTTSpeechEventMapsReferenceMetadata(t *testing.T) {
	event := sarvamSTTSpeechEvent("en-IN", sarvamSTTResponse{
		Transcript:          "hello",
		RequestID:           "req-1",
		LanguageCode:        "ta-IN",
		LanguageProbability: 0.82,
		Timestamps: sarvamSTTTimestamps{
			StartTimeSeconds: []float64{0.1},
			EndTimeSeconds:   []float64{0.4, 0.9},
		},
	})

	if event.Type != stt.SpeechEventFinalTranscript || event.RequestID != "req-1" {
		t.Fatalf("event = %+v, want final transcript with request id", event)
	}
	alt := event.Alternatives[0]
	if alt.Text != "hello" || alt.Language != "ta-IN" || alt.Confidence != 0.82 {
		t.Fatalf("alternative = %+v, want transcript language confidence", alt)
	}
	if alt.StartTime != 0.1 || alt.EndTime != 0.9 {
		t.Fatalf("times = %.1f..%.1f, want 0.1..0.9", alt.StartTime, alt.EndTime)
	}
}

func TestSarvamTTSDefaultsMatchReference(t *testing.T) {
	provider := NewSarvamTTS("test-key", "")

	if provider.baseURL != "https://api.sarvam.ai/text-to-speech" {
		t.Fatalf("base URL = %q, want reference TTS endpoint", provider.baseURL)
	}
	if provider.wsURL != "wss://api.sarvam.ai/text-to-speech/ws" {
		t.Fatalf("ws URL = %q, want reference TTS websocket endpoint", provider.wsURL)
	}
	if provider.model != "bulbul:v3" {
		t.Fatalf("model = %q, want bulbul:v3", provider.model)
	}
	if provider.voice != "shubh" {
		t.Fatalf("voice = %q, want shubh for v3", provider.voice)
	}
	if provider.language != "en-IN" {
		t.Fatalf("language = %q, want en-IN", provider.language)
	}
	if provider.sampleRate != 22050 {
		t.Fatalf("sample rate = %d, want 22050", provider.sampleRate)
	}
	if !provider.Capabilities().Streaming {
		t.Fatalf("capabilities = %+v, want streaming true", provider.Capabilities())
	}
}

func TestBuildSarvamTTSRequestMatchesReferencePayload(t *testing.T) {
	provider := NewSarvamTTS("test-key", "",
		WithSarvamTTSBaseURL("https://sarvam.example/tts"),
		WithSarvamTTSModel("bulbul:v3"),
		WithSarvamTTSVoice("ritu"),
		WithSarvamTTSLanguage("hi-IN"),
		WithSarvamTTSSampleRate(24000),
		WithSarvamTTSTemperature(0.7),
		WithSarvamTTSOutputAudioCodec("wav"),
	)

	req, err := buildSarvamTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.String() != "https://sarvam.example/tts" {
		t.Fatalf("URL = %q, want configured base URL", req.URL.String())
	}
	if req.Header.Get("api-subscription-key") != "test-key" {
		t.Fatalf("api-subscription-key = %q, want test-key", req.Header.Get("api-subscription-key"))
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	assertSarvamJSONField(t, payload, "text", "hello")
	assertSarvamJSONField(t, payload, "target_language_code", "hi-IN")
	assertSarvamJSONField(t, payload, "speaker", "ritu")
	assertSarvamJSONField(t, payload, "model", "bulbul:v3")
	assertSarvamJSONField(t, payload, "output_audio_codec", "wav")
	assertSarvamJSONField(t, payload, "temperature", float64(0.7))
	if _, ok := payload["pitch"]; ok {
		t.Fatalf("pitch included for v3 payload: %+v", payload)
	}
}

func readMultipartFields(t *testing.T, req *http.Request) map[string]string {
	t.Helper()
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	mediaType := req.Header.Get("Content-Type")
	boundary := strings.TrimPrefix(mediaType, "multipart/form-data; boundary=")
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	fields := map[string]string{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("next part: %v", err)
		}
		data, err := io.ReadAll(part)
		if err != nil {
			t.Fatalf("read part: %v", err)
		}
		fields[part.FormName()] = string(data)
	}
	return fields
}

func assertSarvamJSONField(t *testing.T, payload map[string]any, key string, want any) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %#v", key, got, want)
	}
}

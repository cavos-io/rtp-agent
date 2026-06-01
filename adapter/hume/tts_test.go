package hume

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestHumeTTSDefaultsMatchReference(t *testing.T) {
	provider := NewHumeTTS("test-key", "")

	if provider.baseURL != "https://api.hume.ai" {
		t.Fatalf("base URL = %q, want reference base URL", provider.baseURL)
	}
	if provider.modelVersion != "1" {
		t.Fatalf("model version = %q, want 1", provider.modelVersion)
	}
	if provider.voiceName != "Male English Actor" {
		t.Fatalf("voice name = %q, want reference default voice", provider.voiceName)
	}
	if provider.voiceProvider != "HUME_AI" {
		t.Fatalf("voice provider = %q, want HUME_AI", provider.voiceProvider)
	}
	if provider.audioFormat != "mp3" {
		t.Fatalf("audio format = %q, want mp3", provider.audioFormat)
	}
	if !provider.instantMode {
		t.Fatalf("instant mode = false, want true when voice is configured")
	}
	if provider.SampleRate() != 48000 {
		t.Fatalf("sample rate = %d, want reference supported sample rate", provider.SampleRate())
	}
}

func TestHumeTTSSynthesizeRequestUsesReferencePayload(t *testing.T) {
	provider := NewHumeTTS("test-key", "")

	req, err := buildHumeTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://api.hume.ai/v0/tts/stream/json" {
		t.Fatalf("url = %q, want stream json endpoint", req.URL.String())
	}
	if got := req.Header.Get("X-Hume-Api-Key"); got != "test-key" {
		t.Fatalf("api key = %q, want test key", got)
	}
	if got := req.Header.Get("X-Hume-Client-Name"); got != "livekit" {
		t.Fatalf("client name = %q, want livekit", got)
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if payload["version"] != "1" {
		t.Fatalf("version = %#v, want 1", payload["version"])
	}
	if payload["strip_headers"] != true {
		t.Fatalf("strip_headers = %#v, want true", payload["strip_headers"])
	}
	if payload["instant_mode"] != true {
		t.Fatalf("instant_mode = %#v, want true", payload["instant_mode"])
	}
	format := payload["format"].(map[string]any)
	if format["type"] != "mp3" {
		t.Fatalf("format type = %#v, want mp3", format["type"])
	}
	utterances := payload["utterances"].([]any)
	utterance := utterances[0].(map[string]any)
	if utterance["text"] != "hello" {
		t.Fatalf("utterance text = %#v, want hello", utterance["text"])
	}
	voice := utterance["voice"].(map[string]any)
	if voice["name"] != "Male English Actor" {
		t.Fatalf("voice name = %#v, want reference default", voice["name"])
	}
	if voice["provider"] != "HUME_AI" {
		t.Fatalf("voice provider = %#v, want HUME_AI", voice["provider"])
	}
}

func TestHumeTTSOptionsMatchReference(t *testing.T) {
	provider := NewHumeTTS("test-key", "",
		WithHumeTTSBaseURL("https://hume.example/"),
		WithHumeTTSModelVersion("2"),
		WithHumeTTSVoiceName("Narrator", "CUSTOM_VOICE"),
		WithHumeTTSAudioFormat("wav"),
		WithHumeTTSDescription("calm"),
		WithHumeTTSSpeed(1.2),
		WithHumeTTSTrailingSilence(0.4),
		WithHumeTTSInstantMode(false),
	)

	req, err := buildHumeTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.String() != "https://hume.example/v0/tts/stream/json" {
		t.Fatalf("url = %q, want custom stream json endpoint", req.URL.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if payload["version"] != "2" {
		t.Fatalf("version = %#v, want 2", payload["version"])
	}
	if payload["instant_mode"] != false {
		t.Fatalf("instant_mode = %#v, want false", payload["instant_mode"])
	}
	format := payload["format"].(map[string]any)
	if format["type"] != "wav" {
		t.Fatalf("format type = %#v, want wav", format["type"])
	}
	utterance := payload["utterances"].([]any)[0].(map[string]any)
	if utterance["description"] != "calm" {
		t.Fatalf("description = %#v, want calm", utterance["description"])
	}
	if utterance["speed"] != float64(1.2) {
		t.Fatalf("speed = %#v, want 1.2", utterance["speed"])
	}
	if utterance["trailing_silence"] != float64(0.4) {
		t.Fatalf("trailing_silence = %#v, want 0.4", utterance["trailing_silence"])
	}
	voice := utterance["voice"].(map[string]any)
	if voice["name"] != "Narrator" {
		t.Fatalf("voice name = %#v, want Narrator", voice["name"])
	}
	if voice["provider"] != "CUSTOM_VOICE" {
		t.Fatalf("voice provider = %#v, want CUSTOM_VOICE", voice["provider"])
	}
}

func TestHumeTTSContextBuildsReferencePayload(t *testing.T) {
	provider := NewHumeTTS("test-key", "",
		WithHumeTTSContextGenerationID("generation-1"),
	)

	req, err := buildHumeTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build generation context request: %v", err)
	}
	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode generation context body: %v", err)
	}
	contextPayload := payload["context"].(map[string]any)
	if contextPayload["generation_id"] != "generation-1" {
		t.Fatalf("generation_id = %#v, want generation-1", contextPayload["generation_id"])
	}

	speed := 1.1
	trailingSilence := 0.2
	provider = NewHumeTTS("test-key", "",
		WithHumeTTSContextUtterances([]HumeTTSUtterance{
			{
				Text:            "previous line",
				Description:     "warm",
				Speed:           &speed,
				TrailingSilence: &trailingSilence,
				Voice:           &HumeTTSVoice{Name: "Narrator", Provider: "CUSTOM_VOICE"},
			},
		}),
	)

	req, err = buildHumeTTSRequest(context.Background(), provider, "next line")
	if err != nil {
		t.Fatalf("build utterance context request: %v", err)
	}
	payload = map[string]any{}
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode utterance context body: %v", err)
	}
	contextPayload = payload["context"].(map[string]any)
	utterance := contextPayload["utterances"].([]any)[0].(map[string]any)
	if utterance["text"] != "previous line" || utterance["description"] != "warm" {
		t.Fatalf("context utterance = %#v, want previous warm line", utterance)
	}
	if utterance["speed"] != float64(1.1) || utterance["trailing_silence"] != float64(0.2) {
		t.Fatalf("context utterance timing = %#v, want speed and trailing silence", utterance)
	}
	voice := utterance["voice"].(map[string]any)
	if voice["name"] != "Narrator" || voice["provider"] != "CUSTOM_VOICE" {
		t.Fatalf("context voice = %#v, want Narrator custom voice", voice)
	}
}

func TestHumeTTSChunkedStreamDecodesReferenceJSONLines(t *testing.T) {
	stream := &humeTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader([]byte("{\"audio\":\"AQI=\"}\n\n")))},
		sampleRate: 48000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if string(audio.Frame.Data) != string([]byte{0x01, 0x02}) {
		t.Fatalf("audio data = %#v, want decoded base64 audio", audio.Frame.Data)
	}
	if audio.Frame.SampleRate != 48000 {
		t.Fatalf("sample rate = %d, want 48000", audio.Frame.SampleRate)
	}
}

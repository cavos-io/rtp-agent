package smallestai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/tts"
)

func TestSmallestAITTSDefaultsMatchReference(t *testing.T) {
	provider := NewSmallestAITTS("test-key", "")

	if provider.baseURL != "https://api.smallest.ai/waves/v1" {
		t.Fatalf("base URL = %q, want reference base URL", provider.baseURL)
	}
	if provider.model != "lightning_v3.1_pro" {
		t.Fatalf("model = %q, want lightning_v3.1_pro", provider.model)
	}
	if got := tts.Model(provider); got != "lightning_v3.1_pro" {
		t.Fatalf("model metadata = %q, want lightning_v3.1_pro", got)
	}
	if got := tts.Provider(provider); got != "SmallestAI" {
		t.Fatalf("provider metadata = %q, want SmallestAI", got)
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
	if provider.wsURL != "wss://api.smallest.ai/waves/v1/tts/live" {
		t.Fatalf("websocket URL = %q, want reference websocket URL", provider.wsURL)
	}
	if !provider.Capabilities().Streaming {
		t.Fatal("streaming = false, want reference streaming support")
	}
}

func TestNewSmallestAITTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("SMALLEST_API_KEY", "env-key")

	provider := NewSmallestAITTS("", "")
	if provider.apiKey != "env-key" {
		t.Fatalf("apiKey = %q, want env key", provider.apiKey)
	}

	provider = NewSmallestAITTS("explicit-key", "")
	if provider.apiKey != "explicit-key" {
		t.Fatalf("apiKey = %q, want explicit key", provider.apiKey)
	}
}

func TestSmallestAITTSRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("SMALLEST_API_KEY", "")
	provider := NewSmallestAITTS("", "",
		WithSmallestAITTSBaseURL("://bad-url"),
		WithSmallestAITTSWebsocketURL("://bad-ws"),
	)

	_, err := provider.Synthesize(context.Background(), "hello")
	if err == nil {
		t.Fatal("Synthesize returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "SMALLEST_API_KEY") {
		t.Fatalf("Synthesize error = %q, want SMALLEST_API_KEY guidance", err)
	}

	_, err = provider.Stream(context.Background())
	if err == nil {
		t.Fatal("Stream returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "SMALLEST_API_KEY") {
		t.Fatalf("Stream error = %q, want SMALLEST_API_KEY guidance", err)
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
		WithSmallestAITTSWebsocketURL("wss://smallest.example/waves/v1/tts/live"),
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
	if provider.wsURL != "wss://smallest.example/waves/v1/tts/live" {
		t.Fatalf("websocket URL = %q, want custom websocket URL", provider.wsURL)
	}
}

func TestSmallestAITTSUpdateOptionsMatchesReferenceFutureRequests(t *testing.T) {
	provider := NewSmallestAITTS("test-key", "")

	provider.UpdateOptions(
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
	var payload map[string]any
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	assertSmallestAIPayload(t, payload, "model", "lightning_v3.1")
	assertSmallestAIPayload(t, payload, "voice_id", "sophia")
	assertSmallestAIPayload(t, payload, "language", "auto")
	assertSmallestAIPayload(t, payload, "output_format", "wav")
	if payload["sample_rate"] != float64(44100) || payload["speed"] != float64(1.4) {
		t.Fatalf("payload = %+v, want updated sample_rate and speed", payload)
	}

	streamPayload, err := buildSmallestAITTSStreamMessage(provider, "hello")
	if err != nil {
		t.Fatalf("build stream message: %v", err)
	}
	var streamMessage map[string]any
	if err := json.Unmarshal(streamPayload, &streamMessage); err != nil {
		t.Fatalf("decode stream message: %v", err)
	}
	assertSmallestAIPayload(t, streamMessage, "model", "lightning_v3.1")
	assertSmallestAIPayload(t, streamMessage, "voice_id", "sophia")
	assertSmallestAIPayload(t, streamMessage, "language", "auto")
	if streamMessage["sample_rate"] != float64(44100) || streamMessage["speed"] != float64(1.4) {
		t.Fatalf("stream message = %+v, want updated sample_rate and speed", streamMessage)
	}
}

func TestSmallestAITTSStreamMessageMatchesReference(t *testing.T) {
	provider := NewSmallestAITTS("test-key", "",
		WithSmallestAITTSModel("lightning_v3.1"),
		WithSmallestAITTSVoice("sophia"),
		WithSmallestAITTSSampleRate(44100),
		WithSmallestAITTSSpeed(1.4),
		WithSmallestAITTSLanguage("auto"),
	)

	payload, err := buildSmallestAITTSStreamMessage(provider, "hello")
	if err != nil {
		t.Fatalf("build stream message: %v", err)
	}
	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		t.Fatalf("decode stream message: %v", err)
	}
	assertSmallestAIPayload(t, message, "model", "lightning_v3.1")
	assertSmallestAIPayload(t, message, "voice_id", "sophia")
	assertSmallestAIPayload(t, message, "text", "hello")
	assertSmallestAIPayload(t, message, "language", "auto")
	if _, ok := message["output_format"]; ok {
		t.Fatalf("stream message included output_format, want websocket PCM payload")
	}
	if message["sample_rate"] != float64(44100) || message["speed"] != float64(1.4) {
		t.Fatalf("message = %+v, want sample rate and speed", message)
	}
}

func TestSmallestAITTSWebsocketHeadersMatchReference(t *testing.T) {
	provider := NewSmallestAITTS("test-key", "")

	if got := buildSmallestAITTSWebsocketURL(provider); got != "wss://api.smallest.ai/waves/v1/tts/live" {
		t.Fatalf("websocket URL = %q, want reference websocket URL", got)
	}

	headers := buildSmallestAITTSWebsocketHeaders(provider)
	if got := headers.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("authorization = %q, want bearer token", got)
	}
	if got := headers.Get("X-Source"); got != "livekit" {
		t.Fatalf("X-Source = %q, want livekit", got)
	}
	if got := headers.Get("X-LiveKit-Version"); got != "1.5.15" {
		t.Fatalf("X-LiveKit-Version = %q, want plugin version", got)
	}
}

func TestSmallestAITTSAudioFromWebsocketMessage(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte{0x01, 0x02})
	audio, done, err := smallestAITTSAudioFromWebsocketMessage([]byte(`{"status":"chunk","data":{"audio":"`+encoded+`"}}`), 24000, "seg-1")
	if err != nil {
		t.Fatalf("audio message: %v", err)
	}
	if done || string(audio.Frame.Data) != "\x01\x02" || audio.SegmentID != "seg-1" {
		t.Fatalf("audio=%+v done=%v, want decoded segment audio", audio, done)
	}

	audio, done, err = smallestAITTSAudioFromWebsocketMessage([]byte(`{"status":"complete"}`), 24000, "seg-1")
	if err != nil {
		t.Fatalf("complete message: %v", err)
	}
	if audio != nil || !done {
		t.Fatalf("audio=%+v done=%v, want complete marker", audio, done)
	}
}

func TestSmallestAITTSStreamBuffersTextUntilFlush(t *testing.T) {
	stream := &smallestaiTTSSynthesizeStream{}
	if err := stream.PushText("hello "); err != nil {
		t.Fatalf("push first: %v", err)
	}
	if err := stream.PushText("world"); err != nil {
		t.Fatalf("push second: %v", err)
	}
	if got := stream.pendingText.String(); got != "hello world" {
		t.Fatalf("pending text = %q, want concatenated text", got)
	}
}

func TestSmallestAITTSImplementsInterface(t *testing.T) {
	var _ tts.TTS = NewSmallestAITTS("test-key", "")
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

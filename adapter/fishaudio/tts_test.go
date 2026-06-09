package fishaudio

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cavos-io/rtp-agent/core/tts"
	"github.com/vmihailenco/msgpack/v5"
)

func TestFishAudioTTSDefaultsMatchReference(t *testing.T) {
	provider := NewFishAudioTTS("test-key", "")

	if provider.baseURL != "https://api.fish.audio" {
		t.Fatalf("base URL = %q, want reference API base", provider.baseURL)
	}
	if provider.model != "s2-pro" {
		t.Fatalf("model = %q, want s2-pro", provider.model)
	}
	if provider.voice != "933563129e564b19a115bedd57b7406a" {
		t.Fatalf("voice = %q, want default voice id", provider.voice)
	}
	if provider.outputFormat != "wav" {
		t.Fatalf("output format = %q, want wav", provider.outputFormat)
	}
	if provider.sampleRate != 24000 {
		t.Fatalf("sample rate = %d, want wav default 24000", provider.sampleRate)
	}
	if provider.latencyMode != "balanced" {
		t.Fatalf("latency mode = %q, want balanced", provider.latencyMode)
	}
	if provider.chunkLength != 100 {
		t.Fatalf("chunk length = %d, want 100", provider.chunkLength)
	}
	if !provider.Capabilities().Streaming {
		t.Fatalf("streaming = false, want true")
	}
}

func TestNewFishAudioTTSUsesEnvironmentAPIKey(t *testing.T) {
	t.Setenv("FISHAUDIO_API_KEY", "env-key")
	t.Setenv("FISH_AUDIO_API_KEY", "fallback-env-key")

	provider := NewFishAudioTTS("", "")

	if provider.apiKey != "env-key" {
		t.Fatalf("api key = %q, want primary env key", provider.apiKey)
	}

	explicit := NewFishAudioTTS("explicit-key", "")
	if explicit.apiKey != "explicit-key" {
		t.Fatalf("api key = %q, want explicit key", explicit.apiKey)
	}
}

func TestNewFishAudioTTSUsesReferenceEnvironmentAPIKey(t *testing.T) {
	t.Setenv("FISH_API_KEY", "reference-env-key")
	t.Setenv("FISHAUDIO_API_KEY", "")
	t.Setenv("FISH_AUDIO_API_KEY", "")

	provider := NewFishAudioTTS("", "")

	if provider.apiKey != "reference-env-key" {
		t.Fatalf("api key = %q, want reference env key", provider.apiKey)
	}
}

func TestNewFishAudioTTSUsesFallbackEnvironmentAPIKey(t *testing.T) {
	t.Setenv("FISHAUDIO_API_KEY", "")
	t.Setenv("FISH_AUDIO_API_KEY", "fallback-env-key")

	provider := NewFishAudioTTS("", "")

	if provider.apiKey != "fallback-env-key" {
		t.Fatalf("api key = %q, want fallback env key", provider.apiKey)
	}
}

func TestFishAudioTTSRequiresAPIKeyBeforeRequest(t *testing.T) {
	t.Setenv("FISH_API_KEY", "")
	t.Setenv("FISHAUDIO_API_KEY", "")
	t.Setenv("FISH_AUDIO_API_KEY", "")
	provider := NewFishAudioTTS("", "", WithFishAudioTTSBaseURL("://bad-url"))

	_, err := provider.Synthesize(context.Background(), "hello")
	if err == nil {
		t.Fatal("Synthesize returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "FISH_API_KEY") {
		t.Fatalf("Synthesize error = %q, want FISH_API_KEY guidance", err)
	}

	_, err = provider.Stream(context.Background())
	if err == nil {
		t.Fatal("Stream returned nil error, want missing API key error")
	}
	if !strings.Contains(err.Error(), "FISH_API_KEY") {
		t.Fatalf("Stream error = %q, want FISH_API_KEY guidance", err)
	}
}

func TestFishAudioTTSSynthesizeRequestUsesReferenceMsgpackPayload(t *testing.T) {
	provider := NewFishAudioTTS("test-key", "")

	req, err := buildFishAudioTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	if req.Method != http.MethodPost {
		t.Fatalf("method = %q, want POST", req.Method)
	}
	if req.URL.String() != "https://api.fish.audio/v1/tts" {
		t.Fatalf("url = %q, want /v1/tts endpoint", req.URL.String())
	}
	if got := req.Header.Get("Authorization"); got != "Bearer test-key" {
		t.Fatalf("authorization = %q, want bearer token", got)
	}
	if got := req.Header.Get("Content-Type"); got != "application/msgpack" {
		t.Fatalf("content type = %q, want application/msgpack", got)
	}
	if got := req.Header.Get("model"); got != "s2-pro" {
		t.Fatalf("model header = %q, want s2-pro", got)
	}

	var payload map[string]any
	if err := msgpack.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode msgpack body: %v", err)
	}
	assertFishPayload(t, payload, "text", "hello")
	assertFishPayload(t, payload, "format", "wav")
	assertFishPayload(t, payload, "reference_id", "933563129e564b19a115bedd57b7406a")
	assertFishPayload(t, payload, "latency", "balanced")
	if got := payload["chunk_length"]; got != int8(100) && got != int64(100) && got != 100 {
		t.Fatalf("chunk_length = %#v, want 100", got)
	}
	if got := fishPayloadInt(payload["sample_rate"]); got != 24000 {
		t.Fatalf("sample_rate = %#v, want 24000", got)
	}
	if got := payload["normalize"]; got != true {
		t.Fatalf("normalize = %#v, want true", got)
	}
	if got := fishPayloadInt(payload["mp3_bitrate"]); got != 64 {
		t.Fatalf("mp3_bitrate = %#v, want 64", got)
	}
	if got := fishPayloadInt(payload["opus_bitrate"]); got != 64000 {
		t.Fatalf("opus_bitrate = %#v, want 64000", got)
	}
	if got := payload["top_p"]; got != 0.7 {
		t.Fatalf("top_p = %#v, want 0.7", got)
	}
	if got := payload["temperature"]; got != 0.7 {
		t.Fatalf("temperature = %#v, want 0.7", got)
	}
}

func TestFishAudioTTSOptionsMatchReference(t *testing.T) {
	provider := NewFishAudioTTS("test-key", "",
		WithFishAudioTTSBaseURL("https://fish.example"),
		WithFishAudioTTSModel("s1"),
		WithFishAudioTTSVoice("voice-2"),
		WithFishAudioTTSOutputFormat("opus"),
		WithFishAudioTTSSampleRate(48000),
		WithFishAudioTTSLatencyMode("low"),
		WithFishAudioTTSChunkLength(250),
	)

	req, err := buildFishAudioTTSRequest(context.Background(), provider, "hello")
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if req.URL.String() != "https://fish.example/v1/tts" {
		t.Fatalf("url = %q, want custom base /v1/tts endpoint", req.URL.String())
	}
	if got := req.Header.Get("model"); got != "s1" {
		t.Fatalf("model header = %q, want s1", got)
	}

	var payload map[string]any
	if err := msgpack.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("decode msgpack body: %v", err)
	}
	assertFishPayload(t, payload, "reference_id", "voice-2")
	assertFishPayload(t, payload, "format", "opus")
	assertFishPayload(t, payload, "latency", "low")
	if got := fishPayloadInt(payload["sample_rate"]); got != 48000 {
		t.Fatalf("sample_rate = %#v, want 48000", got)
	}
	if got := fishPayloadInt(payload["chunk_length"]); got != 250 {
		t.Fatalf("chunk_length = %#v, want 250", got)
	}
}

func TestFishAudioTTSChunkedStreamUsesConfiguredSampleRate(t *testing.T) {
	stream := &fishaudioTTSChunkedStream{
		resp:       &http.Response{Body: io.NopCloser(bytes.NewReader([]byte{0x01, 0x02}))},
		sampleRate: 48000,
	}

	audio, err := stream.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
	if audio.Frame.SampleRate != 48000 {
		t.Fatalf("sample rate = %d, want 48000", audio.Frame.SampleRate)
	}
}

func TestFishAudioTTSWebsocketURLAndHeadersMatchReference(t *testing.T) {
	provider := NewFishAudioTTS("test-key", "", WithFishAudioTTSBaseURL("https://fish.example"))

	if got := buildFishAudioTTSWebsocketURL(provider); got != "wss://fish.example/v1/tts/live" {
		t.Fatalf("websocket URL = %q, want live websocket URL", got)
	}
	headers := buildFishAudioTTSWebsocketHeaders(provider)
	if headers.Get("Authorization") != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want bearer token", headers.Get("Authorization"))
	}
	if headers.Get("model") != "s2-pro" {
		t.Fatalf("model = %q, want s2-pro", headers.Get("model"))
	}
	if headers.Get("User-Agent") == "" {
		t.Fatal("User-Agent missing")
	}
}

func TestFishAudioTTSStreamMessagesMatchReference(t *testing.T) {
	provider := NewFishAudioTTS("test-key", "")

	start, err := buildFishAudioTTSStartMessage(provider)
	if err != nil {
		t.Fatalf("start message: %v", err)
	}
	var startPayload map[string]any
	if err := msgpack.Unmarshal(start, &startPayload); err != nil {
		t.Fatalf("decode start: %v", err)
	}
	if startPayload["event"] != "start" {
		t.Fatalf("start event = %#v, want start", startPayload["event"])
	}
	request := startPayload["request"].(map[string]any)
	assertFishPayload(t, request, "text", "")
	assertFishPayload(t, request, "reference_id", "933563129e564b19a115bedd57b7406a")

	text, err := buildFishAudioTTSTextMessage("hello")
	if err != nil {
		t.Fatalf("text message: %v", err)
	}
	var textPayload map[string]any
	if err := msgpack.Unmarshal(text, &textPayload); err != nil {
		t.Fatalf("decode text: %v", err)
	}
	if textPayload["event"] != "text" || textPayload["text"] != "hello " {
		t.Fatalf("text payload = %+v, want text event with trailing space", textPayload)
	}

	flush, _ := buildFishAudioTTSSimpleEvent("flush")
	stop, _ := buildFishAudioTTSSimpleEvent("stop")
	assertFishEvent(t, flush, "flush")
	assertFishEvent(t, stop, "stop")
}

func TestFishAudioTTSAudioFromStreamMessage(t *testing.T) {
	audio, done, err := fishAudioTTSAudioFromStreamMessage(mustFishMessage(t, map[string]any{
		"event": "audio",
		"audio": []byte{1, 2, 3, 4},
	}), 24000)
	if err != nil {
		t.Fatalf("audio from message: %v", err)
	}
	if done {
		t.Fatal("done = true for audio event")
	}
	if audio == nil || string(audio.Frame.Data) != string([]byte{1, 2, 3, 4}) {
		t.Fatalf("audio = %+v, want decoded frame", audio)
	}
	if audio.Frame.SampleRate != 24000 || audio.Frame.NumChannels != 1 {
		t.Fatalf("frame = %+v, want 24 kHz mono", audio.Frame)
	}

	finished, done, err := fishAudioTTSAudioFromStreamMessage(mustFishMessage(t, map[string]any{
		"event": "finish",
	}), 24000)
	if err != nil {
		t.Fatalf("finish event: %v", err)
	}
	if finished != nil || !done {
		t.Fatalf("finished=%+v done=%v, want done with no audio", finished, done)
	}
}

func TestFishAudioTTSImplementsStreamingInterface(t *testing.T) {
	var _ tts.TTS = NewFishAudioTTS("test-key", "")
}

func assertFishPayload(t *testing.T, payload map[string]any, key string, want string) {
	t.Helper()
	if got := payload[key]; got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}

func fishPayloadInt(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int8:
		return int(v)
	case int16:
		return int(v)
	case int32:
		return int(v)
	case int64:
		return int(v)
	case uint8:
		return int(v)
	case uint16:
		return int(v)
	case uint32:
		return int(v)
	case uint64:
		return int(v)
	case uint:
		return int(v)
	default:
		return 0
	}
}

func assertFishEvent(t *testing.T, encoded []byte, want string) {
	t.Helper()
	var payload map[string]any
	if err := msgpack.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	if payload["event"] != want {
		t.Fatalf("event = %#v, want %q", payload["event"], want)
	}
}

func mustFishMessage(t *testing.T, message map[string]any) []byte {
	t.Helper()
	encoded, err := msgpack.Marshal(message)
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}
	return encoded
}

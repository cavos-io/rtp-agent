package fishaudio

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

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
